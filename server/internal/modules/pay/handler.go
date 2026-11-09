package pay

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/xiangchu/server/internal/middleware"
	"github.com/xiangchu/server/internal/pkg/errs"
	"github.com/xiangchu/server/internal/pkg/jwtx"
	"github.com/xiangchu/server/internal/pkg/response"
)

// Handler 财务域 HTTP 层
type Handler struct {
	svc Service
}

// NewHandler 构造财务域 Handler
func NewHandler(svc Service) *Handler { return &Handler{svc: svc} }

// Routes 注册财务域路由
func (p *Handler) Routes(r gin.IRouter, mk *jwtx.Maker) {
	// 微信支付回调（微信服务器调用，不走鉴权）
	r.POST("/pay/notify/wx", p.wxNotify)

	// 联调模拟支付（登录即可，便于开发环境调通全流程）
	user := r.Group("", middleware.Auth(mk, "user"))
	user.POST("/pay/mock/:paymentNo", p.mockPay)

	// 乡厨：收入与提现
	chef := r.Group("", middleware.Auth(mk, "chef"))
	{
		chef.GET("/chef/income", p.chefIncome)
		chef.POST("/chef/withdrawals", p.chefWithdrawApply)
		chef.GET("/chef/withdrawals", p.chefWithdrawals)
	}

	// 供应商：收入与提现
	supplier := r.Group("", middleware.Auth(mk, "supplier"))
	{
		supplier.GET("/supplier/income", p.supplierIncome)
		supplier.POST("/supplier/withdrawals", p.supplierWithdrawApply)
		supplier.GET("/supplier/withdrawals", p.supplierWithdrawals)
	}

	// 管理端：结算 / 提现审核 / 费率 / 总览
	admin := r.Group("", middleware.Auth(mk, "admin"))
	{
		admin.GET("/admin/settlements", p.adminSettlements)
		admin.GET("/admin/withdrawals", p.adminWithdrawals)
		admin.POST("/admin/withdrawals/:id/audit", p.auditWithdrawal)
		admin.POST("/admin/withdrawals/:id/mark-paid", p.markWithdrawalPaid)
		admin.GET("/admin/settings", p.getSettings)
		admin.PUT("/admin/settings", p.putSettings)
		admin.GET("/admin/finance-stats", p.financeStats)
		admin.GET("/admin/reconciles", p.listReconciles)
		admin.POST("/admin/reconciles", p.runReconcile)
		admin.GET("/admin/reconciles/:id/diffs", p.listReconcileDiffs)
	}
}

// wxNotify 微信支付回调：按 V3 规范应答 SUCCESS/FAIL
//
//	@Tags		财务域
//	@Summary	微信支付回调
//	@Description	微信服务器调用，不走鉴权、非 R 信封；按 V3 规范应答 SUCCESS/FAIL。真实模式验签+解密（平台证书 + APIv3 key），mock 模式接受明文 JSON
//	@Accept		json
//	@Produce	json
//	@Success	200	{object}	map[string]string	"{code:SUCCESS|FAIL, message}"
//	@Router		/pay/notify/wx [post]
func (p *Handler) wxNotify(c *gin.Context) {
	body, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "FAIL", "message": "读取请求体失败"})
		return
	}
	no, err := p.svc.HandleWxNotify(c.Request.Context(), c.Request.Header, body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": "FAIL", "message": "处理失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": "SUCCESS", "message": "成功", "payment_no": no})
}

// mockPay 联调模拟支付
//
//	@Tags		财务域
//	@Security	BearerAuth
//	@Summary	模拟支付成功（dev）
//	@Description	开发联调用，等价于微信回调成功；触发业务侧支付成功处理（锁档期/锁库存等）
//	@Produce	json
//	@Param		paymentNo	path		string	true	"支付单号"
//	@Success	200	{object}	response.R{data=map[string]string}
//	@Router		/pay/mock/{paymentNo} [post]
func (p *Handler) mockPay(c *gin.Context) {
	if err := p.svc.MockPay(c.Request.Context(), c.Param("paymentNo")); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, gin.H{"payment_no": c.Param("paymentNo")})
}

// ---------- 乡厨 / 供应商 ----------

// chefIncome 乡厨收入总览
//
//	@Tags		财务域
//	@Security	BearerAuth
//	@Summary	乡厨收入总览
//	@Description	chef 角色；金额均为分
//	@Produce	json
//	@Success	200	{object}	response.R{data=pay.IncomeVO}
//	@Router		/chef/income [get]
func (p *Handler) chefIncome(c *gin.Context) {
	p.income(c, RoleChef)
}

// supplierIncome 供应商收入总览
//
//	@Tags		财务域
//	@Security	BearerAuth
//	@Summary	供应商收入总览
//	@Description	supplier 角色；金额均为分
//	@Produce	json
//	@Success	200	{object}	response.R{data=pay.IncomeVO}
//	@Router		/supplier/income [get]
func (p *Handler) supplierIncome(c *gin.Context) {
	p.income(c, RoleSupplier)
}

