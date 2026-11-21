-- +goose Up
-- 按营业日列出已结和已撤的单：先用门店、营业日、状态缩小范围，再按开单时间排序。
CREATE INDEX orders_business_date_status ON orders (store_id, business_date, status);

-- +goose Down
DROP INDEX orders_business_date_status;
