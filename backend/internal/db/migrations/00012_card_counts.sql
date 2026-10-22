-- +goose Up
ALTER TABLE card_types DROP CONSTRAINT card_types_kind_check,
 ADD CONSTRAINT card_types_kind_check CHECK (kind IN ('stored_value','count','package'));
ALTER TABLE card_adjustments ADD COLUMN counts_changed boolean NOT NULL DEFAULT false;
ALTER TABLE card_adjustments DROP CONSTRAINT card_adjustments_check,
 ADD CONSTRAINT card_adjustments_check CHECK (counts_changed OR expiry_changed OR COALESCE(card_type_before <> card_type_after,false) OR ledger_id IS NOT NULL);
CREATE TABLE card_type_items (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 card_type_id uuid NOT NULL,
 item_id uuid NOT NULL,
 paid_count int NOT NULL CHECK (paid_count BETWEEN 1 AND 9999),
 gift_count int NOT NULL CHECK (gift_count BETWEEN 0 AND 9999),
 unit_price NUMERIC(10,2) NOT NULL CHECK (unit_price >= 0),
 PRIMARY KEY (card_type_id,item_id),
 FOREIGN KEY (tenant_id,store_id) REFERENCES stores (tenant_id,id),
 FOREIGN KEY (tenant_id,card_type_id) REFERENCES card_types (tenant_id,id),
 FOREIGN KEY (tenant_id,item_id) REFERENCES items (tenant_id,id)
);
CREATE TABLE card_items (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 card_id uuid NOT NULL,
 item_id uuid NOT NULL,
 unit_price NUMERIC(10,2) NOT NULL CHECK (unit_price >= 0),
 paid_count_opened int NOT NULL CHECK (paid_count_opened BETWEEN 1 AND 9999),
 gift_count_opened int NOT NULL CHECK (gift_count_opened BETWEEN 0 AND 9999),
 PRIMARY KEY (card_id,item_id),
 FOREIGN KEY (tenant_id,store_id) REFERENCES stores (tenant_id,id),
 FOREIGN KEY (tenant_id,card_id) REFERENCES cards (tenant_id,id),
 FOREIGN KEY (tenant_id,item_id) REFERENCES items (tenant_id,id)
);
CREATE TABLE card_count_ledger (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 card_id uuid NOT NULL,
 item_id uuid NOT NULL,
 kind text NOT NULL CHECK (kind IN ('open','adjust')),
 paid_before int NOT NULL,
 paid_delta int NOT NULL,
 paid_after int NOT NULL CHECK (paid_after BETWEEN 0 AND 9999),
 gift_before int NOT NULL,
 gift_delta int NOT NULL,
 gift_after int NOT NULL CHECK (gift_after BETWEEN 0 AND 9999),
 adjustment_id uuid,
 business_date date NOT NULL,
 actor_id uuid NOT NULL,
 at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK (paid_after = paid_before + paid_delta),
 CHECK (gift_after = gift_before + gift_delta),
 FOREIGN KEY (tenant_id,store_id) REFERENCES stores (tenant_id,id),
 FOREIGN KEY (tenant_id,card_id) REFERENCES cards (tenant_id,id),
 FOREIGN KEY (tenant_id,item_id) REFERENCES items (tenant_id,id),
 FOREIGN KEY (card_id,item_id) REFERENCES card_items (card_id,item_id),
 FOREIGN KEY (tenant_id,adjustment_id) REFERENCES card_adjustments (tenant_id,id),
 FOREIGN KEY (tenant_id,actor_id) REFERENCES staff (tenant_id,id)
);
CREATE INDEX card_count_ledger_latest ON card_count_ledger (card_id,item_id,at DESC,id DESC);
ALTER TABLE card_type_items ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON card_type_items
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE card_items ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON card_items
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE card_count_ledger ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON card_count_ledger
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON card_type_items TO salon_app;
GRANT SELECT, INSERT ON card_items, card_count_ledger TO salon_app;

-- +goose Down
DROP TABLE card_count_ledger;
DROP TABLE card_items;
DROP TABLE card_type_items;
ALTER TABLE card_adjustments DROP CONSTRAINT card_adjustments_check;
ALTER TABLE card_adjustments DROP COLUMN counts_changed;
ALTER TABLE card_adjustments ADD CONSTRAINT card_adjustments_check CHECK (expiry_changed OR COALESCE(card_type_before <> card_type_after,false) OR ledger_id IS NOT NULL);
ALTER TABLE card_types DROP CONSTRAINT card_types_kind_check,
 ADD CONSTRAINT card_types_kind_check CHECK (kind IN ('stored_value'));
