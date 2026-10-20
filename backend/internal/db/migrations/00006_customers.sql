-- +goose Up
-- 顾客档案、来源与安全标签（ADR-0010）。

CREATE TABLE customer_sources (
    id        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL,
    store_id  uuid NOT NULL,
    name      text NOT NULL CHECK (name <> ''),
    active    boolean NOT NULL DEFAULT true,
    UNIQUE (tenant_id, id),
    UNIQUE (store_id, name),
    FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id)
);

-- 顾客档案不删除：salon_app 没有 DELETE 权限。
CREATE TABLE customers (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id            uuid NOT NULL,
    store_id             uuid NOT NULL,
    name                 text,
    phone                text CHECK (phone ~ '^1[0-9]{10}$'),
    gender               text CHECK (gender IN ('male', 'female')),
    source_id            uuid,
    responsible_staff_id uuid,
    note                 text,
    merged_into          uuid,
    created_by           uuid NOT NULL,
    created_at           timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (tenant_id, id),
    FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
    FOREIGN KEY (tenant_id, source_id) REFERENCES customer_sources (tenant_id, id),
    FOREIGN KEY (tenant_id, responsible_staff_id) REFERENCES staff (tenant_id, id),
    FOREIGN KEY (tenant_id, created_by) REFERENCES staff (tenant_id, id),
    FOREIGN KEY (tenant_id, merged_into) REFERENCES customers (tenant_id, id),
    CHECK (merged_into IS NULL OR merged_into <> id)
);

CREATE UNIQUE INDEX customers_unique_phone ON customers (tenant_id, phone)
    WHERE phone IS NOT NULL AND merged_into IS NULL;

-- 安全标签仅撤销，不删除：salon_app 没有 DELETE 权限。
CREATE TABLE customer_safety_labels (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid NOT NULL,
    store_id    uuid NOT NULL,
    customer_id uuid NOT NULL,
    kind        text NOT NULL CHECK (kind IN ('allergy', 'scalp_sensitive', 'recent_chemical')),
    source      text NOT NULL CHECK (source IN ('customer_said', 'staff_observed')),
    note        text,
    observed_on date NOT NULL,
    recorded_by uuid NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT clock_timestamp(),
    revoked_at  timestamptz,
    revoked_by  uuid,
    FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
    FOREIGN KEY (tenant_id, customer_id) REFERENCES customers (tenant_id, id),
    FOREIGN KEY (tenant_id, recorded_by) REFERENCES staff (tenant_id, id),
    FOREIGN KEY (tenant_id, revoked_by) REFERENCES staff (tenant_id, id),
    CHECK ((revoked_at IS NULL) = (revoked_by IS NULL))
);

ALTER TABLE customer_sources ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customer_sources
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT, UPDATE ON customer_sources TO salon_app;

ALTER TABLE customers ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customers
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT, UPDATE ON customers TO salon_app;

ALTER TABLE customer_safety_labels ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customer_safety_labels
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT, UPDATE ON customer_safety_labels TO salon_app;

-- +goose Down
DROP TABLE customer_safety_labels;
DROP TABLE customers;
DROP TABLE customer_sources;
