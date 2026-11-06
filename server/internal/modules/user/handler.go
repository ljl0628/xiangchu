package user

import (
	"fmt"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/xiangchu/server/internal/middleware"
	"github.com/xiangchu/server/internal/pkg/errs"
	"github.com/xiangchu/server/internal/pkg/jwtx"
	"github.com/xiangchu/server/internal/pkg/pagination"
	"github.com/xiangchu/server/internal/pkg/response"
)

// Handler 用户模块 HTTP 入口
type Handler struct {
	svc Service
	mk  *jwtx.Maker
}

func NewHandler(svc Service, mk *jwtx.Maker) *Handler {
	return &Handler{svc: svc, mk: mk}
}

// Routes 注册路由：登录/验证码公开，其余需登录（券中心需登录，见 g 组）
func (h *Handler) Routes(r gin.IRouter) {
	pub := r.Group("")
	{
		pub.POST("/user/auth/code", h.sendCode)
		pub.POST("/user/auth/login", h.loginByCode)
		pub.POST("/user/auth/wx", h.wxLogin)
		pub.POST("/user/auth/web", h.webLogin)
	}

	g := r.Group("", middleware.Auth(h.mk, "user"))
	{
		g.POST("/user/auth/refresh", h.refresh)
		g.GET("/user/profile", h.profile)
		g.PUT("/user/profile", h.updateProfile)
		g.PUT("/user/password", h.setPassword)
		g.GET("/user/addresses", h.listAddresses)
		g.POST("/user/addresses", h.saveAddress)
		g.PUT("/user/addresses/:id", h.saveAddress)
		g.DELETE("/user/addresses/:id", h.deleteAddress)
		g.GET("/coupons", h.listCoupons)
		g.GET("/user/coupons", h.myCoupons)
		g.POST("/user/coupons/:id/receive", h.receiveCoupon)
	}

	adm := r.Group("/admin", middleware.Auth(h.mk, "admin"))
	{
		adm.GET("/users", h.adminListUsers)
		adm.POST("/users/:id/status", h.adminSetUserStatus)

		// 券模板管理（管理端）
		adm.GET("/coupons", h.adminListCoupons)
		adm.POST("/coupons", h.adminCreateCoupon)
		adm.PUT("/coupons/:id", h.adminUpdateCoupon)
		adm.POST("/coupons/:id/status", h.adminSetCouponStatus)
		adm.POST("/coupons/:id/grant", h.adminGrantCoupon)
	}
}

// sendCode 发送登录验证码
//
//	@Tags		用户域
//	@Summary	发送验证码
//	@Description	dev 模式验证码固定 123456，仅输出日志
//	@Accept		json
//	@Produce	json
//	@Param		req	body		user.SmsCodeReq	true	"手机号"
//	@Success	200	{object}	response.R
//	@Router		/user/auth/code [post]
func (h *Handler) sendCode(c *gin.Context) {
	var req SmsCodeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, err)
		return
	}
	if err := h.svc.SendCode(c.Request.Context(), req.Phone); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// loginByCode 手机号+验证码登录
//
//	@Tags		用户域
//	@Summary	手机号验证码登录
//	@Description	未注册自动注册；乡厨/供应商双身份按档案状态签发对应角色 JWT
//	@Accept		json
//	@Produce	json
//	@Param		req	body		user.LoginByCodeReq	true	"手机号+验证码"
//	@Success	200	{object}	response.R{data=user.LoginResult}	"data.token 为 JWT"
//	@Router		/user/auth/login [post]
func (h *Handler) loginByCode(c *gin.Context) {
	var req LoginByCodeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, err)
		return
	}
	res, err := h.svc.LoginByCode(c.Request.Context(), req.Phone, req.Code)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, res)
}

// wxLogin 微信 code 登录
//
//	@Tags		用户域
//	@Summary	微信授权登录
//	@Description	小程序 wx.login 的 code 换 openid，未注册自动注册
//	@Accept		json
//	@Produce	json
//	@Param		req	body		user.WxLoginReq	true	"微信 code"
//	@Success	200	{object}	response.R{data=user.LoginResult}
//	@Router		/user/auth/wx [post]
func (h *Handler) wxLogin(c *gin.Context) {
	var req WxLoginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, err)
		return
	}
	res, err := h.svc.WxLogin(c.Request.Context(), req.Code)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, res)
}

