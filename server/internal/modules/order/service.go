package order

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/xiangchu/server/internal/config"
	"github.com/xiangchu/server/internal/infra/logger"
	"github.com/xiangchu/server/internal/infra/mq"
	"github.com/xiangchu/server/internal/modules/chef"
	"github.com/xiangchu/server/internal/modules/notify"
	"github.com/xiangchu/server/internal/modules/pay"
	"github.com/xiangchu/server/internal/modules/user"
	"github.com/xiangchu/server/internal/pkg/errs"
	"github.com/xiangchu/server/internal/pkg/orderid"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

const (
	orderNoPrefix = "XD" // 宴席订单单号前缀
	afterNoPrefix = "XS" // 售后单号前缀
	// acceptTimeout 待接单超时：超时未接单自动取消并退款
	acceptTimeout = 12 * time.Hour
	// settlementRoleChef pay.CreateSettlement 的角色参数：1 乡厨
	settlementRoleChef = 1
)

// Service 订单模块服务接口（跨模块只允许通过此接口调用）
type Service interface {
	// 用户端
	Create(ctx context.Context, uid int64, req *CreateReq) (*CreateResp, error)
	ListMine(ctx context.Context, uid int64, page, size int) ([]*OrderVO, int64, error)
	Detail(ctx context.Context, uid, id int64) (*OrderDetail, error)
	Cancel(ctx context.Context, uid, id int64) error
	PayBalance(ctx context.Context, uid, id int64) (*CreateResp, error)
	CreateReview(ctx context.Context, uid, id int64, req *ReviewReq) error
	ApplyAfterSale(ctx context.Context, uid, id int64, req *AfterSaleReq) error
	// 乡厨端
	ChefList(ctx context.Context, chefID int64, status *int8, page, size int) ([]*OrderVO, int64, error)
	ChefDetail(ctx context.Context, chefID, id int64) (*OrderDetail, error)
	Accept(ctx context.Context, chefID, id int64) error
	Reject(ctx context.Context, chefID, id int64, reason string) error
	StartPrep(ctx context.Context, chefID, id int64) error
	StartService(ctx context.Context, chefID, id int64) error
	Complete(ctx context.Context, chefID, id int64) error
	ChefReviews(ctx context.Context, chefID int64, page, size int) ([]*Review, int64, error)
	// 管理端
	AdminList(ctx context.Context, status *int8, keyword string, page, size int) ([]*OrderVO, int64, error)
	AdminDetail(ctx context.Context, id int64) (*OrderDetail, error)
	AdminStats(ctx context.Context) (*OrderStatsVO, error)
	AdminListReviews(ctx context.Context, page, size int) ([]*Review, int64, error)
	AdminSetReviewStatus(ctx context.Context, id int64, status int8) error
	AfterSaleList(ctx context.Context, status *int8, page, size int) ([]*AfterSale, int64, error)
	AfterSaleAudit(ctx context.Context, id int64, req *AuditReq) error
	// MQ 消费处理
	HandlePaySuccess(ctx context.Context, orderID int64, paymentNo string) error
	HandleTimeout(ctx context.Context, orderID int64) error
	HandleRefunded(ctx context.Context, orderID int64) error
}

// ---------- impl ----------

type serviceImpl struct {
	repo    *Repo
	chefSvc chef.Service
	paySvc  pay.Service
	userSvc user.Service
	cfg     *config.Config
	bus     *mq.MQ
}

// NewService 依赖仅限各模块 Service 接口与配置/MQ
func NewService(repo *Repo, chefSvc chef.Service, paySvc pay.Service, userSvc user.Service, cfg *config.Config, bus *mq.MQ) Service {
	return &serviceImpl{repo: repo, chefSvc: chefSvc, paySvc: paySvc, userSvc: userSvc, cfg: cfg, bus: bus}
}

// notify 发布通知事件（MQ 未连接时 notify.Send 内部降级日志）
func (s *serviceImpl) notify(typ, title, content string, uids ...int64) {
	notify.Send(s.bus, &notify.Event{Type: typ, Title: title, Content: content, UserIDs: uids})
}

// parseBanquetTime 宴席时间解析
func parseBanquetTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	for _, layout := range banquetTimeLayouts {
		if t, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errs.BadRequest("宴席时间格式不正确")
}

