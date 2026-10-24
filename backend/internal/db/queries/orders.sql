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
WITH locked AS MATERIALIZED (
 SELECT o.id FROM orders o WHERE o.customer_id=sqlc.arg(merged_id) ORDER BY o.id FOR NO KEY UPDATE
)
UPDATE orders o SET customer_id=sqlc.arg(keep_id) WHERE o.id IN (SELECT id FROM locked);

-- name: GetOrderItem :one
SELECT * FROM order_items WHERE store_id=$1 AND order_id=$2 AND id=$3;

-- name: OrderInvolvesStaff :one
SELECT EXISTS (
 SELECT 1 FROM orders o WHERE o.store_id=$1 AND o.id=$2 AND
 (o.opened_by=sqlc.arg(staff_id) OR EXISTS (
  SELECT 1 FROM order_items i JOIN order_item_positions p ON p.order_item_id=i.id
  WHERE i.store_id=o.store_id AND i.order_id=o.id AND p.staff_id=sqlc.arg(staff_id)
 ))
)::boolean;

-- name: RemoveOrderItem :exec
UPDATE order_items SET removed_at=clock_timestamp(), removed_by=$2 WHERE id=$1;

-- name: SetOrderItemStaff :exec
UPDATE order_item_positions SET staff_id=$3 WHERE order_item_id=$1 AND slot=$2;

-- name: RequoteOrderItem :exec
UPDATE order_items SET price=$2, price_source=$3, level_id=$4 WHERE id=$1;

-- name: RepriceOrderItem :exec
UPDATE order_items SET price=$2, price_source='manual', level_id=NULL, reprice_reason=$3 WHERE id=$1;

-- name: MoveMergedOrderItems :exec
UPDATE order_items SET order_id=sqlc.arg(order_id)
WHERE store_id=sqlc.arg(store_id) AND order_id=ANY(sqlc.arg(merged_order_ids)::uuid[]);

-- name: MarkOrderMerged :exec
UPDATE orders SET status='merged', merged_into=sqlc.arg(merged_into) WHERE store_id=sqlc.arg(store_id) AND id=sqlc.arg(id);

-- name: CreateCheckout :one
INSERT INTO checkouts (tenant_id,store_id,business_date,checked_out_by)
VALUES ($1,$2,$3,$4) RETURNING *;

-- name: CreateOrderPayment :one
INSERT INTO order_payments (tenant_id,store_id,checkout_id,order_id,method,amount,card_id,card_ledger_id,order_item_id,card_count_ledger_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id;

-- name: ListOrderPayments :many
SELECT p.*, d.id AS debt_id, (-l.principal_delta)::numeric AS principal, (-l.bonus_delta)::numeric AS bonus, c.item_id AS count_item_id, (-COALESCE(c.paid_delta,0))::int AS paid, (-COALESCE(c.gift_delta,0))::int AS gift
FROM order_payments p LEFT JOIN card_ledger l ON l.id = p.card_ledger_id
LEFT JOIN card_count_ledger c ON c.id = p.card_count_ledger_id
LEFT JOIN debts d ON d.order_payment_id=p.id
WHERE p.store_id=$1 AND p.order_id=ANY(sqlc.arg(order_ids)::uuid[]) ORDER BY p.id;

-- name: MarkOrderPaid :exec
UPDATE orders SET status='paid', checkout_id=sqlc.arg(checkout_id) WHERE store_id=sqlc.arg(store_id) AND id=sqlc.arg(id);

-- name: CheckoutOrderItem :exec
UPDATE order_items SET price=$2,price_source=$3,level_id=$4,card_type_id=$5,discount=$6 WHERE id=$1;
