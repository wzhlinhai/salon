-- +goose Up
ALTER TABLE audit_log ADD COLUMN target_kind text, ADD COLUMN customer_id uuid;

UPDATE audit_log a SET target_kind = 'customer', customer_id = a.target_id
FROM customers c WHERE c.tenant_id = a.tenant_id AND c.id = a.target_id;
UPDATE audit_log a SET target_kind = 'staff'
FROM staff s WHERE s.tenant_id = a.tenant_id AND s.id = a.target_id AND a.target_kind IS NULL;
UPDATE audit_log a SET target_kind = 'customer_source'
FROM customer_sources s WHERE s.tenant_id = a.tenant_id AND s.id = a.target_id AND a.target_kind IS NULL;
UPDATE audit_log a SET target_kind = 'item'
FROM items i WHERE i.tenant_id = a.tenant_id AND i.id = a.target_id AND a.target_kind IS NULL;
UPDATE audit_log SET target_kind = 'role'
WHERE action IN ('create_role', 'update_role', 'delete_role') AND target_kind IS NULL;

-- 未识别的历史目标必须使迁移失败，不能猜测类型。
ALTER TABLE audit_log ALTER COLUMN target_kind SET NOT NULL;
-- 顾客不删除（ADR-0010），所有顾客写路径用 FOR NO KEY UPDATE，与此外键的 KEY SHARE 兼容。
ALTER TABLE audit_log
    ADD CONSTRAINT audit_log_customer_fk FOREIGN KEY (tenant_id, customer_id) REFERENCES customers (tenant_id, id),
    ADD CONSTRAINT audit_log_target_kind_check CHECK (target_kind IN ('customer', 'staff', 'role', 'customer_source', 'item')),
    ADD CONSTRAINT audit_log_customer_presence_check CHECK ((target_kind = 'customer') = (customer_id IS NOT NULL)),
    ADD CONSTRAINT audit_log_customer_target_check CHECK (target_kind <> 'customer' OR customer_id = target_id);

-- +goose Down
ALTER TABLE audit_log
    DROP CONSTRAINT audit_log_customer_fk,
    DROP CONSTRAINT audit_log_target_kind_check,
    DROP CONSTRAINT audit_log_customer_presence_check,
    DROP CONSTRAINT audit_log_customer_target_check,
    DROP COLUMN customer_id,
    DROP COLUMN target_kind;
