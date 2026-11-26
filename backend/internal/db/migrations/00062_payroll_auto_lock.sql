-- +goose Up
-- 自动锁月由系统执行，没有员工操作者；settled_by 为空表示系统自动锁定。
ALTER TABLE commission_settlements ALTER COLUMN settled_by DROP NOT NULL;
-- 系统操作日志的员工与顾客操作者都为空；员工或顾客仍恰好一个。
ALTER TABLE audit_log DROP CONSTRAINT audit_log_actor_check;
ALTER TABLE audit_log ADD CONSTRAINT audit_log_actor_check
    CHECK (actor_id IS NULL OR actor_customer_id IS NULL);

-- +goose Down
ALTER TABLE audit_log DROP CONSTRAINT audit_log_actor_check;
ALTER TABLE audit_log ADD CONSTRAINT audit_log_actor_check
    CHECK ((actor_id IS NULL) <> (actor_customer_id IS NULL));
ALTER TABLE commission_settlements ALTER COLUMN settled_by SET NOT NULL;
