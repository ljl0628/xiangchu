-- 需求：补资金侧两张流水表
--   1. refunds 退款单：此前 out_refund_no 现生成不落库，微信退款以交易（支付单）为单位，
--      无法对账、部分退款被支付单状态 2/3 吞掉、REFUND.SUCCESS 回调无单可对；
--      挂 payment_no 而非订单（订单↔支付单 1:N，退款是资金侧概念）
--   2. payment_notifies 通知流水：回调报文不留痕，重推次数/到达时间/处理结果无据可查；
--      append-only 一行一次推送，uk=微信通知 ID（重试推送同 ID）挡重复落行
-- 注意：唯一键自诞生即带上（000002/000004 先例），不留"先裸表后补键"的窗口

CREATE TABLE refunds (
  id BIGINT NOT NULL AUTO_INCREMENT,
  refund_no VARCHAR(32) NOT NULL COMMENT '我方退款单号，同时作为 out_refund_no 传微信',
  payment_no VARCHAR(32) NOT NULL COMMENT '锚定支付单（微信退款以交易为单位）',
  biz_type TINYINT NOT NULL DEFAULT 1 COMMENT '冗余自支付单 1宴席 2采购',
  biz_id BIGINT NOT NULL,
  amount BIGINT NOT NULL COMMENT '退款金额（分），部分退款即多行',
  status TINYINT NOT NULL DEFAULT 0 COMMENT '0退款中 1成功 2异常',
  wx_refund_id VARCHAR(64) NOT NULL DEFAULT '' COMMENT '微信退款单号，回调回填',
  reason VARCHAR(255) NOT NULL DEFAULT '' ,
  success_time DATETIME(3) DEFAULT NULL,
  created_at DATETIME(3) DEFAULT NULL,
  updated_at DATETIME(3) DEFAULT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_refunds_no (refund_no),
  KEY idx_refunds_payment (payment_no),
  KEY idx_refunds_status_created (status, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='退款单';

CREATE TABLE payment_notifies (
  id BIGINT NOT NULL AUTO_INCREMENT,
  notify_id VARCHAR(64) DEFAULT NULL COMMENT '微信通知 ID；重试推送同 ID。mock 明文无 ID 记 NULL（唯一键忽略 NULL）',
  event_type VARCHAR(32) NOT NULL DEFAULT '' COMMENT 'TRANSACTION.SUCCESS / REFUND.SUCCESS 等',
  out_trade_no VARCHAR(32) NOT NULL DEFAULT '' ,
  raw_body TEXT COMMENT 'resource 解密明文，排障用',
  result VARCHAR(16) NOT NULL DEFAULT '' COMMENT 'OK / IGNORE / ERROR',
  error_msg VARCHAR(255) NOT NULL DEFAULT '' ,
  created_at DATETIME(3) DEFAULT NULL,
  updated_at DATETIME(3) DEFAULT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_payment_notifies_notify_id (notify_id),
  KEY idx_payment_notifies_out_trade_no (out_trade_no)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='微信回调通知流水';
