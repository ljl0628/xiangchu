package user

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/xiangchu/server/internal/config"
	"github.com/xiangchu/server/internal/infra/logger"
	"github.com/xiangchu/server/internal/infra/mq"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/xiangchu/server/internal/pkg/errs"
	"github.com/xiangchu/server/internal/pkg/jwtx"
	"github.com/xiangchu/server/internal/pkg/password"
)

var phoneRe = regexp.MustCompile(`^1\d{10}$`)

// Service 用户模块服务接口（跨模块只允许通过此接口调用）
type Service interface {
	SendCode(ctx context.Context, phone string) error
	LoginByCode(ctx context.Context, phone, code string) (*LoginResult, error)
	WxLogin(ctx context.Context, code string) (*LoginResult, error)
	WebLogin(ctx context.Context, phone, pwd string) (*LoginResult, error)
	RefreshSession(ctx context.Context, uid int64, wantRole string) (*LoginResult, error)
	Profile(ctx context.Context, uid int64) (*ProfileVO, error)
	UpdateProfile(ctx context.Context, uid int64, req *UpdateProfileReq) error
	SetPassword(ctx context.Context, uid int64, pwd string) error
	GetUser(ctx context.Context, id int64) (*User, error)
	ListForAdmin(ctx context.Context, keyword string, page, size int) ([]*AdminUserVO, int64, error)
	SetStatus(ctx context.Context, id int64, status int8) error
	ListAddress(ctx context.Context, uid int64) ([]*UserAddress, error)
	GetAddress(ctx context.Context, uid, id int64) (*UserAddress, error)
	SaveAddress(ctx context.Context, uid, id int64, req *AddressReq) (int64, error)
	DeleteAddress(ctx context.Context, uid, id int64) error
	ListCoupon(ctx context.Context, uid int64, onlyActive bool, page, size int) ([]*CouponVO, int64, error)
	ReceiveCoupon(ctx context.Context, uid, couponID int64) error
	MyCoupons(ctx context.Context, uid int64) ([]*UserCouponVO, error)
	UseCoupon(ctx context.Context, uid, ucID, orderID int64) error
	OccupyCoupon(ctx context.Context, uid, ucID int64) error
	ReleaseCoupon(ucID int64) error

	// ---- 管理端券模板 ----
	ListCouponForAdmin(ctx context.Context, status *int8, page, size int) ([]*AdminCouponVO, int64, error)
	CreateCoupon(ctx context.Context, req *CouponReq) (int64, error)
	UpdateCoupon(ctx context.Context, id int64, req *CouponReq) error
	SetCouponStatus(ctx context.Context, id int64, status int8) error
	GrantCoupon(ctx context.Context, couponID int64, userIDs []int64) (*GrantResult, error)
}

// ProfileChecker 由 bootstrap 注入，实现方为 chef / supplier 模块
type ProfileChecker interface {
	HasApprovedProfile(ctx context.Context, uid int64) bool
	// FilterApproved 批量版：整页用户只查一次，避免逐个判角色的 N+1
	FilterApproved(ctx context.Context, uids []int64) (map[int64]bool, error)
}

// ---------- impl ----------

type serviceImpl struct {
	repo        *Repo
	rdb         *redis.Client
	mk          *jwtx.Maker
	cfg         *config.Config
	bus         *mq.MQ
	chefChecker ProfileChecker
	supChecker  ProfileChecker
}

func NewService(repo *Repo, rdb *redis.Client, mk *jwtx.Maker, cfg *config.Config, bus *mq.MQ, chefCk ProfileChecker, supCk ProfileChecker) Service {
	return &serviceImpl{repo: repo, rdb: rdb, mk: mk, cfg: cfg, bus: bus, chefChecker: chefCk, supChecker: supCk}
}

const codeTTL = 5 * time.Minute

