-- name: AddPerformanceEntry :exec
INSERT INTO performance_entries (tenant_id,store_id,kind,business_date,order_id,order_item_id,checkout_id,settings_snapshot_id,slot,position_id,staff_id,method,paid_amount,ratio,amount)
VALUES ($1,$2,sqlc.arg(kind),$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14);

-- name: ReversePerformanceOrder :exec
INSERT INTO performance_entries (tenant_id,store_id,kind,business_date,order_id,order_item_id,checkout_id,settings_snapshot_id,slot,position_id,staff_id,method,paid_amount,ratio,amount,void_id,reverses_id)
SELECT e.tenant_id,e.store_id,e.kind,sqlc.arg(day),e.order_id,e.order_item_id,e.checkout_id,e.settings_snapshot_id,e.slot,e.position_id,e.staff_id,e.method,e.paid_amount,e.ratio,e.amount,sqlc.arg(void_id),e.id
FROM performance_entries e
WHERE e.store_id=sqlc.arg(store_id) AND e.order_id=sqlc.arg(order_id) AND e.reverses_id IS NULL
AND NOT EXISTS (SELECT 1 FROM performance_entries r WHERE r.reverses_id=e.id);

-- name: ListPerformance :many
SELECT e.*, i.item_name, s.name AS staff_name
FROM performance_entries e
JOIN order_items i ON i.id=e.order_item_id
LEFT JOIN staff s ON s.id=e.staff_id
WHERE e.store_id=sqlc.arg(store_id)
AND e.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date
AND (sqlc.narg(self_id)::uuid IS NULL OR e.staff_id=sqlc.narg(self_id))
AND (sqlc.narg(kind)::text IS NULL OR e.kind=sqlc.narg(kind))
AND (sqlc.narg(staff_id)::uuid IS NULL OR e.staff_id=sqlc.narg(staff_id))
AND (sqlc.narg(before_id)::uuid IS NULL OR e.id < sqlc.narg(before_id))
ORDER BY e.id DESC LIMIT sqlc.arg(row_limit);

-- name: PerformanceTotals :many
SELECT e.kind, COALESCE(sum(CASE WHEN e.reverses_id IS NULL THEN e.amount ELSE -e.amount END),0)::text AS amount,
COALESCE(sum(CASE WHEN e.reverses_id IS NULL THEN e.paid_amount ELSE -e.paid_amount END),0)::text AS paid_amount
FROM performance_entries e
WHERE e.store_id=sqlc.arg(store_id)
AND e.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date
AND (sqlc.narg(self_id)::uuid IS NULL OR e.staff_id=sqlc.narg(self_id))
AND (sqlc.narg(kind)::text IS NULL OR e.kind=sqlc.narg(kind))
AND (sqlc.narg(staff_id)::uuid IS NULL OR e.staff_id=sqlc.narg(staff_id))
GROUP BY e.kind;
