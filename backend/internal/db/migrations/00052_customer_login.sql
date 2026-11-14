-- +goose Up
-- 顾客小程序登录绑定与顾客本人的操作者字段（ADR-0011）。

CREATE TABLE customer_logins (
    tenant_id      uuid NOT NULL,
    store_id       uuid NOT NULL,
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    openid         text NOT NULL CHECK (openid <> ''),
    unionid        text CHECK (unionid IS NULL OR unionid <> ''),
    customer_id    uuid NOT NULL,
    token_version  integer NOT NULL DEFAULT 1,
    created_at     timestamptz NOT NULL DEFAULT clock_timestamp(),
    last_login_at  timestamptz,
    UNIQUE (tenant_id, id),
    UNIQUE (store_id, openid),
    FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id),
    FOREIGN KEY (tenant_id, customer_id) REFERENCES customers (tenant_id, id)
);
CREATE INDEX customer_logins_customer ON customer_logins (store_id, customer_id);

ALTER TABLE customer_logins ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customer_logins
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT, UPDATE ON customer_logins TO salon_app;

-- 登录时还不知道租户，用这个函数按门店找到租户；只返回存在的门店。
-- +goose StatementBegin
CREATE FUNCTION customer_login_store(p_store_id uuid)
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
    SELECT s.tenant_id FROM public.stores s WHERE s.id = p_store_id;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION customer_login_store(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION customer_login_store(uuid) TO salon_app;

-- 顾客本人也可以是操作者：新建顾客、审计日志各增加一个顾客操作者列，
-- 员工与顾客恰好一个非空。appointments 等顾客自助预约的字段留到第二步。
ALTER TABLE customers ALTER COLUMN created_by DROP NOT NULL;
ALTER TABLE customers ADD COLUMN created_by_customer uuid;
ALTER TABLE customers ADD CONSTRAINT customers_actor_check
    CHECK ((created_by IS NULL) <> (created_by_customer IS NULL));
ALTER TABLE customers ADD CONSTRAINT customers_created_by_customer_fkey
    FOREIGN KEY (tenant_id, created_by_customer) REFERENCES customers (tenant_id, id);

ALTER TABLE audit_log ALTER COLUMN actor_id DROP NOT NULL;
ALTER TABLE audit_log ADD COLUMN actor_customer_id uuid;
ALTER TABLE audit_log ADD CONSTRAINT audit_log_actor_check
    CHECK ((actor_id IS NULL) <> (actor_customer_id IS NULL));
ALTER TABLE audit_log ADD CONSTRAINT audit_log_actor_customer_fkey
    FOREIGN KEY (tenant_id, actor_customer_id) REFERENCES customers (tenant_id, id);

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM customer_logins)
    OR EXISTS (SELECT 1 FROM customers WHERE created_by_customer IS NOT NULL)
    OR EXISTS (SELECT 1 FROM audit_log WHERE actor_customer_id IS NOT NULL) THEN
  RAISE EXCEPTION '无法回滚 00052：存在顾客登录或顾客操作者数据';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE audit_log DROP CONSTRAINT audit_log_actor_customer_fkey;
ALTER TABLE audit_log DROP CONSTRAINT audit_log_actor_check;
ALTER TABLE audit_log DROP COLUMN actor_customer_id;
ALTER TABLE audit_log ALTER COLUMN actor_id SET NOT NULL;
ALTER TABLE customers DROP CONSTRAINT customers_created_by_customer_fkey;
ALTER TABLE customers DROP CONSTRAINT customers_actor_check;
ALTER TABLE customers DROP COLUMN created_by_customer;
ALTER TABLE customers ALTER COLUMN created_by SET NOT NULL;
DROP FUNCTION customer_login_store(uuid);
DROP TABLE customer_logins;
