package pay

import "time"

// 业务类型
const (
	BizTypeBanquet  int8 = 1 // 宴席订单
	BizTypePurchase int8 = 2 // 采购单
)

// 支付单状态
const (
	PayStatusPending   int8 = 0 // 待支付
	PayStatusPaid      int8 = 1 // 已支付
	PayStatusRefunding int8 = 2 // 退款中
	PayStatusRefunded  int8 = 3 // 已退款
	PayStatusClosed    int8 = 4 // 已关闭（查单补偿：微信侧未付过期/失败，终态，可重新拉起支付另建支付单）
)

// 结算/提现主体角色
const (
	RoleChef     int8 = 1 // 乡厨
	RoleSupplier int8 = 2 // 供应商
)

// 结算单状态
const (
	SettleStatusPending int8 = 0 // 待结算
	SettleStatusDone    int8 = 1 // 已结算
)

// 提现渠道
const (
	ChannelOffline int8 = 1 // 线下
	ChannelWx      int8 = 2 // 微信
)

// 提现状态
const (
	WithdrawStatusApplied  int8 = 0 // 待审核
	WithdrawStatusApproved int8 = 1 // 已通过待打款
	WithdrawStatusRejected int8 = 2 // 已拒绝
	WithdrawStatusPaid     int8 = 3 // 已打款
)

// Payment 支付单（out_trade_no 即 payment_no，独立存储以保留幂等语义）
type Payment struct {
	ID              int64      `gorm:"primaryKey" json:"id"`
	PaymentNo       string     `gorm:"size:32;uniqueIndex" json:"payment_no"`
	OutTradeNo      string     `gorm:"size:32;uniqueIndex" json:"out_trade_no"`
	BizType         int8       `json:"biz_type"` // 1宴席 2采购
	BizID           int64      `gorm:"index" json:"biz_id"`
	UserID          int64      `gorm:"index" json:"user_id"`
	Amount          int64      `json:"amount"` // 分
	Status          int8       `json:"status"` // 0待支付 1已支付 2退款中 3已退款 4已关闭
	WxTransactionID string     `gorm:"size:64" json:"wx_transaction_id"`
	PayTime         *time.Time `json:"pay_time"`
	ExpireTime      time.Time  `json:"expire_time"` // 2h
	CreatedAt       time.Time  `json:"created_at"`
}

// Settlement 结算单（v1 支付成功即自动结算，status=1）
type Settlement struct {
	ID               int64      `gorm:"primaryKey" json:"id"`
	SettleNo         string     `gorm:"size:32;uniqueIndex" json:"settle_no"`
	Role             int8       `json:"role"` // 1乡厨 2供应商
	TargetID         int64      `gorm:"index" json:"target_id"`
	BizNo            string     `gorm:"size:32" json:"biz_no"`
	GrossAmount      int64      `json:"gross_amount"`
	CommissionAmount int64      `json:"commission_amount"`
	NetAmount        int64      `json:"net_amount"`
	Status           int8       `json:"status"` // 0待结算 1已结算
	SettleTime       *time.Time `json:"settle_time"`
	CreatedAt        time.Time  `json:"created_at"`
}

// Withdrawal 提现单
type Withdrawal struct {
	ID          int64      `gorm:"primaryKey" json:"id"`
	WithdrawNo  string     `gorm:"size:32" json:"withdraw_no"`
	Role        int8       `json:"role"` // 1乡厨 2供应商
	TargetID    int64      `gorm:"index" json:"target_id"`
	Amount      int64      `json:"amount"`
	Channel     int8       `json:"channel"` // 1线下 2微信
	Status      int8       `json:"status"`  // 0待审核 1已通过待打款 2已拒绝 3已打款
	AuditRemark string     `gorm:"size:255" json:"audit_remark"`
	AppliedAt   time.Time  `json:"applied_at"`
	AuditedAt   *time.Time `json:"audited_at"`
	PaidAt      *time.Time `json:"paid_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// SystemSetting 系统设置（费率等 KV）
type SystemSetting struct {
	ID        int64     `gorm:"primaryKey" json:"id"`
	Key       string    `gorm:"size:64;uniqueIndex" json:"key"`
	Value     string    `gorm:"size:255" json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}
