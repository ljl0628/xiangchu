package errs

// Err 业务错误：HTTP 层统一返回 200，靠 Code 区分（0 成功）
type Err struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

func (e *Err) Error() string { return e.Msg }

func New(code int, msg string) *Err { return &Err{Code: code, Msg: msg} }

func BadRequest(msg string) *Err { return New(40000, msg) }

var (
	ErrUnauthorized  = New(40100, "请先登录")
	ErrForbidden     = New(40300, "无权限执行该操作")
	ErrNotFound      = New(40400, "资源不存在")
	ErrServer        = New(50000, "服务开小差了，请稍后重试")
	ErrPhoneInvalid  = New(40001, "手机号格式不正确")
	ErrCodeInvalid   = New(40002, "验证码错误或已过期")
	ErrUserDisabled  = New(40301, "账号已被禁用")
	ErrStatusDenied  = New(40900, "当前状态不允许该操作")
	ErrScheduleTaken = New(40901, "该日期档期已被占用")
	ErrStockShort    = New(40902, "库存不足")
	ErrAmountWrong   = New(40903, "金额校验失败")
	ErrCouponTaken   = New(40904, "该优惠券已领取过")
	ErrCouponSoldOut = New(40905, "优惠券已抢完")
)
