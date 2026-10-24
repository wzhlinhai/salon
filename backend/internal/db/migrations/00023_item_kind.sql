-- +goose Up
ALTER TABLE items
 ADD COLUMN kind text NOT NULL DEFAULT 'service' CHECK (kind IN ('service','goods'));
ALTER TABLE order_items
 ADD COLUMN item_kind text NOT NULL DEFAULT 'service' CHECK (item_kind IN ('service','goods'));
ALTER TABLE order_items ALTER COLUMN item_kind DROP DEFAULT;

-- +goose Down
ALTER TABLE order_items DROP COLUMN item_kind;
ALTER TABLE items DROP COLUMN kind;