// depositFor 本次应付：全款=应付总额；定金=应付总额×DepositBP/10000（四舍五入）
func (s *serviceImpl) depositFor(payable int64, payType int8) int64 {
	if payType == PayTypeFull {
		return payable
	}
	dep := (payable*int64(s.cfg.Finance.DepositBP) + 5000) / 10000
	if dep < 1 {
		dep = 1
	}
	if dep > payable {
		dep = payable
	}
	return dep
}

// banquetDay 档期按自然日占用
func banquetDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// ---------- 用户端 ----------

// Create 下单：校验乡厨/套餐/档期/券 → 计价 → 落库(status=0) → 创建支付
func (s *serviceImpl) Create(ctx context.Context, uid int64, req *CreateReq) (*CreateResp, error) {
	banquetAt, err := parseBanquetTime(req.BanquetTime)
	if err != nil {
		return nil, err
	}
	if !banquetAt.After(time.Now()) {
		return nil, errs.BadRequest("宴席时间需晚于当前时间")
	}
	c, err := s.chefSvc.GetChef(ctx, req.ChefID)
	if err != nil {
		return nil, err
	}
	if c.Status != 1 {
		return nil, errs.BadRequest("该乡厨暂不可下单")
	}
	pkg, err := s.repo.GetPackage(ctx, req.PackageID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errs.ErrNotFound
		}
		return nil, err
	}
	if pkg.ChefID != req.ChefID {
		return nil, errs.BadRequest("套餐不属于该乡厨")
	}
	if pkg.Status != 1 {
		return nil, errs.BadRequest("套餐已下架")
	}
	row, exists, err := s.repo.GetSchedule(ctx, req.ChefID, banquetDay(banquetAt))
	if err != nil {
		return nil, err
	}
	if exists && row.Status == scheduleLocked && row.OrderID != 0 {
		return nil, errs.ErrScheduleTaken
	}
	amount, err := s.chargeWithCoupon(ctx, uid, pkg.PricePerTable*int64(req.TableCount), req.UserCouponID)
	if err != nil {
		return nil, err
	}
	deposit := s.depositFor(amount, req.PayType)
	o := &Order{
		OrderNo:      orderid.GenNo(orderNoPrefix),
		UserID:       uid,
		ChefID:       req.ChefID,
		BanquetType:  req.BanquetType,
		BanquetTime:  banquetAt,
		Region:       req.Region,
		Address:      req.Address,
		ContactName:  req.ContactName,
		ContactPhone: req.ContactPhone,
		TableCount:   req.TableCount,
		PeopleCount:  req.PeopleCount,
		PackageID:    req.PackageID,
		MenuRemark:   req.MenuRemark,
		Amount:       amount,
		PayType:      req.PayType,
		Deposit:      deposit,
		Status:       StatusUnpaid,
		UserCouponID: req.UserCouponID,
	}
	if err := s.repo.Create(ctx, o); err != nil {
		// 建单失败回滚锁券，避免券卡在占用态
		if req.UserCouponID > 0 {
			_ = s.userSvc.ReleaseCoupon(req.UserCouponID)
		}
		return nil, err
	}
	pr, err := s.paySvc.CreatePayment(ctx, BizTypeOrder, o.ID, uid, deposit)
	if err != nil {
		return nil, err
	}
	return &CreateResp{OrderID: o.ID, OrderNo: o.OrderNo, Deposit: deposit, PaymentNo: pr.PaymentNo, Params: pr.Params}, nil
}

// chargeWithCoupon 券抵扣计价 + 下单锁券：仅支持满减券(type=1)，抵扣不超过应付。
// 校验通过后立即抢占占用（0→2），创建失败由调用方释放
func (s *serviceImpl) chargeWithCoupon(ctx context.Context, uid, payable, ucID int64) (int64, error) {
	if ucID <= 0 {
		return payable, nil
	}
	c, err := s.repo.CouponUsable(ctx, uid, ucID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, errs.BadRequest("优惠券不可用")
		}
		return 0, err
	}
	if c.Type != 1 {
		return 0, errs.BadRequest("暂不支持该券类型")
	}
	if payable < c.Threshold {
		return 0, errs.BadRequest("未达到该优惠券的使用门槛")
	}
	// 下单即锁券：条件更新 0→2 原子抢占，窗口期其他订单不可再选该券
	if err := s.userSvc.OccupyCoupon(ctx, uid, ucID); err != nil {
		return 0, err
	}
	discount := c.Amount
	if discount > payable {
		discount = payable
	}
	return payable - discount, nil
}