// webLogin Web 手机号+密码登录
//
//	@Tags		用户域
//	@Summary	Web 后台登录（乡厨/供应商）
//	@Description	校验存在已审核通过的 chef/supplier 档案后签发对应角色 JWT
//	@Accept		json
//	@Produce	json
//	@Param		req	body		user.WebLoginReq	true	"手机号+密码"
//	@Success	200	{object}	response.R{data=user.LoginResult}
//	@Router		/user/auth/web [post]
func (h *Handler) webLogin(c *gin.Context) {
	var req WebLoginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, err)
		return
	}
	res, err := h.svc.WebLogin(c.Request.Context(), req.Phone, req.Password)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, res)
}

// refresh POST /user/auth/refresh 角色变更（入驻审核通过/驳回）后换发含最新角色的 JWT。
// body 可选：双身份账号（既是乡厨又是供应商）按期望角色签发
//
//	@Tags		用户域
//	@Security	BearerAuth
//	@Summary	换发 token（角色变更后调用）
//	@Description	入驻审核通过/驳回后换发含最新角色的 JWT。body 可选：双身份账号传期望角色（chef/supplier）
//	@Accept		json
//	@Produce	json
//	@Param		req	body		user.RefreshReq	false	"期望角色（可空 body）"
//	@Success	200	{object}	response.R{data=user.LoginResult}
//	@Router		/user/auth/refresh [post]
func (h *Handler) refresh(c *gin.Context) {
	var req RefreshReq
	_ = c.ShouldBindJSON(&req) // body 可选，空 body 忽略
	res, err := h.svc.RefreshSession(c.Request.Context(), middleware.UID(c), req.Role)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, res)
}

// profile 我的资料
//
//	@Tags		用户域
//	@Security	BearerAuth
//	@Summary	我的资料
//	@Produce	json
//	@Success	200	{object}	response.R{data=user.ProfileVO}
//	@Router		/user/profile [get]
func (h *Handler) profile(c *gin.Context) {
	res, err := h.svc.Profile(c.Request.Context(), middleware.UID(c))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, res)
}

// updateProfile 修改资料
//
//	@Tags		用户域
//	@Security	BearerAuth
//	@Summary	修改昵称/头像
//	@Accept		json
//	@Produce	json
//	@Param		req	body		user.UpdateProfileReq	true	"资料"
//	@Success	200	{object}	response.R
//	@Router		/user/profile [put]
func (h *Handler) updateProfile(c *gin.Context) {
	var req UpdateProfileReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, err)
		return
	}
	if err := h.svc.UpdateProfile(c.Request.Context(), middleware.UID(c), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// setPassword PUT /user/password 设置/修改 Web 登录密码
//
//	@Tags		用户域
//	@Security	BearerAuth
//	@Summary	设置/修改 Web 登录密码
//	@Accept		json
//	@Produce	json
//	@Param		req	body		user.SetPasswordReq	true	"密码"
//	@Success	200	{object}	response.R
//	@Router		/user/password [put]
func (h *Handler) setPassword(c *gin.Context) {
	var req SetPasswordReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, err)
		return
	}
	if err := h.svc.SetPassword(c.Request.Context(), middleware.UID(c), req.Password); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// ---------- 地址簿 ----------

// listAddresses 地址簿列表
//
//	@Tags		用户域
//	@Security	BearerAuth
//	@Summary	地址簿列表
//	@Produce	json
//	@Success	200	{object}	response.R{data=[]user.UserAddress}
//	@Router		/user/addresses [get]
func (h *Handler) listAddresses(c *gin.Context) {
	list, err := h.svc.ListAddress(c.Request.Context(), middleware.UID(c))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, list)
}

// saveAddress 新增/修改地址（POST 新增，PUT 需带 id）
//
//	@Tags		用户域
//	@Security	BearerAuth
//	@Summary	新增/修改地址
//	@Accept		json
//	@Produce	json
//	@Param		id	path		int				false	"地址ID（修改时必传）"
//	@Param		req	body		user.AddressReq	true	"地址"
//	@Success	200	{object}	response.R{data=map[string]int64}	"data.id=地址ID"
//	@Router		/user/addresses [post]
//	@Router		/user/addresses/{id} [put]
func (h *Handler) saveAddress(c *gin.Context) {
	var req AddressReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, err)
		return
	}
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	res, err := h.svc.SaveAddress(c.Request.Context(), middleware.UID(c), id, &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, gin.H{"id": res})
}

