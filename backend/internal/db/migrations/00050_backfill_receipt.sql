-- +goose Up
-- 补录单：断网期间用纸单记下的单，business_date 是纸单那一天的营业日。
ALTER TABLE orders ADD COLUMN backfill boolean NOT NULL DEFAULT false;

-- 收款按营业日归属；补录单的现金/银联收款记在纸单那天的营业日。
-- 旧记录没有这一列，读取时仍按 created_at 换算营业日。
ALTER TABLE collections ADD COLUMN business_date date;
CREATE INDEX collections_business_date ON collections (store_id, business_date);

-- +goose Down
DROP INDEX collections_business_date;
ALTER TABLE collections DROP COLUMN business_date;
ALTER TABLE orders DROP COLUMN backfill;