// HandlePaySuccess 支付成功消费（幂等）：实付重算 → 用券/锁档期 → 0→1 待接单 → 通知+超时任务
func (s *serviceImpl) HandlePaySuccess(ctx context.Context, orderID int64, paymentNo string) error {
	o, err := s.repo.Get(ctx, orderID)
	if err != nil {
		return err
	}
	if paymentNo != "" {
		// 流水必须真实存在
		if _, err := s.repo.PaymentAmountByNo(ctx, paymentNo); err != nil {
			return err
		}
	}
	// 尾款支付也在此重算实付
	if err := s.repo.RecalcPaid(ctx, orderID); err != nil {
		return err
	}
	// 超付兜底：定金+尾款正常恰等于 amount；在途单唯一键防双开，此处为最后防线
	if o2, e := s.repo.Get(ctx, orderID); e == nil && o2.PaidAmount > o2.Amount {
		logger.L().Error("订单超付，需人工核对并退差额",
			zap.Int64("order_id", orderID),
			zap.Int64("paid", o2.PaidAmount), zap.Int64("amount", o2.Amount))
	}
	if o.Status != StatusUnpaid {
		return nil // 首付已处理过，消息重投幂等返回
	}
	if o.UserCouponID > 0 {
		if err := s.userSvc.UseCoupon(ctx, o.UserID, o.UserCouponID, o.ID); err != nil {
			return err
		}
	}
	if err := s.chefSvc.LockSchedule(ctx, o.ChefID, o.BanquetTime, o.ID); err != nil {
		return err
	}
	moved, err := s.repo.Transition(ctx, orderID, StatusUnpaid, StatusPending, nil)
	if err != nil {
		return err
	}
	if !moved {
		return nil
	}
	if err := s.bus.PublishDelay(mq.KeyOrderTimeout, &OrderTimeoutMsg{OrderID: o.ID}, acceptTimeout); err != nil {
		logger.L().Warn("发布接单超时任务失败", zap.Int64("order_id", o.ID), zap.Error(err))
	}
	s.notify(NotifyOrderCreated, "订单支付成功", "您的宴席订单已支付成功，等待乡厨接单", o.UserID)
	if chefUID, err := s.repo.ChefUserID(ctx, o.ChefID); err == nil {
		s.notify(NotifyOrderCreated, "新宴席订单", "您有新的宴席订单，请及时接单", chefUID)
	} else {
		logger.L().Warn("查询乡厨归属用户失败", zap.Int64("chef_id", o.ChefID), zap.Error(err))
	}
	return nil
}

// PayBalance 尾款补付：已接单后且未结清时，为差额创建支付；
// 待支付订单（状态 0）复用此端点重新拉起支付（金额=下单时约定的本次支付额）
func (s *serviceImpl) PayBalance(ctx context.Context, uid, id int64) (*CreateResp, error) {
	o, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if o.UserID != uid {
		return nil, errs.ErrNotFound
	}
	due := o.Amount - o.PaidAmount
	if o.Status == StatusUnpaid {
		// 待支付：原支付单可能已过期，按下单时约定的本次支付额（定金或全款）重新创建
		if o.Deposit <= 0 || o.Deposit > o.Amount {
			return nil, errs.ErrAmountWrong
		}
		due = o.Deposit
	} else if o.Status < StatusAccepted || o.Status > StatusServing {
		return nil, errs.ErrStatusDenied
	}
	if due <= 0 {
		return nil, errs.BadRequest("尾款已结清，无需再支付")
	}
	pr, err := s.paySvc.CreatePayment(ctx, BizTypeOrder, o.ID, uid, due)
	if err != nil {
		return nil, err
	}
	return &CreateResp{OrderID: o.ID, OrderNo: o.OrderNo, Deposit: due, PaymentNo: pr.PaymentNo, Params: pr.Params}, nil
}

