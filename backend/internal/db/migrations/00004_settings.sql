-- +goose Up
-- 门店设置（ADR-0002）：每次修改追加一行，带生效日期和改前改后，不修改也不删除旧行。
-- 某个营业日的设置值，取生效日期不晚于该日的最新一行；没有行时用系统默认做法。
CREATE TABLE setting_changes (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      uuid NOT NULL,
    store_id       uuid NOT NULL,
    key            text NOT NULL CHECK (key <> ''),
    value          jsonb NOT NULL,
    -- 恢复系统默认做法：value 记下当时的默认值，此后按系统当前的默认值执行。
    is_default     boolean NOT NULL,
    -- 修改前在生效日期那天执行的值，以及它是不是系统默认做法。
    old_value      jsonb NOT NULL,
    old_is_default boolean NOT NULL,
    effective_from date NOT NULL,
    changed_by     uuid NOT NULL,
    -- 用 clock_timestamp()：同一事务里的多次修改也能分出先后。
    changed_at     timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
    FOREIGN KEY (tenant_id, changed_by) REFERENCES staff (tenant_id, id)
);
CREATE INDEX setting_changes_lookup ON setting_changes (store_id, key, effective_from, changed_at);

ALTER TABLE setting_changes ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON setting_changes
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

GRANT SELECT, INSERT ON setting_changes TO salon_app;

-- 下单时保存的规则快照：单据引用它，之后修改设置不影响已下的单。
-- 内容相同的快照共用一行（digest 是规则的摘要）。
CREATE TABLE settings_snapshots (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL,
    store_id   uuid NOT NULL,
    rules      jsonb NOT NULL,
    digest     text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
    UNIQUE (tenant_id, id),
    UNIQUE (store_id, digest)
);

ALTER TABLE settings_snapshots ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON settings_snapshots
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

GRANT SELECT, INSERT ON settings_snapshots TO salon_app;

-- +goose Down
DROP TABLE settings_snapshots;
DROP TABLE setting_changes;
