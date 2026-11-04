-- +goose Up
CREATE TABLE punches (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 staff_id uuid NOT NULL,
 at timestamptz NOT NULL,
 business_date date NOT NULL,
 source text NOT NULL CHECK (source IN ('self','manual')),
 reason text NOT NULL DEFAULT '',
 created_by uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 voided_by uuid,
 voided_at timestamptz,
 void_reason text,
 CHECK (source <> 'self' OR (reason = '' AND created_by = staff_id)),
 CHECK (source <> 'manual' OR (reason <> '' AND char_length(reason) <= 100)),
 CHECK ((voided_by IS NULL AND voided_at IS NULL AND void_reason IS NULL) OR (voided_by IS NOT NULL AND voided_at IS NOT NULL AND void_reason IS NOT NULL)),
 CHECK (voided_at IS NULL OR source = 'manual'),
 CHECK (void_reason IS NULL OR (void_reason <> '' AND char_length(void_reason) <= 100)),
 UNIQUE (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, staff_id) REFERENCES staff (tenant_id, id),
 FOREIGN KEY (tenant_id, created_by) REFERENCES staff (tenant_id, id),
 FOREIGN KEY (tenant_id, voided_by) REFERENCES staff (tenant_id, id)
);
CREATE INDEX punches_days ON punches (store_id, business_date, staff_id);
CREATE INDEX punches_staff_time ON punches (store_id, staff_id, at);
CREATE TABLE attendance_approvals (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 staff_id uuid NOT NULL,
 business_date date NOT NULL,
 reason text NOT NULL CHECK (reason <> '' AND char_length(reason) <= 100),
 approved_by uuid NOT NULL,
 approved_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 revoked_by uuid,
 revoked_at timestamptz,
 CHECK ((revoked_by IS NULL AND revoked_at IS NULL) OR (revoked_by IS NOT NULL AND revoked_at IS NOT NULL)),
 UNIQUE (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, staff_id) REFERENCES staff (tenant_id, id),
 FOREIGN KEY (tenant_id, approved_by) REFERENCES staff (tenant_id, id),
 FOREIGN KEY (tenant_id, revoked_by) REFERENCES staff (tenant_id, id)
);
CREATE UNIQUE INDEX attendance_approvals_active ON attendance_approvals (store_id, staff_id, business_date) WHERE revoked_at IS NULL;
ALTER TABLE punches ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON punches
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT ON punches TO salon_app;
ALTER TABLE attendance_approvals ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON attendance_approvals
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT ON attendance_approvals TO salon_app;
GRANT UPDATE (voided_by, voided_at, void_reason) ON punches TO salon_app;
GRANT UPDATE (revoked_by, revoked_at) ON attendance_approvals TO salon_app;
ALTER TABLE audit_log DROP CONSTRAINT audit_log_target_kind_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','card_type','card','card_upgrade_rule','order','red_packet','debt','commission_scheme','base_salary','payroll_adjustment','payroll_month','shift','staff_shift','punch','attendance_approval'));

-- +goose Down
DELETE FROM audit_log WHERE target_kind IN ('punch','attendance_approval');
ALTER TABLE audit_log DROP CONSTRAINT audit_log_target_kind_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','card_type','card','card_upgrade_rule','order','red_packet','debt','commission_scheme','base_salary','payroll_adjustment','payroll_month','shift','staff_shift'));

DROP TABLE attendance_approvals;
DROP TABLE punches;
