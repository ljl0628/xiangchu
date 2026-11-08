-- 需求：支付单创建幂等——同一业务单同时只允许一笔在途（待支付）支付单，
--       防并发双开支付单导致用户重复付款（超付）
-- 说明：MySQL 无部分唯一索引（Partial Unique），用生成列实现条件唯一：
--       status=0 时生成 biz_type:biz_id 参与唯一键，流转到任意其他状态变 NULL
--       （MySQL 唯一索引忽略 NULL，槽位自动释放），定金→尾款分两期互不冲突
-- 注意：存量在途重复行先去重（每组仅保留最新一笔待支付，其余转已关闭 4），
--       否则唯一键创建直接失败
UPDATE payments p
JOIN (
  SELECT biz_type, biz_id, MAX(id) AS keep_id
  FROM payments WHERE status = 0 GROUP BY biz_type, biz_id
) k ON p.biz_type = k.biz_type AND p.biz_id = k.biz_id
SET p.status = 4
WHERE p.status = 0 AND p.id < k.keep_id;

ALTER TABLE payments
  ADD COLUMN active_uk varchar(64)
    GENERATED ALWAYS AS (
      CASE WHEN status = 0 THEN CONCAT(biz_type, ':', biz_id) ELSE NULL END
    ) STORED,
  ADD UNIQUE KEY uk_payments_active (active_uk);
