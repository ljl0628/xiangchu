package order

import (
	"context"
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/xiangchu/server/internal/middleware"
	"github.com/xiangchu/server/internal/modules/chef"
	"github.com/xiangchu/server/internal/pkg/errs"
	"github.com/xiangchu/server/internal/pkg/jwtx"
	"github.com/xiangchu/server/internal/pkg/pagination"
	"github.com/xiangchu/server/internal/pkg/response"
)

// Handler 订单模块 HTTP 层
type Handler struct {
	svc     Service
	chefSvc chef.Service
}

func NewHandler(svc Service, chefSvc chef.Service) *Handler {
	return &Handler{svc: svc, chefSvc: chefSvc}
}

// Routes 路由注册：公开评价 + 用户端 + 乡厨端 + 管理端
func (h *Handler) Routes(r gin.IRouter, mk *jwtx.Maker) {
	// 公开：乡厨评价列表（注意参数名固定 :id，与 chef 模块路由保持一致）
	r.GET("/chefs/:id/reviews", h.listChefReviews)

	u := r.Group("", middleware.Auth(mk, "user"))
	u.POST("/orders", h.create)
	u.GET("/orders", h.listMine)
	u.GET("/orders/:id", h.detail)
	u.POST("/orders/:id/cancel", h.cancel)
	u.POST("/orders/:id/pay-balance", h.payBalance)
	u.POST("/orders/:id/review", h.review)
	u.POST("/orders/:id/aftersale", h.aftersaleApply)

	co := r.Group("/chef/orders", middleware.Auth(mk, "chef"))
	co.GET("", h.chefList)
	co.GET("/:id", h.chefDetail)
	co.POST("/:id/accept", h.accept)
	co.POST("/:id/reject", h.reject)
	co.POST("/:id/start-prep", h.startPrep)
	co.POST("/:id/start-service", h.startService)
	co.POST("/:id/complete", h.complete)

	ad := r.Group("/admin", middleware.Auth(mk, "admin"))
	ad.GET("/orders", h.adminList)
	ad.GET("/orders/:id", h.adminDetail)
	ad.GET("/aftersales", h.aftersaleList)
	ad.POST("/aftersales/:id/audit", h.aftersaleAudit)
	ad.GET("/stats", h.adminStats)
	ad.GET("/reviews", h.adminReviewList)
	ad.POST("/reviews/:id/status", h.adminReviewStatus)
}

// ---------- 公共小工具 ----------

func pathID(c *gin.Context) (int64, error) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, errs.ErrNotFound
	}
	return id, nil
}

func pageOf(c *gin.Context) (int, int, error) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "10"))
	return pagination.Normalize(page, size)
}

func statusOf(c *gin.Context) *int8 {
	raw := c.Query("status")
	if raw == "" {
		return nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return nil
	}
	st := int8(v)
	return &st
}

// chefIDOf 当前登录乡厨：uid → chefID（未入驻返回 ErrNotFound）
func (h *Handler) chefIDOf(c *gin.Context) (int64, error) {
	cf, err := h.chefSvc.GetChefByUser(c.Request.Context(), middleware.UID(c))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, errs.ErrNotFound
		}
		return 0, err
	}
	return cf.ID, nil
}

// ---------- 用户端 ----------

// create 创建宴席订单
//
//	@Tags		订单域
//	@Security	BearerAuth
//	@Summary	下单
//	@Description	校验档期后创建订单；pay_type 1定金30% 2全款；返回支付参数
//	@Accept		json
//	@Produce	json
//	@Param		req	body		order.CreateReq	true	"订单参数"
//	@Success	200	{object}	response.R{data=order.CreateResp}
//	@Router		/orders [post]
func (h *Handler) create(c *gin.Context) {
	var req CreateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, errs.BadRequest("参数有误："+err.Error()))
		return
	}
	resp, err := h.svc.Create(c.Request.Context(), middleware.UID(c), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, resp)
}

// listMine 我的订单
//
//	@Tags		订单域
//	@Security	BearerAuth
//	@Summary	我的订单列表
//	@Produce	json
//	@Param		page	query	int	false	"页码"	Default(1)
//	@Param		size	query	int	false	"每页条数"	Default(10)	Maximum(100)
//	@Success	200	{object}	response.R{data=response.PageList{list=[]order.OrderVO}}
//	@Router		/orders [get]
func (h *Handler) listMine(c *gin.Context) {
	page, size, err := pageOf(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	list, total, err := h.svc.ListMine(c.Request.Context(), middleware.UID(c), page, size)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, response.PageData(list, total, page, size))
}

