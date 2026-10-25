-- +goose Up
CREATE TABLE order_voids (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 order_id uuid NOT NULL UNIQUE,
 reason text NOT NULL CHECK (length(trim(reason)) BETWEEN 1 AND 200),
 business_date date NOT NULL,
 voided_by uuid NOT NULL,
 at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, order_id) REFERENCES orders (tenant_id, id),
 FOREIGN KEY (tenant_id, voided_by) REFERENCES staff (tenant_id, id)
);
ALTER TABLE order_voids ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON order_voids
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT ON order_voids TO salon_app;

ALTER TABLE orders
 DROP CONSTRAINT orders_status_check,
 ADD CONSTRAINT orders_status_check CHECK (status IN ('open','merged','paid','voided')),
 DROP CONSTRAINT orders_check2,
 ADD CONSTRAINT orders_check2 CHECK ((status IN ('paid','voided')) = (checkout_id IS NOT NULL));

ALTER TABLE card_ledger
 DROP CONSTRAINT card_ledger_kind_check,
 ADD CONSTRAINT card_ledger_kind_check CHECK (kind IN ('open','recharge','adjust','upgrade','transfer_out','transfer_in','consume','void')),
 ADD COLUMN reverses_id uuid UNIQUE,
 ADD FOREIGN KEY (tenant_id, reverses_id) REFERENCES card_ledger (tenant_id, id),
 ADD COLUMN order_void_id uuid,
 ADD FOREIGN KEY (tenant_id, order_void_id) REFERENCES order_voids (tenant_id, id),
 ADD CONSTRAINT card_ledger_void_reverses_check CHECK ((kind = 'void') = (reverses_id IS NOT NULL)),
 ADD CONSTRAINT card_ledger_void_order_check CHECK ((kind = 'void') = (order_void_id IS NOT NULL)),
 ADD CONSTRAINT card_ledger_void_delta_check CHECK (kind <> 'void' OR (principal_delta >= 0 AND bonus_delta >= 0 AND principal_delta + bonus_delta > 0));

ALTER TABLE card_count_ledger
 DROP CONSTRAINT card_count_ledger_kind_check,
 ADD CONSTRAINT card_count_ledger_kind_check CHECK (kind IN ('open','adjust','consume','void')),
 ADD COLUMN reverses_id uuid UNIQUE,
 ADD FOREIGN KEY (tenant_id, reverses_id) REFERENCES card_count_ledger (tenant_id, id),
 ADD COLUMN order_void_id uuid,
 ADD FOREIGN KEY (tenant_id, order_void_id) REFERENCES order_voids (tenant_id, id),
 ADD CONSTRAINT card_count_ledger_void_reverses_check CHECK ((kind = 'void') = (reverses_id IS NOT NULL)),
 ADD CONSTRAINT card_count_ledger_void_order_check CHECK ((kind = 'void') = (order_void_id IS NOT NULL)),
 ADD CONSTRAINT card_count_ledger_void_delta_check CHECK (kind <> 'void' OR (paid_delta + gift_delta = 1 AND paid_delta >= 0 AND gift_delta >= 0));

-- 历史支付保留；当前支付仍由 red_packets.order_payment_id UNIQUE
-- 和 checkout / void 共用的红包 FOR NO KEY UPDATE 锁保证唯一。
DROP INDEX order_payments_red_packet;

-- +goose Down
-- 红包退回后再次支付会留下多笔历史记录，无法无损恢复旧索引，明确拒绝回退。
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM order_payments WHERE red_packet_id IS NOT NULL GROUP BY red_packet_id HAVING count(*) > 1) THEN
  RAISE EXCEPTION 'cannot downgrade 00024: a restored red packet has multiple historical payments';
 END IF;
END $$;
-- +goose StatementEnd
DELETE FROM card_ledger WHERE kind='void';
DELETE FROM card_count_ledger WHERE kind='void';
DELETE FROM order_voids;
UPDATE orders SET status='paid' WHERE status='voided';
CREATE UNIQUE INDEX order_payments_red_packet ON order_payments (red_packet_id) WHERE red_packet_id IS NOT NULL;
ALTER TABLE orders
 DROP CONSTRAINT orders_status_check,
 ADD CONSTRAINT orders_status_check CHECK (status IN ('open','merged','paid')),
 DROP CONSTRAINT orders_check2,
 ADD CONSTRAINT orders_check2 CHECK ((status = 'paid') = (checkout_id IS NOT NULL));
ALTER TABLE card_ledger
 DROP CONSTRAINT card_ledger_void_reverses_check,
 DROP CONSTRAINT card_ledger_void_order_check,
 DROP CONSTRAINT card_ledger_void_delta_check,
 DROP COLUMN reverses_id,
 DROP COLUMN order_void_id,
 DROP CONSTRAINT card_ledger_kind_check,
 ADD CONSTRAINT card_ledger_kind_check CHECK (kind IN ('open','recharge','adjust','upgrade','transfer_out','transfer_in','consume'));
ALTER TABLE card_count_ledger
 DROP CONSTRAINT card_count_ledger_void_reverses_check,
 DROP CONSTRAINT card_count_ledger_void_order_check,
 DROP CONSTRAINT card_count_ledger_void_delta_check,
 DROP COLUMN reverses_id,
 DROP COLUMN order_void_id,
 DROP CONSTRAINT card_count_ledger_kind_check,
 ADD CONSTRAINT card_count_ledger_kind_check CHECK (kind IN ('open','adjust','consume'));
DROP TABLE order_voids;
