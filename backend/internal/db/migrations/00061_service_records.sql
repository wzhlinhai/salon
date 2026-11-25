-- +goose Up
ALTER TABLE order_items ADD CONSTRAINT order_items_tenant_store_id_key UNIQUE (tenant_id, store_id, id);

-- 一次服务的做成什么样、配方和顾客反馈，记在单上的服务项目行上；不是钱，封账后也能改。
CREATE TABLE service_records (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 order_item_id uuid NOT NULL UNIQUE,
 result text CHECK (result IS NULL OR char_length(result) <= 500),
 formula text CHECK (formula IS NULL OR char_length(formula) <= 500),
 feedback text CHECK (feedback IN ('satisfied','neutral','unsatisfied')),
 recorded_by uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (tenant_id, id),
 CHECK (NULLIF(result, '') IS NOT NULL OR NULLIF(formula, '') IS NOT NULL OR feedback IS NOT NULL),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id, order_item_id) REFERENCES order_items (tenant_id, store_id, id),
 FOREIGN KEY (tenant_id, recorded_by) REFERENCES staff (tenant_id, id)
);
ALTER TABLE service_records ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON service_records
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT, UPDATE ON service_records TO salon_app;

-- +goose Down
DROP TABLE service_records;
ALTER TABLE order_items DROP CONSTRAINT order_items_tenant_store_id_key;
