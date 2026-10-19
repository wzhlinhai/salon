-- +goose Up
-- 员工、级别、岗位、角色与权限（ADR-0009）。

CREATE TABLE levels (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL,
    store_id   uuid NOT NULL,
    name       text NOT NULL CHECK (name <> ''),
    active     boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
    UNIQUE (tenant_id, id),
    UNIQUE (store_id, name)
);

CREATE TABLE positions (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL,
    store_id   uuid NOT NULL,
    name       text NOT NULL CHECK (name <> ''),
    active     boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
    UNIQUE (tenant_id, id),
    UNIQUE (store_id, name)
);

-- 员工离职后停用，不删除：salon_app 没有 DELETE 权限。
CREATE TABLE staff (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      uuid NOT NULL,
    store_id       uuid NOT NULL,
    name           text NOT NULL CHECK (name <> ''),
    number         text NOT NULL CHECK (number <> ''),
    level_id       uuid,
    position_id    uuid,
    -- 登录账号全局唯一：登录时还不知道租户。
    login_account  text UNIQUE CHECK (login_account <> ''),
    password_hash  text,
    active         boolean NOT NULL DEFAULT true,
    deactivated_at timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
    FOREIGN KEY (tenant_id, level_id) REFERENCES levels (tenant_id, id),
    FOREIGN KEY (tenant_id, position_id) REFERENCES positions (tenant_id, id),
    UNIQUE (tenant_id, id),
    UNIQUE (store_id, number),
    CHECK ((login_account IS NULL) = (password_hash IS NULL)),
    CHECK (active OR deactivated_at IS NOT NULL)
);

CREATE TABLE roles (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL,
    store_id   uuid NOT NULL,
    name       text NOT NULL CHECK (name <> ''),
    -- 老板是内置角色，拥有全部权限，不在 role_permissions 中列出。
    is_owner   boolean NOT NULL DEFAULT false,
    -- 用 clock_timestamp()：开店时同一事务里建的默认角色按建立顺序排列。
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
    UNIQUE (tenant_id, id),
    UNIQUE (store_id, name)
);
CREATE UNIQUE INDEX roles_one_owner_per_store ON roles (store_id) WHERE is_owner;

CREATE TABLE role_permissions (
    tenant_id   uuid NOT NULL,
    store_id    uuid NOT NULL,
    role_id     uuid NOT NULL,
    permission  text NOT NULL,
    -- 数据范围：本人或全店；不区分范围的权限为 NULL。
    scope       text CHECK (scope IN ('self', 'store')),
    -- 时间窗口：NULL 不限，0 只限当天，N 为 N 天内。
    window_days integer CHECK (window_days >= 0),
    PRIMARY KEY (role_id, permission),
    FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
    FOREIGN KEY (tenant_id, role_id) REFERENCES roles (tenant_id, id) ON DELETE CASCADE
);

CREATE TABLE staff_roles (
    tenant_id uuid NOT NULL,
    store_id  uuid NOT NULL,
    staff_id  uuid NOT NULL,
    role_id   uuid NOT NULL,
    PRIMARY KEY (staff_id, role_id),
    FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
    FOREIGN KEY (tenant_id, staff_id) REFERENCES staff (tenant_id, id),
    FOREIGN KEY (tenant_id, role_id) REFERENCES roles (tenant_id, id) ON DELETE CASCADE
);

ALTER TABLE levels ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON levels
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE positions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON positions
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE staff ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON staff
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE roles ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON roles
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE role_permissions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON role_permissions
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE staff_roles ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON staff_roles
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

GRANT SELECT, INSERT, UPDATE ON levels, positions, staff TO salon_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON roles, role_permissions, staff_roles TO salon_app;

-- 登录时还没有租户上下文，用这个函数按账号查找员工。
-- SECURITY DEFINER 以表属主身份执行，只返回登录需要的字段。
-- +goose StatementBegin
CREATE FUNCTION auth_find_login(account text)
RETURNS TABLE (staff_id uuid, tenant_id uuid, password_hash text, active boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
    SELECT s.id, s.tenant_id, s.password_hash, s.active
    FROM public.staff s
    WHERE s.login_account = account;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION auth_find_login(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION auth_find_login(text) TO salon_app;

-- +goose Down
DROP FUNCTION auth_find_login(text);
DROP TABLE staff_roles;
DROP TABLE role_permissions;
DROP TABLE roles;
DROP TABLE staff;
DROP TABLE positions;
DROP TABLE levels;