// ListMine 我的订单
func (s *serviceImpl) ListMine(ctx context.Context, uid int64, page, size int) ([]*OrderVO, int64, error) {
	list, total, err := s.repo.ListByUser(ctx, uid, page, size)
	if err != nil {
		return nil, 0, err
	}
	return s.buildVOs(ctx, list), total, nil
}

// Detail 用户查看自己的订单详情
func (s *serviceImpl) Detail(ctx context.Context, uid, id int64) (*OrderDetail, error) {
	o, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if o.UserID != uid {
		return nil, errs.ErrNotFound
	}
	return s.buildDetail(ctx, o)
}

// Cancel 用户取消：0 未支付直接关单；1 待接单释放档期并全额退款；2-4 走售后
func (s *serviceImpl) Cancel(ctx context.Context, uid, id int64) error {
	o, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if o.UserID != uid {
		return errs.ErrNotFound
	}
	switch o.Status {
	case StatusUnpaid:
		moved, err := s.repo.Transition(ctx, id, StatusUnpaid, StatusCancelled,
			map[string]any{"cancel_reason": "用户未支付取消"})
		if err != nil || !moved {
			return errOrDenied(err)
		}
		// 释放下单时锁定的券（占用态 2→0；兼容存量已核销 1→0）
		if o.UserCouponID > 0 {
			_ = s.userSvc.ReleaseCoupon(o.UserCouponID)
		}
		return nil
	case StatusPending:
		return s.cancelPaid(ctx, o, "用户取消订单", NotifyOrderRejected)
	case StatusAccepted, StatusPrepping, StatusServing:
		return errs.BadRequest("订单已确认，如需退款请申请售后")
	default:
		return errs.ErrStatusDenied
	}
}

// errOrDenied 迁移语义归一：db 错误透传；未迁移视为状态不允许
func errOrDenied(err error) error {
	if err != nil {
		return err
	}
	return errs.ErrStatusDenied
}

// cancelPaid 待接单取消：释放券/档期 + 全额退款 + 通知双方
func (s *serviceImpl) cancelPaid(ctx context.Context, o *Order, reason, notifyType string) error {
	moved, err := s.repo.Transition(ctx, o.ID, StatusPending, StatusCancelled, map[string]any{
		"cancel_reason": reason,
		"refund_status": RefundApproved,
	})
	if err != nil || !moved {
		return errOrDenied(err)
	}
	if o.UserCouponID > 0 {
		_ = s.userSvc.ReleaseCoupon(o.UserCouponID)
	}
	if err := s.chefSvc.ReleaseSchedule(ctx, o.ChefID, o.ID); err != nil {
		return err
	}
	if err := s.refundAll(ctx, o); err != nil {
		return err
	}
	if chefUID, err := s.repo.ChefUserID(ctx, o.ChefID); err == nil {
		s.notify(notifyType, "订单已取消", "用户已取消订单 "+o.OrderNo, chefUID)
	}
	s.notify(notifyType, "订单已取消", "订单已取消，退款将原路退回", o.UserID)
	return nil
}

// refundAll 按已支付流水逐笔原路退款
func (s *serviceImpl) refundAll(ctx context.Context, o *Order) error {
	pays, err := s.repo.ListActivePayments(ctx, o.ID)
	if err != nil {
		return err
	}
	for _, p := range pays {
		if err := s.paySvc.Refund(ctx, p.PaymentNo, p.Amount); err != nil {
			return err
		}
	}
	return nil
}

// ---------- 乡厨端 ----------

func (s *serviceImpl) ChefList(ctx context.Context, chefID int64, status *int8, page, size int) ([]*OrderVO, int64, error) {
	list, total, err := s.repo.ListByChef(ctx, chefID, status, page, size)
	if err != nil {
		return nil, 0, err
	}
	return s.buildVOs(ctx, list), total, nil
}

func (s *serviceImpl) ChefDetail(ctx context.Context, chefID, id int64) (*OrderDetail, error) {
	o, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if o.ChefID != chefID {
		return nil, errs.ErrNotFound
	}
	return s.buildDetail(ctx, o)
}

