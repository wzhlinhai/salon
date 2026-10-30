-- +goose Up
ALTER TABLE order_items
 ADD COLUMN labor_fee_amount numeric(10,2) CHECK (labor_fee_amount >= 0),
 ADD COLUMN labor_fee_percent numeric(5,2) CHECK (labor_fee_percent BETWEEN 0 AND 100),
 ADD CONSTRAINT order_items_labor_fee_check CHECK (labor_fee_amount IS NULL OR labor_fee_percent IS NULL);
ALTER TABLE performance_entries DROP CONSTRAINT performance_entries_kind_check, DROP CONSTRAINT performance_entries_method_check;
ALTER TABLE performance_entries
 ADD CONSTRAINT performance_entries_kind_check CHECK (kind IN ('item','consume','labor')),
 ADD CONSTRAINT performance_entries_method_check CHECK (method IN ('cash','unionpay','wechat','alipay','debt','red_packet','card_principal','card_bonus','count_paid','count_gift','labor')),
 ADD CONSTRAINT performance_entries_item_method_check CHECK ((kind = 'item') = (method IN ('cash','unionpay','wechat','alipay','debt','red_packet','card_principal','card_bonus'))),
 ADD CONSTRAINT performance_entries_consume_method_check CHECK ((kind = 'consume') = (method IN ('count_paid','count_gift'))),
 ADD CONSTRAINT performance_entries_labor_method_check CHECK ((kind = 'labor') = (method = 'labor')),
 ADD CONSTRAINT performance_entries_labor_ratio_check CHECK (kind <> 'labor' OR ratio = 100);

-- +goose Down
DELETE FROM performance_entries WHERE kind IN ('consume','labor');
ALTER TABLE performance_entries
 DROP CONSTRAINT performance_entries_kind_check,
 DROP CONSTRAINT performance_entries_method_check,
 DROP CONSTRAINT performance_entries_item_method_check,
 DROP CONSTRAINT performance_entries_consume_method_check,
 DROP CONSTRAINT performance_entries_labor_method_check,
 DROP CONSTRAINT performance_entries_labor_ratio_check;
ALTER TABLE performance_entries
 ADD CONSTRAINT performance_entries_kind_check CHECK (kind IN ('item')),
 ADD CONSTRAINT performance_entries_method_check CHECK (method IN ('cash','unionpay','wechat','alipay','debt','red_packet','card_principal','card_bonus'));
ALTER TABLE order_items DROP COLUMN labor_fee_amount, DROP COLUMN labor_fee_percent;
