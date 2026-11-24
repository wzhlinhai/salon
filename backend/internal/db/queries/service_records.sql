-- name: ShareServiceOrder :one
SELECT * FROM orders WHERE id = $1 FOR SHARE;

-- name: GetServiceOrder :one
SELECT * FROM orders WHERE id = $1;

-- name: GetServiceOrderItem :one
SELECT * FROM order_items WHERE store_id = $1 AND id = $2;

-- name: LockServiceRecord :one
SELECT * FROM service_records WHERE tenant_id = $1 AND order_item_id = $2 FOR NO KEY UPDATE;

-- name: LockServiceItem :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(tenant_id)::uuid::text || sqlc.arg(order_item_id)::uuid::text, 0));

-- name: UpsertServiceRecord :one
INSERT INTO service_records (tenant_id, store_id, order_item_id, result, formula, feedback, recorded_by, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
ON CONFLICT (order_item_id) DO UPDATE
SET result = EXCLUDED.result, formula = EXCLUDED.formula, feedback = EXCLUDED.feedback,
    recorded_by = EXCLUDED.recorded_by, updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: CountServiceHistory :one
SELECT count(*)::bigint FROM orders o
JOIN order_items oi ON oi.tenant_id = o.tenant_id AND oi.store_id = o.store_id AND oi.order_id = o.id
WHERE o.store_id = sqlc.arg(store_id)
 AND o.customer_id = sqlc.arg(customer_id)
 AND o.status = 'paid'
 AND oi.item_kind = 'service'
 AND oi.removed_at IS NULL;

-- name: ListServiceHistory :many
SELECT o.id AS order_id, o.business_date, oi.id AS order_item_id, oi.item_id, oi.item_name,
       oi.category_id, oi.added_at
FROM orders o
JOIN order_items oi ON oi.tenant_id = o.tenant_id AND oi.store_id = o.store_id AND oi.order_id = o.id
LEFT JOIN checkouts c ON c.tenant_id = o.tenant_id AND c.id = o.checkout_id
WHERE o.store_id = sqlc.arg(store_id)
 AND o.customer_id = sqlc.arg(customer_id)
 AND o.status = 'paid'
 AND oi.item_kind = 'service'
 AND oi.removed_at IS NULL
ORDER BY o.business_date DESC, COALESCE(c.at, o.opened_at) DESC, oi.added_at, oi.id
LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: ListServiceHistoryStaff :many
SELECT p.order_item_id, p.slot, pos.name AS position_name, s.id AS staff_id, s.name AS staff_name
FROM order_item_positions p
JOIN positions pos ON pos.tenant_id = p.tenant_id AND pos.id = p.position_id
JOIN staff s ON s.tenant_id = p.tenant_id AND s.id = p.staff_id
WHERE p.store_id = sqlc.arg(store_id) AND p.order_item_id = ANY(sqlc.arg(item_ids)::uuid[])
ORDER BY p.order_item_id, p.slot;

-- name: ListServiceRecordsByItems :many
SELECT * FROM service_records
WHERE tenant_id = sqlc.arg(tenant_id) AND order_item_id = ANY(sqlc.arg(item_ids)::uuid[]);
