-- +goose Up
CREATE TABLE commission_settlements (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 month date NOT NULL CHECK (date_trunc('month', month) = month),
 settled_by uuid NOT NULL,
 settled_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (tenant_id, store_id, id),
 UNIQUE (tenant_id, store_id, id, month),
 UNIQUE (store_id, month),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, settled_by) REFERENCES staff (tenant_id, id)
);
CREATE TABLE settled_commissions (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 settlement_id uuid NOT NULL,
 month date NOT NULL,
 id uuid NOT NULL,
 business_date date NOT NULL CHECK (date_trunc('month', business_date) = month),
 source text NOT NULL CHECK (source IN ('item','consume','labor','sale')),
 staff_id uuid NOT NULL,
 amount numeric(10,2) NOT NULL,
 reversal boolean NOT NULL,
 explanation jsonb NOT NULL,
 unit_created_at timestamptz NOT NULL,
 UNIQUE (store_id, id),
 FOREIGN KEY (tenant_id, store_id, settlement_id, month) REFERENCES commission_settlements (tenant_id, store_id, id, month),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, staff_id) REFERENCES staff (tenant_id, id)
);
CREATE INDEX settled_commissions_lookup ON settled_commissions (store_id, month, staff_id);
CREATE TABLE payroll_lines (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 settlement_id uuid NOT NULL,
 staff_id uuid NOT NULL,
 mode text NOT NULL CHECK (mode IN ('base','guarantee')),
 applied_amount numeric(10,2) NOT NULL CHECK (applied_amount >= 0),
 base_pay numeric(10,2) NOT NULL,
 item_commission numeric(10,2) NOT NULL,
 consume_commission numeric(10,2) NOT NULL,
 labor_commission numeric(10,2) NOT NULL,
 sale_commission numeric(10,2) NOT NULL,
 commission_total numeric(10,2) NOT NULL,
 guarantee_top_up numeric(10,2) NOT NULL,
 adjustments_total numeric(10,2) NOT NULL,
 total numeric(10,2) NOT NULL,
 CHECK (commission_total = item_commission + consume_commission + labor_commission + sale_commission),
 CHECK (total = base_pay + commission_total + guarantee_top_up + adjustments_total),
 CHECK (mode <> 'base' OR (base_pay = applied_amount AND guarantee_top_up = 0)),
 CHECK (mode <> 'guarantee' OR (base_pay = 0 AND guarantee_top_up = greatest(0, applied_amount - commission_total))),
 UNIQUE (settlement_id, staff_id),
 FOREIGN KEY (tenant_id, store_id, settlement_id) REFERENCES commission_settlements (tenant_id, store_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, staff_id) REFERENCES staff (tenant_id, id)
);
ALTER TABLE commission_settlements ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON commission_settlements
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT ON commission_settlements TO salon_app;
ALTER TABLE settled_commissions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON settled_commissions
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT ON settled_commissions TO salon_app;
ALTER TABLE payroll_lines ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON payroll_lines
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT ON payroll_lines TO salon_app;
ALTER TABLE audit_log DROP CONSTRAINT audit_log_target_kind_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','card_type','card','card_upgrade_rule','order','red_packet','debt','commission_scheme','base_salary','payroll_adjustment','payroll_month'));


-- +goose Down
DELETE FROM audit_log WHERE target_kind = 'payroll_month';
ALTER TABLE audit_log DROP CONSTRAINT audit_log_target_kind_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','card_type','card','card_upgrade_rule','order','red_packet','debt','commission_scheme','base_salary','payroll_adjustment'));

DROP TABLE payroll_lines;
DROP TABLE settled_commissions;
DROP TABLE commission_settlements;