// Accept 接单：1→2
func (s *serviceImpl) Accept(ctx context.Context, chefID, id int64) error {
	moved, err := s.repo.TransitionByChef(ctx, id, chefID, StatusPending, StatusAccepted, nil)
	if err != nil || !moved {
		return errOrDenied(err)
	}
	o, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	s.notify(NotifyOrderAccepted, "订单已接单", "乡厨已接单，请保持电话畅通", o.UserID)
	return nil
}

// Reject 拒单：1→7 + 释放档期/券 + 全额退款
func (s *serviceImpl) Reject(ctx context.Context, chefID, id int64, reason string) error {
	o, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if o.ChefID != chefID {
		return errs.ErrNotFound
	}
	if o.Status != StatusPending {
		return errs.ErrStatusDenied
	}
	moved, err := s.repo.Transition(ctx, id, StatusPending, StatusRejected, map[string]any{
		"cancel_reason": reason,
		"refund_status": RefundApproved,
	})
	if err != nil || !moved {
		return errOrDenied(err)
	}
	if o.UserCouponID > 0 {
		_ = s.userSvc.ReleaseCoupon(o.UserCouponID)
	}
	if err := s.chefSvc.ReleaseSchedule(ctx, chefID, o.ID); err != nil {
		return err
	}
	if err := s.refundAll(ctx, o); err != nil {
		return err
	}
	s.notify(NotifyOrderRejected, "订单已拒绝", "乡厨无法承接该订单，退款将原路退回", o.UserID)
	return nil
}

// StartPrep 备菜中：2→3
func (s *serviceImpl) StartPrep(ctx context.Context, chefID, id int64) error {
	moved, err := s.repo.TransitionByChef(ctx, id, chefID, StatusAccepted, StatusPrepping, nil)
	if err != nil || !moved {
		return errOrDenied(err)
	}
	return nil
}

// StartService 服务中：3→4
func (s *serviceImpl) StartService(ctx context.Context, chefID, id int64) error {
	moved, err := s.repo.TransitionByChef(ctx, id, chefID, StatusPrepping, StatusServing, nil)
	if err != nil || !moved {
		return errOrDenied(err)
	}
	return nil
}

// Complete 完成：4→5 + 乡厨单量+1 + 生成结算单 + 通知评价
func (s *serviceImpl) Complete(ctx context.Context, chefID, id int64) error {
	o, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	// 尾款未结清不允许完成：结算按 paid_amount 生成，漏收的尾款无法事后追补
	if o.PaidAmount < o.Amount {
		return errs.BadRequest("尾款未结清，暂不能完成订单")
	}
	moved, err := s.repo.TransitionByChef(ctx, id, chefID, StatusServing, StatusDone, nil)
	if err != nil || !moved {
		return errOrDenied(err)
	}
	// 结算失败不阻断完成，仅记录告警待补偿
	if err := s.paySvc.CreateSettlement(ctx, settlementRoleChef, chefID, o.OrderNo, o.PaidAmount); err != nil {
		logger.L().Warn("生成乡厨结算单失败", zap.Int64("order_id", o.ID), zap.Error(err))
	}
	s.notify(NotifyOrderDone, "宴席已完成", "您的宴席已圆满结束，快来评价一下这次的菜品和服务吧", o.UserID)
	return nil
}

// ChefReviews 乡厨评价列表（公开）
func (s *serviceImpl) ChefReviews(ctx context.Context, chefID int64, page, size int) ([]*Review, int64, error) {
	return s.repo.ListReviewByChef(ctx, chefID, page, size)
}

// ---------- 评价 ----------

