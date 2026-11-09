package pay

import "github.com/xiangchu/server/internal/infra/wxpay"

// PayResult 下单结果：单号 + JSAPI 调起参数
type PayResult struct {
	PaymentNo string           `json:"payment_no"`
	Params    *wxpay.PayParams `json:"params"`
}

// WithdrawApplyReq 发起提现
type WithdrawApplyReq struct {
	Amount int64 `json:"amount" binding:"required,gt=0"` // 分
}

// AuditReq 提现审核
type AuditReq struct {
	Approve bool   `json:"approve"`
	Remark  string `json:"remark" binding:"max=255"`
}

// SettingsReq 费率设置（BP，1% = 100）
type SettingsReq struct {
	ChefBP     int `json:"chef_bp" binding:"gte=0,lte=10000"`
	SupplierBP int `json:"supplier_bp" binding:"gte=0,lte=10000"`
}

// ReconcileRunReq 手动触发对账
type ReconcileRunReq struct {
	BillDate string `json:"bill_date" binding:"required" example:"2026-11-08"` // 账单日，须早于今日
}

// WFilter 管理端提现单筛选
type WFilter struct {
	Role   *int8 `form:"role"`
	Status *int8 `form:"status"`
	Page   int   `form:"page"`
	Size   int   `form:"size"`
}

// SFilter 管理端结算单筛选
type SFilter struct {
	Role *int8 `form:"role"`
	Page int   `form:"page"`
	Size int   `form:"size"`
}

// IncomeVO 收入总览（金额均为分）
type IncomeVO struct {
	TotalNet       int64 `json:"total_net"`
	MonthNet       int64 `json:"month_net"`
	FrozenAmount   int64 `json:"frozen_amount"` // 冻结中：未满冻结期（T+N 自动解冻为可提）
	TotalWithdrawn int64 `json:"total_withdrawn"`
	Available      int64 `json:"available"`
}

// SettingsVO 费率设置视图
type SettingsVO struct {
	ChefBP     int `json:"chef_bp"`
	SupplierBP int `json:"supplier_bp"`
}

// FinanceStatsVO 财务总览
type FinanceStatsVO struct {
	GMV              int64 `json:"gmv"`
	SettlementCount  int64 `json:"settlement_count"`
	SettlementAmount int64 `json:"settlement_amount"`
	WithdrawPending  int64 `json:"withdraw_pending"`
}
