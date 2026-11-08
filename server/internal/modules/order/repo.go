package order

import (
	"context"
	"time"

	"gorm.io/gorm"
)

type Repo struct {
	db *gorm.DB
}

func NewRepo(db *gorm.DB) *Repo { return &Repo{db: db} }

// q 带上下文的会话（避免与 db 字段重名）
func (r *Repo) q(ctx context.Context) *gorm.DB { return r.db.WithContext(ctx) }

// ---------- 订单 ----------

func (r *Repo) Create(ctx context.Context, o *Order) error {
	return r.q(ctx).Create(o).Error
}

func (r *Repo) Get(ctx context.Context, id int64) (*Order, error) {
	var o Order
	if err := r.q(ctx).First(&o, id).Error; err != nil {
		return nil, err
	}
	return &o, nil
}

// Transition 状态机迁移（乐观锁：仅 from 匹配时更新），返回是否迁移成功；
// (from,to) 先过 checkEdge，非法流转在落库前即被拒绝
func (r *Repo) Transition(ctx context.Context, id int64, from, to int8, cols map[string]any) (bool, error) {
	if err := checkEdge(from, to); err != nil {
		return false, err
	}
	upd := map[string]any{"status": to}
	for k, v := range cols {
		upd[k] = v
	}
	res := r.q(ctx).Model(&Order{}).Where("id = ? AND status = ?", id, from).Updates(upd)
	return res.RowsAffected > 0, res.Error
}

// TransitionByChef 带乡厨归属校验的状态迁移（同样受 checkEdge 约束）
func (r *Repo) TransitionByChef(ctx context.Context, id, chefID int64, from, to int8, cols map[string]any) (bool, error) {
	if err := checkEdge(from, to); err != nil {
		return false, err
	}
	upd := map[string]any{"status": to}
	for k, v := range cols {
		upd[k] = v
	}
	res := r.q(ctx).Model(&Order{}).
		Where("id = ? AND chef_id = ? AND status = ?", id, chefID, from).Updates(upd)
	return res.RowsAffected > 0, res.Error
}

// RecalcPaid 实付累计以已支付流水求和为准（天然幂等，消息重投不会重复累加）
func (r *Repo) RecalcPaid(ctx context.Context, orderID int64) error {
	return r.q(ctx).Exec(
		"UPDATE orders SET paid_amount = ("+
			"SELECT COALESCE(SUM(amount), 0) FROM payments "+
			"WHERE biz_type = ? AND biz_id = ? AND status = 1) WHERE id = ?",
		BizTypeOrder, orderID, orderID).Error
}

func (r *Repo) SetRefundStatus(ctx context.Context, id int64, st int8) error {
	return r.q(ctx).Model(&Order{}).Where("id = ?", id).
		Update("refund_status", st).Error
}

func (r *Repo) ListByUser(ctx context.Context, uid int64, page, size int) ([]*Order, int64, error) {
	q := r.q(ctx).Model(&Order{}).Where("user_id = ?", uid)
	return listOrders(q, page, size)
}

func (r *Repo) ListByChef(ctx context.Context, chefID int64, status *int8, page, size int) ([]*Order, int64, error) {
	q := r.q(ctx).Model(&Order{}).Where("chef_id = ?", chefID)
	if status != nil {
		q = q.Where("status = ?", *status)
	}
	return listOrders(q, page, size)
}

func (r *Repo) ListAdmin(ctx context.Context, status *int8, keyword string, page, size int) ([]*Order, int64, error) {
	q := r.q(ctx).Model(&Order{})
	if status != nil {
		q = q.Where("status = ?", *status)
	}
	if keyword != "" {
		q = q.Where("order_no LIKE ?", "%"+keyword+"%")
	}
	return listOrders(q, page, size)
}

func listOrders(q *gorm.DB, page, size int) ([]*Order, int64, error) {
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []*Order
	err := q.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error
	return list, total, err
}

// ---------- 评价 ----------

func (r *Repo) CreateReview(ctx context.Context, rv *Review) error {
	return r.q(ctx).Create(rv).Error
}

func (r *Repo) GetReviewByOrder(ctx context.Context, orderID int64) (*Review, error) {
	var rv Review
	if err := r.q(ctx).Where("order_id = ?", orderID).First(&rv).Error; err != nil {
		return nil, err
	}
	return &rv, nil
}

