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

// 退款单状态
const (
	RefundStatusProcessing int8 = 0 // 退款中（微信已受理，等回调确认）
	RefundStatusSuccess    int8 = 1 // 退款成功（回调确认 / mock 即时）
	RefundStatusAbnormal   int8 = 2 // 异常（受理失败/退款关闭，人工核对）
)

// 通知流水处理结果
const (
	NotifyResultOK     = "OK"     // 处理成功
	NotifyResultIgnore = "IGNORE" // 收到但不需处理（如非 SUCCESS 状态）
	NotifyResultError  = "ERROR"  // 处理失败（微信将重推，行内回填原因）
)

// Payment 支付单（out_trade_no 即 payment_no，独立存储以保留幂等语义）
type Payment struct {
	ID              int64      `gorm:"primaryKey" json:"id"`
	PaymentNo       string     `gorm:"size:32;uniqueIndex" json:"payment_no"`
	OutTradeNo      string     `gorm:"size:32;uniqueIndex" json:"out_trade_no"`
	BizType         int8       `json:"biz_type"` // 1宴席 2采购
	BizID           int64      `gorm:"index" json:"biz_id"`
	UserID          int64      `gorm:"index" json:"user_id"`
	Amount          int64      `json:"amount"`                           // 分
	Status          int8       `json:"status"`                           // 0待支付 1已支付 2退款中 3已退款 4已关闭
	WxTransactionID string     `gorm:"size:64" json:"wx_transaction_id"` // 微信交易单号——钱真正划走那一刻，微信给这笔实际资金交易分配的编号
	PayTime         *time.Time `json:"pay_time"`
	ExpireTime      time.Time  `json:"expire_time"` // 2h
	CreatedAt       time.Time  `json:"created_at"`
}

// Refund 退款单。微信退款以交易（支付单）为单位，与订单无直接关系，
// 故挂在 payment_no 上；"订单累计退了多少"经 payments → refunds 聚合得出。
// refund_no 同时作为 out_refund_no 传微信（对内对外同号，同 payments 的双号约定）
type Refund struct {
	ID          int64      `gorm:"primaryKey" json:"id"`
	RefundNo    string     `gorm:"size:32;uniqueIndex" json:"refund_no"`
	PaymentNo   string     `gorm:"size:32;index" json:"payment_no"` // 锚定支付单
	BizType     int8       `json:"biz_type"`                        // 冗余自支付单，免回查
	BizID       int64      `gorm:"index" json:"biz_id"`
	Amount      int64      `json:"amount"`                      // 退款金额（分），部分退款即多行
	Status      int8       `json:"status"`                      // 0退款中 1成功 2异常
	WxRefundID  string     `gorm:"size:64" json:"wx_refund_id"` // 微信退款单号，回调回填
	Reason      string     `gorm:"size:255" json:"reason"`
	SuccessTime *time.Time `json:"success_time"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// PaymentNotify 微信回调通知流水（append-only 审计）：一行一次推送。
// uk = 微信通知 ID（重试推送同 ID）；先落行再处理，处理结果回填本行。
// NotifyID 为空记 NULL——唯一键忽略 NULL，mock 明文回调（无信封 ID）不冲突
type PaymentNotify struct {
	ID         int64     `gorm:"primaryKey" json:"id"`
	NotifyID   *string   `gorm:"size:64;uniqueIndex" json:"notify_id"`
	EventType  string    `gorm:"size:32" json:"event_type"`
	OutTradeNo string    `gorm:"size:32;index" json:"out_trade_no"`
	RawBody    string    `gorm:"type:text" json:"raw_body"` // resource 解密明文，排障用
	Result     string    `gorm:"size:16" json:"result"`     // OK / IGNORE / ERROR
	ErrorMsg   string    `gorm:"size:255" json:"error_msg"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
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