// deleteAddress 删除地址
//
//	@Tags		用户域
//	@Security	BearerAuth
//	@Summary	删除地址
//	@Produce	json
//	@Param		id	path		int	true	"地址ID"
//	@Success	200	{object}	response.R
//	@Router		/user/addresses/{id} [delete]
func (h *Handler) deleteAddress(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.svc.DeleteAddress(c.Request.Context(), middleware.UID(c), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// ---------- 优惠券 ----------

// listCoupons 券中心（需登录）
//
//	@Tags		用户域
//	@Summary	券中心列表
//	@Description	需登录；received 标记当前用户已持有的券
//	@Produce	json
//	@Param		only_active	query		string	false	"仅有效券"	Default(1)	Enums(0,1)
//	@Param		page		query		int		false	"页码"	Default(1)
//	@Param		size		query		int		false	"每页条数"	Default(10)	Maximum(100)
//	@Success	200	{object}	response.R{data=response.PageList{list=[]user.CouponVO}}
//	@Router		/coupons [get]
func (h *Handler) listCoupons(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "10"))
	page, size, err := pagination.Normalize(page, size)
	if err != nil {
		response.Fail(c, err)
		return
	}
	onlyActive := c.DefaultQuery("only_active", "1") == "1"
	list, total, err := h.svc.ListCoupon(c.Request.Context(), middleware.UID(c), onlyActive, page, size)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, response.PageData(list, total, page, size))
}

// myCoupons 我的优惠券
//
//	@Tags		用户域
//	@Security	BearerAuth
//	@Summary	我的优惠券
//	@Produce	json
//	@Success	200	{object}	response.R{data=[]user.UserCouponVO}
//	@Router		/user/coupons [get]
func (h *Handler) myCoupons(c *gin.Context) {
	list, err := h.svc.MyCoupons(c.Request.Context(), middleware.UID(c))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, list)
}

// receiveCoupon 领取优惠券
//
//	@Tags		用户域
//	@Security	BearerAuth
//	@Summary	领券
//	@Produce	json
//	@Param		id	path		int	true	"券ID"
//	@Success	200	{object}	response.R
//	@Router		/user/coupons/{id}/receive [post]
func (h *Handler) receiveCoupon(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.svc.ReceiveCoupon(c.Request.Context(), middleware.UID(c), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// ---------- 管理端 ----------

// adminListUsers 用户列表（手机号/昵称关键词）
//
//	@Tags		管理端
//	@Security	BearerAuth
//	@Summary	用户列表
//	@Description	admin 角色
//	@Produce	json
//	@Param		keyword	query		string	false	"手机号/昵称关键词"
//	@Param		page	query		int		false	"页码"	Default(1)
//	@Param		size	query		int		false	"每页条数"	Default(10)	Maximum(100)
//	@Success	200	{object}	response.R{data=response.PageList{list=[]user.AdminUserVO}}
//	@Router		/admin/users [get]
func (h *Handler) adminListUsers(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "10"))
	page, size, err := pagination.Normalize(page, size)
	if err != nil {
		response.Fail(c, err)
		return
	}
	list, total, err := h.svc.ListForAdmin(c.Request.Context(), c.Query("keyword"), page, size)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, response.PageData(list, total, page, size))
}

// adminSetUserStatus 禁用/启用用户
//
//	@Tags		管理端
//	@Security	BearerAuth
//	@Summary	禁用/启用用户
//	@Description	admin 角色
//	@Accept		json
//	@Produce	json
//	@Param		id	path		int				true	"用户ID"
//	@Param		req	body		user.StatusReq	true	"状态"
//	@Success	200	{object}	response.R
//	@Router		/admin/users/{id}/status [post]
func (h *Handler) adminSetUserStatus(c *gin.Context) {
	var req StatusReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, err)
		return
	}
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.svc.SetStatus(c.Request.Context(), id, *req.Status); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// ---------- 管理端优惠券 ----------

