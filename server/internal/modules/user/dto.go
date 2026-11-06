package user

import "time"

// AdminUserVO 管理端用户列表项（roles 为按档案派生的全部角色，均无档案则 ["user"]）
type AdminUserVO struct {
	ID        int64     `json:"id"`
	Phone     string    `json:"phone"`
	Nickname  string    `json:"nickname"`
	Avatar    string    `json:"avatar"`
	Status    int8      `json:"status"`
	Roles     []string  `json:"roles"` // chef/supplier，均无则 ["user"]
	CreatedAt time.Time `json:"created_at"`
}

// SmsCodeReq sends a login sms code.
type SmsCodeReq struct {
	Phone string `json:"phone" binding:"required,len=11"`
}

// LoginByCodeReq logs in with phone + code.
type LoginByCodeReq struct {
	Phone string `json:"phone" binding:"required,len=11"`
	Code  string `json:"code" binding:"required,len=6"`
}

// WxLoginReq logs in via wechat code.
type WxLoginReq struct {
	Code string `json:"code" binding:"required"`
}

// WebLoginReq logs into web admin via phone + password.
type WebLoginReq struct {
	Phone    string `json:"phone" binding:"required,len=11"`
	Password string `json:"password" binding:"required,min=6,max=32"`
}

// LoginResult returned after login.
type LoginResult struct {
	Token  string `json:"token"`
	Role   string `json:"role"`
	UserID int64  `json:"user_id"`
	IsNew  bool   `json:"is_new"`
}

// RefreshReq 换发 token 的可选 body：双身份账号按期望角色签发
type RefreshReq struct {
	Role string `json:"role"`
}

// StatusReq 禁用/启用状态
type StatusReq struct {
	Status *int8 `json:"status" binding:"required"`
}

// UpdateProfileReq updates nickname/avatar.
type UpdateProfileReq struct {
	Nickname string `json:"nickname"`
	Avatar   string `json:"avatar"`
}

// SetPasswordReq 设置/修改 Web 登录密码
type SetPasswordReq struct {
	Password string `json:"password" binding:"required,min=6,max=32"`
}

// ProfileVO is the user profile view.
type ProfileVO struct {
	ID       int64    `json:"id"`
	Phone    string   `json:"phone"`
	Nickname string   `json:"nickname"`
	Avatar   string   `json:"avatar"`
	Roles    []string `json:"roles"`
}

// AddressReq creates or updates an address.
type AddressReq struct {
	ContactName  string `json:"contact_name" binding:"required"`
	ContactPhone string `json:"contact_phone" binding:"required,len=11"`
	Region       string `json:"region"`
	Address      string `json:"address" binding:"required"`
	IsDefault    bool   `json:"is_default"`
}

// CouponVO is the platform coupon view.
type CouponVO struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Type      int8      `json:"type"`
	Threshold int64     `json:"threshold"`
	Amount    int64     `json:"amount"`
	ValidTo   time.Time `json:"valid_to"`
	Received  bool      `json:"received"` // 当前用户已持有该券（未登录恒为 false）
}

// UserCouponVO is a coupon held by a user.
type UserCouponVO struct {
	ID        int64     `json:"id"`
	CouponID  int64     `json:"coupon_id"`
	Name      string    `json:"name"`
	Type      int8      `json:"type"`
	Threshold int64     `json:"threshold"`
	Amount    int64     `json:"amount"`
	Status    int8      `json:"status"` // 0未用 2订单占用 1已使用
	ExpireAt  time.Time `json:"expire_at"`
}

// AdminCouponVO 管理端券模板全字段视图
type AdminCouponVO struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	Type          int8      `json:"type"`
	Threshold     int64     `json:"threshold"` // 分
	Amount        int64     `json:"amount"`    // 分
	ValidFrom     time.Time `json:"valid_from"`
	ValidTo       time.Time `json:"valid_to"`
	TotalCount    int       `json:"total_count"`
	ReceivedCount int       `json:"received_count"`
	Status        int8      `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
}

// CouponReq 创建/编辑券模板（金额单位分；received_count/status 不经此入口）
type CouponReq struct {
	Name       string    `json:"name" binding:"required,max=64"`
	Type       int8      `json:"type" binding:"required"` // 目前仅 1=满减
	Threshold  int64     `json:"threshold" binding:"min=0"`
	Amount     int64     `json:"amount" binding:"min=1"`
	ValidFrom  time.Time `json:"valid_from" binding:"required"`
	ValidTo    time.Time `json:"valid_to" binding:"required"`
	TotalCount int       `json:"total_count" binding:"min=1"`
}

// GrantReq 手动发放
type GrantReq struct {
	UserIDs []int64 `json:"user_ids" binding:"required,min=1,max=500"`
}

// GrantResult 发放结果
type GrantResult struct {
	Granted int64 `json:"granted"`
	Skipped int64 `json:"skipped"`
}
