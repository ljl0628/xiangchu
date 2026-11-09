package pay

import (
	"context"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/xiangchu/server/internal/infra/logger"
	"github.com/xiangchu/server/internal/infra/wxpay"
	"github.com/xiangchu/server/internal/pkg/errs"
	"github.com/xiangchu/server/internal/pkg/pagination"
)

// 每日对账：T+1 拉微信交易账单（CSV）与本地 payments/refunds 双向核对。
// 定位——回调是"微信告诉我"，查单补偿是"我问微信单笔"，对账是"微信给我全部，
// 整体核对"：覆盖前两者都发现不了的掉单/金额错/退款异常/状态机 bug。
// v1 只检测落表不自动改账（人工处置），金额以微信账单为准。
//
// 已知噪声：本地按 pay_time 归日、账单按微信侧交易时间归日，
// 跨日零点边界的支付可能两侧归到不同日期，产生一条 MISSING_* 差异，人工核销即可。

const (
	reconcileCheckInterval = 30 * time.Minute // 巡检周期；账单 T+1 上午 10 点后生成，无需精确触发
)

// ---------- 比对核心（纯函数，单测见 reconcile_test.go） ----------

// billAgg 账单侧按商户订单号聚合：TRADE_SUCCESS 行取付款额，REFUND 行累计退款额
type billAgg struct {
	PayFen    int64
	RefundFen int64
}

func aggregateBill(records []wxpay.BillRecord) map[string]*billAgg {
	m := make(map[string]*billAgg, len(records))
	for _, r := range records {
		if r.State != "TRADE_SUCCESS" && r.State != "REFUND" {
			continue // REVOKED 等未成交行不参与资金核对
		}
		agg, ok := m[r.OutTradeNo]
		if !ok {
			agg = &billAgg{}
			m[r.OutTradeNo] = agg
		}
		if r.State == "TRADE_SUCCESS" {
			agg.PayFen += r.PayFen
		} else {
			agg.RefundFen += r.RefundFen
		}
	}
	return m
}

// reconcileDiffRow 比对产物（落表前的中间结构）
type reconcileDiffRow struct {
	DiffType    string
	OutTradeNo  string
	WxAmount    int64
	LocalAmount int64
	Detail      string
}

// compareBill 本地 vs 账单双向核对：
//   - 账单有本地无 → MISSING_LOCAL（掉单，最严重）
//   - 本地有账单无 → MISSING_BILL
//   - 双方都有但付款额不同 → AMOUNT_MISMATCH
//   - 退款成功合计不同（按支付单聚合）→ REFUND_MISMATCH
//
// 返回差异列表与账单侧统计。local 的 key 为 payment_no（与 out_trade_no 同值）
func compareBill(records []wxpay.BillRecord, local []*Payment, localRefund map[string]int64) (diffs []reconcileDiffRow, tradeCount int, tradeTotal int64) {
	agg := aggregateBill(records)
	localSet := make(map[string]*Payment, len(local))
	for _, p := range local {
		localSet[p.PaymentNo] = p
	}

	// 账单侧每笔 + 双方共有的金额/退款比对
	for no, b := range agg {
		p, ok := localSet[no]
		if !ok {
			if b.PayFen > 0 { // 纯退款行且本地也无支付单：支付单缺失已单独暴露，不重复计
				diffs = append(diffs, reconcileDiffRow{
					DiffType: DiffMissingLocal, OutTradeNo: no, WxAmount: b.PayFen,
					Detail: "账单有支付记录但本地无此支付单（掉单）",
				})
			}
			continue
		}
		if b.PayFen != p.Amount {
			diffs = append(diffs, reconcileDiffRow{
				DiffType: DiffAmountMismatch, OutTradeNo: no,
				WxAmount: b.PayFen, LocalAmount: p.Amount,
				Detail: "付款金额与本地支付单不符",
			})
		}
		if b.RefundFen != localRefund[no] {
			diffs = append(diffs, reconcileDiffRow{
				DiffType: DiffRefundMismatch, OutTradeNo: no,
				WxAmount: b.RefundFen, LocalAmount: localRefund[no],
				Detail: "退款总额与本地退款单合计不符",
			})
		}
		tradeCount++
		tradeTotal += b.PayFen
	}

	// 本地侧独有
	for no, p := range localSet {
		if _, ok := agg[no]; !ok {
			diffs = append(diffs, reconcileDiffRow{
				DiffType: DiffMissingBill, OutTradeNo: no, LocalAmount: p.Amount,
				Detail: "本地已支付但账单无此交易",
			})
		}
	}
	return diffs, tradeCount, tradeTotal
}

// ---------- 跑批 ----------

var reconcileRunning atomic.Bool // 单飞：定时器与管理端手动触发互斥

// ReconcileOnce 跑一个账单日：拉账单 → 解析 → 比对 → 差异落表。
// 同日已有完成/进行中批次直接返回（uk 再兜底）；失败批次允许重跑
func (s *serviceImpl) ReconcileOnce(ctx context.Context, billDate string) (*ReconcileBatch, error) {
	if s.mock() {
		return nil, errs.BadRequest("对账依赖微信真实账单，仅真实模式可用")
	}
	if prev, err := s.repo.GetBatchByDate(ctx, billDate); err == nil {
		if prev.Status != ReconcileFailed {
			return prev, nil // 已跑过（完成或进行中）
		}
		// 失败批次重跑：沿用原行
		return s.runBatch(ctx, prev)
	}
	b := &ReconcileBatch{BillDate: billDate, Status: ReconcileRunning}
	if err := s.repo.CreateBatch(ctx, b); err != nil {
		// 并发撞 uk：另一触发方已在跑，读回它的批次
		if isDuplicateKey(err) {
			return s.repo.GetBatchByDate(ctx, billDate)
		}
		return nil, err
	}
	return s.runBatch(ctx, b)
}