func (r *Repo) ListReviewByChef(ctx context.Context, chefID int64, page, size int) ([]*Review, int64, error) {
	q := r.q(ctx).Model(&Review{}).Where("chef_id = ? AND status = 1", chefID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []*Review
	err := q.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error
	return list, total, err
}

// SetReviewStatus 管理端隐藏/恢复评价
func (r *Repo) SetReviewStatus(ctx context.Context, id int64, st int8) error {
	return r.q(ctx).Model(&Review{}).Where("id = ?", id).Update("status", st).Error
}

// ---------- 售后 ----------

func (r *Repo) CreateAfterSale(ctx context.Context, a *AfterSale) error {
	return r.q(ctx).Create(a).Error
}

func (r *Repo) GetAfterSale(ctx context.Context, id int64) (*AfterSale, error) {
	var a AfterSale
	if err := r.q(ctx).First(&a, id).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

func (r *Repo) UpdateAfterSale(ctx context.Context, id int64, cols map[string]any) error {
	return r.q(ctx).Model(&AfterSale{}).Where("id = ?", id).Updates(cols).Error
}

// ListAfterSale 管理端售后列表
func (r *Repo) ListAfterSale(ctx context.Context, status *int8, page, size int) ([]*AfterSale, int64, error) {
	q := r.q(ctx).Model(&AfterSale{})
	if status != nil {
		q = q.Where("status = ?", *status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []*AfterSale
	err := q.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error
	return list, total, err
}

// ListAfterSaleByOrder 订单详情内嵌售后记录
func (r *Repo) ListAfterSaleByOrder(ctx context.Context, orderID int64) ([]*AfterSale, error) {
	var list []*AfterSale
	err := r.q(ctx).Where("order_id = ?", orderID).Order("id DESC").Find(&list).Error
	return list, err
}

// ---------- 跨表只读快查（sqlc 级原始 SQL，均为只读） ----------

// PackageBrief 套餐下单校验所需字段（packages 表只读）
type PackageBrief struct {
	ID            int64
	ChefID        int64
	Name          string
	PricePerTable int64
	Status        int8
}

func (r *Repo) GetPackage(ctx context.Context, id int64) (*PackageBrief, error) {
	var p PackageBrief
	res := r.q(ctx).Raw(
		"SELECT id, chef_id, name, price_per_table, status FROM packages WHERE id = ?", id,
	).Scan(&p)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return &p, nil
}

// PackageNames 批量套餐名（列表展示用）
func (r *Repo) PackageNames(ctx context.Context, ids []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		ID   int64
		Name string
	}
	if err := r.q(ctx).Raw(
		"SELECT id, name FROM packages WHERE id IN (?)", ids,
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ID] = row.Name
	}
	return out, nil
}

// scheduleRow 档期行（chef_schedules 表只读）
type scheduleRow struct {
	Status  int8
	OrderID int64
}

// GetSchedule 读档期；无行视为空闲
func (r *Repo) GetSchedule(ctx context.Context, chefID int64, day time.Time) (*scheduleRow, bool, error) {
	var row scheduleRow
	res := r.q(ctx).Raw(
		"SELECT status, order_id FROM chef_schedules WHERE chef_id = ? AND date = ?", chefID, day,
	).Scan(&row)
	if res.Error != nil {
		return nil, false, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, false, nil
	}
	return &row, true, nil
}

// ChefUserID chefs 表只读：乡厨归属用户ID（用于给乡厨发通知）
func (r *Repo) ChefUserID(ctx context.Context, chefID int64) (int64, error) {
	var uid int64
	res := r.q(ctx).Raw("SELECT user_id FROM chefs WHERE id = ?", chefID).Scan(&uid)
	if res.Error != nil {
		return 0, res.Error
	}
	if res.RowsAffected == 0 {
		return 0, gorm.ErrRecordNotFound
	}
	return uid, nil
}

// couponRow 下单用券校验（user_coupons JOIN coupons 只读）
type couponRow struct {
	ID        int64
	Status    int8
	Threshold int64
	Amount    int64
	Type      int8
}

func (r *Repo) CouponUsable(ctx context.Context, uid, ucID int64) (*couponRow, error) {
	var c couponRow
	res := r.q(ctx).Raw(
		"SELECT uc.id, uc.status, c.threshold, c.amount, c.type "+
			"FROM user_coupons uc JOIN coupons c ON c.id = uc.coupon_id "+
			"WHERE uc.id = ? AND uc.user_id = ? AND uc.status = 0 AND c.valid_to > NOW()",
		ucID, uid,
	).Scan(&c)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return &c, nil
}

// ---------- payments 表只读 ----------

type paymentRow struct {
	PaymentNo string
	Amount    int64
}

// ListActivePayments 已支付（status=1）流水，按下单顺序
func (r *Repo) ListActivePayments(ctx context.Context, bizID int64) ([]paymentRow, error) {
	var list []paymentRow
	err := r.q(ctx).Raw(
		"SELECT payment_no, amount FROM payments "+
			"WHERE biz_type = ? AND biz_id = ? AND status = 1 ORDER BY id",
		BizTypeOrder, bizID,
	).Scan(&list).Error
	return list, err
}

// LatestActivePaymentNo 最近一笔已支付流水（售后按单笔退款用）
func (r *Repo) LatestActivePaymentNo(ctx context.Context, bizID int64) (string, error) {
	var no string
	res := r.q(ctx).Raw(
		"SELECT payment_no FROM payments "+
			"WHERE biz_type = ? AND biz_id = ? AND status = 1 ORDER BY id DESC LIMIT 1",
		BizTypeOrder, bizID,
	).Scan(&no)
	if res.Error != nil {
		return "", res.Error
	}
	if res.RowsAffected == 0 {
		return "", gorm.ErrRecordNotFound
	}
	return no, nil
}

// PaymentAmountByNo 按支付单号查实付金额
func (r *Repo) PaymentAmountByNo(ctx context.Context, paymentNo string) (int64, error) {
	var amt int64
	res := r.q(ctx).Raw(
		"SELECT amount FROM payments WHERE payment_no = ?", paymentNo,
	).Scan(&amt)
	if res.Error != nil {
		return 0, res.Error
	}
	if res.RowsAffected == 0 {
		return 0, gorm.ErrRecordNotFound
	}
	return amt, nil
}

// ---------- 统计（orders / payments 只读聚合） ----------

// StatsGMV 已支付流水总额
func (r *Repo) StatsGMV(ctx context.Context) (int64, error) {
	var v int64
	err := r.q(ctx).Raw(
		"SELECT COALESCE(SUM(amount), 0) FROM payments WHERE biz_type = ? AND status = 1",
		BizTypeOrder,
	).Scan(&v).Error
	return v, err
}

// StatsStatusCounts 各状态订单数
func (r *Repo) StatsStatusCounts(ctx context.Context) (map[int8]int64, error) {
	var rows []struct {
		Status int8
		Cnt    int64
	}
	if err := r.q(ctx).Raw(
		"SELECT status, COUNT(*) AS cnt FROM orders GROUP BY status",
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int8]int64, len(rows))
	for _, row := range rows {
		out[row.Status] = row.Cnt
	}
	return out, nil
}

// StatsRegions 已完成订单区域分布 TOP10
func (r *Repo) StatsRegions(ctx context.Context) ([]RegionCount, error) {
	var list []RegionCount
	err := r.q(ctx).Raw(
		"SELECT region, COUNT(*) AS cnt FROM orders WHERE status = ? "+
			"GROUP BY region ORDER BY cnt DESC LIMIT 10",
		StatusDone,
	).Scan(&list).Error
	return list, err
}

// StatsRepurchase 复购率分子/分母：完成 >=2 单用户数 / 完成 >=1 单用户数
func (r *Repo) StatsRepurchase(ctx context.Context) (repeat, total int64, err error) {
	err = r.q(ctx).Raw(
		"SELECT COUNT(DISTINCT user_id) FROM orders WHERE status = ?",
		StatusDone,
	).Scan(&total).Error
	if err != nil {
		return 0, 0, err
	}
	err = r.q(ctx).Raw(
		"SELECT COUNT(*) FROM ("+
			"SELECT user_id FROM orders WHERE status = ? GROUP BY user_id HAVING COUNT(*) >= 2) t",
		StatusDone,
	).Scan(&repeat).Error
	return repeat, total, err
}

// UpdateAfterSaleStatusByOrder 退款到账后按订单推进售后单状态
func (r *Repo) UpdateAfterSaleStatusByOrder(ctx context.Context, orderID int64, from, to int8) error {
	return r.q(ctx).Model(&AfterSale{}).
		Where("order_id = ? AND status = ?", orderID, from).
		Update("status", to).Error
}

// GetReview 按 ID 取评价
func (r *Repo) GetReview(ctx context.Context, id int64) (*Review, error) {
	var rv Review
	if err := r.q(ctx).First(&rv, id).Error; err != nil {
		return nil, err
	}
	return &rv, nil
}

// AdminListReviews 管理端评价列表（联表带订单号便于定位）
func (r *Repo) AdminListReviews(ctx context.Context, page, size int) ([]*Review, int64, error) {
	q := r.q(ctx).Model(&Review{})
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []*Review
	err := q.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error
	return list, total, err
}
