-- +goose Up
ALTER TABLE card_types ADD COLUMN discount_percent NUMERIC(5,2)
 CHECK (discount_percent > 0 AND discount_percent < 100);
ALTER TABLE card_ledger
 DROP CONSTRAINT card_ledger_kind_check,
 ADD CONSTRAINT card_ledger_kind_check CHECK (kind IN ('open','recharge','adjust','upgrade','transfer_out','transfer_in','consume')),
 ADD CONSTRAINT card_ledger_consume_check CHECK (kind <> 'consume' OR (principal_delta <= 0 AND bonus_delta <= 0 AND principal_delta + bonus_delta < 0));
ALTER TABLE order_items
 DROP CONSTRAINT order_items_price_source_check,
 ADD CONSTRAINT order_items_price_source_check CHECK (price_source IN ('standard','level','manual','member')),
 ADD COLUMN card_type_id uuid,
 ADD FOREIGN KEY (tenant_id, card_type_id) REFERENCES card_types (tenant_id, id),
 ADD CONSTRAINT order_items_member_check CHECK ((price_source = 'member') = (card_type_id IS NOT NULL)),
 ADD COLUMN discount NUMERIC(10,2) NOT NULL DEFAULT 0 CHECK (discount >= 0 AND discount <= price);
GRANT UPDATE (card_type_id, discount) ON order_items TO salon_app;
ALTER TABLE order_payments
 DROP CONSTRAINT order_payments_method_check,
 ADD CONSTRAINT order_payments_method_check CHECK (method IN ('cash','unionpay','wechat','alipay','debt','card')),
 ADD COLUMN card_id uuid,
 ADD FOREIGN KEY (tenant_id, card_id) REFERENCES cards (tenant_id, id),
 ADD COLUMN card_ledger_id uuid UNIQUE,
 ADD FOREIGN KEY (tenant_id, card_ledger_id) REFERENCES card_ledger (tenant_id, id),
 ADD CONSTRAINT order_payments_card_check CHECK ((method = 'card') = (card_id IS NOT NULL)),
 ADD CONSTRAINT order_payments_card_ledger_check CHECK ((method = 'card') = (card_ledger_id IS NOT NULL));

-- +goose Down
DELETE FROM order_payments WHERE method='card';
DELETE FROM card_ledger WHERE kind='consume';
UPDATE order_items SET price_source='standard', card_type_id=NULL WHERE price_source='member';
REVOKE UPDATE (card_type_id, discount) ON order_items FROM salon_app;
ALTER TABLE order_items
 DROP COLUMN card_type_id,
 DROP COLUMN discount,
 DROP CONSTRAINT order_items_price_source_check,
 ADD CONSTRAINT order_items_price_source_check CHECK (price_source IN ('standard','level','manual'));
ALTER TABLE order_payments
 DROP COLUMN card_id,
 DROP COLUMN card_ledger_id,
 DROP CONSTRAINT order_payments_method_check,
 ADD CONSTRAINT order_payments_method_check CHECK (method IN ('cash','unionpay','wechat','alipay','debt'));
ALTER TABLE card_ledger
 DROP CONSTRAINT card_ledger_consume_check,
 DROP CONSTRAINT card_ledger_kind_check,
 ADD CONSTRAINT card_ledger_kind_check CHECK (kind IN ('open','recharge','adjust','upgrade','transfer_out','transfer_in'));
ALTER TABLE card_types DROP COLUMN discount_percent;