func (s *serviceImpl) SendCode(ctx context.Context, phone string) error {
	if !phoneRe.MatchString(phone) {
		return errs.ErrPhoneInvalid
	}
	code := s.cfg.SMS.DevCode
	if !s.cfg.SMS.DevMode {
		// TODO: 接入真实短信通道时改为调用 Notifier
		code = genCode()
	}
	if err := s.rdb.Set(ctx, "sms:code:"+phone, code, codeTTL).Err(); err != nil {
		return err
	}
	logger.L().Info("send code", zap.String("phone", phone))
	return nil
}

func genCode() string {
	return fmt.Sprintf("%06d", time.Now().UnixNano()%1000000)
}

// resolveRole 登录角色判定：chef 优先于 supplier，默认 user
func (s *serviceImpl) resolveRole(ctx context.Context, uid int64) string {
	role := "user"
	if s.chefChecker != nil && s.chefChecker.HasApprovedProfile(ctx, uid) {
		role = "chef"
	} else if s.supChecker != nil && s.supChecker.HasApprovedProfile(ctx, uid) {
		role = "supplier"
	}
	return role
}

// login 颁发 JWT
func (s *serviceImpl) login(ctx context.Context, u *User) (*LoginResult, error) {
	if u.Status == StatusDisabled {
		return nil, errs.ErrUserDisabled
	}
	role := s.resolveRole(ctx, u.ID)
	token, err := s.mk.Sign(u.ID, role)
	if err != nil {
		return nil, err
	}
	return &LoginResult{Token: token, Role: role, UserID: u.ID}, nil
}

// RefreshSession 角色变更（如入驻审核通过）后凭有效 token 换发含最新角色的新 JWT，无需重新登录。
// wantRole 非空时（chef/supplier）：双身份账号（既是乡厨又是供应商）按期望角色签发——
// resolveRole 固定 chef 优先，供应商小程序不指定角色会永远换发到 chef。
func (s *serviceImpl) RefreshSession(ctx context.Context, uid int64, wantRole string) (*LoginResult, error) {
	u, err := s.repo.Get(ctx, uid)
	if err != nil {
		return nil, err
	}
	role := s.resolveRole(ctx, u.ID)
	if (wantRole == "chef" || wantRole == "supplier") && wantRole != role && s.hasApproved(ctx, u.ID, wantRole) {
		role = wantRole
	}
	if u.Status == StatusDisabled {
		return nil, errs.ErrUserDisabled
	}
	token, err := s.mk.Sign(u.ID, role)
	if err != nil {
		return nil, err
	}
	return &LoginResult{Token: token, Role: role, UserID: u.ID}, nil
}

// hasApproved 该 uid 是否已有对应角色的已通过档案
func (s *serviceImpl) hasApproved(ctx context.Context, uid int64, role string) bool {
	switch role {
	case "chef":
		return s.chefChecker != nil && s.chefChecker.HasApprovedProfile(ctx, uid)
	case "supplier":
		return s.supChecker != nil && s.supChecker.HasApprovedProfile(ctx, uid)
	}
	return false
}

// LoginByCode 手机号验证码登录（未注册自动注册）
func (s *serviceImpl) LoginByCode(ctx context.Context, phone, code string) (*LoginResult, error) {
	if !phoneRe.MatchString(phone) {
		return nil, errs.ErrPhoneInvalid
	}
	key := "sms:code:" + phone
	stored, err := s.rdb.Get(ctx, key).Result()
	if err != nil || stored != code {
		return nil, errs.ErrCodeInvalid
	}
	_ = s.rdb.Del(ctx, key).Err()
	u, err := s.repo.GetByPhone(ctx, phone)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		u = &User{Phone: phone, Nickname: "用户" + phone[7:], Status: StatusEnabled}
		if err := s.repo.Create(ctx, u); err != nil {
			return nil, err
		}
	}
	return s.login(ctx, u)
}