// CreateReview 完成单评价：唯一约束防重 + 发布 review.created 事件
func (s *serviceImpl) CreateReview(ctx context.Context, uid, id int64, req *ReviewReq) error {
	o, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if o.UserID != uid {
		return errs.ErrNotFound
	}
	if o.Status != StatusDone {
		return errs.ErrStatusDenied
	}
	if _, err := s.repo.GetReviewByOrder(ctx, id); err == nil {
		return errs.BadRequest("该订单已评价")
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	imgs, _ := json.Marshal(req.Images)
	rv := &Review{
		OrderID: o.ID,
		UserID:  uid,
		ChefID:  o.ChefID,
		Rating:  req.Rating,
		Tags:    strings.Join(req.Tags, ","),
		Content: req.Content,
		Images:  string(imgs),
		Status:  1,
	}
	if err := s.repo.CreateReview(ctx, rv); err != nil {
		return err
	}
	// 通知乡厨模块重算评分
	if err := s.bus.Publish(mq.EventExchange, mq.KeyReviewCreated,
		map[string]any{"chef_id": o.ChefID, "review_id": rv.ID, "rating": rv.Rating}); err != nil {
		logger.L().Warn("发布评价事件失败", zap.Int64("order_id", o.ID), zap.Error(err))
	}
	if chefUID, err := s.repo.ChefUserID(ctx, o.ChefID); err == nil {
		s.notify(NotifyOrderDone, "收到新评价", "您的宴席服务收到了新的评价，快去看看吧", chefUID)
	}
	return nil
}

// ApplyAfterSale 售后申请：进行中（2-4）或已完成（5）订单，金额不超过实付。
// 进行中审核同意 → 取消订单+退款；已完成审核同意 → 仅退款（服务已发生）。
// 0/1 走取消通道，6/7 钱已两清，均不允许售后
func (s *serviceImpl) ApplyAfterSale(ctx context.Context, uid, id int64, req *AfterSaleReq) error {
	o, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if o.UserID != uid {
		return errs.ErrNotFound
	}
	switch o.Status {
	case StatusAccepted, StatusPrepping, StatusServing, StatusDone:
	default:
		return errs.ErrStatusDenied
	}
	if req.Amount > o.PaidAmount {
		return errs.BadRequest("退款金额不能超过实付金额")
	}
	list, err := s.repo.ListAfterSaleByOrder(ctx, id)
	if err != nil {
		return err
	}
	for _, a := range list {
		if a.Status == AfterSalePending || a.Status == AfterSaleApproved {
			return errs.BadRequest("已有处理中的售后申请")
		}
	}
	imgs, _ := json.Marshal(req.Images)
	a := &AfterSale{
		AfterNo: orderid.GenNo(afterNoPrefix),
		OrderID: o.ID,
		UserID:  o.UserID,
		ChefID:  o.ChefID,
		Amount:  req.Amount,
		Reason:  req.Reason,
		Images:  string(imgs),
		Status:  AfterSalePending,
	}
	if err := s.repo.CreateAfterSale(ctx, a); err != nil {
		return err
	}
	return s.repo.SetRefundStatus(ctx, o.ID, RefundApplying)
}

// AfterSaleList 管理端售后列表
func (s *serviceImpl) AfterSaleList(ctx context.Context, status *int8, page, size int) ([]*AfterSale, int64, error) {
	return s.repo.ListAfterSale(ctx, status, page, size)
}

// AfterSaleAudit 售后审核：同意→按申请额从最近流水退款（pay.refunded 事件回写已退款）
func (s *serviceImpl) AfterSaleAudit(ctx context.Context, id int64, req *AuditReq) error {
	a, err := s.repo.GetAfterSale(ctx, id)
	if err != nil {
		return err
	}
	if a.Status != AfterSalePending {
		return errs.ErrStatusDenied
	}
	o, err := s.repo.Get(ctx, a.OrderID)
	if err != nil {
		return err
	}
	if req.Approve {
		amount := a.Amount
		if amount > o.PaidAmount {
			amount = o.PaidAmount
		}
		// 进行中订单先取消再退款（同 cancelPaid 教义：状态先行，退款收尾——
		// 退款发起前订单已释放资源，退款失败重试不会造成半取消状态）
		s.cancelInflight(ctx, o)
		// refund_status=2 必须先于 Refund 落库：mock 链路退款同步终态化，
		// pay.refunded 事件消费者会把 refund_status 推到 4——若 2 写在退款之后，
		// 会被函数尾部的 2 覆盖成倒退（真实模式回调慢未暴露，mock 必现）
		if err := s.repo.SetRefundStatus(ctx, o.ID, RefundApproved); err != nil {
			return err
		}
		no, err := s.repo.LatestActivePaymentNo(ctx, o.ID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errs.BadRequest("该订单无可退款支付流水")
			}
			return err
		}
		if err := s.paySvc.Refund(ctx, no, amount); err != nil {
			return err
		}
		// 退款失败时售后单留在 Pending 可重审；成功后事件侧终态化 refund_status=4，
		// 本函数不再触碰 refund_status，无覆盖风险
		return s.repo.UpdateAfterSale(ctx, id, map[string]any{
			"status": AfterSaleApproved, "audit_remark": req.Remark,
		})
	}
	if err := s.repo.UpdateAfterSale(ctx, id, map[string]any{
		"status": AfterSaleRefused, "audit_remark": req.Remark,
	}); err != nil {
		return err
	}
	return s.repo.SetRefundStatus(ctx, o.ID, RefundRejected)
}