// detail 订单详情
//
//	@Tags		订单域
//	@Security	BearerAuth
//	@Summary	订单详情
//	@Description	含套餐名、评价与售后记录
//	@Produce	json
//	@Param		id	path		int	true	"订单ID"
//	@Success	200	{object}	response.R{data=order.OrderDetail}
//	@Router		/orders/{id} [get]
func (h *Handler) detail(c *gin.Context) {
	id, err := pathID(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	d, err := h.svc.Detail(c.Request.Context(), middleware.UID(c), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, d)
}

// cancel 取消订单（未接单）
//
//	@Tags		订单域
//	@Security	BearerAuth
//	@Summary	取消订单
//	@Description	仅待支付/待接单可取消；已支付原路退款并释放档期
//	@Produce	json
//	@Param		id	path		int	true	"订单ID"
//	@Success	200	{object}	response.R
//	@Router		/orders/{id}/cancel [post]
func (h *Handler) cancel(c *gin.Context) {
	id, err := pathID(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	if err := h.svc.Cancel(c.Request.Context(), middleware.UID(c), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// payBalance 补尾款
//
//	@Tags		订单域
//	@Security	BearerAuth
//	@Summary	补尾款
//	@Description	宴席结束后补付尾款，返回支付参数
//	@Produce	json
//	@Param		id	path		int	true	"订单ID"
//	@Success	200	{object}	response.R{data=order.CreateResp}
//	@Router		/orders/{id}/pay-balance [post]
func (h *Handler) payBalance(c *gin.Context) {
	id, err := pathID(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	resp, err := h.svc.PayBalance(c.Request.Context(), middleware.UID(c), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, resp)
}

// review 发表评价
//
//	@Tags		订单域
//	@Security	BearerAuth
//	@Summary	评价
//	@Description	仅已完成订单，完成后 7 天内，一单一评
//	@Accept		json
//	@Produce	json
//	@Param		id	path		int				true	"订单ID"
//	@Param		req	body		order.ReviewReq	true	"评价"
//	@Success	200	{object}	response.R
//	@Router		/orders/{id}/review [post]
func (h *Handler) review(c *gin.Context) {
	id, err := pathID(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	var req ReviewReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, errs.BadRequest("参数有误："+err.Error()))
		return
	}
	if err := h.svc.CreateReview(c.Request.Context(), middleware.UID(c), id, &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// aftersaleApply 申请售后退款
//
//	@Tags		订单域
//	@Security	BearerAuth
//	@Summary	申请售后
//	@Description	进行中（2-4）或已完成（5）订单可申请；金额不超实付；进行中订单审核同意后取消并退款
//	@Produce	json
//	@Param		id	path		int					true	"订单ID"
//	@Param		req	body		order.AfterSaleReq	true	"售后申请"
//	@Success	200	{object}	response.R
//	@Router		/orders/{id}/aftersale [post]
func (h *Handler) aftersaleApply(c *gin.Context) {
	id, err := pathID(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	var req AfterSaleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, errs.BadRequest("参数有误："+err.Error()))
		return
	}
	if err := h.svc.ApplyAfterSale(c.Request.Context(), middleware.UID(c), id, &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// ---------- 乡厨端 ----------

// chefList 乡厨订单列表
//
//	@Tags		订单域
//	@Security	BearerAuth
//	@Summary	乡厨订单列表
//	@Description	chef 角色
//	@Produce	json
//	@Param		status	query		int		false	"状态筛选：1待接单 2已接单 3备菜中 4服务中 5已完成 6已取消 7已拒绝"
//	@Param		page	query		int		false	"页码"	Default(1)
//	@Param		size	query		int		false	"每页条数"	Default(10)	Maximum(100)
//	@Success	200	{object}	response.R{data=response.PageList{list=[]order.OrderVO}}
//	@Router		/chef/orders [get]
func (h *Handler) chefList(c *gin.Context) {
	chefID, err := h.chefIDOf(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	page, size, err := pageOf(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	list, total, err := h.svc.ChefList(c.Request.Context(), chefID, statusOf(c), page, size)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, response.PageData(list, total, page, size))
}

// chefDetail 乡厨订单详情
//
//	@Tags		订单域
//	@Security	BearerAuth
//	@Summary	乡厨订单详情
//	@Description	chef 角色
//	@Produce	json
//	@Param		id	path		int	true	"订单ID"
//	@Success	200	{object}	response.R{data=order.OrderDetail}
//	@Router		/chef/orders/{id} [get]
func (h *Handler) chefDetail(c *gin.Context) {
	chefID, err := h.chefIDOf(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	id, err := pathID(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	d, err := h.svc.ChefDetail(c.Request.Context(), chefID, id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, d)
}

// reject 拒单
//
//	@Tags		订单域
//	@Security	BearerAuth
//	@Summary	拒单
//	@Description	chef 角色；待接单(1)→已拒绝(7)，自动全额原路退款
//	@Accept		json
//	@Produce	json
//	@Param		id	path		int				true	"订单ID"
//	@Param		req	body		order.RejectReq	true	"拒单原因"
//	@Success	200	{object}	response.R
//	@Router		/chef/orders/{id}/reject [post]
func (h *Handler) reject(c *gin.Context) {
	chefID, err := h.chefIDOf(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	id, err := pathID(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	var req RejectReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, errs.BadRequest("参数有误："+err.Error()))
		return
	}
	if err := h.svc.Reject(c.Request.Context(), chefID, id, req.Reason); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// chefTransition accept / start-prep / start-service / complete 共用壳
func (h *Handler) chefTransition(
	c *gin.Context,
	act func(ctx context.Context, chefID, id int64) error,
) {
	chefID, err := h.chefIDOf(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	id, err := pathID(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	if err := act(c.Request.Context(), chefID, id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// accept 接单
//
//	@Tags		订单域
//	@Security	BearerAuth
//	@Summary	接单
//	@Description	chef 角色；待接单(1)→已接单(2)，锁定档期
//	@Produce	json
//	@Param		id	path		int	true	"订单ID"
//	@Success	200	{object}	response.R
//	@Router		/chef/orders/{id}/accept [post]
func (h *Handler) accept(c *gin.Context) {
	h.chefTransition(c, h.svc.Accept)
}

// startPrep 开始备菜
//
//	@Tags		订单域
//	@Security	BearerAuth
//	@Summary	开始备菜
//	@Description	chef 角色；已接单(2)→备菜中(3)
//	@Produce	json
//	@Param		id	path		int	true	"订单ID"
//	@Success	200	{object}	response.R
//	@Router		/chef/orders/{id}/start-prep [post]
func (h *Handler) startPrep(c *gin.Context) {
	h.chefTransition(c, h.svc.StartPrep)
}

// startService 开席服务
//
//	@Tags		订单域
//	@Security	BearerAuth
//	@Summary	开席服务
//	@Description	chef 角色；备菜中(3)→服务中(4)
//	@Produce	json
//	@Param		id	path		int	true	"订单ID"
//	@Success	200	{object}	response.R
//	@Router		/chef/orders/{id}/start-service [post]
func (h *Handler) startService(c *gin.Context) {
	h.chefTransition(c, h.svc.StartService)
}

// complete 完成订单
//
//	@Tags		订单域
//	@Security	BearerAuth
//	@Summary	完成订单
//	@Description	chef 角色；服务中(4)→已完成(5)，触发乡厨结算与评分重算
//	@Produce	json
//	@Param		id	path		int	true	"订单ID"
//	@Success	200	{object}	response.R
//	@Router		/chef/orders/{id}/complete [post]
func (h *Handler) complete(c *gin.Context) {
	h.chefTransition(c, h.svc.Complete)
}

// ---------- 管理端 ----------

// adminList 全量订单列表
//
//	@Tags		管理端
//	@Security	BearerAuth
//	@Summary	订单监控列表
//	@Description	admin 角色
//	@Produce	json
//	@Param		status	query		int		false	"状态筛选：1待接单 2已接单 3备菜中 4服务中 5已完成 6已取消 7已拒绝"
//	@Param		keyword	query		string	false	"关键词"
//	@Param		page	query		int		false	"页码"	Default(1)
//	@Param		size	query		int		false	"每页条数"	Default(10)	Maximum(100)
//	@Success	200	{object}	response.R{data=response.PageList{list=[]order.OrderVO}}
//	@Router		/admin/orders [get]
func (h *Handler) adminList(c *gin.Context) {
	page, size, err := pageOf(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	list, total, err := h.svc.AdminList(c.Request.Context(), statusOf(c), c.Query("keyword"), page, size)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, response.PageData(list, total, page, size))
}

// adminDetail 订单详情（管理端）
//
//	@Tags		管理端
//	@Security	BearerAuth
//	@Summary	订单详情
//	@Description	admin 角色
//	@Produce	json
//	@Param		id	path		int	true	"订单ID"
//	@Success	200	{object}	response.R{data=order.OrderDetail}
//	@Router		/admin/orders/{id} [get]
func (h *Handler) adminDetail(c *gin.Context) {
	id, err := pathID(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	d, err := h.svc.AdminDetail(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, d)
}

// aftersaleList 售后列表（管理端）
//
//	@Tags		管理端
//	@Security	BearerAuth
//	@Summary	售后申请列表
//	@Description	admin 角色；status 0待审核 1已同意 2已拒绝 3已退款
//	@Produce	json
//	@Param		status	query		int		false	"状态筛选"
//	@Param		page	query		int		false	"页码"	Default(1)
//	@Param		size	query		int		false	"每页条数"	Default(10)	Maximum(100)
//	@Success	200	{object}	response.R{data=response.PageList{list=[]order.AfterSale}}
//	@Router		/admin/aftersales [get]
func (h *Handler) aftersaleList(c *gin.Context) {
	page, size, err := pageOf(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	list, total, err := h.svc.AfterSaleList(c.Request.Context(), statusOf(c), page, size)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, response.PageData(list, total, page, size))
}

// aftersaleAudit 售后审核（管理端）
//
//	@Tags		管理端
//	@Security	BearerAuth
//	@Summary	售后审核
//	@Description	admin 角色；同意后走原路退款
//	@Accept		json
//	@Produce	json
//	@Param		id	path		int				true	"售后单ID"
//	@Param		req	body		order.AuditReq	true	"审核结果"
//	@Success	200	{object}	response.R
//	@Router		/admin/aftersales/{id}/audit [post]
func (h *Handler) aftersaleAudit(c *gin.Context) {
	id, err := pathID(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	var req AuditReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, errs.BadRequest("参数有误："+err.Error()))
		return
	}
	if err := h.svc.AfterSaleAudit(c.Request.Context(), id, &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// listChefReviews 公开：乡厨评价分页
//
//	@Tags		乡厨域
//	@Summary	乡厨评价列表（公开）
//	@Produce	json
//	@Param		id	path		int	true	"乡厨ID"
//	@Param		page	query		int	false	"页码"	Default(1)
//	@Param		size	query		int	false	"每页条数"	Default(10)	Maximum(100)
//	@Success	200	{object}	response.R{data=response.PageList{list=[]order.Review}}
//	@Router		/chefs/{id}/reviews [get]
func (h *Handler) listChefReviews(c *gin.Context) {
	chefID, err := pathID(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	page, size, err := pageOf(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	list, total, err := h.svc.ChefReviews(c.Request.Context(), chefID, page, size)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, response.PageData(list, total, page, size))
}

// ---------- 管理端补充 ----------

// adminStats 订单统计（GMV/状态计数/复购率/区域分布）
//
//	@Tags		管理端
//	@Security	BearerAuth
//	@Summary	订单统计看板
//	@Description	admin 角色
//	@Produce	json
//	@Success	200	{object}	response.R{data=order.OrderStatsVO}
//	@Router		/admin/stats [get]
func (h *Handler) adminStats(c *gin.Context) {
	res, err := h.svc.AdminStats(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, res)
}

// adminReviewList 评价列表（管理端）
//
//	@Tags		管理端
//	@Security	BearerAuth
//	@Summary	评价列表
//	@Description	admin 角色
//	@Produce	json
//	@Param		page	query		int		false	"页码"	Default(1)
//	@Param		size	query		int		false	"每页条数"	Default(10)	Maximum(100)
//	@Success	200	{object}	response.R{data=response.PageList{list=[]order.Review}}
//	@Router		/admin/reviews [get]
func (h *Handler) adminReviewList(c *gin.Context) {
	page, size, err := pageOf(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	list, total, err := h.svc.AdminListReviews(c.Request.Context(), page, size)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, response.PageData(list, total, page, size))
}

// adminReviewStatus 隐藏/恢复评价
//
//	@Tags		管理端
//	@Security	BearerAuth
//	@Summary	隐藏/恢复评价
//	@Description	admin 角色
//	@Accept		json
//	@Produce	json
//	@Param		id	path		int						true	"评价ID"
//	@Param		req	body		order.ReviewStatusReq	true	"状态"
//	@Success	200	{object}	response.R
//	@Router		/admin/reviews/{id}/status [post]
func (h *Handler) adminReviewStatus(c *gin.Context) {
	var req ReviewStatusReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, err)
		return
	}
	id, err := pathID(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	if err := h.svc.AdminSetReviewStatus(c.Request.Context(), id, *req.Status); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}
