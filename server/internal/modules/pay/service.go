package pay

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/xiangchu/server/internal/config"
	"github.com/xiangchu/server/internal/infra/logger"
	"github.com/xiangchu/server/internal/infra/mq"
	"github.com/xiangchu/server/internal/infra/wxpay"
	"github.com/xiangchu/server/internal/pkg/errs"
	"github.com/xiangchu/server/internal/pkg/orderid"
	"github.com/xiangchu/server/internal/pkg/pagination"
)

// Service 财务域服务接口（跨模块只允许通过此接口调用）
type Service interface {
	// CreatePayment 创建支付单并返回 JSAPI 调起参数（bizType 1宴席 2采购）
	CreatePayment(ctx context.Context, bizType int, bizID, userID, amount int64) (*PayResult, error)
	// HandleWxNotify 处理微信支付回调：验签解析 + 记账 + 发布 pay.success
	HandleWxNotify(ctx context.Context, headers http.Header, body []byte) (string, error)
	// MockPay 模拟支付（联调）：待支付 → 已支付
	MockPay(ctx context.Context, paymentNo string) error
	// StartCompensator 启动查单补偿循环（真实模式；回调丢失时主动向微信对齐支付结果）
	StartCompensator()
	// Refund 发起退款；mock 模式即时成功并发布 pay.refunded
	Refund(ctx context.Context, paymentNo string, refundFen int64) error
	// CreateSettlement 按费率生成结算单（v1 即时结算，status=1）
	CreateSettlement(ctx context.Context, role int8, targetID int64, bizNo string, gross int64) error
	// WithdrawApply 发起提现（role 1乡厨 2供应商，来自 token）
	WithdrawApply(ctx context.Context, uid int64, role int8, amount int64) error
	// Withdrawals 我的提现记录
	Withdrawals(ctx context.Context, uid int64, role int8) ([]*Withdrawal, error)
	// AdminWithdrawals 管理端提现单列表
	AdminWithdrawals(ctx context.Context, f *WFilter) ([]*Withdrawal, int64, error)
	// AuditWithdrawal 审核：通过 0→1 / 拒绝 0→2
	AuditWithdrawal(ctx context.Context, id int64, approve bool, remark string) error
	// MarkWithdrawalPaid 线下打款标记 1→3
	MarkWithdrawalPaid(ctx context.Context, id int64) error
	// Income 收入总览
	Income(ctx context.Context, uid int64, role int8) (*IncomeVO, error)
	// AdminListSettlements 管理端结算单列表
	AdminListSettlements(ctx context.Context, f *SFilter) ([]*Settlement, int64, error)
	// GetSettings 费率设置
	GetSettings(ctx context.Context) (*SettingsVO, error)
	// SetSettings 费率设置落库
	SetSettings(ctx context.Context, chefBP, supplierBP int) error
	// FinanceStats 财务总览
	FinanceStats(ctx context.Context) (*FinanceStatsVO, error)
}

// ---------- impl ----------

const (
	payExpire     = 2 * time.Hour        // 支付单有效期
	settingChef   = "chef_commission_bp" // system_settings 键
	settingSup    = "supplier_commission_bp"
	defChefBP     = 300 // 配置缺失兜底 3%
	defSupplierBP = 100 // 配置缺失兜底 1%
)

type serviceImpl struct {
	repo *Repo
	wx   *wxpay.Client
	bus  *mq.MQ
	cfg  *config.Config
}

// NewService 构造财务域服务
func NewService(repo *Repo, wx *wxpay.Client, bus *mq.MQ, cfg *config.Config) Service {
	return &serviceImpl{repo: repo, wx: wx, bus: bus, cfg: cfg}
}

func (s *serviceImpl) mock() bool { return s.cfg.Wx.Pay.Mock || !s.cfg.Wx.Pay.Enabled }

