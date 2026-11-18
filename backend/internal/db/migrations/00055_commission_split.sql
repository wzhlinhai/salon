-- +goose Up
ALTER TABLE performance_entries
 ADD COLUMN commission_paid_amount numeric(10,2),
 ADD COLUMN commission_amount numeric(10,2);
UPDATE performance_entries SET commission_paid_amount = paid_amount, commission_amount = amount;
ALTER TABLE performance_entries
 ALTER COLUMN commission_paid_amount SET NOT NULL,
 ALTER COLUMN commission_amount SET NOT NULL,
 ADD CONSTRAINT performance_entries_commission_paid_check CHECK (commission_paid_amount >= 0 AND (commission_paid_amount <= paid_amount OR aftersale_id IS NOT NULL)),
 ADD CONSTRAINT performance_entries_commission_amount_check CHECK (commission_amount = round(commission_paid_amount * ratio * category_ratio / 10000.0, 2) OR (aftersale_id IS NOT NULL AND commission_amount >= 0 AND commission_amount <= commission_paid_amount)),
 ADD CONSTRAINT performance_entries_commission_labor_sale_check CHECK (kind NOT IN ('labor','sale') OR (commission_paid_amount = paid_amount AND commission_amount = amount));

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM performance_entries WHERE commission_paid_amount <> paid_amount) THEN
  RAISE EXCEPTION '无法回滚 00055：存在提成分摊与业绩不同的业绩记录';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE performance_entries
 DROP CONSTRAINT performance_entries_commission_paid_check,
 DROP CONSTRAINT performance_entries_commission_amount_check,
 DROP CONSTRAINT performance_entries_commission_labor_sale_check,
 DROP COLUMN commission_paid_amount,
 DROP COLUMN commission_amount;
