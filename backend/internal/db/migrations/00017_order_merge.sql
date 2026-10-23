-- +goose Up
ALTER TABLE orders
 DROP CONSTRAINT orders_status_check,
 ADD CONSTRAINT orders_status_check CHECK (status IN ('open','merged')),
 ADD COLUMN merged_into uuid,
 ADD FOREIGN KEY (tenant_id, merged_into) REFERENCES orders (tenant_id, id),
 ADD CHECK ((status = 'merged') = (merged_into IS NOT NULL)),
 ADD CHECK (merged_into <> id);
REVOKE UPDATE ON orders FROM salon_app;
GRANT UPDATE (customer_id, status, merged_into) ON orders TO salon_app;
GRANT UPDATE (order_id) ON order_items TO salon_app;

-- +goose Down
REVOKE UPDATE (order_id) ON order_items FROM salon_app;
REVOKE UPDATE (customer_id, status, merged_into) ON orders FROM salon_app;
GRANT UPDATE ON orders TO salon_app;
UPDATE orders SET status='open', merged_into=NULL;
ALTER TABLE orders
 DROP COLUMN merged_into,
 DROP CONSTRAINT orders_status_check,
 ADD CONSTRAINT orders_status_check CHECK (status IN ('open'));
