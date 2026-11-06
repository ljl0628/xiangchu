package user

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/xiangchu/server/internal/pkg/errs"
)

type Repo struct {
	db *gorm.DB
}

func NewRepo(db *gorm.DB) *Repo { return &Repo{db: db} }

// ---------- user ----------

func (r *Repo) q(ctx context.Context) *gorm.DB {
	return r.db.WithContext(ctx)
}

func (r *Repo) GetByPhone(ctx context.Context, phone string) (*User, error) {
	var u User
	err := r.q(ctx).Where("phone = ?", phone).First(&u).Error
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *Repo) Get(ctx context.Context, id int64) (*User, error) {
	var u User
	err := r.q(ctx).First(&u, id).Error
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *Repo) Create(ctx context.Context, u *User) error {
	return r.q(ctx).Create(u).Error
}

func (r *Repo) Update(ctx context.Context, u *User, fields ...string) error {
	m := r.q(ctx).Model(u)
	if len(fields) > 0 {
		m = m.Select(fields)
	}
	return m.Updates(u).Error
}

func (r *Repo) SetStatus(ctx context.Context, id int64, status int8) error {
	return r.q(ctx).Model(&User{}).Where("id = ?", id).
		Update("status", status).Error
}

func (r *Repo) List(ctx context.Context, keyword string, page, size int) ([]*User, int64, error) {
	q := r.q(ctx).Model(&User{})
	if keyword != "" {
		kw := "%" + keyword + "%"
		q = q.Where("phone LIKE ? OR nickname LIKE ?", kw, kw)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []*User
	err := q.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error
	return list, total, err
}

// ---------- address ----------

func (r *Repo) ListAddress(ctx context.Context, uid int64) ([]*UserAddress, error) {
	var list []*UserAddress
	err := r.q(ctx).Where("user_id = ?", uid).Order("is_default DESC, id DESC").Find(&list).Error
	return list, err
}

func (r *Repo) GetAddress(ctx context.Context, uid, id int64) (*UserAddress, error) {
	var a UserAddress
	err := r.q(ctx).Where("user_id = ?", uid).First(&a, id).Error
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (r *Repo) CreateAddress(ctx context.Context, a *UserAddress) error {
	return r.q(ctx).Create(a).Error
}

func (r *Repo) UpdateAddress(ctx context.Context, a *UserAddress, fields ...string) error {
	m := r.q(ctx).Model(a)
	if len(fields) > 4 {
		m = m.Select(fields)
	}
	return m.Updates(a).Error
}

func (r *Repo) DeleteAddress(ctx context.Context, uid, id int64) error {
	return r.q(ctx).Where("user_id = ?", uid).Delete(&UserAddress{}, id).Error
}

// ---------- coupon ----------

func (r *Repo) ListCoupon(ctx context.Context, onlyActive bool, page, size int) ([]*Coupon, int64, error) {
	q := r.q(ctx).Model(&Coupon{})
	if onlyActive {
		now := time.Now()
		q = q.Where("status = 1 AND valid_from <= ? AND valid_to > ?", now, now)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []*Coupon
	err := q.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error
	return list, total, err
}

func (r *Repo) GetCoupon(ctx context.Context, id int64) (*Coupon, error) {
	var c Coupon
	err := r.q(ctx).First(&c, id).Error
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ReceiveCouponTxn 单事务领取：先插实例（uk_user_coupon 唯一索引短路重复领取，不占热点行锁），
// 再条件递增计数（上架中 + 有效期内 + 未超发；RowsAffected==0 即整体回滚）。
// 库存与时效判定以此条件更新为唯一权威，替代旧的「读计数→递增→插行」三步无事务写法。
func (r *Repo) ReceiveCouponTxn(ctx context.Context, uid, couponID int64, expireAt time.Time) error {
	return r.q(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Clauses(clause.OnConflict{DoNothing: true}).
			Create(&UserCoupon{UserID: uid, CouponID: couponID, ExpireAt: expireAt})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			// ON DUPLICATE KEY UPDATE id=id 下重复行影响行数为 0：一人一券由唯一索引兜底
			return errs.ErrCouponTaken
		}
		now := time.Now()
		res2 := tx.Model(&Coupon{}).
			Where("id = ? AND status = 1 AND valid_from <= ? AND valid_to > ? AND received_count < total_count",
				couponID, now, now).
			UpdateColumn("received_count", gorm.Expr("received_count + 1"))
		if res2.Error != nil {
			return res2.Error
		}
		if res2.RowsAffected == 0 {
			// 已抢完 / 已下架 / 不在有效期 → 回滚上面的插行
			return errs.ErrCouponSoldOut
		}
		return nil
	})
}

func (r *Repo) MyCoupons(ctx context.Context, uid int64) ([]*UserCoupon, error) {
	var list []*UserCoupon
	err := r.q(ctx).Where("user_id = ?", uid).Order("id DESC").Find(&list).Error
	return list, err
}

// HeldCouponIDs 用户在给定券模板中已持有的模板 ID 集合（券中心"已领取"标记用）。
// 未登录（uid=0）或空列表直接返回空，不发查询。
func (r *Repo) HeldCouponIDs(ctx context.Context, uid int64, couponIDs []int64) ([]int64, error) {
	if uid == 0 || len(couponIDs) == 0 {
		return nil, nil
	}
	var ids []int64
	err := r.q(ctx).Model(&UserCoupon{}).
		Where("user_id = ? AND coupon_id IN ?", uid, couponIDs).
		Pluck("coupon_id", &ids).Error
	return ids, err
}

func (r *Repo) GetUserCoupon(ctx context.Context, uid, id int64) (*UserCoupon, error) {
	var uc UserCoupon
	err := r.q(ctx).Where("user_id = ?", uid).First(&uc, id).Error
	if err != nil {
		// not found or not mine
		return nil, err
	}
	return &uc, nil
}

// OccupyCoupon 下单锁券：状态 0→2 条件更新防并发双花（两张订单抢同一张券，
// 仅一张 RowsAffected=1，另一张报错），库存语义同 received_count 的条件递增
func (r *Repo) OccupyCoupon(ctx context.Context, uid, id int64) error {
	res := r.q(ctx).Model(&UserCoupon{}).
		Where("id = ? AND user_id = ? AND status = 0", id, uid).
		Update("status", 2)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errs.BadRequest("优惠券已被其他订单使用")
	}
	return nil
}

// UseCoupon 支付成功核销：2→1（兼容存量 0→1），条件更新兜底双花
func (r *Repo) UseCoupon(ctx context.Context, id int64, orderID int64) error {
	res := r.q(ctx).Model(&UserCoupon{}).
		Where("id = ? AND status IN (0, 2)", id).
		Updates(map[string]any{"status": 1, "order_id": orderID})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errs.BadRequest("优惠券不在可核销状态")
	}
	return nil
}