// CreatePayment 创建支付单并下单
func (s *serviceImpl) CreatePayment(ctx context.Context, bizType int, bizID, userID, amount int64) (*PayResult, error) {
	if bizType != int(BizTypeBanquet) && bizType != int(BizTypePurchase) {
		return nil, errs.BadRequest("不支持的支付业务类型")
	}
	if amount <= 0 {
		return nil, errs.ErrAmountWrong
	}
	no := orderid.GenNo("ZF")
	p := &Payment{
		PaymentNo:  no,
		OutTradeNo: no,
		BizType:    int8(bizType),
		BizID:      bizID,
		UserID:     userID,
		Amount:     amount,
		Status:     PayStatusPending,
		ExpireTime: time.Now().Add(payExpire),
	}
	if err := s.repo.CreatePayment(ctx, p); err != nil {
		return nil, err
	}
	params, err := s.wx.JSAPI(ctx, s.openID(ctx, userID), no, amount, "乡厨订单支付")
	if err != nil {
		return nil, err
	}
	logger.L().Info("创建支付单", zap.String("payment_no", no),
		zap.Int("biz_type", bizType), zap.Int64("biz_id", bizID), zap.Int64("amount", amount))
	return &PayResult{PaymentNo: no, Params: params}, nil
}

// openID 实名支付需要 openid：从 users 表只读补齐（mock 模式跳过查询）
func (s *serviceImpl) openID(ctx context.Context, uid int64) string {
	if s.mock() {
		return ""
	}
	var openID string
	if err := s.repo.q(ctx).Raw("SELECT open_id FROM users WHERE id = ?", uid).Scan(&openID).Error; err != nil {
		logger.L().Warn("查询用户 openid 失败", zap.Int64("uid", uid), zap.Error(err))
		return ""
	}
	return openID
}

// markPaid 记账并广播 pay.success；已支付则幂等跳过
func (s *serviceImpl) markPaid(ctx context.Context, p *Payment, txID string) error {
	ok, err := s.repo.MarkPaid(ctx, p.ID, txID)
	if err != nil {
		return err
	}
	if ok {
		s.publishEvent(p, mq.KeyPaySuccess)
	}
	return nil
}

// HandleWxNotify 微信支付回调：解析（真实模式含验签解密）+ 记账 + 发布 pay.success
func (s *serviceImpl) HandleWxNotify(ctx context.Context, headers http.Header, body []byte) (string, error) {
	res, err := s.wx.ParseNotify(headers, body)
	if err != nil {
		return "", err
	}
	if res.TradeState != "SUCCESS" {
		// V3 支付通知只在支付成功时推送；其余状态记日志后应答 SUCCESS，避免微信对终态反复重试
		logger.L().Warn("微信回调交易状态非成功，忽略并应答 SUCCESS",
			zap.String("out_trade_no", res.OutTradeNo), zap.String("trade_state", res.TradeState))
		return "", nil
	}
	p, err := s.repo.GetPaymentByOutTradeNo(ctx, res.OutTradeNo)
	if err != nil {
		return "", err
	}
	if err := s.markPaid(ctx, p, res.TransactionID); err != nil {
		return "", err
	}
	return p.PaymentNo, nil
}

// MockPay 联调模拟支付
func (s *serviceImpl) MockPay(ctx context.Context, paymentNo string) error {
	p, err := s.repo.GetPaymentByNo(ctx, paymentNo)
	if err != nil {
		return err
	}
	if p.Status == PayStatusPaid {
		return nil // 幂等
	}
	if p.Status != PayStatusPending {
		return errs.ErrStatusDenied
	}
	return s.markPaid(ctx, p, "MOCK"+paymentNo)
}

// publishEvent 广播支付域事件（biz_type/biz_id/payment_no）
func (s *serviceImpl) publishEvent(p *Payment, key string) {
	if s.bus == nil {
		logger.L().Warn("MQ 未连接，支付事件降级为日志",
			zap.String("key", key), zap.String("payment_no", p.PaymentNo))
		return
	}
	payload := map[string]any{
		"biz_type":   p.BizType,
		"biz_id":     p.BizID,
		"payment_no": p.PaymentNo,
	}
	if err := s.bus.Publish(mq.EventExchange, key, payload); err != nil {
		logger.L().Error("支付事件发布失败",
			zap.String("key", key), zap.String("payment_no", p.PaymentNo), zap.Error(err))
	}
}

