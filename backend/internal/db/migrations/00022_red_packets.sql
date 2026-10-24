-- +goose Up
CREATE TABLE red_packets (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 customer_id uuid NOT NULL,
 kind text NOT NULL CHECK (kind IN ('sold','gift')),
 face_value NUMERIC(10,2) NOT NULL CHECK (face_value > 0),
 price NUMERIC(10,2) CHECK (price > 0),
 expires_on date NOT NULL,
 business_date date NOT NULL,
 issued_by uuid NOT NULL,
 at timestamptz NOT NULL DEFAULT clock_timestamp(),
 order_payment_id uuid UNIQUE,
 revoked_by uuid,
 revoked_at timestamptz,
 revoke_reason text,
 CHECK ((kind = 'sold') = (price IS NOT NULL)),
 CHECK ((revoked_by IS NULL AND revoked_at IS NULL AND revoke_reason IS NULL) OR
        (revoked_by IS NOT NULL AND revoked_at IS NOT NULL AND revoke_reason IS NOT NULL)),
 CHECK (order_payment_id IS NULL OR revoked_at IS NULL),
 UNIQUE (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, customer_id) REFERENCES customers (tenant_id, id),
 FOREIGN KEY (tenant_id, issued_by) REFERENCES staff (tenant_id, id),
 FOREIGN KEY (tenant_id, revoked_by) REFERENCES staff (tenant_id, id),
 FOREIGN KEY (tenant_id, order_payment_id) REFERENCES order_payments (tenant_id, id)
);
CREATE INDEX red_packets_customer ON red_packets (store_id, customer_id);
ALTER TABLE red_packets ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON red_packets
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT, UPDATE (customer_id, order_payment_id, revoked_by, revoked_at, revoke_reason) ON red_packets TO salon_app;

CREATE TABLE red_packet_sales (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 red_packet_id uuid NOT NULL,
 staff_id uuid NOT NULL,
 PRIMARY KEY (red_packet_id, staff_id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, red_packet_id) REFERENCES red_packets (tenant_id, id),
 FOREIGN KEY (tenant_id, staff_id) REFERENCES staff (tenant_id, id)
);
ALTER TABLE red_packet_sales ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON red_packet_sales
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT ON red_packet_sales TO salon_app;

ALTER TABLE order_payments
 DROP CONSTRAINT order_payments_method_check,
 ADD CONSTRAINT order_payments_method_check CHECK (method IN ('cash','unionpay','wechat','alipay','debt','card','count','red_packet')),
 ADD COLUMN red_packet_id uuid,
 ADD FOREIGN KEY (tenant_id, red_packet_id) REFERENCES red_packets (tenant_id, id),
 ADD CONSTRAINT order_payments_red_packet_check CHECK ((method = 'red_packet') = (red_packet_id IS NOT NULL));
CREATE UNIQUE INDEX order_payments_red_packet ON order_payments (red_packet_id) WHERE red_packet_id IS NOT NULL;

-- +goose Down
ALTER TABLE red_packets DROP CONSTRAINT red_packets_tenant_id_order_payment_id_fkey;
DELETE FROM order_payments WHERE method='red_packet';
ALTER TABLE order_payments
 DROP COLUMN red_packet_id,
 DROP CONSTRAINT order_payments_method_check,
 ADD CONSTRAINT order_payments_method_check CHECK (method IN ('cash','unionpay','wechat','alipay','debt','card','count'));
DROP TABLE red_packet_sales;
DROP TABLE red_packets;