func (p *Handler) income(c *gin.Context, role int8) {
	vo, err := p.svc.Income(c.Request.Context(), middleware.UID(c), role)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, vo)
}

// chefWithdrawApply 乡厨发起提现
//
//	@Tags		财务域
//	@Security	BearerAuth
//	@Summary	发起提现
//	@Description	chef 角色；一期线下打款，admin 审核后标记
//	@Accept		json
//	@Produce	json
//	@Param		req	body		pay.WithdrawApplyReq	true	"金额（分）"
//	@Success	200	{object}	response.R
//	@Router		/chef/withdrawals [post]
func (p *Handler) chefWithdrawApply(c *gin.Context) {
	p.withdrawApply(c, RoleChef)
}

// supplierWithdrawApply 供应商发起提现
//
//	@Tags		财务域
//	@Security	BearerAuth
//	@Summary	发起提现
//	@Description	supplier 角色；一期线下打款，admin 审核后标记
//	@Accept		json
//	@Produce	json
//	@Param		req	body		pay.WithdrawApplyReq	true	"金额（分）"
//	@Success	200	{object}	response.R
//	@Router		/supplier/withdrawals [post]
func (p *Handler) supplierWithdrawApply(c *gin.Context) {
	p.withdrawApply(c, RoleSupplier)
}

func (p *Handler) withdrawApply(c *gin.Context, role int8) {
	var req WithdrawApplyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, errs.BadRequest("金额格式不正确"))
		return
	}
	if err := p.svc.WithdrawApply(c.Request.Context(), middleware.UID(c), role, req.Amount); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// chefWithdrawals 乡厨提现记录
//
//	@Tags		财务域
//	@Security	BearerAuth
//	@Summary	提现记录
//	@Description	chef 角色
//	@Produce	json
//	@Success	200	{object}	response.R{data=[]pay.Withdrawal}
//	@Router		/chef/withdrawals [get]
func (p *Handler) chefWithdrawals(c *gin.Context) {
	p.myWithdrawals(c, RoleChef)
}

// supplierWithdrawals 供应商提现记录
//
//	@Tags		财务域
//	@Security	BearerAuth
//	@Summary	提现记录
//	@Description	supplier 角色
//	@Produce	json
//	@Success	200	{object}	response.R{data=[]pay.Withdrawal}
//	@Router		/supplier/withdrawals [get]
func (p *Handler) supplierWithdrawals(c *gin.Context) {
	p.myWithdrawals(c, RoleSupplier)
}

func (p *Handler) myWithdrawals(c *gin.Context, role int8) {
	list, err := p.svc.Withdrawals(c.Request.Context(), middleware.UID(c), role)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, list)
}

// ---------- 管理端 ----------

// adminSettlements 结算单列表
//
//	@Tags		管理端
//	@Security	BearerAuth
//	@Summary	结算单列表
//	@Description	admin 角色；role 1乡厨 2供应商
//	@Produce	json
//	@Param		role	query		int		false	"结算对象角色"	Enums(1,2)
//	@Param		page	query		int		false	"页码"	Default(1)
//	@Param		size	query		int		false	"每页条数"	Default(10)	Maximum(100)
//	@Success	200	{object}	response.R{data=response.PageList{list=[]pay.Settlement}}
//	@Router		/admin/settlements [get]
func (p *Handler) adminSettlements(c *gin.Context) {
	var f SFilter
	if err := c.ShouldBindQuery(&f); err != nil {
		response.Fail(c, errs.BadRequest("查询参数不正确"))
		return
	}
	list, total, err := p.svc.AdminListSettlements(c.Request.Context(), &f)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, response.PageData(list, total, f.Page, f.Size))
}

// adminWithdrawals 提现单列表
//
//	@Tags		管理端
//	@Security	BearerAuth
//	@Summary	提现单列表
//	@Description	admin 角色；role 1乡厨 2供应商，status 0待审核 1通过 2拒绝 3已打款
//	@Produce	json
//	@Param		role	query		int		false	"提现人角色"	Enums(1,2)
//	@Param		status	query		int		false	"状态筛选"
//	@Param		page	query		int		false	"页码"	Default(1)
//	@Param		size	query		int		false	"每页条数"	Default(10)	Maximum(100)
//	@Success	200	{object}	response.R{data=response.PageList{list=[]pay.Withdrawal}}
//	@Router		/admin/withdrawals [get]
func (p *Handler) adminWithdrawals(c *gin.Context) {
	var f WFilter
	if err := c.ShouldBindQuery(&f); err != nil {
		response.Fail(c, errs.BadRequest("查询参数不正确"))
		return
	}
	list, total, err := p.svc.AdminWithdrawals(c.Request.Context(), &f)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, response.PageData(list, total, f.Page, f.Size))
}

