-- 需求：支付查单补偿——定时扫描待支付单主动向微信对齐支付结果
-- 说明：扫描条件 status=0 等值 + created_at 范围，联合索引 (status, created_at)
--       首列低基数按分区各自有序，等价于把未决流水单独拎出一张小表；
--       已支付/已关闭终态自动退出扫描集，集合大小与流水总量无关
ALTER TABLE payments ADD KEY idx_payments_status_created (status, created_at);
