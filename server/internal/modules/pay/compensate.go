package pay

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/xiangchu/server/internal/infra/logger"
)

// 支付查单补偿：微信回调丢失（notify_url 不可达/服务重启窗口/长时间宕机）时，
// 定时扫描待支付单主动向微信查单对齐结果。要点：
//   - SUCCESS 走与回调完全相同的 markPaid 链路（条件更新幂等，并发安全，
//     pay.success 至多发一次），不另起状态机
//   - CLOSED/PAYERROR/REVOKED 为终态，0→4 关闭后退出扫描集；
//     预下单未传 time_expire，微信侧 2 小时自动失效，届时查单返回 CLOSED 自然收敛
//   - NOTPAY/USERPAYING 继续等回调或下一轮
const (
	compensateInterval = 5 * time.Minute // 扫描周期
	compensateMinAge   = 5 * time.Minute // 只查创建超过 5 分钟的单，避让正常支付窗口
	compensateBatch    = 100             // 单轮上限，防服务停机一夜后一次性打爆微信接口
)

// 补偿动作
type compAction int8

const (
	compWait     compAction = iota // 中间态：等回调或下一轮
	compMarkPaid                   // SUCCESS：走回调同款记账
	compClose                      // 终态：0→4 关闭，退出扫描集
	compManual                     // 未预期状态：人工核对，不自动流转
)

// compensateActionFor 微信 trade_state → 补偿动作（纯函数，单测见 compensate_test.go）
func compensateActionFor(state string) compAction {
	switch state {
	case "SUCCESS":
		return compMarkPaid
	case "NOTPAY", "USERPAYING":
		return compWait
	case "CLOSED", "PAYERROR", "REVOKED":
		return compClose
	default:
		// REFUND 等异常态：本地仍是待支付但微信侧已流转到退款，账务需人工核对
		return compManual
	}
}

// StartCompensator 启动查单补偿循环；mock 模式无回调丢失问题，跳过
func (s *serviceImpl) StartCompensator() {
	if s.mock() {
		logger.L().Info("wxpay mock 模式，跳过查单补偿")
		return
	}
	go func() {
		t := time.NewTicker(compensateInterval)
		defer t.Stop()
		for range t.C {
			s.compensateOnce()
		}
	}()
	logger.L().Info("查单补偿已启动", zap.Duration("interval", compensateInterval))
}

func (s *serviceImpl) compensateOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), compensateInterval)
	defer cancel()
	list, err := s.repo.ListStalePending(ctx, time.Now().Add(-compensateMinAge), compensateBatch)
	if err != nil {
		logger.L().Error("查单补偿扫描失败", zap.Error(err))
		return
	}
	for _, p := range list {
		s.compensateOne(ctx, p)
	}
}

func (s *serviceImpl) compensateOne(ctx context.Context, p *Payment) {
	q, err := s.wx.QueryOrderByOutTradeNo(p.OutTradeNo)
	if err != nil {
		// 网络抖动等瞬时错误：本轮跳过，下一轮重试
		logger.L().Warn("查单失败", zap.String("payment_no", p.PaymentNo), zap.Error(err))
		return
	}
	switch compensateActionFor(q.TradeState) {
	case compMarkPaid:
		if err := s.markPaid(ctx, p, q.TransactionID); err != nil {
			logger.L().Error("查单补偿记账失败", zap.String("payment_no", p.PaymentNo), zap.Error(err))
			return
		}
		logger.L().Info("查单补偿补单成功",
			zap.String("payment_no", p.PaymentNo), zap.String("transaction_id", q.TransactionID))
	case compClose:
		if _, err := s.repo.TransitPayment(ctx, p.ID, PayStatusPending, PayStatusClosed); err != nil {
			logger.L().Error("查单补偿关单失败", zap.String("payment_no", p.PaymentNo), zap.Error(err))
			return
		}
		logger.L().Info("查单补偿关闭过期支付单",
			zap.String("payment_no", p.PaymentNo), zap.String("trade_state", q.TradeState))
	case compManual:
		logger.L().Error("查单发现未预期交易状态，需人工核对",
			zap.String("payment_no", p.PaymentNo), zap.String("trade_state", q.TradeState))
	}
}
