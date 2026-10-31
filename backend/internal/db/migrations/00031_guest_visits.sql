-- +goose Up
CREATE TABLE guest_visits (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 business_date date NOT NULL,
 order_id uuid NOT NULL,
 checkout_id uuid NOT NULL,
 settings_snapshot_id uuid NOT NULL,
 staff_id uuid,
 share numeric(5,4) NOT NULL CHECK (share > 0 AND share <= 1),
 designated boolean NOT NULL,
 gender text NOT NULL CHECK (gender IN ('male','female')),
 void_id uuid,
 reverses_id uuid UNIQUE,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (tenant_id, id),
 CHECK ((void_id IS NULL) = (reverses_id IS NULL)),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, order_id) REFERENCES orders (tenant_id, id),
 FOREIGN KEY (tenant_id, checkout_id) REFERENCES checkouts (tenant_id, id),
 FOREIGN KEY (tenant_id, settings_snapshot_id) REFERENCES settings_snapshots (tenant_id, id),
 FOREIGN KEY (tenant_id, staff_id) REFERENCES staff (tenant_id, id),
 FOREIGN KEY (tenant_id, void_id) REFERENCES order_voids (tenant_id, id),
 FOREIGN KEY (tenant_id, reverses_id) REFERENCES guest_visits (tenant_id, id)
);
ALTER TABLE guest_visits ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON guest_visits
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT ON guest_visits TO salon_app;
CREATE INDEX guest_visits_range ON guest_visits (store_id, business_date, id);
CREATE INDEX guest_visits_staff_range ON guest_visits (store_id, staff_id, business_date, id);


-- +goose Down
DROP TABLE guest_visits;
