-- +goose Up
CREATE TABLE collections (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 order_id uuid NOT NULL,
 order_payment_id uuid UNIQUE,
 channel text NOT NULL,
 amount numeric(10,2) NOT NULL CHECK (amount > 0),
 status text NOT NULL CHECK (status IN ('pending','processing','succeeded','failed','cancelled','refunded')),
 external_ref text,
 created_by uuid NOT NULL,
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 UNIQUE (tenant_id,id),
 FOREIGN KEY (tenant_id,store_id) REFERENCES stores (tenant_id,id),
 FOREIGN KEY (tenant_id,order_id) REFERENCES orders (tenant_id,id),
 FOREIGN KEY (tenant_id,order_payment_id) REFERENCES order_payments (tenant_id,id),
 FOREIGN KEY (tenant_id,created_by) REFERENCES staff (tenant_id,id)
);
CREATE INDEX collections_order ON collections (store_id,order_id,id);
CREATE TABLE collection_events (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 collection_id uuid NOT NULL,
 from_status text NOT NULL,
 to_status text NOT NULL,
 actor_id uuid NOT NULL,
 at timestamptz NOT NULL,
 reason text NOT NULL,
 UNIQUE (tenant_id,id),
 FOREIGN KEY (tenant_id,store_id) REFERENCES stores (tenant_id,id),
 FOREIGN KEY (tenant_id,collection_id) REFERENCES collections (tenant_id,id),
 FOREIGN KEY (tenant_id,actor_id) REFERENCES staff (tenant_id,id)
);
ALTER TABLE collections ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON collections
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE collection_events ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON collection_events
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT ON collections, collection_events TO salon_app;
GRANT UPDATE (status,updated_at,external_ref) ON collections TO salon_app;
ALTER TABLE audit_log DROP CONSTRAINT audit_log_target_kind_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','card_type','card','card_upgrade_rule','order','red_packet','debt','commission_scheme','base_salary','payroll_adjustment','payroll_month','shift','staff_shift','punch','attendance_approval','collection'));

-- +goose Down
DELETE FROM audit_log WHERE target_kind='collection';
ALTER TABLE audit_log DROP CONSTRAINT audit_log_target_kind_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','card_type','card','card_upgrade_rule','order','red_packet','debt','commission_scheme','base_salary','payroll_adjustment','payroll_month','shift','staff_shift','punch','attendance_approval'));
DROP TABLE collection_events;
DROP TABLE collections;
