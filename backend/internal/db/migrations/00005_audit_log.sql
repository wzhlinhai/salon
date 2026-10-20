-- +goose Up
-- 员工与角色的操作日志只追加，保留改前改后。
CREATE TABLE audit_log (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL,
    store_id uuid NOT NULL,
    action text NOT NULL,
    target_id uuid NOT NULL,
    before jsonb,
    after jsonb,
    actor_id uuid NOT NULL,
    at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
    FOREIGN KEY (tenant_id, actor_id) REFERENCES staff (tenant_id, id)
);
CREATE INDEX audit_log_lookup ON audit_log (store_id, at);

ALTER TABLE audit_log ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON audit_log
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

GRANT SELECT, INSERT ON audit_log TO salon_app;

-- +goose Down
DROP TABLE audit_log;
