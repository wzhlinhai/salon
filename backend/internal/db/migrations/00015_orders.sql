-- +goose Up
CREATE TABLE orders (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 customer_id uuid NOT NULL,
 gender text NOT NULL CHECK (gender IN ('male','female')),
 designated boolean NOT NULL,
 status text NOT NULL DEFAULT 'open' CHECK (status IN ('open')),
 settings_snapshot_id uuid NOT NULL,
 business_date date NOT NULL,
 opened_by uuid NOT NULL,
 opened_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (tenant_id, id),
 FOREIGN KEY (tenant_id, customer_id) REFERENCES customers (tenant_id, id),
 FOREIGN KEY (tenant_id, settings_snapshot_id) REFERENCES settings_snapshots (tenant_id, id),
 FOREIGN KEY (tenant_id, opened_by) REFERENCES staff (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id)
);
ALTER TABLE orders ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON orders
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE TABLE order_items (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 order_id uuid NOT NULL,
 item_id uuid NOT NULL,
 item_name text NOT NULL CHECK (item_name <> ''),
 price NUMERIC(10,2) NOT NULL CHECK (price >= 0),
 price_source text NOT NULL CHECK (price_source IN ('standard','level')),
 level_id uuid,
 added_by uuid NOT NULL,
 added_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK ((price_source = 'level') = (level_id IS NOT NULL)),
 UNIQUE (tenant_id, id),
 FOREIGN KEY (tenant_id, order_id) REFERENCES orders (tenant_id, id),
 FOREIGN KEY (tenant_id, item_id) REFERENCES items (tenant_id, id),
 FOREIGN KEY (tenant_id, level_id) REFERENCES levels (tenant_id, id),
 FOREIGN KEY (tenant_id, added_by) REFERENCES staff (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id)
);
ALTER TABLE order_items ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON order_items
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE TABLE order_item_positions (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 order_item_id uuid NOT NULL,
 slot smallint NOT NULL CHECK (slot >= 1),
 position_id uuid NOT NULL,
 required boolean NOT NULL,
 staff_id uuid,
 PRIMARY KEY (order_item_id, slot),
 FOREIGN KEY (tenant_id, order_item_id) REFERENCES order_items (tenant_id, id),
 FOREIGN KEY (tenant_id, position_id) REFERENCES positions (tenant_id, id),
 FOREIGN KEY (tenant_id, staff_id) REFERENCES staff (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id)
);
ALTER TABLE order_item_positions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON order_item_positions
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE INDEX orders_open ON orders (store_id, opened_at, id) WHERE status = 'open';
CREATE INDEX order_items_order ON order_items (store_id, order_id, added_at, id);
GRANT SELECT, INSERT, UPDATE ON orders TO salon_app;
GRANT SELECT, INSERT ON order_items, order_item_positions TO salon_app;
ALTER TABLE audit_log
 DROP CONSTRAINT audit_log_target_kind_check,
 DROP CONSTRAINT audit_log_customer_presence_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','card_type','card','card_upgrade_rule','order')),
 ADD CONSTRAINT audit_log_customer_presence_check CHECK ((target_kind IN ('customer','card','order')) = (customer_id IS NOT NULL));

-- +goose Down
DELETE FROM audit_log WHERE target_kind = 'order';
ALTER TABLE audit_log
 DROP CONSTRAINT audit_log_target_kind_check,
 DROP CONSTRAINT audit_log_customer_presence_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','card_type','card','card_upgrade_rule')),
 ADD CONSTRAINT audit_log_customer_presence_check CHECK ((target_kind IN ('customer','card')) = (customer_id IS NOT NULL));
DROP TABLE order_item_positions;
DROP TABLE order_items;
DROP TABLE orders;
