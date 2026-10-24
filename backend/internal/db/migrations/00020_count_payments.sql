-- +goose Up
ALTER TABLE card_count_ledger
 ADD CONSTRAINT card_count_ledger_tenant_id_id_key UNIQUE (tenant_id, id),
 DROP CONSTRAINT card_count_ledger_kind_check,
 ADD CONSTRAINT card_count_ledger_kind_check CHECK (kind IN ('open','adjust','consume')),
 ADD CONSTRAINT card_count_ledger_consume_check CHECK (kind <> 'consume' OR (paid_delta + gift_delta = -1 AND paid_delta <= 0 AND gift_delta <= 0));
ALTER TABLE order_payments
 DROP CONSTRAINT order_payments_method_check,
 ADD CONSTRAINT order_payments_method_check CHECK (method IN ('cash','unionpay','wechat','alipay','debt','card','count')),
 DROP CONSTRAINT order_payments_card_check,
 ADD CONSTRAINT order_payments_card_check CHECK ((method IN ('card','count')) = (card_id IS NOT NULL)),
 ADD COLUMN order_item_id uuid,
 ADD FOREIGN KEY (tenant_id, order_item_id) REFERENCES order_items (tenant_id, id),
 ADD COLUMN card_count_ledger_id uuid UNIQUE,
 ADD FOREIGN KEY (tenant_id, card_count_ledger_id) REFERENCES card_count_ledger (tenant_id, id),
 ADD CONSTRAINT order_payments_count_item_check CHECK ((method = 'count') = (order_item_id IS NOT NULL)),
 ADD CONSTRAINT order_payments_count_ledger_check CHECK ((method = 'count') = (card_count_ledger_id IS NOT NULL));
CREATE UNIQUE INDEX order_payments_count_item ON order_payments (order_item_id) WHERE method = 'count';

-- +goose Down
DELETE FROM order_payments WHERE method='count';
DELETE FROM card_count_ledger WHERE kind='consume';
DROP INDEX order_payments_count_item;
ALTER TABLE order_payments
 DROP COLUMN order_item_id,
 DROP COLUMN card_count_ledger_id,
 DROP CONSTRAINT order_payments_method_check,
 ADD CONSTRAINT order_payments_method_check CHECK (method IN ('cash','unionpay','wechat','alipay','debt','card')),
 DROP CONSTRAINT order_payments_card_check,
 ADD CONSTRAINT order_payments_card_check CHECK ((method = 'card') = (card_id IS NOT NULL));
ALTER TABLE card_count_ledger
 DROP CONSTRAINT card_count_ledger_tenant_id_id_key,
 DROP CONSTRAINT card_count_ledger_consume_check,
 DROP CONSTRAINT card_count_ledger_kind_check,
 ADD CONSTRAINT card_count_ledger_kind_check CHECK (kind IN ('open','adjust'));
