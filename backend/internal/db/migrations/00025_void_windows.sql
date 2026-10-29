-- +goose Up
CREATE TABLE void_windows (
 tenant_id uuid NOT NULL,
 store_id uuid NOT NULL,
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 staff_id uuid NOT NULL,
 opened_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL,
 CHECK (expires_at > opened_at),
 UNIQUE (tenant_id, id),
 FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
 FOREIGN KEY (tenant_id, staff_id) REFERENCES staff (tenant_id, id)
);
CREATE INDEX void_windows_latest ON void_windows (store_id, staff_id, expires_at DESC);
ALTER TABLE void_windows ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON void_windows
 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT ON void_windows TO salon_app;

-- +goose Down
DROP TABLE void_windows;
