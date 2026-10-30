-- +goose Up
ALTER TABLE item_positions ADD COLUMN share_percent smallint CHECK (share_percent BETWEEN 0 AND 100);
ALTER TABLE order_item_positions ADD COLUMN share_percent smallint CHECK (share_percent BETWEEN 0 AND 100);

CREATE TABLE performance_entries (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 kind text NOT NULL CHECK (kind IN ('item')),
 business_date date NOT NULL,
 order_id uuid NOT NULL,
 order_item_id uuid NOT NULL,
 checkout_id uuid NOT NULL,
 settings_snapshot_id uuid NOT NULL,
 slot smallint,
 position_id uuid,
 staff_id uuid,
 method text NOT NULL CHECK (method IN ('cash','unionpay','wechat','alipay','debt','red_packet','card_principal','card_bonus')),
 paid_amount numeric(10,2) NOT NULL CHECK (paid_amount > 0),
 ratio smallint NOT NULL CHECK (ratio BETWEEN 0 AND 100),
 amount numeric(10,2) NOT NULL,
 void_id uuid,
 reverses_id uuid UNIQUE,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (tenant_id, id),
 CHECK (amount = round(paid_amount * ratio / 100.0, 2)),
 CHECK ((slot IS NULL) = (staff_id IS NULL) AND (slot IS NULL) = (position_id IS NULL)),
 CHECK ((void_id IS NULL) = (reverses_id IS NULL)),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, order_id) REFERENCES orders (tenant_id, id),
 FOREIGN KEY (tenant_id, order_item_id) REFERENCES order_items (tenant_id, id),
 FOREIGN KEY (tenant_id, checkout_id) REFERENCES checkouts (tenant_id, id),
 FOREIGN KEY (tenant_id, settings_snapshot_id) REFERENCES settings_snapshots (tenant_id, id),
 FOREIGN KEY (tenant_id, position_id) REFERENCES positions (tenant_id, id),
 FOREIGN KEY (tenant_id, staff_id) REFERENCES staff (tenant_id, id),
 FOREIGN KEY (tenant_id, void_id) REFERENCES order_voids (tenant_id, id),
 FOREIGN KEY (tenant_id, reverses_id) REFERENCES performance_entries (tenant_id, id)
);
ALTER TABLE performance_entries ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON performance_entries
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT ON performance_entries TO salon_app;
CREATE INDEX performance_entries_range ON performance_entries (store_id, business_date, id);
CREATE INDEX performance_entries_staff_range ON performance_entries (store_id, staff_id, business_date, id);

-- +goose Down
DROP TABLE performance_entries;
ALTER TABLE order_item_positions DROP COLUMN share_percent;
ALTER TABLE item_positions DROP COLUMN share_percent;
