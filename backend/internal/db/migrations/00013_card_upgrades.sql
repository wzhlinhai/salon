-- +goose Up
ALTER TABLE card_types ADD CONSTRAINT card_types_tenant_id_id_kind_key UNIQUE (tenant_id,id,kind);
CREATE TABLE card_upgrade_rules (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 from_type_id uuid NOT NULL,
 to_type_id uuid NOT NULL,
 type_kind text NOT NULL DEFAULT 'stored_value' CHECK (type_kind = 'stored_value'),
 condition_kind text NOT NULL CHECK (condition_kind IN ('single_recharge','total_recharge')),
 threshold NUMERIC(10,2) NOT NULL CHECK (threshold > 0),
 active boolean NOT NULL DEFAULT true,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK (from_type_id <> to_type_id),
 UNIQUE (tenant_id,id),
 UNIQUE (from_type_id,to_type_id,condition_kind),
 FOREIGN KEY (tenant_id,store_id) REFERENCES stores (tenant_id,id),
 FOREIGN KEY (tenant_id,from_type_id,type_kind) REFERENCES card_types (tenant_id,id,kind),
 FOREIGN KEY (tenant_id,to_type_id,type_kind) REFERENCES card_types (tenant_id,id,kind)
);
CREATE TABLE card_upgrades (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 card_id uuid NOT NULL,
 type_before uuid NOT NULL,
 type_after uuid NOT NULL,
 trigger text NOT NULL CHECK (trigger IN ('manual','auto')),
 rule_id uuid,
 ledger_id uuid,
 business_date date NOT NULL,
 actor_id uuid NOT NULL,
 at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK (type_before <> type_after),
 CHECK ((trigger = 'auto') = (rule_id IS NOT NULL)),
 FOREIGN KEY (tenant_id,store_id) REFERENCES stores (tenant_id,id),
 FOREIGN KEY (tenant_id,card_id) REFERENCES cards (tenant_id,id),
 FOREIGN KEY (tenant_id,type_before) REFERENCES card_types (tenant_id,id),
 FOREIGN KEY (tenant_id,type_after) REFERENCES card_types (tenant_id,id),
 FOREIGN KEY (tenant_id,rule_id) REFERENCES card_upgrade_rules (tenant_id,id),
 FOREIGN KEY (tenant_id,ledger_id) REFERENCES card_ledger (tenant_id,id),
 FOREIGN KEY (tenant_id,actor_id) REFERENCES staff (tenant_id,id)
);
CREATE INDEX card_upgrades_latest ON card_upgrades (card_id,at DESC,id DESC);
CREATE TABLE card_transfers (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 from_card_id uuid NOT NULL,
 to_card_id uuid NOT NULL,
 reason text NOT NULL CHECK (length(trim(reason)) BETWEEN 1 AND 200),
 from_ledger_id uuid NOT NULL UNIQUE,
 to_ledger_id uuid NOT NULL UNIQUE,
 business_date date NOT NULL,
 actor_id uuid NOT NULL,
 at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK (from_card_id <> to_card_id),
 FOREIGN KEY (tenant_id,store_id) REFERENCES stores (tenant_id,id),
 FOREIGN KEY (tenant_id,from_card_id) REFERENCES cards (tenant_id,id),
 FOREIGN KEY (tenant_id,to_card_id) REFERENCES cards (tenant_id,id),
 FOREIGN KEY (tenant_id,from_ledger_id) REFERENCES card_ledger (tenant_id,id),
 FOREIGN KEY (tenant_id,to_ledger_id) REFERENCES card_ledger (tenant_id,id),
 FOREIGN KEY (tenant_id,actor_id) REFERENCES staff (tenant_id,id)
);
ALTER TABLE card_upgrade_rules ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON card_upgrade_rules
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE card_upgrades ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON card_upgrades
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE card_transfers ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON card_transfers
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT, UPDATE ON card_upgrade_rules TO salon_app;
GRANT SELECT, INSERT ON card_upgrades, card_transfers TO salon_app;
ALTER TABLE card_ledger DROP CONSTRAINT card_ledger_kind_check,
 ADD CONSTRAINT card_ledger_kind_check CHECK (kind IN ('open','recharge','adjust','upgrade','transfer_out','transfer_in'));
ALTER TABLE audit_log DROP CONSTRAINT audit_log_target_kind_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','card_type','card','card_upgrade_rule'));

-- +goose Down
DELETE FROM audit_log WHERE target_kind='card_upgrade_rule';
ALTER TABLE audit_log DROP CONSTRAINT audit_log_target_kind_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','card_type','card'));
DROP TABLE card_transfers;
DROP TABLE card_upgrades;
DROP TABLE card_upgrade_rules;
ALTER TABLE card_types DROP CONSTRAINT card_types_tenant_id_id_kind_key;
ALTER TABLE card_ledger DROP CONSTRAINT card_ledger_kind_check,
 ADD CONSTRAINT card_ledger_kind_check CHECK (kind IN ('open','recharge','adjust'));
