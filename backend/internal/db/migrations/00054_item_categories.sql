-- +goose Up
CREATE TABLE item_categories (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 name text NOT NULL CHECK (name <> ''),
 performance_ratio smallint NOT NULL DEFAULT 100 CHECK (performance_ratio BETWEEN 0 AND 100),
 active boolean NOT NULL DEFAULT true,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (store_id, name),
 UNIQUE (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id)
);

ALTER TABLE item_categories ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON item_categories
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

GRANT SELECT, INSERT, UPDATE ON item_categories TO salon_app;

ALTER TABLE items ADD COLUMN category_id uuid;
ALTER TABLE items ADD CONSTRAINT items_category_fkey
 FOREIGN KEY (tenant_id, category_id) REFERENCES item_categories (tenant_id, id);

ALTER TABLE order_items
 ADD COLUMN category_id uuid,
 ADD COLUMN category_ratio smallint NOT NULL DEFAULT 100 CHECK (category_ratio BETWEEN 0 AND 100);
ALTER TABLE order_items ADD CONSTRAINT order_items_category_fkey
 FOREIGN KEY (tenant_id, category_id) REFERENCES item_categories (tenant_id, id);

ALTER TABLE performance_entries DROP CONSTRAINT performance_entries_amount_check;
ALTER TABLE performance_entries ADD COLUMN category_ratio smallint NOT NULL DEFAULT 100 CHECK (category_ratio BETWEEN 0 AND 100);
ALTER TABLE performance_entries
 ADD CONSTRAINT performance_entries_amount_check CHECK (amount = round(paid_amount * ratio * category_ratio / 10000.0, 2) OR (aftersale_id IS NOT NULL AND amount >= 0 AND amount <= paid_amount)),
 ADD CONSTRAINT performance_entries_category_ratio_kind_check CHECK (kind NOT IN ('labor','sale') OR category_ratio = 100);

ALTER TABLE audit_log DROP CONSTRAINT audit_log_target_kind_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','item_category','card_type','card','card_upgrade_rule','order','red_packet','debt','commission_scheme','base_salary','payroll_adjustment','payroll_month','shift','staff_shift','punch','attendance_approval','collection','coupon_redemption','import_batch','data_export','rotation_queue','groupbuy_deal','groupbuy_settlement','appointment','appointment_block','day_close','closing','aftersale'));

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM performance_entries WHERE category_ratio <> 100) THEN
  RAISE EXCEPTION '无法回滚 00054：存在类别比例不是 100 的业绩记录';
 END IF;
END $$;
-- +goose StatementEnd
DELETE FROM audit_log WHERE target_kind = 'item_category';
ALTER TABLE audit_log DROP CONSTRAINT audit_log_target_kind_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','card_type','card','card_upgrade_rule','order','red_packet','debt','commission_scheme','base_salary','payroll_adjustment','payroll_month','shift','staff_shift','punch','attendance_approval','collection','coupon_redemption','import_batch','data_export','rotation_queue','groupbuy_deal','groupbuy_settlement','appointment','appointment_block','day_close','closing','aftersale'));

ALTER TABLE performance_entries
 DROP CONSTRAINT performance_entries_amount_check,
 DROP CONSTRAINT performance_entries_category_ratio_kind_check,
 DROP COLUMN category_ratio;
ALTER TABLE performance_entries
 ADD CONSTRAINT performance_entries_amount_check CHECK (amount = round(paid_amount * ratio / 100.0, 2) OR (aftersale_id IS NOT NULL AND amount >= 0 AND amount <= paid_amount));

ALTER TABLE order_items DROP CONSTRAINT order_items_category_fkey;
ALTER TABLE order_items DROP COLUMN category_id, DROP COLUMN category_ratio;

ALTER TABLE items DROP CONSTRAINT items_category_fkey;
ALTER TABLE items DROP COLUMN category_id;

DROP TABLE item_categories;