// Refund 发起退款：已支付 → 退款中；mock 模式即时已退款并广播
func (s *serviceImpl) Refund(ctx context.Context, paymentNo string, refundFen int64) error {
	if refundFen <= 0 {
		return errs.ErrAmountWrong
	}
	p, err := s.repo.GetPaymentByNo(ctx, paymentNo)
	if err != nil {
		return err
	}
	if p.Status != PayStatusPaid {
		return errs.ErrStatusDenied
	}
	if refundFen > p.Amount {
		return errs.ErrAmountWrong
	}
	ok, err := s.repo.TransitPayment(ctx, p.ID, PayStatusPaid, PayStatusRefunding)
	if err != nil {
		return err
	}
	if !ok {
		return errs.ErrStatusDenied
	}
	if err := s.wx.Refund(p.OutTradeNo, orderid.GenNo("TK"), refundFen, p.Amount); err != nil {
		// 退款受理失败，回滚为已支付以便重试
		_, _ = s.repo.TransitPayment(ctx, p.ID, PayStatusRefunding, PayStatusPaid)
		return err
	}
	// 真实模式退款结果走回调确认（后续接入）；mock 即时完成
	if s.mock() {
		ok, err := s.repo.TransitPayment(ctx, p.ID, PayStatusRefunding, PayStatusRefunded)
		if err != nil {
			return err
		}
		if ok {
			s.publishEvent(p, mq.KeyPayRefunded)
		}
	}
	return nil
}

// getRateBP 费率解析：system_settings 覆盖 > 配置默认 > 内置兜底（BP）
func (s *serviceImpl) getRateBP(ctx context.Context, role int8) int {
	defChef, defSup := s.cfg.Finance.ChefCommissionBP, s.cfg.Finance.SupplierCommissionBP
	if defChef <= 0 {
		defChef = defChefBP
	}
	if defSup <= 0 {
		defSup = defSupplierBP
	}
	key, def := settingChef, defChef
	if role == RoleSupplier {
		key, def = settingSup, defSup
	}
	if v, err := s.repo.GetSetting(ctx, key); err == nil && v != "" {
		if bp, err2 := strconv.Atoi(v); err2 == nil && bp >= 0 && bp <= 10000 {
			return bp
		}
	}
	return def
}

// CreateSettlement 生成结算单：佣金 = gross × bp / 10000（decimal 四舍五入）
// v1 支付成功即自动结算，status=1、SettleTime=now
func (s *serviceImpl) CreateSettlement(ctx context.Context, role int8, targetID int64, bizNo string, gross int64) error {
	if role != RoleChef && role != RoleSupplier {
		return errs.BadRequest("不支持的结算主体")
	}
	if gross < 0 {
		return errs.ErrAmountWrong
	}
	bp := s.getRateBP(ctx, role)
	commission := decimal.NewFromInt(gross).
		Mul(decimal.NewFromInt(int64(bp))).
		Div(decimal.NewFromInt(10000)).Round(0).IntPart()
	if commission < 0 {
		commission = 0
	}
	if commission > gross {
		commission = gross
	}
	now := time.Now()
	err := s.repo.CreateSettlement(ctx, &Settlement{
		SettleNo:         orderid.GenNo("JS"),
		Role:             role,
		TargetID:         targetID,
		BizNo:            bizNo,
		GrossAmount:      gross,
		CommissionAmount: commission,
		NetAmount:        gross - commission,
		Status:           SettleStatusDone,
		SettleTime:       &now,
	})
	if err == nil {
		logger.L().Info("生成结算单", zap.Int8("role", role), zap.Int64("target_id", targetID),
			zap.String("biz_no", bizNo), zap.Int64("gross", gross), zap.Int("bp", bp))
	}
	return err
}

// ---------- 提现 ----------

// resolveTarget token 身份 → 结算主体 ID（chefID / supplierID）
func (s *serviceImpl) resolveTarget(ctx context.Context, uid int64, role int8) (int64, error) {
	switch role {
	case RoleChef:
		return s.repo.ChefIDByUser(ctx, uid)
	case RoleSupplier:
		return s.repo.SupplierIDByUser(ctx, uid)
	}
	return 0, errs.ErrForbidden
}