// cancelInflight 售后审核通过后若订单仍在进行中（2-4）则取消并释放券/档期。
// 状态先行（同 cancelPaid）：取消在退款发起之前，退款失败可重试而不产生半取消状态；
// 未迁移成功说明订单已不在进行中（与乡厨推进并发，如恰好 4→5），跳过取消不影响退款——
// 那种情形按已完成订单售后处理（仅退款）。资源释放失败仅告警不回滚，待人工
func (s *serviceImpl) cancelInflight(ctx context.Context, o *Order) {
	if o.Status < StatusAccepted || o.Status > StatusServing {
		return
	}
	moved, err := s.repo.Transition(ctx, o.ID, o.Status, StatusCancelled, map[string]any{
		"cancel_reason": "售后审核同意取消",
	})
	if err != nil || !moved {
		logger.L().Warn("售后取消：订单状态已变化，跳过取消", zap.Int64("order_id", o.ID), zap.Error(err))
		return
	}
	if o.UserCouponID > 0 {
		_ = s.userSvc.ReleaseCoupon(o.UserCouponID)
	}
	if err := s.chefSvc.ReleaseSchedule(ctx, o.ChefID, o.ID); err != nil {
		logger.L().Error("售后取消：释放档期失败，待人工处理", zap.Int64("order_id", o.ID), zap.Error(err))
	}
	if chefUID, err := s.repo.ChefUserID(ctx, o.ChefID); err == nil {
		s.notify(NotifyOrderRejected, "订单已取消", "售后审核通过，订单 "+o.OrderNo+" 已取消", chefUID)
	}
	s.notify(NotifyOrderRejected, "订单已取消", "售后审核通过，订单已取消，退款将原路退回", o.UserID)
}

// HandleRefunded 退款到账：订单退款状态→已退款；同意中的售后单→已退款
func (s *serviceImpl) HandleRefunded(ctx context.Context, orderID int64) error {
	if err := s.repo.SetRefundStatus(ctx, orderID, RefundDone); err != nil {
		return err
	}
	return s.repo.UpdateAfterSaleStatusByOrder(ctx, orderID, AfterSaleApproved, AfterSaleRefunded)
}

// ---------- 管理端 ----------

func (s *serviceImpl) AdminList(ctx context.Context, status *int8, keyword string, page, size int) ([]*OrderVO, int64, error) {
	list, total, err := s.repo.ListAdmin(ctx, status, keyword, page, size)
	if err != nil {
		return nil, 0, err
	}
	return s.buildVOs(ctx, list), total, nil
}

func (s *serviceImpl) AdminDetail(ctx context.Context, id int64) (*OrderDetail, error) {
	o, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.buildDetail(ctx, o)
}

// AdminStats GMV（支付流水口径）/ 状态计数 / 复购率 / 区域分布
func (s *serviceImpl) AdminStats(ctx context.Context) (*OrderStatsVO, error) {
	vo := &OrderStatsVO{Regions: []RegionCount{}}
	gmv, err := s.repo.StatsGMV(ctx)
	if err != nil {
		return nil, err
	}
	vo.GMV = gmv
	counts, err := s.repo.StatsStatusCounts(ctx)
	if err != nil {
		return nil, err
	}
	for st, n := range counts {
		vo.Total += n
		if st == StatusPending {
			vo.Pending = n
		}
		if st == StatusDone {
			vo.Completed = n
		}
	}
	regions, err := s.repo.StatsRegions(ctx)
	if err != nil {
		return nil, err
	}
	vo.Regions = regions
	repeat, users, err := s.repo.StatsRepurchase(ctx)
	if err != nil {
		return nil, err
	}
	if users > 0 {
		vo.RepurchaseRate = math.Round(float64(repeat)/float64(users)*10000) / 10000
	}
	return vo, nil
}

