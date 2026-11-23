-- name: AddPerformanceEntry :exec
INSERT INTO performance_entries (tenant_id,store_id,kind,business_date,order_id,order_item_id,checkout_id,settings_snapshot_id,slot,position_id,staff_id,method,paid_amount,commission_paid_amount,ratio,category_ratio,amount,commission_amount,member)
VALUES ($1,$2,sqlc.arg(kind),$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,sqlc.arg(member));

-- name: ReversePerformanceOrder :exec
INSERT INTO performance_entries (tenant_id,store_id,kind,business_date,order_id,order_item_id,checkout_id,settings_snapshot_id,slot,position_id,staff_id,method,paid_amount,commission_paid_amount,ratio,category_ratio,amount,commission_amount,void_id,reverses_id,member)
SELECT e.tenant_id,e.store_id,e.kind,sqlc.arg(day),e.order_id,e.order_item_id,e.checkout_id,e.settings_snapshot_id,e.slot,e.position_id,e.staff_id,e.method,e.paid_amount,e.commission_paid_amount,e.ratio,e.category_ratio,e.amount,e.commission_amount,sqlc.arg(void_id),e.id,e.member
FROM performance_entries e
WHERE e.store_id=sqlc.arg(store_id) AND e.order_id=sqlc.arg(order_id) AND e.reverses_id IS NULL
AND NOT EXISTS (SELECT 1 FROM performance_entries r WHERE r.reverses_id=e.id);

-- name: OrderPerformanceMember :one
SELECT COALESCE(member,false)::boolean AS member FROM performance_entries
WHERE store_id=sqlc.arg(store_id) AND order_id=sqlc.arg(order_id) AND kind<>'sale' AND reverses_id IS NULL
ORDER BY created_at,id LIMIT 1;

-- name: ReversePaidItemPerformance :exec
INSERT INTO performance_entries (tenant_id,store_id,kind,business_date,order_id,order_item_id,checkout_id,settings_snapshot_id,slot,position_id,staff_id,method,paid_amount,commission_paid_amount,ratio,category_ratio,amount,commission_amount,reverses_id,staff_change_id,member)
SELECT e.tenant_id,e.store_id,e.kind,sqlc.arg(day),e.order_id,e.order_item_id,e.checkout_id,e.settings_snapshot_id,e.slot,e.position_id,e.staff_id,e.method,e.paid_amount,e.commission_paid_amount,e.ratio,e.category_ratio,e.amount,e.commission_amount,e.id,sqlc.arg(staff_change_id),e.member
FROM performance_entries e
WHERE e.store_id=sqlc.arg(store_id) AND e.order_item_id=sqlc.arg(order_item_id) AND e.reverses_id IS NULL
AND NOT EXISTS (SELECT 1 FROM performance_entries r WHERE r.reverses_id=e.id);

-- name: AddStaffChangePerformanceEntry :exec
INSERT INTO performance_entries (tenant_id,store_id,kind,business_date,order_id,order_item_id,checkout_id,settings_snapshot_id,slot,position_id,staff_id,method,paid_amount,commission_paid_amount,ratio,category_ratio,amount,commission_amount,staff_change_id,member)
VALUES (sqlc.arg(tenant_id),sqlc.arg(store_id),sqlc.arg(kind),sqlc.arg(business_date),sqlc.arg(order_id),sqlc.arg(order_item_id),sqlc.arg(checkout_id),sqlc.arg(settings_snapshot_id),sqlc.arg(slot),sqlc.arg(position_id),sqlc.arg(staff_id),sqlc.arg(method),sqlc.arg(paid_amount),sqlc.arg(commission_paid_amount),sqlc.arg(ratio),sqlc.arg(category_ratio),sqlc.arg(amount),sqlc.arg(commission_amount),sqlc.arg(staff_change_id),sqlc.arg(member));

-- name: ListPerformance :many
SELECT e.*, i.item_name, s.name AS staff_name
FROM performance_entries e
LEFT JOIN order_items i ON i.id=e.order_item_id
LEFT JOIN staff s ON s.id=e.staff_id
WHERE e.store_id=sqlc.arg(store_id)
AND e.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date
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
AND (sqlc.narg(kind)::text IS NULL OR e.kind=sqlc.narg(kind))
AND (sqlc.narg(staff_id)::uuid IS NULL OR e.staff_id=sqlc.narg(staff_id))
GROUP BY e.kind;

-- name: AddSalePerformance :exec
INSERT INTO performance_entries (tenant_id,store_id,kind,business_date,staff_id,method,paid_amount,commission_paid_amount,ratio,amount,commission_amount,card_ledger_id,red_packet_id,purchase)
VALUES ($1,$2,'sale',$3,$4,$5,$6,$6,100,$6,$6,$7,$8,$9);

-- name: PerformanceUnitEntries :many
SELECT e.* FROM performance_entries e
WHERE e.store_id = @store_id AND e.business_date BETWEEN @date_from::date AND @date_to::date
AND e.staff_id IS NOT NULL AND (sqlc.narg(staff_id)::uuid IS NULL OR e.staff_id = sqlc.narg(staff_id))
AND (e.aftersale_id IS NULL OR EXISTS (SELECT 1 FROM aftersales a WHERE a.tenant_id=e.tenant_id AND a.id=e.aftersale_id AND a.status='approved' AND a.deduct_commission));

-- name: PerformanceOriginalEntries :many
SELECT * FROM performance_entries WHERE store_id = @store_id AND id = ANY(@ids::uuid[]);

-- name: PerformanceLineFacts :many
SELECT i.id, i.item_id, i.category_id, i.price, o.designated, e.checkout_id,
CASE
  WHEN COALESCE(sum(e.commission_paid_amount) FILTER (WHERE e.kind = 'item' AND e.staff_id IS NOT NULL),0) > (i.price - i.discount)
  THEN (i.price - i.discount)::text
  ELSE COALESCE(sum(e.commission_paid_amount) FILTER (WHERE e.kind = 'item'),0)::text
END AS line_paid
FROM performance_entries e JOIN order_items i ON i.id = e.order_item_id JOIN orders o ON o.id = e.order_id
WHERE e.store_id = @store_id AND e.order_item_id = ANY(@ids::uuid[]) AND e.reverses_id IS NULL
GROUP BY i.id, i.item_id, i.category_id, i.price, i.discount, o.designated, e.checkout_id;
