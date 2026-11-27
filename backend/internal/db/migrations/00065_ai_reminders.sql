-- +goose Up
-- AI 提醒：每条规则的档位、每个营业日的检查记录和写下的提醒（含观察档被拦下的原因）。
CREATE TABLE ai_reminder_rules (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 rule_key text NOT NULL CHECK (rule_key <> ''),
 mode text NOT NULL CHECK (mode IN ('off','observe','on')),
 updated_by uuid NOT NULL,
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (tenant_id, id),
 UNIQUE (tenant_id, store_id, rule_key),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, updated_by) REFERENCES staff (tenant_id, id)
);
ALTER TABLE ai_reminder_rules ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ai_reminder_rules
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE ai_reminder_runs (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 rule_key text NOT NULL CHECK (rule_key <> ''),
 business_date date NOT NULL,
 status text NOT NULL CHECK (status IN ('no_match','done','error')),
 failures integer NOT NULL DEFAULT 0 CHECK (failures >= 0),
 last_error text,
 matched integer NOT NULL DEFAULT 0 CHECK (matched >= 0),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (tenant_id, id),
 UNIQUE (tenant_id, store_id, rule_key, business_date),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id)
);
CREATE INDEX ai_reminder_runs_range ON ai_reminder_runs (store_id, rule_key, business_date);
ALTER TABLE ai_reminder_runs ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ai_reminder_runs
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE ai_reminders (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 rule_key text NOT NULL CHECK (rule_key <> ''),
 rule_version integer NOT NULL CHECK (rule_version >= 1),
 mode text NOT NULL CHECK (mode IN ('observe','on')),
 recipient_staff_id uuid NOT NULL,
 subject_key text NOT NULL CHECK (subject_key <> ''),
 business_date date NOT NULL,
 urgent boolean NOT NULL DEFAULT false,
 facts jsonb NOT NULL,
 fallback_text text NOT NULL CHECK (fallback_text <> ''),
 ai_text text,
 outcome text NOT NULL CHECK (outcome IN ('emitted','deduped','cooldown','max_times','expired','daily_cap')),
 feedback text CHECK (feedback IS NULL OR feedback IN ('helpful','not_helpful')),
 feedback_by uuid,
 feedback_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK ((feedback IS NULL AND feedback_by IS NULL AND feedback_at IS NULL)
     OR (feedback IS NOT NULL AND feedback_by IS NOT NULL AND feedback_at IS NOT NULL)),
 UNIQUE (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, recipient_staff_id) REFERENCES staff (tenant_id, id),
 FOREIGN KEY (tenant_id, feedback_by) REFERENCES staff (tenant_id, id)
);
CREATE UNIQUE INDEX ai_reminders_emitted_unique ON ai_reminders
 (tenant_id, store_id, rule_key, mode, recipient_staff_id, subject_key, business_date)
 WHERE outcome = 'emitted';
CREATE INDEX ai_reminders_gate ON ai_reminders
 (store_id, rule_key, recipient_staff_id, subject_key, business_date, outcome);
CREATE INDEX ai_reminders_daily ON ai_reminders
 (store_id, recipient_staff_id, mode, business_date)
 WHERE outcome = 'emitted' AND NOT urgent;
CREATE INDEX ai_reminders_summary ON ai_reminders (store_id, rule_key, business_date, outcome);
ALTER TABLE ai_reminders ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ai_reminders
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

GRANT SELECT, INSERT ON ai_reminder_rules TO salon_app;
GRANT UPDATE (mode, updated_by, updated_at) ON ai_reminder_rules TO salon_app;
GRANT SELECT, INSERT ON ai_reminder_runs TO salon_app;
GRANT UPDATE (status, failures, last_error, matched, updated_at) ON ai_reminder_runs TO salon_app;
GRANT SELECT, INSERT ON ai_reminders TO salon_app;
GRANT UPDATE (ai_text, feedback, feedback_by, feedback_at) ON ai_reminders TO salon_app;

ALTER TABLE audit_log DROP CONSTRAINT audit_log_target_kind_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','card_type','card','card_upgrade_rule','order','red_packet','debt','commission_scheme','base_salary','payroll_adjustment','payroll_month','shift','staff_shift','punch','attendance_approval','collection','coupon_redemption','import_batch','data_export','rotation_queue','groupbuy_deal','groupbuy_settlement','appointment','appointment_block','day_close','closing','ai_reminder_rule'));

-- +goose Down
DELETE FROM audit_log WHERE target_kind = 'ai_reminder_rule';
ALTER TABLE audit_log DROP CONSTRAINT audit_log_target_kind_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','card_type','card','card_upgrade_rule','order','red_packet','debt','commission_scheme','base_salary','payroll_adjustment','payroll_month','shift','staff_shift','punch','attendance_approval','collection','coupon_redemption','import_batch','data_export','rotation_queue','groupbuy_deal','groupbuy_settlement','appointment','appointment_block','day_close','closing'));
DROP TABLE ai_reminders;
DROP TABLE ai_reminder_runs;
DROP TABLE ai_reminder_rules;