// WxLogin 微信授权登录（MP 未配置时降级 mock openid 便于联调）
func (s *serviceImpl) WxLogin(ctx context.Context, code string) (*LoginResult, error) {
	openID := "mock_" + code
	if s.cfg.Wx.MP.Enabled {
		var err error
		openID, err = s.wxOpenID(ctx, code)
		if err != nil {
			return nil, err
		}
	}
	u, err := s.repo.GetByOpenID(ctx, openID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		u = &User{OpenID: openID, Nickname: "微信用户", Status: StatusEnabled}
		if err := s.repo.Create(ctx, u); err != nil {
			return nil, err
		}
		return s.login(ctx, u)
	}
	if err != nil {
		return nil, err
	}
	return s.login(ctx, u)
}

// wxOpenID 调微信 code2session 换取 openid
func (s *serviceImpl) wxOpenID(ctx context.Context, code string) (string, error) {
	var out struct {
		OpenID  string `json:"openid"`
		Errcode int    `json:"errcode"`
		Errmsg  string `json:"errmsg"`
	}
	api := "https://api.weixin.qq.com/sns/jscode2session"
	params := url.Values{}
	params.Set("appid", s.cfg.Wx.MP.AppID)
	params.Set("secret", s.cfg.Wx.MP.Secret)
	params.Set("js_code", code)
	params.Set("grant_type", "authorization_code")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api+"?"+params.Encode(), nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Errcode != 0 {
		return "", fmt.Errorf("code2session 失败: %d %s", out.Errcode, out.Errmsg)
	}
	return out.OpenID, nil
}

// Profile 用户资料
func (s *serviceImpl) Profile(ctx context.Context, uid int64) (*ProfileVO, error) {
	u, err := s.repo.Get(ctx, uid)
	if err != nil {
		return nil, err
	}
	return &ProfileVO{ID: u.ID, Phone: u.Phone, Nickname: u.Nickname, Avatar: u.Avatar}, nil
}

// UpdateProfile 更新昵称/头像
func (s *serviceImpl) UpdateProfile(ctx context.Context, uid int64, req *UpdateProfileReq) error {
	u, err := s.repo.Get(ctx, uid)
	if err != nil {
		return err
	}
	fields := make([]string, 0, 2)
	if req.Nickname != "" {
		u.Nickname = req.Nickname
		fields = append(fields, "nickname")
	}
	if req.Avatar != "" {
		u.Avatar = req.Avatar
		fields = append(fields, "avatar")
	}
	if len(fields) == 0 {
		return nil
	}
	return s.repo.Update(ctx, u, fields...)
}

// SetPassword 设置/修改 Web 登录密码（供应商/乡厨后台登录用），bcrypt 存储
func (s *serviceImpl) SetPassword(ctx context.Context, uid int64, pwd string) error {
	if len(pwd) < 6 || len(pwd) > 32 {
		return errs.BadRequest("密码长度需为 6-32 位")
	}
	hash, err := password.Hash(pwd)
	if err != nil {
		return err
	}
	u, err := s.repo.Get(ctx, uid)
	if err != nil {
		return err
	}
	u.PasswordHash = hash
	return s.repo.Update(ctx, u, "password_hash")
}

// GetUser 内部/管理端查询
func (s *serviceImpl) GetUser(ctx context.Context, id int64) (*User, error) {
	return s.repo.Get(ctx, id)
}

