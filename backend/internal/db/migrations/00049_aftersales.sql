-- +goose Up
ALTER TABLE orders ADD CONSTRAINT orders_tenant_store_id_key UNIQUE (tenant_id,store_id,id);
ALTER TABLE cards ADD CONSTRAINT cards_tenant_store_id_key UNIQUE (tenant_id,store_id,id);

CREATE TABLE aftersales (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 order_id uuid NOT NULL,
 type text NOT NULL CHECK (type IN ('repair','redo','partial_refund','full_refund')),
 reason text NOT NULL CHECK (char_length(btrim(reason)) BETWEEN 1 AND 200),
 refund_lines jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(refund_lines) = 'array'),
 status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected')),
 business_date date NOT NULL,
 created_by uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 approved_by uuid,
 approved_at timestamptz,
 rejected_by uuid,
 rejected_at timestamptz,
 reject_note text CHECK (reject_note IS NULL OR char_length(reject_note) <= 200),
 deduct_performance boolean NOT NULL DEFAULT false,
 deduct_commission boolean NOT NULL DEFAULT false,
 CHECK ((status = 'approved') = (approved_by IS NOT NULL)),
 CHECK ((status = 'approved') = (approved_at IS NOT NULL)),
 CHECK ((status = 'rejected') = (rejected_by IS NOT NULL)),
 CHECK ((status = 'rejected') = (rejected_at IS NOT NULL)),
 CHECK (status = 'rejected' OR reject_note IS NULL),
 CHECK (NOT deduct_commission OR deduct_performance),
 CHECK (type IN ('partial_refund','full_refund') OR (refund_lines = '[]'::jsonb AND NOT deduct_performance AND NOT deduct_commission)),
 UNIQUE (tenant_id,id),
 UNIQUE (tenant_id,store_id,id),
 FOREIGN KEY (tenant_id,store_id) REFERENCES stores (tenant_id,id),
 FOREIGN KEY (tenant_id,store_id,order_id) REFERENCES orders (tenant_id,store_id,id),
 FOREIGN KEY (tenant_id,created_by) REFERENCES staff (tenant_id,id),
 FOREIGN KEY (tenant_id,approved_by) REFERENCES staff (tenant_id,id),
 FOREIGN KEY (tenant_id,rejected_by) REFERENCES staff (tenant_id,id)
);
CREATE INDEX aftersales_order ON aftersales (store_id,order_id,status);
CREATE INDEX aftersales_range ON aftersales (store_id,business_date,created_at DESC,id DESC);
ALTER TABLE aftersales ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON aftersales
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE aftersale_events (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 aftersale_id uuid NOT NULL,
 kind text NOT NULL CHECK (kind IN ('created','approved','rejected')),
 note text CHECK (note IS NULL OR (btrim(note) <> '' AND char_length(note) <= 200)),
 actor_id uuid NOT NULL,
 at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (tenant_id,id),
 FOREIGN KEY (tenant_id,store_id) REFERENCES stores (tenant_id,id),
 FOREIGN KEY (tenant_id,store_id,aftersale_id) REFERENCES aftersales (tenant_id,store_id,id),
 FOREIGN KEY (tenant_id,actor_id) REFERENCES staff (tenant_id,id)
);
CREATE INDEX aftersale_events_order ON aftersale_events (store_id,aftersale_id,at,id);
ALTER TABLE aftersale_events ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON aftersale_events
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE aftersale_refunds (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 aftersale_id uuid NOT NULL,
 method text NOT NULL CHECK (method IN ('cash','unionpay','wechat','alipay','card_principal')),
 amount numeric(10,2) NOT NULL CHECK (amount > 0),
 card_id uuid,
 business_date date NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK ((method = 'card_principal') = (card_id IS NOT NULL)),
 UNIQUE (tenant_id,id),
 FOREIGN KEY (tenant_id,store_id) REFERENCES stores (tenant_id,id),
 FOREIGN KEY (tenant_id,store_id,aftersale_id) REFERENCES aftersales (tenant_id,store_id,id),
 FOREIGN KEY (tenant_id,store_id,card_id) REFERENCES cards (tenant_id,store_id,id)
);
CREATE INDEX aftersale_refunds_day ON aftersale_refunds (store_id,business_date,method);
CREATE INDEX aftersale_refunds_order ON aftersale_refunds (store_id,aftersale_id,id);
ALTER TABLE aftersale_refunds ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON aftersale_refunds
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

GRANT SELECT, INSERT ON aftersales TO salon_app;
GRANT UPDATE (status,approved_by,approved_at,rejected_by,rejected_at,reject_note,deduct_performance,deduct_commission) ON aftersales TO salon_app;
GRANT SELECT, INSERT ON aftersale_events, aftersale_refunds TO salon_app;

