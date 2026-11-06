package food

import (
	"context"

	"gorm.io/gorm"

	"github.com/xiangchu/server/internal/modules/pay"
	"github.com/xiangchu/server/internal/pkg/errs"
)

type Repo struct {
	db *gorm.DB
}

func NewRepo(db *gorm.DB) *Repo { return &Repo{db: db} }

// q 带 ctx 的会话（避免与字段 db 重名）
func (r *Repo) q(ctx context.Context) *gorm.DB {
	return r.db.WithContext(ctx)
}

// ---------- ingredient ----------

func (r *Repo) CreateIngredient(ctx context.Context, ing *Ingredient) error {
	return r.q(ctx).Create(ing).Error
}

// UpdateIngredient 更新食材字段（仅供应商自己的记录）
func (r *Repo) UpdateIngredient(ctx context.Context, ing *Ingredient) error {
	return r.q(ctx).Model(ing).Select(
		"name", "category", "unit", "price", "stock", "origin", "images",
	).Updates(ing).Error
}

// GetIngredient 主键查询
func (r *Repo) GetIngredient(ctx context.Context, id int64) (*Ingredient, error) {
	var ing Ingredient
	if err := r.q(ctx).First(&ing, id).Error; err != nil {
		return nil, err
	}
	return &ing, nil
}

// GetOwnIngredient 校验归属：supplier_id + id
func (r *Repo) GetOwnIngredient(ctx context.Context, supID, id int64) (*Ingredient, error) {
	var ing Ingredient
	err := r.q(ctx).Where("supplier_id = ?", supID).First(&ing, id).Error
	if err != nil {
		return nil, err
	}
	return &ing, nil
}

// SetIngredientStatus 下架/上架（删除即下架）
func (r *Repo) SetIngredientStatus(ctx context.Context, supID, id int64, status int8) error {
	return r.q(ctx).Model(&Ingredient{}).
		Where("id = ? AND supplier_id = ?", id, supID).
		Update("status", status).Error
}

// ListIngredients 通用食材分页查询
func (r *Repo) ListIngredients(ctx context.Context, f *IngredientFilter, onlyOn bool) ([]*Ingredient, int64, error) {
	q := r.q(ctx).Model(&Ingredient{})
	if f.SupplierID > 0 {
		q = q.Where("supplier_id = ?", f.SupplierID)
	}
	if onlyOn {
		q = q.Where("status = ?", IngStatusOn)
	}
	if f.Category != "" {
		q = q.Where("category = ?", f.Category)
	}
	if f.Keyword != "" {
		kw := "%" + f.Keyword + "%"
		q = q.Where("name LIKE ? OR origin LIKE ?", kw, kw)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []*Ingredient
	err := q.Order("id DESC").Offset((f.Page - 1) * f.Size).Limit(f.Size).Find(&list).Error
	return list, total, err
}

// ListIngredientsByIDs 按 ID 批量取在售食材（下单校验用）
func (r *Repo) ListIngredientsByIDs(ctx context.Context, ids []int64) ([]*Ingredient, error) {
	var list []*Ingredient
	err := r.q(ctx).Where("id IN ?", ids).Find(&list).Error
	return list, err
}

// LockStock 接单锁定库存：stock 扣减 + locked_stock 增加，任一行不足即整体回滚
func (r *Repo) LockStock(ctx context.Context, items []*PurchaseItem) error {
	return r.q(ctx).Transaction(func(tx *gorm.DB) error {
		for _, it := range items {
			// gorm.Expr 把值编译成 SQL 表达式，读改写在一条 UPDATE 内原子完成；
			// 若直接写 "stock": it.Quantity 会变成 SET stock = 5 的绝对赋值，
			// 并发接单互相覆盖导致丢更新。
			// 生成的 SQL（以 ingredient_id=42、quantity=5 为例）：
			//   UPDATE ingredients
			//   SET stock = stock - 5,
			//       locked_stock = locked_stock + 5,
			//       updated_at = '...'                -- GORM 自动维护
			//   WHERE id = 42 AND stock >= 5
			//   -- "检查够不够 + 扣减"在同一行锁内完成；RowsAffected=0 即库存不足
			res := tx.Model(&Ingredient{}).
				Where("id = ? AND stock >= ?", it.IngredientID, it.Quantity).
				Updates(map[string]any{
					"stock":        gorm.Expr("stock - ?", it.Quantity),
					"locked_stock": gorm.Expr("locked_stock + ?", it.Quantity),
				})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return errs.ErrStockShort
			}
		}
		return nil
	})
}

// ---------- purchase order ----------

// CreatePurchase 采购单 + 明细同一事务落库
func (r *Repo) CreatePurchase(ctx context.Context, po *PurchaseOrder, items []*PurchaseItem) error {
	return r.q(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(po).Error; err != nil {
			return err
		}
		for _, it := range items {
			it.PurchaseOrderID = po.ID
		}
		return tx.Create(&items).Error
	})
}

// GetPurchase 主键查询采购单
func (r *Repo) GetPurchase(ctx context.Context, id int64) (*PurchaseOrder, error) {
	var po PurchaseOrder
	if err := r.q(ctx).First(&po, id).Error; err != nil {
		return nil, err
	}
	return &po, nil
}

// ListPurchaseItems 采购单明细
func (r *Repo) ListPurchaseItems(ctx context.Context, poID int64) ([]*PurchaseItem, error) {
	var list []*PurchaseItem
	err := r.q(ctx).Where("purchase_order_id = ?", poID).Order("id ASC").Find(&list).Error
	return list, err
}

