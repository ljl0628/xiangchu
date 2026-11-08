package pay

import (
	"context"
	"errors"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

type Repo struct {
	db *gorm.DB
}

func NewRepo(db *gorm.DB) *Repo { return &Repo{db: db} }

func (r *Repo) q(ctx context.Context) *gorm.DB {
	return r.db.WithContext(ctx)
}

// ---------- payment ----------

func (r *Repo) CreatePayment(ctx context.Context, p *Payment) error {
	return r.q(ctx).Create(p).Error
}

func (r *Repo) GetPaymentByNo(ctx context.Context, no string) (*Payment, error) {
	var p Payment
	if err := r.q(ctx).Where("payment_no = ?", no).First(&p).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *Repo) GetPaymentByOutTradeNo(ctx context.Context, no string) (*Payment, error) {
	var p Payment
	if err := r.q(ctx).Where("out_trade_no = ?", no).First(&p).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

// MarkPaid 待支付 → 已支付（乐观锁，防回调重放/并发重复入账）
func (r *Repo) MarkPaid(ctx context.Context, id int64, txID string) (bool, error) {
	now := time.Now()
	res := r.q(ctx).Model(&Payment{}).
		Where("id = ? AND status = ?", id, PayStatusPending).
		Updates(map[string]any{
			"status":            PayStatusPaid,
			"wx_transaction_id": txID, // 微信交易单号——钱真正划走那一刻，微信给这笔实际资金交易分配的编号
			"pay_time":          &now,
		})
	return res.RowsAffected > 0, res.Error
}

// TransitPayment 按前置状态流转支付单状态，返回是否命中
func (r *Repo) TransitPayment(ctx context.Context, id int64, from, to int8) (bool, error) {
	res := r.q(ctx).Model(&Payment{}).
		Where("id = ? AND status = ?", id, from).
		Update("status", to)
	return res.RowsAffected > 0, res.Error
}

// ListStalePending 查单补偿扫描集：待支付且创建早于 before 的支付单。
// 走 idx_payments_status_created；已补单/已关闭的行转入终态自动退出集合
func (r *Repo) ListStalePending(ctx context.Context, before time.Time, limit int) ([]*Payment, error) {
	var list []*Payment
	err := r.q(ctx).
		Where("status = ? AND created_at < ?", PayStatusPending, before).
		Order("id ASC").
		Limit(limit).
		Find(&list).Error
	return list, err
}

// ListPendingByBiz 业务单当前全部待支付支付单（含已过期未关单），
// 创建幂等的在途判定用；正常情况下最多一笔（uk_payments_active 兜底）
func (r *Repo) ListPendingByBiz(ctx context.Context, bizType int8, bizID int64) ([]*Payment, error) {
	var list []*Payment
	err := r.q(ctx).
		Where("biz_type = ? AND biz_id = ? AND status = ?", bizType, bizID, PayStatusPending).
		Order("id DESC").
		Find(&list).Error
	return list, err
}

// isDuplicateKey MySQL 1062 重复键（项目未开 GORM TranslateError，按驱动错误码判定）
func isDuplicateKey(err error) bool {
	var me *mysqldriver.MySQLError
	return errors.As(err, &me) && me.Number == 1062
}

// SumGMV 已产生实付的支付单总额（含退款中/已退款）
func (r *Repo) SumGMV(ctx context.Context) (int64, error) {
	var sum int64
	err := r.q(ctx).Model(&Payment{}).
		Where("status IN ?", []int8{PayStatusPaid, PayStatusRefunding, PayStatusRefunded}).
		Select("COALESCE(SUM(amount),0)").Scan(&sum).Error
	return sum, err
}

// ---------- refund ----------

func (r *Repo) CreateRefund(ctx context.Context, rf *Refund) error {
	return r.q(ctx).Create(rf).Error
}

func (r *Repo) GetRefundByNo(ctx context.Context, refundNo string) (*Refund, error) {
	var rf Refund
	if err := r.q(ctx).Where("refund_no = ?", refundNo).First(&rf).Error; err != nil {
		return nil, err
	}
	return &rf, nil
}

// TransitRefundStatus 退款单状态条件流转（乐观锁），cols 随行写入（wx_refund_id 等）
func (r *Repo) TransitRefundStatus(ctx context.Context, id int64, from, to int8, cols map[string]any) (bool, error) {
	ups := map[string]any{"status": to}
	for k, v := range cols {
		ups[k] = v
	}
	res := r.q(ctx).Model(&Refund{}).Where("id = ? AND status = ?", id, from).Updates(ups)
	return res.RowsAffected > 0, res.Error
}

// SumRefundQuota 支付单名下占用退款额度的金额合计（在途 0 + 已成功 1；
// 异常单不占额度，人工核对后另行处理）
func (r *Repo) SumRefundQuota(ctx context.Context, paymentNo string) (int64, error) {
	var sum int64
	err := r.q(ctx).Model(&Refund{}).
		Where("payment_no = ? AND status IN ?", paymentNo, []int8{RefundStatusProcessing, RefundStatusSuccess}).
		Select("COALESCE(SUM(amount),0)").Scan(&sum).Error
	return sum, err
}

// ---------- notify ----------

// CreateNotify 通知流水落行；微信通知 ID 撞唯一键即重复推送（由调用方判定）
func (r *Repo) CreateNotify(ctx context.Context, n *PaymentNotify) error {
	return r.q(ctx).Create(n).Error
}

// GetNotifyByWxID 按微信通知 ID 取已有流水行（重复推送时查上次处理结果）
func (r *Repo) GetNotifyByWxID(ctx context.Context, notifyID string) (*PaymentNotify, error) {
	var n PaymentNotify
	if err := r.q(ctx).Where("notify_id = ?", notifyID).First(&n).Error; err != nil {
		return nil, err
	}
	return &n, nil
}

// FinishNotify 回填处理结果（OK/IGNORE/ERROR 与原因）
func (r *Repo) FinishNotify(ctx context.Context, id int64, outTradeNo, result, errMsg string) error {
	return r.q(ctx).Model(&PaymentNotify{}).Where("id = ?", id).
		Updates(map[string]any{
			"out_trade_no": outTradeNo,
			"result":       result,
			"error_msg":    errMsg,
		}).Error
}

// ---------- settlement ----------

func (r *Repo) CreateSettlement(ctx context.Context, s *Settlement) error {
	return r.q(ctx).Create(s).Error
}

func (r *Repo) ListSettlements(ctx context.Context, role *int8, page, size int) ([]*Settlement, int64, error) {
	q := r.q(ctx).Model(&Settlement{})
	if role != nil {
		q = q.Where("role = ?", *role)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []*Settlement
	err := q.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error
	return list, total, err
}

// SumNet 结算净额合计；month 为 true 时只统计当月
func (r *Repo) SumNet(ctx context.Context, role int8, targetID int64, month bool) (int64, error) {
	q := r.q(ctx).Model(&Settlement{}).
		Where("role = ? AND target_id = ?", role, targetID)
	if month {
		// 注意：datetime 列与 "YYYY-MM" 字符串比较，字面量报 1525、参数化静默匹配不到行，
		// 必须传真实时间值（当月 1 号 0 点）
		now := time.Now()
		q = q.Where("created_at >= ?", time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()))
	}
	var sum int64
	err := q.Select("COALESCE(SUM(net_amount),0)").Scan(&sum).Error
	return sum, err
}

// SumNetReleased 已过冻结期的已结算净额合计（settle_time <= deadline 的部分才可提）
func (r *Repo) SumNetReleased(ctx context.Context, role int8, targetID int64, deadline time.Time) (int64, error) {
	var sum int64
	err := r.q(ctx).Model(&Settlement{}).
		Where("role = ? AND target_id = ? AND status = ?", role, targetID, SettleStatusDone).
		Where("settle_time <= ?", deadline).
		Select("COALESCE(SUM(net_amount),0)").Scan(&sum).Error
	return sum, err
}

// ---------- withdrawal ----------

func (r *Repo) CreateWithdrawal(ctx context.Context, w *Withdrawal) error {
	return r.q(ctx).Create(w).Error
}

func (r *Repo) GetWithdrawal(ctx context.Context, id int64) (*Withdrawal, error) {
	var w Withdrawal
	if err := r.q(ctx).First(&w, id).Error; err != nil {
		return nil, err
	}
	return &w, nil
}

func (r *Repo) ListWithdrawalsByTarget(ctx context.Context, role int8, targetID int64) ([]*Withdrawal, error) {
	var list []*Withdrawal
	err := r.q(ctx).Where("role = ? AND target_id = ?", role, targetID).
		Order("id DESC").Find(&list).Error
	return list, err
}

func (r *Repo) ListWithdrawals(ctx context.Context, f *WFilter, page, size int) ([]*Withdrawal, int64, error) {
	q := r.q(ctx).Model(&Withdrawal{})
	if f.Role != nil {
		q = q.Where("role = ?", *f.Role)
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []*Withdrawal
	err := q.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error
	return list, total, err
}

// TransitWithdrawal 按前置状态流转提现单，并按需写入字段
func (r *Repo) TransitWithdrawal(ctx context.Context, id int64, from, to int8, fields map[string]any) (bool, error) {
	ups := map[string]any{"status": to}
	for k, v := range fields {
		ups[k] = v
	}
	res := r.q(ctx).Model(&Withdrawal{}).
		Where("id = ? AND status = ?", id, from).Updates(ups)
	return res.RowsAffected > 0, res.Error
}

// SumWithdrawn 按状态集合合计提现金额（占用余额的状态）
func (r *Repo) SumWithdrawn(ctx context.Context, role int8, targetID int64, statuses []int8) (int64, error) {
	var sum int64
	err := r.q(ctx).Model(&Withdrawal{}).
		Where("role = ? AND target_id = ? AND status IN ?", role, targetID, statuses).
		Select("COALESCE(SUM(amount),0)").Scan(&sum).Error
	return sum, err
}

// SumWithdrawPending 待打款提现总额（待审核 + 已通过）
func (r *Repo) SumWithdrawPending(ctx context.Context) (int64, error) {
	var sum int64
	err := r.q(ctx).Model(&Withdrawal{}).
		Where("status IN ?", []int8{WithdrawStatusApplied, WithdrawStatusApproved}).
		Select("COALESCE(SUM(amount),0)").Scan(&sum).Error
	return sum, err
}

// ---------- settings / stats ----------

func (r *Repo) GetSetting(ctx context.Context, key string) (string, error) {
	var s SystemSetting
	if err := r.q(ctx).Where("`key` = ?", key).First(&s).Error; err != nil {
		return "", err
	}
	return s.Value, nil
}

func (r *Repo) UpsertSetting(ctx context.Context, key, value string) error {
	s := SystemSetting{Key: key, Value: value}
	return r.q(ctx).Where(SystemSetting{Key: key}).
		Assign(SystemSetting{Value: value}).FirstOrCreate(&s).Error
}

// SettlementTotal 结算单笔数与总额
func (r *Repo) SettlementTotal(ctx context.Context) (int64, int64, error) {
	var out struct {
		Cnt int64
		Sum int64
	}
	err := r.q(ctx).Model(&Settlement{}).
		Select("COUNT(*) AS cnt, COALESCE(SUM(gross_amount),0) AS sum").Scan(&out).Error
	return out.Cnt, out.Sum, err
}

// ---------- 跨模块只读白名单（约定允许的原生 SQL） ----------

// ChefIDByUser 取该用户已通过审核的乡厨 ID
func (r *Repo) ChefIDByUser(ctx context.Context, uid int64) (int64, error) {
	return r.targetIDByUser(ctx, "chefs", uid)
}

// SupplierIDByUser 取该用户已通过审核的供应商 ID
func (r *Repo) SupplierIDByUser(ctx context.Context, uid int64) (int64, error) {
	return r.targetIDByUser(ctx, "suppliers", uid)
}

func (r *Repo) targetIDByUser(ctx context.Context, table string, uid int64) (int64, error) {
	var id int64
	err := r.q(ctx).
		Raw("SELECT id FROM `"+table+"` WHERE user_id = ? AND status = 1 ORDER BY id DESC LIMIT 1", uid).
		Scan(&id).Error
	if err != nil {
		return 0, err
	}
	if id == 0 {
		return 0, gorm.ErrRecordNotFound
	}
	return id, nil
}
