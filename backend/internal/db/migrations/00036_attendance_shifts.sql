-- +goose Up
CREATE TABLE shifts (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 name text NOT NULL CHECK (name <> '' AND char_length(name) <= 20),
 start_time time NOT NULL,
 end_time time NOT NULL,
 CHECK (start_time < end_time),
 CHECK (extract(second FROM start_time) = 0),
 CHECK (extract(second FROM end_time) = 0),
 active boolean NOT NULL DEFAULT true,
 created_by uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (tenant_id, id),
 UNIQUE (store_id, name),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, created_by) REFERENCES staff (tenant_id, id)
);
CREATE TABLE staff_shifts (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 staff_id uuid NOT NULL,
 shift_id uuid,
 effective_from date NOT NULL,
 saved_by uuid NOT NULL,
 saved_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, staff_id) REFERENCES staff (tenant_id, id),
 FOREIGN KEY (tenant_id, saved_by) REFERENCES staff (tenant_id, id),
 FOREIGN KEY (tenant_id, shift_id) REFERENCES shifts (tenant_id, id)
);
CREATE INDEX staff_shifts_lookup ON staff_shifts (store_id, staff_id, effective_from, saved_at);
ALTER TABLE shifts ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON shifts
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT ON shifts TO salon_app;
ALTER TABLE staff_shifts ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON staff_shifts
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT ON staff_shifts TO salon_app;
GRANT UPDATE (active) ON shifts TO salon_app;
ALTER TABLE audit_log DROP CONSTRAINT audit_log_target_kind_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','card_type','card','card_upgrade_rule','order','red_packet','debt','commission_scheme','base_salary','payroll_adjustment','payroll_month','shift','staff_shift'));

-- +goose Down
DELETE FROM audit_log WHERE target_kind IN ('shift','staff_shift');
ALTER TABLE audit_log DROP CONSTRAINT audit_log_target_kind_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','card_type','card','card_upgrade_rule','order','red_packet','debt','commission_scheme','base_salary','payroll_adjustment','payroll_month'));
DROP TABLE staff_shifts;
DROP TABLE shifts;