// Transition 状态机推进：仅 from 状态匹配时更新（乐观并发）
func (r *Repo) Transition(ctx context.Context, id int64, from, to int8) (int64, error) {
	res := r.q(ctx).Model(&PurchaseOrder{}).
		Where("id = ? AND status = ?", id, from).
		Update("status", to)
	return res.RowsAffected, res.Error
}

// MarkPaid 支付成功回调：paid_amount=total_amount，状态保持待接单
func (r *Repo) MarkPaid(ctx context.Context, id, amount int64) error {
	return r.q(ctx).Model(&PurchaseOrder{}).Where("id = ?", id).
		Update("paid_amount", amount).Error
}

// ListPurchases 通用采购单分页查询
func (r *Repo) ListPurchases(ctx context.Context, f *PurchaseFilter) ([]*PurchaseOrder, int64, error) {
	q := r.q(ctx).Model(&PurchaseOrder{})
	if f.ChefID > 0 {
		q = q.Where("chef_id = ?", f.ChefID)
	}
	if f.SupplierID > 0 {
		q = q.Where("supplier_id = ?", f.SupplierID)
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	if f.FulfillMethod > 0 {
		q = q.Where("fulfill_method = ?", f.FulfillMethod)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []*PurchaseOrder
	err := q.Order("id DESC").Offset((f.Page - 1) * f.Size).Limit(f.Size).Find(&list).Error
	return list, total, err
}

// PurchaseStats 管理端统计：总量/待接单/已完成/履约分布/已支付 GMV
func (r *Repo) PurchaseStats(ctx context.Context) (*PurchaseStatsVO, error) {
	vo := &PurchaseStatsVO{ByFulfill: map[int8]int64{}}
	base := r.q(ctx).Model(&PurchaseOrder{})
	if err := base.Count(&vo.Total).Error; err != nil {
		return nil, err
	}
	if err := r.q(ctx).Model(&PurchaseOrder{}).
		Where("status = ?", POStatusPending).Count(&vo.Pending).Error; err != nil {
		return nil, err
	}
	if err := r.q(ctx).Model(&PurchaseOrder{}).
		Where("status = ?", POStatusCompleted).Count(&vo.Completed).Error; err != nil {
		return nil, err
	}
	type row struct {
		FulfillMethod int8
		N             int64
	}
	var rows []row
	if err := r.q(ctx).Model(&PurchaseOrder{}).
		Select("fulfill_method, COUNT(*) AS n").
		Group("fulfill_method").Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, rw := range rows {
		vo.ByFulfill[rw.FulfillMethod] = rw.N
	}
	err := r.q(ctx).Model(&PurchaseOrder{}).
		Where("paid_amount > 0").
		Select("COALESCE(SUM(total_amount),0) AS gmv").Scan(&vo.GMV).Error
	return vo, err
}

// ---------- 跨模块只读（raw SQL，经批准的只读访问） ----------

// ChefIDByUser 登录用户 → 已通过审核的乡厨 ID（chefs 表只读）
func (r *Repo) ChefIDByUser(ctx context.Context, uid int64) (int64, error) {
	var id int64
	err := r.q(ctx).Raw(
		"SELECT id FROM chefs WHERE user_id = ? AND status = 1 ORDER BY id LIMIT 1", uid,
	).Scan(&id).Error
	if err != nil {
		return 0, err
	}
	if id == 0 {
		return 0, errs.BadRequest("请先完成乡厨入驻")
	}
	return id, nil
}

// ChefUIDByID 乡厨 ID → 登录用户 ID（通知用，chefs 表只读）
func (r *Repo) ChefUIDByID(ctx context.Context, chefID int64) (int64, error) {
	var uid int64
	err := r.q(ctx).Raw(
		"SELECT user_id FROM chefs WHERE id = ? ORDER BY id LIMIT 1", chefID,
	).Scan(&uid).Error
	return uid, err
}

// OrderOwner 校验宴席订单归属：返回 orders.user_id（orders 表只读）
func (r *Repo) OrderOwner(ctx context.Context, orderID int64) (int64, error) {
	var uid int64
	err := r.q(ctx).Raw(
		"SELECT user_id FROM orders WHERE id = ? ORDER BY id LIMIT 1", orderID,
	).Scan(&uid).Error
	return uid, err
}

// LatestPaidPaymentNo 采购单最近一笔已支付支付单号（payments 表只读，退款定位用）
func (r *Repo) LatestPaidPaymentNo(ctx context.Context, bizID int64) (string, error) {
	var no string
	err := r.q(ctx).Raw(
		"SELECT payment_no FROM payments WHERE biz_type = ? AND biz_id = ? AND status = ? ORDER BY id DESC LIMIT 1",
		pay.BizTypePurchase, bizID, pay.PayStatusPaid,
	).Scan(&no).Error
	return no, err
}

// SupplierUIDByID 供应商 ID → 登录用户 ID（通知用，suppliers 表只读）
func (r *Repo) SupplierUIDByID(ctx context.Context, supID int64) (int64, error) {
	var uid int64
	err := r.q(ctx).Raw(
		"SELECT user_id FROM suppliers WHERE id = ? ORDER BY id LIMIT 1", supID,
	).Scan(&uid).Error
	return uid, err
}