// HandleTimeout 接单超时：仅对待接单生效，自动取消并退款
func (s *serviceImpl) HandleTimeout(ctx context.Context, orderID int64) error {
	o, err := s.repo.Get(ctx, orderID)
	if err != nil {
		return err
	}
	if o.Status != StatusPending {
		return nil
	}
	// 双重保险：延迟消息若因交换机异常被立即投递，未到超时点不取消
	if time.Since(o.CreatedAt) < acceptTimeout {
		logger.L().Warn("接单超时消息早于超时时间到达，跳过取消", zap.Int64("order_id", orderID))
		return nil
	}
	return s.cancelPaid(ctx, o, "超时未接单自动取消", NotifyOrderRejected)
}

// ---------- 视图组装 ----------

func (s *serviceImpl) buildVOs(ctx context.Context, list []*Order) []*OrderVO {
	ids := make([]int64, 0, len(list))
	for _, o := range list {
		ids = append(ids, o.PackageID)
	}
	names, err := s.repo.PackageNames(ctx, ids)
	if err != nil {
		logger.L().Warn("批量查询套餐名失败", zap.Error(err))
		names = map[int64]string{}
	}
	vos := make([]*OrderVO, 0, len(list))
	for _, o := range list {
		vos = append(vos, &OrderVO{Order: o, PackageName: names[o.PackageID]})
	}
	return vos
}

func (s *serviceImpl) buildDetail(ctx context.Context, o *Order) (*OrderDetail, error) {
	d := &OrderDetail{Order: o, AfterSales: []*AfterSale{}}
	if pkg, err := s.repo.GetPackage(ctx, o.PackageID); err == nil {
		d.PackageName = pkg.Name
	}
	if rv, err := s.repo.GetReviewByOrder(ctx, o.ID); err == nil {
		d.Review = rv
	}
	afters, err := s.repo.ListAfterSaleByOrder(ctx, o.ID)
	if err != nil {
		return nil, err
	}
	d.AfterSales = afters
	return d, nil
}

// ---------- RegisterConsumers MQ 消费者注册（bootstrap 调用一次） ----------

func RegisterConsumers(bus *mq.MQ, svc Service) {
	bus.Consume(mq.QueueOrderPay, func(ctx context.Context, body []byte) error {
		var m PaySuccessMsg
		if err := json.Unmarshal(body, &m); err != nil {
			return err
		}
		if m.BizType != BizTypeOrder {
			return nil
		}
		return svc.HandlePaySuccess(ctx, m.BizID, m.PaymentNo)
	})
	bus.Consume(mq.QueueOrderTimeout, func(ctx context.Context, body []byte) error {
		var m OrderTimeoutMsg
		if err := json.Unmarshal(body, &m); err != nil {
			return err
		}
		return svc.HandleTimeout(ctx, m.OrderID)
	})
	bus.Consume(mq.QueueOrderRefund, func(ctx context.Context, body []byte) error {
		var m BizMsg
		if err := json.Unmarshal(body, &m); err != nil {
			return err
		}
		if m.BizType != BizTypeOrder {
			return nil
		}
		return svc.HandleRefunded(ctx, m.BizID)
	})
}

// AdminListReviews 管理端评价列表
func (s *serviceImpl) AdminListReviews(ctx context.Context, page, size int) ([]*Review, int64, error) {
	return s.repo.AdminListReviews(ctx, page, size)
}

// AdminSetReviewStatus 隐藏/恢复评价（同步触发重算评分由 chef 模块消费端处理）
func (s *serviceImpl) AdminSetReviewStatus(ctx context.Context, id int64, status int8) error {
	rv, err := s.repo.GetReview(ctx, id)
	if err != nil {
		return err
	}
	if err := s.repo.SetReviewStatus(ctx, id, status); err != nil {
		return err
	}
	// 隐藏/恢复影响评分，重发评分重算事件
	return s.bus.Publish(mq.EventExchange, mq.KeyReviewCreated, map[string]any{"chef_id": rv.ChefID, "review_id": rv.ID, "rating": rv.Rating})
}