// available 可提现余额 = 已结算净额 − 占用中的提现（待审核/待打款/已打款）
// settleDeadline 冻结期截止时刻：结算时间早于它的金额可提（T+N 盖住售后窗口）
func (s *serviceImpl) settleDeadline() time.Time {
	days := s.cfg.Finance.SettleDelayDays
	if days <= 0 {
		return time.Now() // 未配置冻结期 = 完成即可提
	}
	return time.Now().AddDate(0, 0, -days)
}

func (s *serviceImpl) available(ctx context.Context, role int8, targetID int64) (int64, error) {
	// 可提余额只累计已过冻结期的结算单，用时间闸门代替人工审核
	net, err := s.repo.SumNetReleased(ctx, role, targetID, s.settleDeadline())
	if err != nil {
		return 0, err
	}
	used, err := s.repo.SumWithdrawn(ctx, role, targetID, []int8{
		WithdrawStatusApplied, WithdrawStatusApproved, WithdrawStatusPaid,
	})
	if err != nil {
		return 0, err
	}
	if v := net - used; v > 0 {
		return v, nil
	}
	return 0, nil
}

// WithdrawApply 发起提现（v1 统一线下打款渠道）
func (s *serviceImpl) WithdrawApply(ctx context.Context, uid int64, role int8, amount int64) error {
	if amount <= 0 {
		return errs.ErrAmountWrong
	}
	targetID, err := s.resolveTarget(ctx, uid, role)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errs.BadRequest("账号资料尚未通过审核，暂不可提现")
		}
		return err
	}
	avail, err := s.available(ctx, role, targetID)
	if err != nil {
		return err
	}
	if amount > avail {
		return errs.BadRequest("可提现余额不足")
	}
	now := time.Now()
	w := &Withdrawal{
		WithdrawNo: orderid.GenNo("TX"),
		Role:       role,
		TargetID:   targetID,
		Amount:     amount,
		Channel:    ChannelOffline,
		Status:     WithdrawStatusApplied,
		AppliedAt:  now,
	}
	if err := s.repo.CreateWithdrawal(ctx, w); err != nil {
		return err
	}
	// 规则放行：冻结期外的可提余额 + 金额不超限 → 自动通过并 mock 打款，人工只处理例外
	limit := s.cfg.Finance.AutoReleaseMaxAmount
	if limit <= 0 || amount <= limit {
		ok, err := s.repo.TransitWithdrawal(ctx, w.ID, WithdrawStatusApplied, WithdrawStatusApproved,
			map[string]any{"audit_remark": "系统自动放行", "audited_at": time.Now()})
		if err == nil && ok {
			logger.L().Info("提现自动放行（mock 打款）",
				zap.String("withdraw_no", w.WithdrawNo), zap.Int64("amount", amount))
			_, _ = s.repo.TransitWithdrawal(ctx, w.ID, WithdrawStatusApproved, WithdrawStatusPaid,
				map[string]any{"paid_at": time.Now()})
		}
	}
	return nil
}

// Withdrawals 我的提现记录
func (s *serviceImpl) Withdrawals(ctx context.Context, uid int64, role int8) ([]*Withdrawal, error) {
	targetID, err := s.resolveTarget(ctx, uid, role)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return []*Withdrawal{}, nil
		}
		return nil, err
	}
	return s.repo.ListWithdrawalsByTarget(ctx, role, targetID)
}

// AdminWithdrawals 管理端提现单列表
func (s *serviceImpl) AdminWithdrawals(ctx context.Context, f *WFilter) ([]*Withdrawal, int64, error) {
	if f == nil {
		f = &WFilter{}
	}
	page, size, err := pagination.Normalize(f.Page, f.Size)
	if err != nil {
		return nil, 0, err
	}
	return s.repo.ListWithdrawals(ctx, f, page, size)
}

// AuditWithdrawal 提现审核：通过 0→1（待打款）/ 拒绝 0→2
func (s *serviceImpl) AuditWithdrawal(ctx context.Context, id int64, approve bool, remark string) error {
	to := WithdrawStatusRejected
	if approve {
		to = WithdrawStatusApproved
	}
	fields := map[string]any{"audit_remark": remark, "audited_at": time.Now()}
	ok, err := s.repo.TransitWithdrawal(ctx, id, WithdrawStatusApplied, to, fields)
	if err != nil {
		return err
	}
	if !ok {
		return errs.ErrStatusDenied
	}
	return nil
}

