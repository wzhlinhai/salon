-- +goose Up
ALTER TABLE card_ledger DROP CONSTRAINT card_ledger_kind_check,
 ADD CONSTRAINT card_ledger_kind_check CHECK (kind IN ('open','recharge','adjust'));
CREATE TABLE card_adjustments (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 card_id uuid NOT NULL,
 reason text NOT NULL CHECK (length(trim(reason)) BETWEEN 1 AND 200),
 expires_before date,
 expires_after date,
 expiry_changed boolean NOT NULL,
 card_type_before uuid,
 card_type_after uuid,
 ledger_id uuid,
 business_date date NOT NULL,
 actor_id uuid NOT NULL,
 at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK (expiry_changed OR COALESCE(card_type_before <> card_type_after, false) OR ledger_id IS NOT NULL),
 UNIQUE (tenant_id,id),
 UNIQUE (ledger_id),
 FOREIGN KEY (tenant_id,store_id) REFERENCES stores (tenant_id,id),
 FOREIGN KEY (tenant_id,card_id) REFERENCES cards (tenant_id,id),
 FOREIGN KEY (tenant_id,card_type_before) REFERENCES card_types (tenant_id,id),
 FOREIGN KEY (tenant_id,card_type_after) REFERENCES card_types (tenant_id,id),
 FOREIGN KEY (tenant_id,ledger_id) REFERENCES card_ledger (tenant_id,id),
 FOREIGN KEY (tenant_id,actor_id) REFERENCES staff (tenant_id,id)
);
CREATE INDEX card_adjustments_latest ON card_adjustments (card_id,at DESC,id DESC);
ALTER TABLE card_adjustments ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON card_adjustments
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT ON card_adjustments TO salon_app;

-- +goose Down
DROP TABLE card_adjustments;
ALTER TABLE card_ledger DROP CONSTRAINT card_ledger_kind_check,
 ADD CONSTRAINT card_ledger_kind_check CHECK (kind IN ('open','recharge'));
