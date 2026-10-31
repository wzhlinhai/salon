-- +goose Up
CREATE TABLE commission_scheme_versions (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 version integer NOT NULL CHECK (version > 0),
 effective_from date NOT NULL,
 scheme jsonb NOT NULL,
 saved_by uuid NOT NULL,
 saved_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (tenant_id, id),
 UNIQUE (store_id, version),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, saved_by) REFERENCES staff (tenant_id, id)
);
ALTER TABLE commission_scheme_versions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON commission_scheme_versions
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT ON commission_scheme_versions TO salon_app;
CREATE INDEX commission_scheme_effective ON commission_scheme_versions (store_id, effective_from, saved_at);
ALTER TABLE audit_log DROP CONSTRAINT audit_log_target_kind_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','card_type','card','card_upgrade_rule','order','red_packet','debt','commission_scheme'));

-- +goose Down
DELETE FROM audit_log WHERE target_kind = 'commission_scheme';
ALTER TABLE audit_log DROP CONSTRAINT audit_log_target_kind_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','card_type','card','card_upgrade_rule','order','red_packet','debt'));
DROP TABLE commission_scheme_versions;