// MarkWithdrawalPaid 线下打款完成标记 1→3
func (s *serviceImpl) MarkWithdrawalPaid(ctx context.Context, id int64) error {
	ok, err := s.repo.TransitWithdrawal(ctx, id, WithdrawStatusApproved, WithdrawStatusPaid,
		map[string]any{"paid_at": time.Now()})
	if err != nil {
		return err
	}
	if !ok {
		return errs.ErrStatusDenied
	}
	return nil
}

// Income 收入总览：累计净额 / 当月净额 / 冻结中 / 已提现 / 可提现
func (s *serviceImpl) Income(ctx context.Context, uid int64, role int8) (*IncomeVO, error) {
	targetID, err := s.resolveTarget(ctx, uid, role)
	if err != nil {
		return nil, err
	}
	totalNet, err := s.repo.SumNet(ctx, role, targetID, false)
	if err != nil {
		return nil, err
	}
	released, err := s.repo.SumNetReleased(ctx, role, targetID, s.settleDeadline())
	if err != nil {
		return nil, err
	}
	frozen := totalNet - released
	if frozen < 0 {
		frozen = 0
	}
	monthNet, err := s.repo.SumNet(ctx, role, targetID, true)
	if err != nil {
		return nil, err
	}
	withdrawn, err := s.repo.SumWithdrawn(ctx, role, targetID, []int8{WithdrawStatusPaid})
	if err != nil {
		return nil, err
	}
	avail, err := s.available(ctx, role, targetID)
	if err != nil {
		return nil, err
	}
	return &IncomeVO{
		TotalNet:       totalNet,
		MonthNet:       monthNet,
		FrozenAmount:   frozen,
		TotalWithdrawn: withdrawn,
		Available:      avail,
	}, nil
}

// AdminListSettlements 管理端结算单列表
func (s *serviceImpl) AdminListSettlements(ctx context.Context, f *SFilter) ([]*Settlement, int64, error) {
	if f == nil {
		f = &SFilter{}
	}
	page, size, err := pagination.Normalize(f.Page, f.Size)
	if err != nil {
		return nil, 0, err
	}
	return s.repo.ListSettlements(ctx, f.Role, page, size)
}

// ---------- 费率设置 / 财务总览 ----------

// GetSettings 费率视图：settings 覆盖 > 配置默认
func (s *serviceImpl) GetSettings(ctx context.Context) (*SettingsVO, error) {
	return &SettingsVO{
		ChefBP:     s.getRateBP(ctx, RoleChef),
		SupplierBP: s.getRateBP(ctx, RoleSupplier),
	}, nil
}

// SetSettings 费率落库（system_settings 覆盖配置默认值）
func (s *serviceImpl) SetSettings(ctx context.Context, chefBP, supplierBP int) error {
	if chefBP < 0 || chefBP > 10000 || supplierBP < 0 || supplierBP > 10000 {
		return errs.BadRequest("费率需在 0-10000 基点之间")
	}
	if err := s.repo.UpsertSetting(ctx, settingChef, strconv.Itoa(chefBP)); err != nil {
		return err
	}
	return s.repo.UpsertSetting(ctx, settingSup, strconv.Itoa(supplierBP))
}

// FinanceStats 财务总览：GMV / 结算笔数与金额 / 待打款提现
func (s *serviceImpl) FinanceStats(ctx context.Context) (*FinanceStatsVO, error) {
	gmv, err := s.repo.SumGMV(ctx)
	if err != nil {
		return nil, err
	}
	cnt, amount, err := s.repo.SettlementTotal(ctx)
	if err != nil {
		return nil, err
	}
	pending, err := s.repo.SumWithdrawPending(ctx)
	if err != nil {
		return nil, err
	}
	return &FinanceStatsVO{
		GMV:              gmv,
		SettlementCount:  cnt,
		SettlementAmount: amount,
		WithdrawPending:  pending,
	}, nil
}
