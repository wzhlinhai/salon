-- +goose Up
ALTER TABLE audit_log DROP CONSTRAINT audit_log_target_kind_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','card_type','card','card_upgrade_rule','order','red_packet','debt','commission_scheme','base_salary','payroll_adjustment','payroll_month','shift','staff_shift','punch','attendance_approval','collection','coupon_redemption','import_batch','data_export','rotation_queue','groupbuy_deal','groupbuy_settlement'));

CREATE TABLE groupbuy_deals (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 platform text NOT NULL CHECK (platform <> '' AND char_length(platform) <= 100),
 name text NOT NULL CHECK (name <> '' AND char_length(name) <= 200),
 purchase_price numeric(10,2) NOT NULL CHECK (purchase_price >= 0),
 item_id uuid NOT NULL,
 active boolean NOT NULL DEFAULT true,
 created_by uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (tenant_id, id),
 UNIQUE (store_id, platform, name),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, item_id) REFERENCES items (tenant_id, id),
 FOREIGN KEY (tenant_id, created_by) REFERENCES staff (tenant_id, id)
);
ALTER TABLE groupbuy_deals ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON groupbuy_deals
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT ON groupbuy_deals TO salon_app;
GRANT UPDATE (name, purchase_price, item_id, active) ON groupbuy_deals TO salon_app;

ALTER TABLE coupon_redemptions
 ADD COLUMN deal_id uuid,
 ADD COLUMN purchase_price numeric(10,2) CHECK (purchase_price >= 0),
 ADD COLUMN store_price numeric(10,2) CHECK (store_price >= 0),
 ADD COLUMN linked_at timestamptz,
 ADD COLUMN order_payment_id uuid UNIQUE,
 ADD FOREIGN KEY (tenant_id, deal_id) REFERENCES groupbuy_deals (tenant_id, id),
 ADD FOREIGN KEY (tenant_id, order_payment_id) REFERENCES order_payments (tenant_id, id),
 ADD CONSTRAINT coupon_redemptions_link_check CHECK (
  (deal_id IS NULL) = (purchase_price IS NULL)
  AND (deal_id IS NULL) = (store_price IS NULL)
  AND (order_id IS NULL OR deal_id IS NOT NULL)
  AND ((linked_at IS NULL) = (order_id IS NULL))),
 ADD CONSTRAINT coupon_redemptions_payment_check CHECK (order_payment_id IS NULL OR order_id IS NOT NULL);
GRANT UPDATE (deal_id, purchase_price, store_price, linked_at, order_id, order_payment_id) ON coupon_redemptions TO salon_app;

ALTER TABLE order_payments
 DROP CONSTRAINT order_payments_method_check,
 ADD CONSTRAINT order_payments_method_check CHECK (method IN ('cash','unionpay','wechat','alipay','debt','card','count','red_packet','groupbuy')),
 ADD COLUMN coupon_redemption_id uuid,
 ADD FOREIGN KEY (tenant_id, coupon_redemption_id) REFERENCES coupon_redemptions (tenant_id, id),
 ADD CONSTRAINT order_payments_groupbuy_check CHECK ((method = 'groupbuy') = (coupon_redemption_id IS NOT NULL));
CREATE INDEX order_payments_redemption ON order_payments (store_id, coupon_redemption_id);

ALTER TABLE performance_entries
 DROP CONSTRAINT performance_entries_method_check,
 DROP CONSTRAINT performance_entries_item_method_check,
 ADD CONSTRAINT performance_entries_method_check CHECK (method IN ('cash','unionpay','wechat','alipay','debt','red_packet','card_principal','card_bonus','count_paid','count_gift','labor','card_open','card_recharge','card_upgrade','red_packet_sold','groupbuy')),
 ADD CONSTRAINT performance_entries_item_method_check CHECK ((kind = 'item') = (method IN ('cash','unionpay','wechat','alipay','debt','red_packet','card_principal','card_bonus','groupbuy')));

CREATE TABLE groupbuy_settlements (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 platform text NOT NULL CHECK (platform <> '' AND char_length(platform) <= 100),
 month text NOT NULL CHECK (month ~ '^[0-9]{4}-(0[1-9]|1[0-2])$'),
 amount numeric(10,2) NOT NULL CHECK (amount >= 0),
 note text NOT NULL DEFAULT '' CHECK (char_length(note) <= 500),
 entered_by uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, entered_by) REFERENCES staff (tenant_id, id)
);
CREATE INDEX groupbuy_settlements_month ON groupbuy_settlements (store_id, platform, month, created_at, id);
ALTER TABLE groupbuy_settlements ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON groupbuy_settlements
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT ON groupbuy_settlements TO salon_app;

-- +goose Down
UPDATE coupon_redemptions SET order_payment_id = NULL;
DELETE FROM performance_entries WHERE method = 'groupbuy';
DELETE FROM order_payments WHERE method = 'groupbuy';
ALTER TABLE performance_entries
 DROP CONSTRAINT performance_entries_method_check,
 DROP CONSTRAINT performance_entries_item_method_check,
 ADD CONSTRAINT performance_entries_method_check CHECK (method IN ('cash','unionpay','wechat','alipay','debt','red_packet','card_principal','card_bonus','count_paid','count_gift','labor','card_open','card_recharge','card_upgrade','red_packet_sold')),
 ADD CONSTRAINT performance_entries_item_method_check CHECK ((kind = 'item') = (method IN ('cash','unionpay','wechat','alipay','debt','red_packet','card_principal','card_bonus')));
ALTER TABLE order_payments
 DROP CONSTRAINT order_payments_groupbuy_check,
 DROP CONSTRAINT order_payments_method_check,
 DROP COLUMN coupon_redemption_id,
 ADD CONSTRAINT order_payments_method_check CHECK (method IN ('cash','unionpay','wechat','alipay','debt','card','count','red_packet'));
REVOKE UPDATE (deal_id, purchase_price, store_price, linked_at, order_id, order_payment_id) ON coupon_redemptions FROM salon_app;
ALTER TABLE coupon_redemptions
 DROP CONSTRAINT coupon_redemptions_link_check,
 DROP CONSTRAINT coupon_redemptions_payment_check,
 DROP COLUMN deal_id,
 DROP COLUMN purchase_price,
 DROP COLUMN store_price,
 DROP COLUMN linked_at,
 DROP COLUMN order_payment_id;
GRANT UPDATE (status, redeemed_at, revoked_at) ON coupon_redemptions TO salon_app;
DROP TABLE groupbuy_settlements;
DROP TABLE groupbuy_deals;
DELETE FROM audit_log WHERE target_kind IN ('groupbuy_deal','groupbuy_settlement');
ALTER TABLE audit_log DROP CONSTRAINT audit_log_target_kind_check,
 ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer','staff','role','customer_source','item','card_type','card','card_upgrade_rule','order','red_packet','debt','commission_scheme','base_salary','payroll_adjustment','payroll_month','shift','staff_shift','punch','attendance_approval','collection','coupon_redemption','import_batch','data_export','rotation_queue'));