-- 一个原业绩可以有一次撤单冲回和多笔按退款比例分摊的售后冲回。
ALTER TABLE performance_entries DROP CONSTRAINT performance_entries_reverses_id_key;
ALTER TABLE performance_entries DROP CONSTRAINT performance_entries_check2;
ALTER TABLE performance_entries DROP CONSTRAINT performance_entries_check;
ALTER TABLE performance_entries
 ADD COLUMN aftersale_id uuid,
 ADD FOREIGN KEY (tenant_id,store_id,aftersale_id) REFERENCES aftersales (tenant_id,store_id,id),
 ADD CONSTRAINT performance_entries_amount_check CHECK (
  amount = round(paid_amount * ratio / 100.0, 2)
  OR (aftersale_id IS NOT NULL AND amount >= 0 AND amount <= paid_amount)
 ),
 ADD CONSTRAINT performance_entries_reversal_source_check
 CHECK (((void_id IS NULL AND aftersale_id IS NULL) = (reverses_id IS NULL)) AND (void_id IS NULL OR aftersale_id IS NULL));
CREATE UNIQUE INDEX performance_entries_void_reverses ON performance_entries (reverses_id) WHERE void_id IS NOT NULL;
CREATE INDEX performance_entries_aftersale ON performance_entries (store_id,aftersale_id) WHERE aftersale_id IS NOT NULL;

ALTER TABLE card_ledger
 DROP CONSTRAINT card_ledger_kind_check,
 ADD CONSTRAINT card_ledger_kind_check CHECK (kind IN ('open','recharge','adjust','upgrade','transfer_out','transfer_in','consume','void','refund','import','aftersale_refund')),
 ADD COLUMN aftersale_id uuid,
 ADD FOREIGN KEY (tenant_id,store_id,aftersale_id) REFERENCES aftersales (tenant_id,store_id,id),
 ADD CONSTRAINT card_ledger_aftersale_check CHECK ((kind = 'aftersale_refund') = (aftersale_id IS NOT NULL));

ALTER TABLE audit_log DROP CONSTRAINT audit_log_target_kind_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','card_type','card','card_upgrade_rule','order','red_packet','debt','commission_scheme','base_salary','payroll_adjustment','payroll_month','shift','staff_shift','punch','attendance_approval','collection','coupon_redemption','import_batch','data_export','rotation_queue','groupbuy_deal','groupbuy_settlement','appointment','appointment_block','day_close','closing','aftersale'));

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM aftersales) THEN
  RAISE EXCEPTION '无法回滚 00049：存在售后单记录，会破坏卡流水和业绩历史';
 END IF;
END $$;
-- +goose StatementEnd
DELETE FROM audit_log WHERE target_kind = 'aftersale';
ALTER TABLE audit_log DROP CONSTRAINT audit_log_target_kind_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','card_type','card','card_upgrade_rule','order','red_packet','debt','commission_scheme','base_salary','payroll_adjustment','payroll_month','shift','staff_shift','punch','attendance_approval','collection','coupon_redemption','import_batch','data_export','rotation_queue','groupbuy_deal','groupbuy_settlement','appointment','appointment_block','day_close','closing'));

DROP INDEX performance_entries_void_reverses;
DROP INDEX performance_entries_aftersale;
ALTER TABLE performance_entries
 DROP CONSTRAINT performance_entries_amount_check,
 DROP CONSTRAINT performance_entries_reversal_source_check,
 DROP COLUMN aftersale_id,
 ADD CONSTRAINT performance_entries_check CHECK (amount = round(paid_amount * ratio / 100.0, 2)),
 ADD CONSTRAINT performance_entries_check2 CHECK ((void_id IS NULL) = (reverses_id IS NULL));
ALTER TABLE performance_entries ADD CONSTRAINT performance_entries_reverses_id_key UNIQUE (reverses_id);

ALTER TABLE card_ledger
 DROP CONSTRAINT card_ledger_aftersale_check,
 DROP CONSTRAINT card_ledger_kind_check,
 DROP COLUMN aftersale_id,
 ADD CONSTRAINT card_ledger_kind_check CHECK (kind IN ('open','recharge','adjust','upgrade','transfer_out','transfer_in','consume','void','refund','import'));

DROP TABLE aftersale_refunds;
DROP TABLE aftersale_events;
DROP TABLE aftersales;
ALTER TABLE cards DROP CONSTRAINT cards_tenant_store_id_key;
ALTER TABLE orders DROP CONSTRAINT orders_tenant_store_id_key;
