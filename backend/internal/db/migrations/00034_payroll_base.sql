-- +goose Up
CREATE TABLE staff_base_salaries (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 staff_id uuid NOT NULL,
 effective_month date NOT NULL CHECK (date_trunc('month', effective_month) = effective_month),
 mode text NOT NULL CHECK (mode IN ('base','guarantee')),
 amount numeric(10,2) NOT NULL CHECK (amount >= 0),
 saved_by uuid NOT NULL,
 saved_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, staff_id) REFERENCES staff (tenant_id, id),
 FOREIGN KEY (tenant_id, saved_by) REFERENCES staff (tenant_id, id)
);
ALTER TABLE staff_base_salaries ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON staff_base_salaries
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT ON staff_base_salaries TO salon_app;
CREATE INDEX staff_base_salaries_lookup ON staff_base_salaries (store_id, staff_id, effective_month, saved_at);
CREATE TABLE payroll_adjustments (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 staff_id uuid NOT NULL,
 month date NOT NULL CHECK (date_trunc('month', month) = month),
 name text NOT NULL CHECK (name <> '' AND char_length(name) <= 20),
 amount numeric(10,2) NOT NULL CHECK (amount <> 0),
 note text NOT NULL DEFAULT '' CHECK (char_length(note) <= 100),
 created_by uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, staff_id) REFERENCES staff (tenant_id, id),
 FOREIGN KEY (tenant_id, created_by) REFERENCES staff (tenant_id, id)
);
ALTER TABLE payroll_adjustments ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON payroll_adjustments
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT, DELETE ON payroll_adjustments TO salon_app;
CREATE INDEX payroll_adjustments_lookup ON payroll_adjustments (store_id, month, created_at, id);
ALTER TABLE audit_log DROP CONSTRAINT audit_log_target_kind_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','card_type','card','card_upgrade_rule','order','red_packet','debt','commission_scheme','base_salary','payroll_adjustment'));

-- +goose Down
DELETE FROM audit_log WHERE target_kind IN ('base_salary','payroll_adjustment');
ALTER TABLE audit_log DROP CONSTRAINT audit_log_target_kind_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','card_type','card','card_upgrade_rule','order','red_packet','debt','commission_scheme'));
DROP TABLE payroll_adjustments;
DROP TABLE staff_base_salaries;
