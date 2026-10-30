-- +goose Up
ALTER TABLE cards ADD COLUMN status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','refunded'));
CREATE TABLE card_refunds (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 card_id uuid NOT NULL UNIQUE,
 method text NOT NULL CHECK (method IN ('cash','unionpay','wechat','alipay')),
 principal NUMERIC(10,2) NOT NULL CHECK (principal >= 0),
 bonus NUMERIC(10,2) NOT NULL CHECK (bonus >= 0),
 counts_amount NUMERIC(10,2) NOT NULL CHECK (counts_amount >= 0),
 bonus_policy text NOT NULL CHECK (bonus_policy IN ('forfeit','deduct_used')),
 bonus_deducted NUMERIC(10,2) NOT NULL CHECK (bonus_deducted >= 0),
 fee_percent int NOT NULL CHECK (fee_percent BETWEEN 0 AND 100),
 fee NUMERIC(10,2) NOT NULL CHECK (fee >= 0),
 amount NUMERIC(10,2) NOT NULL CHECK (amount >= 0),
 business_date date NOT NULL,
 refunded_by uuid NOT NULL,
 at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK (bonus_policy = 'deduct_used' OR bonus_deducted = 0),
 CHECK (bonus_deducted <= principal + counts_amount),
 CHECK (fee = round((principal + counts_amount - bonus_deducted) * fee_percent / 100.0, 2)),
 CHECK (amount = principal + counts_amount - bonus_deducted - fee),
 UNIQUE (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, card_id) REFERENCES cards (tenant_id, id),
 FOREIGN KEY (tenant_id, refunded_by) REFERENCES staff (tenant_id, id)
);
ALTER TABLE card_refunds ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON card_refunds
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT ON card_refunds TO salon_app;

ALTER TABLE card_ledger
 DROP CONSTRAINT card_ledger_kind_check,
 ADD CONSTRAINT card_ledger_kind_check CHECK (kind IN ('open','recharge','adjust','upgrade','transfer_out','transfer_in','consume','void','refund')),
 ADD COLUMN refund_id uuid UNIQUE,
 ADD FOREIGN KEY (tenant_id, refund_id) REFERENCES card_refunds (tenant_id, id),
 ADD CONSTRAINT card_ledger_refund_presence_check CHECK ((kind = 'refund') = (refund_id IS NOT NULL)),
 ADD CONSTRAINT card_ledger_refund_zero_check CHECK (kind <> 'refund' OR (principal_after = 0 AND bonus_after = 0));

ALTER TABLE card_count_ledger
 DROP CONSTRAINT card_count_ledger_kind_check,
 ADD CONSTRAINT card_count_ledger_kind_check CHECK (kind IN ('open','adjust','consume','void','refund')),
 ADD COLUMN refund_id uuid,
 ADD FOREIGN KEY (tenant_id, refund_id) REFERENCES card_refunds (tenant_id, id),
 ADD CONSTRAINT card_count_ledger_refund_presence_check CHECK ((kind = 'refund') = (refund_id IS NOT NULL)),
 ADD CONSTRAINT card_count_ledger_refund_zero_check CHECK (kind <> 'refund' OR (paid_after = 0 AND gift_after = 0));

-- +goose Down
DELETE FROM card_ledger WHERE kind='refund';
DELETE FROM card_count_ledger WHERE kind='refund';
ALTER TABLE card_ledger
 DROP CONSTRAINT card_ledger_refund_presence_check,
 DROP CONSTRAINT card_ledger_refund_zero_check,
 DROP COLUMN refund_id,
 DROP CONSTRAINT card_ledger_kind_check,
 ADD CONSTRAINT card_ledger_kind_check CHECK (kind IN ('open','recharge','adjust','upgrade','transfer_out','transfer_in','consume','void'));
ALTER TABLE card_count_ledger
 DROP CONSTRAINT card_count_ledger_refund_presence_check,
 DROP CONSTRAINT card_count_ledger_refund_zero_check,
 DROP COLUMN refund_id,
 DROP CONSTRAINT card_count_ledger_kind_check,
 ADD CONSTRAINT card_count_ledger_kind_check CHECK (kind IN ('open','adjust','consume','void'));
DROP TABLE card_refunds;
ALTER TABLE cards DROP COLUMN status;
