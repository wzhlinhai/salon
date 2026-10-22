-- +goose Up
CREATE TABLE item_member_prices (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 item_id uuid NOT NULL,
 card_type_id uuid NOT NULL,
 price NUMERIC(10,2) NOT NULL CHECK (price >= 0),
 PRIMARY KEY (item_id,card_type_id),
 FOREIGN KEY (tenant_id,store_id) REFERENCES stores (tenant_id,id),
 FOREIGN KEY (tenant_id,item_id) REFERENCES items (tenant_id,id),
 FOREIGN KEY (tenant_id,card_type_id) REFERENCES card_types (tenant_id,id)
);
CREATE TABLE card_points_ledger (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 card_id uuid NOT NULL,
 kind text NOT NULL CHECK (kind IN ('open','recharge','upgrade','adjust')),
 before int NOT NULL,
 delta int NOT NULL,
 after int NOT NULL CHECK (after BETWEEN 0 AND 99999999),
 card_ledger_id uuid,
 adjustment_id uuid,
 business_date date NOT NULL,
 actor_id uuid NOT NULL,
 at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK (after = before + delta),
 FOREIGN KEY (tenant_id,store_id) REFERENCES stores (tenant_id,id),
 FOREIGN KEY (tenant_id,card_id) REFERENCES cards (tenant_id,id),
 FOREIGN KEY (tenant_id,card_ledger_id) REFERENCES card_ledger (tenant_id,id),
 FOREIGN KEY (tenant_id,adjustment_id) REFERENCES card_adjustments (tenant_id,id),
 FOREIGN KEY (tenant_id,actor_id) REFERENCES staff (tenant_id,id)
);
CREATE INDEX card_points_ledger_latest ON card_points_ledger (card_id,at DESC,id DESC);
ALTER TABLE item_member_prices ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON item_member_prices
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE card_points_ledger ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON card_points_ledger
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON item_member_prices TO salon_app;
GRANT SELECT, INSERT ON card_points_ledger TO salon_app;
ALTER TABLE card_adjustments ADD COLUMN points_changed boolean NOT NULL DEFAULT false;
ALTER TABLE card_adjustments DROP CONSTRAINT card_adjustments_check;
ALTER TABLE card_adjustments ADD CONSTRAINT card_adjustments_check
 CHECK (expiry_changed OR COALESCE(card_type_before <> card_type_after,false) OR ledger_id IS NOT NULL OR counts_changed OR points_changed);

-- +goose Down
ALTER TABLE card_adjustments DROP CONSTRAINT card_adjustments_check;
ALTER TABLE card_adjustments DROP COLUMN points_changed;
ALTER TABLE card_adjustments ADD CONSTRAINT card_adjustments_check
 CHECK (expiry_changed OR COALESCE(card_type_before <> card_type_after,false) OR ledger_id IS NOT NULL OR counts_changed);
DROP TABLE card_points_ledger;
DROP TABLE item_member_prices;
