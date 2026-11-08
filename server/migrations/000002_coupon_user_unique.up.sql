-- 需求：管理端优惠券管理——修复领券重复领取/超发隐患
-- 说明：user_coupons 加 (user_id, coupon_id) 唯一键，存量重复行先去重
--       （每组优先保留 status=0 未使用中最小 id，全组已用则保留最小 id）。
--       uk 最左前缀可覆盖原 idx_user_coupons_user_id 单列查询，故一并删除。
-- 注意：docs/coupon-redemption-design.md（核销流水表）落地时版本号按当时最新顺延。

DELETE uc FROM user_coupons uc
JOIN (
  SELECT user_id, coupon_id,
         COALESCE(MIN(CASE WHEN status = 0 THEN id END), MIN(id)) AS keep_id
  FROM user_coupons
  GROUP BY user_id, coupon_id
) k ON k.user_id = uc.user_id AND k.coupon_id = uc.coupon_id AND k.keep_id <> uc.id;

ALTER TABLE user_coupons
  ADD UNIQUE KEY uk_user_coupon (user_id, coupon_id),
  DROP KEY idx_user_coupons_user_id;
