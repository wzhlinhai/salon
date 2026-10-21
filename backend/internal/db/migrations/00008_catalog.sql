-- +goose Up
CREATE TABLE items (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 name text NOT NULL CHECK (name <> ''),
 price NUMERIC(10,2) NOT NULL CHECK (price >= 0),
 discountable boolean NOT NULL DEFAULT true,
 labor_fee_amount NUMERIC(10,2) CHECK (labor_fee_amount >= 0),
 labor_fee_percent NUMERIC(5,2) CHECK (labor_fee_percent BETWEEN 0 AND 100),
 active boolean NOT NULL DEFAULT true,
 created_at timestamptz NOT NULL DEFAULT now(),
 CHECK (labor_fee_amount IS NULL OR labor_fee_percent IS NULL),
 UNIQUE (store_id, name),
 UNIQUE (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id)
);
CREATE TABLE item_level_prices (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 item_id uuid NOT NULL,
 level_id uuid NOT NULL,
 price NUMERIC(10,2) NOT NULL CHECK (price >= 0),
 PRIMARY KEY (item_id, level_id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, item_id) REFERENCES items (tenant_id, id),
 FOREIGN KEY (tenant_id, level_id) REFERENCES levels (tenant_id, id)
);
CREATE TABLE item_positions (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 item_id uuid NOT NULL,
 slot smallint NOT NULL CHECK (slot >= 1),
 position_id uuid NOT NULL,
 required boolean NOT NULL,
 PRIMARY KEY (item_id, slot),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, item_id) REFERENCES items (tenant_id, id),
 FOREIGN KEY (tenant_id, position_id) REFERENCES positions (tenant_id, id)
);
ALTER TABLE items ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON items
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE item_level_prices ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON item_level_prices
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE item_positions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON item_positions
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT, UPDATE ON items TO salon_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON item_level_prices, item_positions TO salon_app;

-- +goose Down
DROP TABLE item_positions;
DROP TABLE item_level_prices;
DROP TABLE items;