// ---------- repo 补充方法 ----------

func (r *Repo) GetByOpenID(ctx context.Context, openID string) (*User, error) {
	var u User
	err := r.q(ctx).Where("open_id = ?", openID).First(&u).Error
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *Repo) ClearDefault(ctx context.Context, uid int64) error {
	return r.q(ctx).Model(&UserAddress{}).Where("user_id = ?", uid).
		Update("is_default", false).Error
}

// GrantCouponTxn 批量发放：事务内幂等插行（已持有的自动跳过）+ 一次条件递增，
// 剩余额不足则整体回滚。返回实际新发放的用户数。
func (r *Repo) GrantCouponTxn(ctx context.Context, couponID int64, uids []int64, expireAt time.Time) (int64, error) {
	granted := int64(0)
	err := r.q(ctx).Transaction(func(tx *gorm.DB) error {
		granted = 0
		rows := make([]*UserCoupon, 0, len(uids))
		for _, uid := range uids {
			rows = append(rows, &UserCoupon{UserID: uid, CouponID: couponID, ExpireAt: expireAt})
		}
		res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows)
		if res.Error != nil {
			return res.Error
		}
		granted = res.RowsAffected
		if granted == 0 {
			return nil // 全部已持有，无需递增，直接提交空事务
		}
		res2 := tx.Model(&Coupon{}).
			Where("id = ? AND status = 1 AND received_count + ? <= total_count", couponID, granted).
			UpdateColumn("received_count", gorm.Expr("received_count + ?", granted))
		if res2.Error != nil {
			return res2.Error
		}
		if res2.RowsAffected == 0 {
			return errs.BadRequest("剩余数量不足，发放已回滚")
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return granted, nil
}

// ReleaseCoupon 取消/超时/拒绝时释放：2→0（未支付占用）或 1→0（已核销退回），
// 条件更新幂等——券已释放时 RowsAffected=0 静默成功
func (r *Repo) ReleaseCoupon(ctx context.Context, ucID int64) error {
	return r.q(ctx).Model(&UserCoupon{}).
		Where("id = ? AND status IN (1, 2)", ucID).
		Update("status", 0).Error
}

// ---------- 管理端券模板 ----------

// AdminListCoupon 管理端券模板列表（含下架），status 传 nil 查全部
func (r *Repo) AdminListCoupon(ctx context.Context, status *int8, page, size int) ([]*Coupon, int64, error) {
	q := r.q(ctx).Model(&Coupon{})
	if status != nil {
		q = q.Where("status = ?", *status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []*Coupon
	err := q.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error
	return list, total, err
}

func (r *Repo) CreateCoupon(ctx context.Context, c *Coupon) error {
	return r.q(ctx).Create(c).Error
}

// UpdateCoupon 只更新指定列（received_count/status 不经此入口）
func (r *Repo) UpdateCoupon(ctx context.Context, c *Coupon, fields ...string) error {
	return r.q(ctx).Model(c).Where("id = ?", c.ID).Select(fields).Updates(c).Error
}

func (r *Repo) SetCouponStatus(ctx context.Context, id int64, status int8) error {
	return r.q(ctx).Model(&Coupon{}).Where("id = ?", id).Update("status", status).Error
}
