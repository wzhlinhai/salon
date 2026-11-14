-- +goose Up
-- 全量导出按 store_id + 旧 ORDER BY 的列 keyset 翻页，避免每页全店扫描和排序。
CREATE INDEX customers_export ON customers (store_id, created_at, id);
CREATE INDEX cards_export ON cards (store_id, opened_at, id);
CREATE INDEX card_ledger_export ON card_ledger (store_id, at, id);
CREATE INDEX orders_export ON orders (store_id, opened_at, id);
CREATE INDEX order_payments_export ON order_payments (store_id, id);
CREATE INDEX card_items_export ON card_items (store_id, card_id, item_id);

-- +goose Down
DROP INDEX card_items_export;
DROP INDEX order_payments_export;
DROP INDEX orders_export;
DROP INDEX card_ledger_export;
DROP INDEX cards_export;
DROP INDEX customers_export;
