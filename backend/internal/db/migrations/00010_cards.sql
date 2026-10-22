-- +goose Up
CREATE TABLE card_types (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 name text NOT NULL CHECK (name <> ''),
 kind text NOT NULL CHECK (kind IN ('stored_value')),
 validity_kind text NOT NULL CHECK (validity_kind IN ('none','fixed_date','days_after_open')),
 validity_date date,
 validity_days int CHECK (validity_days > 0),
 active boolean NOT NULL DEFAULT true,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK ((validity_kind = 'fixed_date') = (validity_date IS NOT NULL)),
 CHECK ((validity_kind = 'days_after_open') = (validity_days IS NOT NULL)),
 UNIQUE (store_id, name),
 UNIQUE (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id)
);
CREATE TABLE cards (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 customer_id uuid NOT NULL,
 card_type_id uuid NOT NULL,
 opened_at timestamptz NOT NULL,
 expires_on date,
 family_group_id uuid,
 created_by uuid NOT NULL,
 UNIQUE (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, customer_id) REFERENCES customers (tenant_id, id),
 FOREIGN KEY (tenant_id, card_type_id) REFERENCES card_types (tenant_id, id),
 FOREIGN KEY (tenant_id, created_by) REFERENCES staff (tenant_id, id)
);
COMMENT ON COLUMN cards.family_group_id IS '家庭卡、共用卡关联，P0 只预留，界面不做';
CREATE TABLE card_ledger (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 card_id uuid NOT NULL,
 kind text NOT NULL CHECK (kind IN ('open','recharge')),
 principal_before NUMERIC(10,2) NOT NULL,
 principal_delta NUMERIC(10,2) NOT NULL,
 principal_after NUMERIC(10,2) NOT NULL CHECK (principal_after >= 0),
 bonus_before NUMERIC(10,2) NOT NULL,
 bonus_delta NUMERIC(10,2) NOT NULL,
 bonus_after NUMERIC(10,2) NOT NULL CHECK (bonus_after >= 0),
 business_date date NOT NULL,
 actor_id uuid NOT NULL,
 at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK (principal_after = principal_before + principal_delta),
 CHECK (bonus_after = bonus_before + bonus_delta),
 UNIQUE (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, card_id) REFERENCES cards (tenant_id, id),
 FOREIGN KEY (tenant_id, actor_id) REFERENCES staff (tenant_id, id)
);
CREATE INDEX card_ledger_latest ON card_ledger (card_id, at DESC, id DESC);
CREATE INDEX cards_customer ON cards (store_id, customer_id);
CREATE TABLE card_ledger_sales (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 ledger_id uuid NOT NULL,
 staff_id uuid NOT NULL,
 PRIMARY KEY (ledger_id, staff_id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, ledger_id) REFERENCES card_ledger (tenant_id, id),
 FOREIGN KEY (tenant_id, staff_id) REFERENCES staff (tenant_id, id)
);
ALTER TABLE card_types ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON card_types
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE cards ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON cards
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE card_ledger ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON card_ledger
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE card_ledger_sales ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON card_ledger_sales
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT, UPDATE ON card_types, cards TO salon_app;
GRANT SELECT, INSERT ON card_ledger, card_ledger_sales TO salon_app;
ALTER TABLE audit_log
 DROP CONSTRAINT audit_log_target_kind_check,
 DROP CONSTRAINT audit_log_customer_presence_check,
 DROP CONSTRAINT audit_log_customer_target_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','card_type','card')),
 ADD CONSTRAINT audit_log_customer_presence_check CHECK ((target_kind IN ('customer','card')) = (customer_id IS NOT NULL)),
 ADD CONSTRAINT audit_log_customer_target_check CHECK (target_kind <> 'customer' OR customer_id = target_id);

-- +goose Down
DELETE FROM audit_log WHERE target_kind IN ('card','card_type');
ALTER TABLE audit_log
 DROP CONSTRAINT audit_log_target_kind_check,
 DROP CONSTRAINT audit_log_customer_presence_check,
 DROP CONSTRAINT audit_log_customer_target_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item')),
 ADD CONSTRAINT audit_log_customer_presence_check CHECK ((target_kind = 'customer') = (customer_id IS NOT NULL)),
 ADD CONSTRAINT audit_log_customer_target_check CHECK (target_kind <> 'customer' OR customer_id = target_id);
DROP TABLE card_ledger_sales;
DROP TABLE card_ledger;
DROP TABLE cards;
DROP TABLE card_types;
