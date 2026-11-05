-- +goose Up
CREATE TABLE coupon_redemptions (
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 platform text NOT NULL,
 coupon_code text NOT NULL,
 idempotency_key text NOT NULL,
 status text NOT NULL CHECK (status IN ('pending','unknown','redeemed','revoked','failed')),
 redeemed_at timestamptz,
 redeemed_by uuid NOT NULL,
 revoked_at timestamptz,
 order_id uuid,
 created_at timestamptz NOT NULL,
 UNIQUE (tenant_id, id),
 UNIQUE (tenant_id, store_id, idempotency_key),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, redeemed_by) REFERENCES staff (tenant_id, id),
 FOREIGN KEY (tenant_id, order_id) REFERENCES orders (tenant_id, id)
);
ALTER TABLE coupon_redemptions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON coupon_redemptions
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT ON coupon_redemptions TO salon_app;
GRANT UPDATE (status, redeemed_at, revoked_at) ON coupon_redemptions TO salon_app;

-- +goose Down
DROP TABLE coupon_redemptions;
