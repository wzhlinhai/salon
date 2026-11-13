-- +goose Up
ALTER TABLE card_ledger ADD CONSTRAINT card_ledger_tenant_store_id_business_date_key UNIQUE (tenant_id,store_id,id,business_date);
ALTER TABLE red_packets ADD CONSTRAINT red_packets_tenant_store_id_business_date_key UNIQUE (tenant_id,store_id,id,business_date);
CREATE TABLE sale_payments (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 card_ledger_id uuid,
 red_packet_id uuid,
 method text NOT NULL CHECK (method IN ('cash','unionpay','wechat','alipay')),
 amount numeric(10,2) NOT NULL CHECK (amount > 0),
 business_date date NOT NULL,
 created_by uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK ((card_ledger_id IS NULL) <> (red_packet_id IS NULL)),
 UNIQUE (tenant_id,id),
 FOREIGN KEY (tenant_id,store_id) REFERENCES stores (tenant_id,id),
 FOREIGN KEY (tenant_id,store_id,card_ledger_id,business_date) REFERENCES card_ledger (tenant_id,store_id,id,business_date),
 FOREIGN KEY (tenant_id,store_id,red_packet_id,business_date) REFERENCES red_packets (tenant_id,store_id,id,business_date),
 FOREIGN KEY (tenant_id,created_by) REFERENCES staff (tenant_id,id)
);
CREATE INDEX sale_payments_day ON sale_payments (store_id,business_date,method);
CREATE INDEX sale_payments_card ON sale_payments (store_id,card_ledger_id);
CREATE INDEX sale_payments_packet ON sale_payments (store_id,red_packet_id);

CREATE TABLE day_closes (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 business_date date NOT NULL,
 version integer NOT NULL CHECK (version >= 1),
 system jsonb NOT NULL,
 differences jsonb NOT NULL,
 cash_counted numeric(10,2) NOT NULL CHECK (cash_counted >= 0),
 unionpay_actual numeric(10,2) NOT NULL CHECK (unionpay_actual >= 0),
 wechat_actual numeric(10,2) NOT NULL CHECK (wechat_actual >= 0),
 alipay_actual numeric(10,2) NOT NULL CHECK (alipay_actual >= 0),
 note text CHECK (note IS NULL OR char_length(note) <= 200),
 created_by uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (tenant_id,id),
 UNIQUE (store_id,business_date,version),
 FOREIGN KEY (tenant_id,store_id) REFERENCES stores (tenant_id,id),
 FOREIGN KEY (tenant_id,created_by) REFERENCES staff (tenant_id,id)
);
CREATE INDEX day_closes_range ON day_closes (store_id,business_date,version DESC);

CREATE TABLE closing_events (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 business_date date NOT NULL,
 previous_through date,
 kind text NOT NULL CHECK (kind IN ('seal','unseal')),
 reason text CHECK (reason IS NULL OR (reason <> '' AND char_length(reason) <= 200)),
 actor_id uuid NOT NULL,
 at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK (kind <> 'unseal' OR reason IS NOT NULL),
 UNIQUE (tenant_id,id),
 FOREIGN KEY (tenant_id,store_id) REFERENCES stores (tenant_id,id),
 FOREIGN KEY (tenant_id,actor_id) REFERENCES staff (tenant_id,id)
);
CREATE INDEX closing_events_latest ON closing_events (store_id,at DESC,id DESC);
CREATE INDEX closing_events_seals ON closing_events (store_id,business_date) WHERE kind='seal';

CREATE TABLE store_sealings (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 sealed_through date,
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,store_id),
 FOREIGN KEY (tenant_id,store_id) REFERENCES stores (tenant_id,id)
);

ALTER TABLE sale_payments ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON sale_payments
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE day_closes ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON day_closes
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE closing_events ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON closing_events
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE store_sealings ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON store_sealings
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT ON sale_payments, day_closes, closing_events, store_sealings TO salon_app;
GRANT UPDATE (sealed_through,updated_at) ON store_sealings TO salon_app;

ALTER TABLE audit_log DROP CONSTRAINT audit_log_target_kind_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','card_type','card','card_upgrade_rule','order','red_packet','debt','commission_scheme','base_salary','payroll_adjustment','payroll_month','shift','staff_shift','punch','attendance_approval','collection','coupon_redemption','import_batch','data_export','rotation_queue','groupbuy_deal','groupbuy_settlement','appointment','appointment_block','day_close','closing'));

-- +goose Down
DELETE FROM audit_log WHERE target_kind IN ('day_close','closing');
ALTER TABLE audit_log DROP CONSTRAINT audit_log_target_kind_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','card_type','card','card_upgrade_rule','order','red_packet','debt','commission_scheme','base_salary','payroll_adjustment','payroll_month','shift','staff_shift','punch','attendance_approval','collection','coupon_redemption','import_batch','data_export','rotation_queue','groupbuy_deal','groupbuy_settlement','appointment','appointment_block'));
DROP TABLE store_sealings;
DROP TABLE closing_events;
DROP TABLE day_closes;
DROP TABLE sale_payments;
ALTER TABLE card_ledger DROP CONSTRAINT card_ledger_tenant_store_id_business_date_key;
ALTER TABLE red_packets DROP CONSTRAINT red_packets_tenant_store_id_business_date_key;
