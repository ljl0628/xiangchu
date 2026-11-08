ALTER TABLE payments
  DROP KEY uk_payments_active,
  DROP COLUMN active_uk;
