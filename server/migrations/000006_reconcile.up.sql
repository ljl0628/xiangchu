-- 需求：每日对账（T+1 拉微信交易账单与本地核对）——回调/查单补偿都是"问单笔"，
--       对账是拿权威账单整体核对，覆盖掉单、金额错、退款异常、状态机 bug。
--       差异只检测落表不自动改账（v1 人工处置），金额一律以微信账单为准
-- reconcile_batches uk=bill_date：一个账单日一批，防定时器与管理端触发并发重跑

CREATE TABLE reconcile_batches (
  id BIGINT NOT NULL AUTO_INCREMENT,
  bill_date VARCHAR(10) NOT NULL COMMENT '账单日 YYYY-MM-DD',
  status TINYINT NOT NULL DEFAULT 0 COMMENT '0进行中 1完成 2失败',
  trade_count INT NOT NULL DEFAULT 0 COMMENT '账单支付笔数',
  trade_total BIGINT NOT NULL DEFAULT 0 COMMENT '账单支付总额（分）',
  local_count INT NOT NULL DEFAULT 0 COMMENT '本地当日已支付笔数',
  local_total BIGINT NOT NULL DEFAULT 0 COMMENT '本地当日已支付总额（分）',
  diff_count INT NOT NULL DEFAULT 0,
  error_msg VARCHAR(255) NOT NULL DEFAULT '',
  created_at DATETIME(3) DEFAULT NULL,
  finished_at DATETIME(3) DEFAULT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_reconcile_batches_date (bill_date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='每日对账批次';

CREATE TABLE reconcile_diffs (
  id BIGINT NOT NULL AUTO_INCREMENT,
  batch_id BIGINT NOT NULL,
  bill_date VARCHAR(10) NOT NULL,
  diff_type VARCHAR(32) NOT NULL COMMENT 'MISSING_LOCAL/MISSING_BILL/AMOUNT_MISMATCH/REFUND_MISMATCH',
  out_trade_no VARCHAR(32) NOT NULL,
  wx_amount BIGINT NOT NULL DEFAULT 0 COMMENT '账单侧金额（分）',
  local_amount BIGINT NOT NULL DEFAULT 0 COMMENT '本地侧金额（分）',
  detail VARCHAR(255) NOT NULL DEFAULT '',
  status TINYINT NOT NULL DEFAULT 0 COMMENT '0待处理 1已处理',
  created_at DATETIME(3) DEFAULT NULL,
  PRIMARY KEY (id),
  KEY idx_reconcile_diffs_batch (batch_id),
  KEY idx_reconcile_diffs_type_status (diff_type, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='对账差异明细';
