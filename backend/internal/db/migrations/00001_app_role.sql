-- +goose Up
-- 业务代码在事务中 SET LOCAL ROLE salon_app 后再访问数据。
-- salon_app 不是表的属主，也没有 BYPASSRLS，因此始终受行级安全约束。
-- 角色属于整个 PostgreSQL 集群，可能被同一集群的其他数据库使用，所以回滚时只撤销本库的授权，不删除角色。
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'salon_app') THEN
        CREATE ROLE salon_app NOLOGIN NOSUPERUSER NOBYPASSRLS;
    END IF;
END
$$;
-- +goose StatementEnd
GRANT USAGE ON SCHEMA public TO salon_app;

-- +goose Down
REVOKE USAGE ON SCHEMA public FROM salon_app;
