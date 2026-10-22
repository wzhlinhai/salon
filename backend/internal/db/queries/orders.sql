-- name: ShareOrderCustomer :one
SELECT * FROM customers WHERE store_id=$1 AND id=$2 FOR SHARE;

-- name: ShareOrderItem :one
SELECT * FROM items WHERE store_id=$1 AND id=$2 FOR SHARE;

-- name: CreateOrder :one
INSERT INTO orders (tenant_id,store_id,customer_id,gender,designated,settings_snapshot_id,business_date,opened_by)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING *;

-- name: LockOrder :one
SELECT * FROM orders WHERE store_id=$1 AND id=$2 FOR NO KEY UPDATE;

-- name: GetOrder :one
SELECT * FROM orders WHERE store_id=$1 AND id=$2;

-- name: ListOpenOrders :many
SELECT * FROM orders WHERE store_id=$1 AND status='open' ORDER BY opened_at,id;

-- name: CreateOrderItem :one
INSERT INTO order_items (tenant_id,store_id,order_id,item_id,item_name,price,price_source,level_id,added_by)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING *;

-- name: AddOrderItemPosition :exec
INSERT INTO order_item_positions (tenant_id,store_id,order_item_id,slot,position_id,required,staff_id)
VALUES ($1,$2,$3,$4,$5,$6,$7);

-- name: ListOrderItems :many
SELECT oi.* FROM order_items oi
WHERE oi.store_id=$1 AND oi.order_id=ANY(sqlc.arg(order_ids)::uuid[]) ORDER BY oi.added_at,oi.id;

-- name: ListOrderItemPositions :many
SELECT * FROM order_item_positions WHERE store_id=$1 AND order_item_id=ANY(sqlc.arg(item_ids)::uuid[]) ORDER BY order_item_id,slot;

-- name: MergeCustomerOrders :exec
UPDATE orders SET customer_id=sqlc.arg(keep_id) WHERE customer_id=sqlc.arg(merged_id);

-- name: ShareOrderStaff :one
SELECT * FROM staff WHERE id=$1 FOR SHARE;