// runBatch 单批次执行：任何阶段失败置失败态（error_msg 留原因），成功写统计收尾
func (s *serviceImpl) runBatch(ctx context.Context, b *ReconcileBatch) (*ReconcileBatch, error) {
	now := time.Now()
	fail := func(msg string) (*ReconcileBatch, error) {
		_ = s.repo.FinishBatch(ctx, b.ID, map[string]any{
			"status": ReconcileFailed, "error_msg": truncate(msg, 255), "finished_at": &now,
		})
		return nil, errs.BadRequest("对账失败: " + msg)
	}

	csv, err := s.wx.GetTradeBill(b.BillDate)
	if err != nil {
		return fail(err.Error())
	}
	records, err := wxpay.ParseTradeBill(csv)
	if err != nil {
		return fail(err.Error())
	}

	// 本地侧：账单日内曾收款的支付单 + 全量成功退款聚合
	day, err := time.ParseInLocation("2006-01-02", b.BillDate, time.Local)
	if err != nil {
		return fail("账单日格式错误: " + err.Error())
	}
	localPays, err := s.repo.ListPaidOnDate(ctx, day, day.AddDate(0, 0, 1))
	if err != nil {
		return fail("加载本地支付单失败: " + err.Error())
	}
	refAgg, err := s.repo.RefundSuccessAgg(ctx)
	if err != nil {
		return fail("加载本地退款聚合失败: " + err.Error())
	}

	diffs, tradeCount, tradeTotal := compareBill(records, localPays, refAgg)
	rows := make([]*ReconcileDiff, 0, len(diffs))
	for _, d := range diffs {
		rows = append(rows, &ReconcileDiff{
			BatchID: b.ID, BillDate: b.BillDate, DiffType: d.DiffType,
			OutTradeNo: d.OutTradeNo, WxAmount: d.WxAmount, LocalAmount: d.LocalAmount, Detail: d.Detail,
		})
	}
	if err := s.repo.CreateDiffs(ctx, rows); err != nil {
		return fail("差异落表失败: " + err.Error())
	}

	localTotal := int64(0)
	for _, p := range localPays {
		localTotal += p.Amount
	}
	finish := time.Now()
	if err := s.repo.FinishBatch(ctx, b.ID, map[string]any{
		"status": ReconcileDone, "trade_count": tradeCount, "trade_total": tradeTotal,
		"local_count": len(localPays), "local_total": localTotal,
		"diff_count": len(rows), "finished_at": &finish,
	}); err != nil {
		return nil, err
	}
	if len(rows) > 0 {
		logger.L().Warn("对账发现差异",
			zap.String("bill_date", b.BillDate), zap.Int("diff_count", len(rows)))
	} else {
		logger.L().Info("对账完成无差异", zap.String("bill_date", b.BillDate))
	}
	out, err := s.repo.GetBatchByDate(ctx, b.BillDate)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// StartReconciler 启动对账巡检循环：每半小时看一眼"昨日账单"是否已跑
// （微信 T+1 上午 10 点出账，首个覆盖到它的巡检即执行；失败批次下轮自动重试）
func (s *serviceImpl) StartReconciler() {
	if s.mock() {
		return
	}
	go func() {
		tick := time.NewTicker(reconcileCheckInterval)
		defer tick.Stop()
		for range tick.C {
			s.reconcileYesterday()
		}
	}()
}

func (s *serviceImpl) reconcileYesterday() {
	if !reconcileRunning.CompareAndSwap(false, true) {
		return
	}
	defer reconcileRunning.Store(false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	if _, err := s.ReconcileOnce(ctx, yesterday); err != nil {
		logger.L().Error("每日对账执行失败", zap.String("bill_date", yesterday), zap.Error(err))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// ---------- Service 接口实现 ----------

// RunReconcile 管理端手动触发（可与定时任务同日并发，reconcileRunning 单飞互斥）
func (s *serviceImpl) RunReconcile(ctx context.Context, billDate string) (*ReconcileBatch, error) {
	if _, err := time.ParseInLocation("2006-01-02", billDate, time.Local); err != nil {
		return nil, errs.BadRequest("账单日格式需为 YYYY-MM-DD")
	}
	if billDate >= time.Now().Format("2006-01-02") {
		return nil, errs.BadRequest("账单日须早于今日（微信账单 T+1 生成）")
	}
	if !reconcileRunning.CompareAndSwap(false, true) {
		return nil, errs.BadRequest("已有对账任务在执行中，请稍后再试")
	}
	defer reconcileRunning.Store(false)
	return s.ReconcileOnce(ctx, billDate)
}

func (s *serviceImpl) ListReconciles(ctx context.Context, page, size int) ([]*ReconcileBatch, int64, error) {
	page, size, err := pagination.Normalize(page, size)
	if err != nil {
		return nil, 0, err
	}
	return s.repo.ListBatches(ctx, page, size)
}

func (s *serviceImpl) ListReconcileDiffs(ctx context.Context, batchID int64, page, size int) ([]*ReconcileDiff, int64, error) {
	page, size, err := pagination.Normalize(page, size)
	if err != nil {
		return nil, 0, err
	}
	return s.repo.ListDiffs(ctx, batchID, page, size)
}