// ListForAdmin 管理端用户列表。roles 由档案派生（双身份账号返回多个角色），
// 两个批量查询拿整页角色，避免逐用户判角色的 N+1
func (s *serviceImpl) ListForAdmin(ctx context.Context, keyword string, page, size int) ([]*AdminUserVO, int64, error) {
	users, total, err := s.repo.List(ctx, keyword, page, size)
	if err != nil {
		return nil, 0, err
	}
	uids := make([]int64, 0, len(users))
	for _, u := range users {
		uids = append(uids, u.ID)
	}
	var chefSet, supSet map[int64]bool
	if s.chefChecker != nil {
		if chefSet, err = s.chefChecker.FilterApproved(ctx, uids); err != nil {
			return nil, 0, err
		}
	}
	if s.supChecker != nil {
		if supSet, err = s.supChecker.FilterApproved(ctx, uids); err != nil {
			return nil, 0, err
		}
	}
	list := make([]*AdminUserVO, 0, len(users))
	for _, u := range users {
		// 有档案的角色都返回（双身份 ["chef","supplier"]），均无档案归为 ["user"]
		roles := make([]string, 0, 2)
		if chefSet[u.ID] {
			roles = append(roles, "chef")
		}
		if supSet[u.ID] {
			roles = append(roles, "supplier")
		}
		if len(roles) == 0 {
			roles = append(roles, "user")
		}
		list = append(list, &AdminUserVO{
			ID:        u.ID,
			Phone:     u.Phone,
			Nickname:  u.Nickname,
			Avatar:    u.Avatar,
			Status:    u.Status,
			Roles:     roles,
			CreatedAt: u.CreatedAt,
		})
	}
	return list, total, nil
}

// SetStatus 管理端禁用/启用
func (s *serviceImpl) SetStatus(ctx context.Context, id int64, status int8) error {
	return s.repo.SetStatus(ctx, id, status)
}

// ---------- 地址簿 ----------

func (s *serviceImpl) ListAddress(ctx context.Context, uid int64) ([]*UserAddress, error) {
	return s.repo.ListAddress(ctx, uid)
}

func (s *serviceImpl) GetAddress(ctx context.Context, uid, id int64) (*UserAddress, error) {
	return s.repo.GetAddress(ctx, uid, id)
}

// SaveAddress 新建/更新地址；设为默认时清除其他默认
func (s *serviceImpl) SaveAddress(ctx context.Context, uid, id int64, req *AddressReq) (int64, error) {
	addr := &UserAddress{
		UserID:       uid,
		ContactName:  req.ContactName,
		ContactPhone: req.ContactPhone,
		Region:       req.Region,
		Address:      req.Address,
		IsDefault:    req.IsDefault,
	}
	if req.IsDefault {
		_ = s.repo.ClearDefault(ctx, uid)
	}
	if id > 0 {
		addr.ID = id
		if err := s.repo.UpdateAddress(ctx, addr, "contact_name", "contact_phone", "region", "address", "is_default"); err != nil {
			return 0, err
		}
		return id, nil
	}
	if err := s.repo.CreateAddress(ctx, addr); err != nil {
		return 0, err
	}
	return addr.ID, nil
}

func (s *serviceImpl) DeleteAddress(ctx context.Context, uid, id int64) error {
	return s.repo.DeleteAddress(ctx, uid, id)
}

// ---------- 优惠券 ----------

// ListCoupon 券中心：uid>0 时附带"当前用户已持有"标记，未登录恒为 false
func (s *serviceImpl) ListCoupon(ctx context.Context, uid int64, onlyActive bool, page, size int) ([]*CouponVO, int64, error) {
	list, total, err := s.repo.ListCoupon(ctx, onlyActive, page, size)
	if err != nil {
		return nil, 0, err
	}
	vos := make([]*CouponVO, 0, len(list))
	ids := make([]int64, 0, len(list))
	for _, c := range list {
		ids = append(ids, c.ID)
		vos = append(vos, &CouponVO{
			ID:        c.ID,
			Name:      c.Name,
			Type:      c.Type,
			Threshold: c.Threshold,
			Amount:    c.Amount,
			ValidTo:   c.ValidTo,
		})
	}
	// 批量标记已持有：一次 IN 查询，未登录（uid=0）时 repo 直接返回空
	held, err := s.repo.HeldCouponIDs(ctx, uid, ids)
	fmt.Println("=======>", held)
	if err != nil {
		return nil, 0, err
	}
	heldSet := make(map[int64]struct{}, len(held))
	for _, id := range held {
		heldSet[id] = struct{}{}
	}
	for _, vo := range vos {
		if _, ok := heldSet[vo.ID]; ok {
			vo.Received = true
		}
	}
	return vos, total, nil
}

