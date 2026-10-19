-- +goose Up
-- 租户和门店两层结构（ADR-0003、ADR-0005）。
-- 当前租户由事务内的 app.tenant_id 指定；未指定时策略不匹配任何行。

CREATE TABLE tenants (
    id         uuid PRIMARY KEY,
    name       text NOT NULL CHECK (name <> ''),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE stores (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES tenants (id),
    name       text NOT NULL CHECK (name <> ''),
    created_at timestamptz NOT NULL DEFAULT now(),
    -- 业务表用 (tenant_id, store_id) 复合外键引用此约束，保证门店属于同一租户。
    UNIQUE (tenant_id, id)
);

ALTER TABLE tenants ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tenants
    USING (id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

ALTER TABLE stores ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON stores
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON tenants, stores TO salon_app;

-- +goose Down
DROP TABLE stores;
DROP TABLE tenants;
