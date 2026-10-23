-- +goose Up
CREATE TABLE checkouts (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 business_date date NOT NULL,
 checked_out_by uuid NOT NULL,
 at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, checked_out_by) REFERENCES staff (tenant_id, id)
);
ALTER TABLE checkouts ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON checkouts
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT ON checkouts TO salon_app;

-- UUID v7 保留支付登记顺序，视图按 id 排列。
CREATE TABLE order_payments (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 checkout_id uuid NOT NULL,
 order_id uuid NOT NULL,
 method text NOT NULL CHECK (method IN ('cash','unionpay','wechat','alipay','debt')),
 amount NUMERIC(10,2) NOT NULL CHECK (amount > 0),
 UNIQUE (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, checkout_id) REFERENCES checkouts (tenant_id, id),
 FOREIGN KEY (tenant_id, order_id) REFERENCES orders (tenant_id, id)
);
CREATE INDEX order_payments_order ON order_payments (store_id, order_id);
ALTER TABLE order_payments ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON order_payments
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT ON order_payments TO salon_app;

ALTER TABLE orders
 DROP CONSTRAINT orders_status_check,
 ADD CONSTRAINT orders_status_check CHECK (status IN ('open','merged','paid')),
 ADD COLUMN checkout_id uuid,
 ADD FOREIGN KEY (tenant_id, checkout_id) REFERENCES checkouts (tenant_id, id),
 ADD CHECK ((status = 'paid') = (checkout_id IS NOT NULL));
GRANT UPDATE (checkout_id) ON orders TO salon_app;

-- +goose Down
REVOKE UPDATE (checkout_id) ON orders FROM salon_app;
UPDATE orders SET status='open', checkout_id=NULL WHERE status='paid';
ALTER TABLE orders
 DROP COLUMN checkout_id,
 DROP CONSTRAINT orders_status_check,
 ADD CONSTRAINT orders_status_check CHECK (status IN ('open','merged'));
DROP TABLE order_payments;
DROP TABLE checkouts;