// auditWithdrawal 提现审核
//
//	@Tags		管理端
//	@Security	BearerAuth
//	@Summary	提现审核
//	@Description	admin 角色；驳回需附 remark
//	@Accept		json
//	@Produce	json
//	@Param		id	path		int				true	"提现单ID"
//	@Param		req	body		pay.AuditReq	true	"审核结果"
//	@Success	200	{object}	response.R
//	@Router		/admin/withdrawals/{id}/audit [post]
func (p *Handler) auditWithdrawal(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Fail(c, errs.BadRequest("参数不正确"))
		return
	}
	var req AuditReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, errs.BadRequest("参数不正确"))
		return
	}
	if err := p.svc.AuditWithdrawal(c.Request.Context(), id, req.Approve, req.Remark); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// markWithdrawalPaid 标记已打款
//
//	@Tags		管理端
//	@Security	BearerAuth
//	@Summary	标记已打款
//	@Description	admin 角色；一期线下转账后人工标记
//	@Produce	json
//	@Param		id	path		int	true	"提现单ID"
//	@Success	200	{object}	response.R
//	@Router		/admin/withdrawals/{id}/mark-paid [post]
func (p *Handler) markWithdrawalPaid(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Fail(c, errs.BadRequest("参数不正确"))
		return
	}
	if err := p.svc.MarkWithdrawalPaid(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// getSettings 费率设置
//
//	@Tags		管理端
//	@Security	BearerAuth
//	@Summary	查询佣金费率
//	@Description	admin 角色；BP 计价，1% = 100
//	@Produce	json
//	@Success	200	{object}	response.R{data=pay.SettingsVO}
//	@Router		/admin/settings [get]
func (p *Handler) getSettings(c *gin.Context) {
	vo, err := p.svc.GetSettings(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, vo)
}

// putSettings 修改费率
//
//	@Tags		管理端
//	@Security	BearerAuth
//	@Summary	修改佣金费率
//	@Description	admin 角色；BP 取值 0-10000
//	@Accept		json
//	@Produce	json
//	@Param		req	body		pay.SettingsReq	true	"乡厨/供应商费率（BP）"
//	@Success	200	{object}	response.R
//	@Router		/admin/settings [put]
func (p *Handler) putSettings(c *gin.Context) {
	var req SettingsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, errs.BadRequest("费率需在 0-10000 基点之间"))
		return
	}
	if err := p.svc.SetSettings(c.Request.Context(), req.ChefBP, req.SupplierBP); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// financeStats 财务总览
//
//	@Tags		管理端
//	@Security	BearerAuth
//	@Summary	财务总览看板
//	@Description	admin 角色
//	@Produce	json
//	@Success	200	{object}	response.R{data=pay.FinanceStatsVO}
//	@Router		/admin/finance-stats [get]
func (p *Handler) financeStats(c *gin.Context) {
	vo, err := p.svc.FinanceStats(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, vo)
}

// listReconciles 对账批次列表
//
//	@Tags		管理端
//	@Security	BearerAuth
//	@Summary	对账批次列表
//	@Description	admin 角色；status 0进行中 1完成 2失败；金额均为分
//	@Produce	json
//	@Param		page	query		int		false	"页码"	Default(1)
//	@Param		size	query		int		false	"每页条数"	Default(10)	Maximum(100)
//	@Success	200	{object}	response.R{data=response.PageList{list=[]pay.ReconcileBatch}}
//	@Router		/admin/reconciles [get]
func (p *Handler) listReconciles(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "10"))
	list, total, err := p.svc.ListReconciles(c.Request.Context(), page, size)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, response.PageData(list, total, page, size))
}

// runReconcile 手动触发对账
//
//	@Tags		管理端
//	@Security	BearerAuth
//	@Summary	手动触发对账
//	@Description	admin 角色；指定账单日拉微信交易账单与本地核对，同日已跑（非失败）直接返回既有批次；仅真实支付模式可用
//	@Accept		json
//	@Produce	json
//	@Param		req	body		pay.ReconcileRunReq	true	"账单日"
//	@Success	200	{object}	response.R{data=pay.ReconcileBatch}
//	@Router		/admin/reconciles [post]
func (p *Handler) runReconcile(c *gin.Context) {
	var req ReconcileRunReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, errs.BadRequest("参数不正确"))
		return
	}
	b, err := p.svc.RunReconcile(c.Request.Context(), req.BillDate)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, b)
}

// listReconcileDiffs 对账差异明细
//
//	@Tags		管理端
//	@Security	BearerAuth
//	@Summary	对账差异明细
//	@Description	admin 角色；diff_type MISSING_LOCAL掉单/MISSING_BILL/AMOUNT_MISMATCH/REFUND_MISMATCH，status 0待处理 1已处理
//	@Produce	json
//	@Param		id		path		int		true	"批次ID"
//	@Param		page	query		int		false	"页码"	Default(1)
//	@Param		size	query		int		false	"每页条数"	Default(20)	Maximum(100)
//	@Success	200	{object}	response.R{data=response.PageList{list=[]pay.ReconcileDiff}}
//	@Router		/admin/reconciles/{id}/diffs [get]
func (p *Handler) listReconcileDiffs(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Fail(c, errs.BadRequest("参数不正确"))
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	list, total, err := p.svc.ListReconcileDiffs(c.Request.Context(), id, page, size)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, response.PageData(list, total, page, size))
}