// ReceiveCoupon 领取优惠券：预检只为友好文案（404/下架/过期），
// 防超发与防重全权交给 ReceiveCouponTxn 的条件更新（库存与时效的唯一权威）。
func (s *serviceImpl) ReceiveCoupon(ctx context.Context, uid, couponID int64) error {
	c, err := s.repo.GetCoupon(ctx, couponID)
	if err != nil {
		return err
	}
	if c.Status != 1 || time.Now().After(c.ValidTo) {
		return errs.BadRequest("优惠券已过期或下架")
	}
	return s.repo.ReceiveCouponTxn(ctx, uid, couponID, c.ValidTo)
}

// MyCoupons 我持有的优惠券列表（联表带出券模板信息）
func (s *serviceImpl) MyCoupons(ctx context.Context, uid int64) ([]*UserCouponVO, error) {
	list, err := s.repo.MyCoupons(ctx, uid)
	if err != nil {
		return nil, err
	}
	vos := make([]*UserCouponVO, 0, len(list))
	for _, uc := range list {
		vo := &UserCouponVO{
			ID:       uc.ID,
			CouponID: uc.CouponID,
			Status:   uc.Status,
			ExpireAt: uc.ExpireAt,
		}
		if c, err := s.repo.GetCoupon(ctx, uc.CouponID); err == nil {
			vo.Name = c.Name
			vo.Type = c.Type
			vo.Threshold = c.Threshold
			vo.Amount = c.Amount
		}
		vos = append(vos, vo)
	}
	return vos, nil
}

// OccupyCoupon 下单锁券：条件更新 0→2，抢不到即已被其他订单占用
func (s *serviceImpl) OccupyCoupon(ctx context.Context, uid, ucID int64) error {
	return s.repo.OccupyCoupon(ctx, uid, ucID)
}

// UseCoupon 支付成功后标记优惠券已使用
func (s *serviceImpl) UseCoupon(ctx context.Context, uid, ucID, orderID int64) error {
	return s.repo.UseCoupon(ctx, ucID, orderID)
}

// ReleaseCoupon 取消订单时释放优惠券（MQ 消费场景无调用方 ctx，用 Background）
func (s *serviceImpl) ReleaseCoupon(ucID int64) error {
	return s.repo.ReleaseCoupon(context.Background(), ucID)
}

// WebLogin 乡厨/供应商 Web 端登录（手机号+密码）
func (s *serviceImpl) WebLogin(ctx context.Context, phone, pwd string) (*LoginResult, error) {
	if !phoneRe.MatchString(phone) {
		return nil, errs.ErrPhoneInvalid
	}
	u, err := s.repo.GetByPhone(ctx, phone)
	if err != nil {
		return nil, errs.BadRequest("账号或密码错误")
	}
	if u.PasswordHash == "" {
		return nil, errs.BadRequest("账号尚未设置密码，请先在小程序完成入驻")
	}
	if !password.Check(u.PasswordHash, pwd) {
		return nil, errs.BadRequest("账号或密码错误")
	}
	return s.login(ctx, u)
}

// ---------- 管理端券模板 ----------

// ListCouponForAdmin 管理端券模板列表（含下架，可按状态筛选）
func (s *serviceImpl) ListCouponForAdmin(ctx context.Context, status *int8, page, size int) ([]*AdminCouponVO, int64, error) {
	list, total, err := s.repo.AdminListCoupon(ctx, status, page, size)
	if err != nil {
		return nil, 0, err
	}
	vos := make([]*AdminCouponVO, 0, len(list))
	for _, c := range list {
		vos = append(vos, toAdminCouponVO(c))
	}
	return vos, total, nil
}

