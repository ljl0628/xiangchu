-- 需求：回滚 000002——去掉 (user_id, coupon_id) 唯一键，还原 user_id 单列索引
-- 说明：去重删除的存量行无法恢复（回滚前请自行评估）

ALTER TABLE user_coupons
  ADD KEY idx_user_coupons_user_id (user_id),
  DROP KEY uk_user_coupon;
