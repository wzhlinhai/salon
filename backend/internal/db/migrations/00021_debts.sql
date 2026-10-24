-- +goose Up
CREATE TABLE debts (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 customer_id uuid NOT NULL,
 amount NUMERIC(10,2) NOT NULL CHECK (amount > 0),
 business_date date NOT NULL,
 order_id uuid,
 order_payment_id uuid UNIQUE,
 note text,
 recorded_by uuid NOT NULL,
 at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK ((order_id IS NULL) = (order_payment_id IS NULL)),
 UNIQUE (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, customer_id) REFERENCES customers (tenant_id, id),
 FOREIGN KEY (tenant_id, order_id) REFERENCES orders (tenant_id, id),
 FOREIGN KEY (tenant_id, order_payment_id) REFERENCES order_payments (tenant_id, id),
 FOREIGN KEY (tenant_id, recorded_by) REFERENCES staff (tenant_id, id)
);
CREATE INDEX debts_customer ON debts (store_id, customer_id);
ALTER TABLE debts ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON debts
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT, UPDATE (customer_id) ON debts TO salon_app;

CREATE TABLE debt_repayments (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 debt_id uuid NOT NULL,
 amount NUMERIC(10,2) NOT NULL CHECK (amount > 0),
 method text NOT NULL CHECK (method IN ('cash','unionpay','wechat','alipay')),
 business_date date NOT NULL,
 repaid_by uuid NOT NULL,
 at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, debt_id) REFERENCES debts (tenant_id, id),
 FOREIGN KEY (tenant_id, repaid_by) REFERENCES staff (tenant_id, id)
);
CREATE INDEX debt_repayments_debt ON debt_repayments (store_id, debt_id);
ALTER TABLE debt_repayments ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON debt_repayments
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT ON debt_repayments TO salon_app;

-- 开发库已有的挂账也建立欠款，正式库尚无生产数据。
INSERT INTO debts (tenant_id,store_id,customer_id,amount,business_date,order_id,order_payment_id,recorded_by)
SELECT p.tenant_id,p.store_id,o.customer_id,p.amount,c.business_date,o.id,p.id,c.checked_out_by
FROM order_payments p JOIN orders o ON o.tenant_id=p.tenant_id AND o.id=p.order_id
JOIN checkouts c ON c.tenant_id=p.tenant_id AND c.id=p.checkout_id WHERE p.method='debt';

-- +goose Down
DROP TABLE debt_repayments;
DROP TABLE debts;