func toAdminCouponVO(c *Coupon) *AdminCouponVO {
	return &AdminCouponVO{
		ID: c.ID, Name: c.Name, Type: c.Type,
		Threshold: c.Threshold, Amount: c.Amount,
		ValidFrom: c.ValidFrom, ValidTo: c.ValidTo,
		TotalCount: c.TotalCount, ReceivedCount: c.ReceivedCount,
		Status: c.Status, CreatedAt: c.CreatedAt,
	}
}

// CreateCoupon 创建券模板
func (s *serviceImpl) CreateCoupon(ctx context.Context, req *CouponReq) (int64, error) {
	c, err := buildCoupon(req, nil)
	if err != nil {
		return 0, err
	}
	if err := s.repo.CreateCoupon(ctx, c); err != nil {
		return 0, err
	}
	return c.ID, nil
}

// UpdateCoupon 编辑券模板（received_count/status 不可经此入口修改）
func (s *serviceImpl) UpdateCoupon(ctx context.Context, id int64, req *CouponReq) error {
	old, err := s.repo.GetCoupon(ctx, id)
	if err != nil {
		return err
	}
	c, err := buildCoupon(req, old)
	if err != nil {
		return err
	}
	c.ID = old.ID // buildCoupon 不回填主键，repo 的 WHERE id = ? 依赖它
	fields := []string{"name", "type", "threshold", "amount", "valid_from", "valid_to", "total_count"}
	return s.repo.UpdateCoupon(ctx, c, fields...)
}

// SetCouponStatus 上架/下架券模板（只挡领取与发放，不阻断已领券使用）
func (s *serviceImpl) SetCouponStatus(ctx context.Context, id int64, status int8) error {
	if status != 0 && status != 1 {
		return errs.BadRequest("非法状态值")
	}
	return s.repo.SetCouponStatus(ctx, id, status)
}

// GrantCoupon 管理端手动发放：入参去重，预检给友好文案，判定权威在 GrantCouponTxn
func (s *serviceImpl) GrantCoupon(ctx context.Context, couponID int64, userIDs []int64) (*GrantResult, error) {
	ids := dedupe(userIDs)
	if len(ids) == 0 {
		return &GrantResult{}, nil
	}
	c, err := s.repo.GetCoupon(ctx, couponID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if c.Status != 1 || !now.Before(c.ValidTo) {
		return nil, errs.BadRequest("券已下架或已过期，无法发放")
	}
	// 剩余量不做服务层拦截：全部已持有时 repo 返回 granted=0（合法），
	// 真正超发由 GrantCouponTxn 条件递增回滚兜底
	granted, err := s.repo.GrantCouponTxn(ctx, couponID, ids, c.ValidTo)
	if err != nil {
		return nil, err
	}
	return &GrantResult{Granted: granted, Skipped: int64(len(ids)) - granted}, nil
}

// buildCoupon 请求转模板（old 传 nil 表示创建；编辑时校验总量不得小于已领数）
func buildCoupon(req *CouponReq, old *Coupon) (*Coupon, error) {
	if req.Type != 1 {
		return nil, errs.BadRequest("暂仅支持满减券（type=1）")
	}
	if !req.ValidTo.After(req.ValidFrom) {
		return nil, errs.BadRequest("有效期止必须晚于有效期起")
	}
	if old != nil && req.TotalCount < old.ReceivedCount {
		return nil, errs.BadRequest(fmt.Sprintf("总量不能小于已领数量（已领 %d）", old.ReceivedCount))
	}
	return &Coupon{
		Name: req.Name, Type: req.Type,
		Threshold: req.Threshold, Amount: req.Amount,
		ValidFrom: req.ValidFrom, ValidTo: req.ValidTo,
		TotalCount: req.TotalCount,
		Status:     StatusEnabled, // 创建即上架
	}, nil
}

// dedupe 保序去重
func dedupe(ids []int64) []int64 {
	seen := make(map[int64]bool, len(ids))
	out := make([]int64, 0, len(ids))
	for _, v := range ids {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