// adminListCoupons 券模板列表（管理端）
//
//	@Tags		管理端
//	@Security	BearerAuth
//	@Summary	优惠券模板列表
//	@Description	admin 角色；含下架券，可按状态筛选
//	@Param		status	query	int	false	"状态 1上架 0下架，缺省全部"
//	@Param		page	query	int	false	"页码"	Default(1)
//	@Param		size	query	int	false	"每页条数"	Default(10)	Maximum(100)
//	@Success	200	{object}	response.R{data=response.PageList{list=[]user.AdminCouponVO}}
//	@Router		/admin/coupons [get]
func (h *Handler) adminListCoupons(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "10"))
	page, size, err := pagination.Normalize(page, size)
	if err != nil {
		response.Fail(c, err)
		return
	}
	var status *int8
	if v := c.DefaultQuery("status", ""); v != "" {
		if n, e := strconv.Atoi(v); e == nil {
			st := int8(n)
			status = &st
		}
	}
	list, total, err := h.svc.ListCouponForAdmin(c.Request.Context(), status, page, size)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, response.PageData(list, total, page, size))
}

// adminCreateCoupon 创建券模板（管理端）
//
//	@Tags		管理端
//	@Security	BearerAuth
//	@Summary	创建优惠券模板
//	@Description	admin 角色；金额单位分，暂仅满减券（type=1）
//	@Accept		json
//	@Produce	json
//	@Param		req	body	user.CouponReq	true	"券模板参数"
//	@Success	200	{object}	response.R{data=map[string]int64}
//	@Router		/admin/coupons [post]
func (h *Handler) adminCreateCoupon(c *gin.Context) {
	var req CouponReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, errs.BadRequest("参数有误："+err.Error()))
		return
	}
	id, err := h.svc.CreateCoupon(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, gin.H{"id": id})
}

// adminUpdateCoupon 编辑券模板（管理端）
//
//	@Tags		管理端
//	@Security	BearerAuth
//	@Summary	编辑优惠券模板
//	@Description	admin 角色；received_count/status 不经此入口；总量不得小于已领数
//	@Accept		json
//	@Produce	json
//	@Param		id	path		int	true	"券ID"
//	@Param		req	body	user.CouponReq	true	"券模板参数"
//	@Success	200	{object}	response.R
//	@Router		/admin/coupons/{id} [put]
func (h *Handler) adminUpdateCoupon(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req CouponReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, errs.BadRequest("参数有误："+err.Error()))
		return
	}
	if err := h.svc.UpdateCoupon(c.Request.Context(), id, &req); err != nil {
		// a, _ := json.MarshalIndent(err, "", "")
		fmt.Println("======>", err)
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// adminSetCouponStatus 上架/下架券模板（管理端）
//
//	@Tags		管理端
//	@Security	BearerAuth
//	@Summary	上下架优惠券模板
//	@Description	admin 角色；下架只挡领取与发放，不阻断已领券在有效期内使用
//	@Accept		json
//	@Produce	json
//	@Param		id	path		int	true	"券ID"
//	@Param		req	body	user.StatusReq	true	"{status: 0|1}"
//	@Success	200	{object}	response.R
//	@Router		/admin/coupons/{id}/status [post]
func (h *Handler) adminSetCouponStatus(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req StatusReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, errs.BadRequest("参数有误："+err.Error()))
		return
	}
	if req.Status == nil {
		response.Fail(c, errs.BadRequest("status 不能为空"))
		return
	}
	if err := h.svc.SetCouponStatus(c.Request.Context(), id, *req.Status); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// adminGrantCoupon 手动发放（管理端）
//
//	@Tags		管理端
//	@Security	BearerAuth
//	@Summary	发放优惠券给指定用户
//	@Description	admin 角色；已持有的用户自动跳过；剩余量不足整体回滚
//	@Accept		json
//	@Produce	json
//	@Param		id	path		int				true	"券ID"
//	@Param		req	body	user.GrantReq	true	"用户ID列表（≤500）"
//	@Success	200	{object}	response.R{data=user.GrantResult}
//	@Router		/admin/coupons/{id}/grant [post]
func (h *Handler) adminGrantCoupon(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req GrantReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, errs.BadRequest("参数有误："+err.Error()))
		return
	}
	result, err := h.svc.GrantCoupon(c.Request.Context(), id, req.UserIDs)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, result)
}
