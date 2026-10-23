-- +goose Up
ALTER TABLE order_items
 ADD COLUMN removed_at timestamptz,
 ADD COLUMN removed_by uuid,
 ADD COLUMN reprice_reason text,
 ADD FOREIGN KEY (tenant_id, removed_by) REFERENCES staff (tenant_id, id),
 ADD CHECK ((removed_at IS NULL) = (removed_by IS NULL)),
 DROP CONSTRAINT order_items_price_source_check,
 ADD CONSTRAINT order_items_price_source_check CHECK (price_source IN ('standard','level','manual')),
 ADD CHECK ((price_source = 'manual') = (reprice_reason IS NOT NULL)),
 ADD CHECK (reprice_reason IS NULL OR btrim(reprice_reason) <> '');
GRANT UPDATE (price, price_source, level_id, reprice_reason, removed_at, removed_by) ON order_items TO salon_app;
GRANT UPDATE (staff_id) ON order_item_positions TO salon_app;

-- +goose Down
REVOKE UPDATE (staff_id) ON order_item_positions FROM salon_app;
REVOKE UPDATE (price, price_source, level_id, reprice_reason, removed_at, removed_by) ON order_items FROM salon_app;
ALTER TABLE order_items
 DROP COLUMN removed_at,
 DROP COLUMN removed_by,
 DROP COLUMN reprice_reason;
UPDATE order_items SET price_source='standard' WHERE price_source='manual';
ALTER TABLE order_items
 DROP CONSTRAINT order_items_price_source_check,
 ADD CONSTRAINT order_items_price_source_check CHECK (price_source IN ('standard','level'));
