-- name: ShareOrder :one
SELECT * FROM orders WHERE store_id=sqlc.arg(store_id) AND id=sqlc.arg(id) FOR SHARE;

-- name: CreateAftersale :one
INSERT INTO aftersales (tenant_id,store_id,order_id,type,reason,refund_lines,business_date,created_by)
VALUES (sqlc.arg(tenant_id),sqlc.arg(store_id),sqlc.arg(order_id),sqlc.arg(type),sqlc.arg(reason),sqlc.arg(refund_lines)::jsonb,sqlc.arg(business_date),sqlc.arg(created_by))
RETURNING *;

-- name: GetAftersale :one
SELECT * FROM aftersales WHERE store_id=sqlc.arg(store_id) AND id=sqlc.arg(id);

-- name: LockAftersale :one
SELECT * FROM aftersales WHERE store_id=sqlc.arg(store_id) AND id=sqlc.arg(id) FOR NO KEY UPDATE;

-- name: ListAftersales :many
SELECT a.* FROM aftersales a
WHERE a.store_id=sqlc.arg(store_id)
AND a.business_date BETWEEN @date_from::date AND @date_to::date
AND (sqlc.narg(status)::text IS NULL OR a.status=sqlc.narg(status))
AND (sqlc.narg(order_id)::uuid IS NULL OR a.order_id=sqlc.narg(order_id))
AND (sqlc.narg(before_id)::uuid IS NULL OR (a.created_at,a.id) < (SELECT b.created_at,b.id FROM aftersales b WHERE b.id=sqlc.narg(before_id) AND b.store_id=sqlc.arg(store_id)))
ORDER BY a.created_at DESC,a.id DESC
LIMIT @row_limit;

-- name: AddAftersaleEvent :one
INSERT INTO aftersale_events (tenant_id,store_id,aftersale_id,kind,note,actor_id,at)
VALUES (sqlc.arg(tenant_id),sqlc.arg(store_id),sqlc.arg(aftersale_id),sqlc.arg(kind),sqlc.arg(note),sqlc.arg(actor_id),sqlc.arg(at))
RETURNING *;

-- name: ListAftersaleEvents :many
SELECT * FROM aftersale_events
WHERE store_id=sqlc.arg(store_id) AND aftersale_id=ANY(@aftersale_ids::uuid[])
ORDER BY at,id;

-- name: ApproveAftersale :one
UPDATE aftersales
SET status='approved',approved_by=sqlc.arg(approved_by),approved_at=sqlc.arg(approved_at),deduct_performance=sqlc.arg(deduct_performance),deduct_commission=sqlc.arg(deduct_commission)
WHERE store_id=sqlc.arg(store_id) AND id=sqlc.arg(id) AND status='pending'
RETURNING *;

-- name: RejectAftersale :one
UPDATE aftersales
SET status='rejected',rejected_by=sqlc.arg(rejected_by),rejected_at=sqlc.arg(rejected_at),reject_note=sqlc.arg(reject_note)
WHERE store_id=sqlc.arg(store_id) AND id=sqlc.arg(id) AND status='pending'
RETURNING *;

-- name: AddAftersaleRefund :one
INSERT INTO aftersale_refunds (tenant_id,store_id,aftersale_id,method,amount,card_id,business_date)
VALUES (sqlc.arg(tenant_id),sqlc.arg(store_id),sqlc.arg(aftersale_id),sqlc.arg(method),sqlc.arg(amount),sqlc.arg(card_id),sqlc.arg(business_date))
RETURNING *;

-- name: ListAftersaleRefunds :many
SELECT * FROM aftersale_refunds
WHERE store_id=sqlc.arg(store_id) AND aftersale_id=ANY(@aftersale_ids::uuid[])
ORDER BY aftersale_id,id;

-- name: OrderRefundableBase :one
SELECT (
 COALESCE((SELECT sum(p.amount) FROM order_payments p WHERE p.store_id=sqlc.arg(store_id) AND p.order_id=sqlc.arg(order_id) AND p.method IN ('cash','unionpay','wechat','alipay')),0)
 + COALESCE((SELECT sum(-l.principal_delta) FROM order_payments p JOIN card_ledger l ON l.tenant_id=p.tenant_id AND l.id=p.card_ledger_id WHERE p.store_id=sqlc.arg(store_id) AND p.order_id=sqlc.arg(order_id) AND p.method='card'),0)
)::text AS amount;

-- name: ApprovedRefundsByMethod :many
SELECT r.method,COALESCE(sum(r.amount),0)::text AS amount
FROM aftersale_refunds r JOIN aftersales a ON a.tenant_id=r.tenant_id AND a.id=r.aftersale_id
WHERE r.store_id=sqlc.arg(store_id) AND a.order_id=sqlc.arg(order_id) AND a.status='approved'
GROUP BY r.method;

-- name: OrderCardPrincipalPaid :many
SELECT p.card_id,COALESCE(sum(-l.principal_delta),0)::text AS amount
FROM order_payments p JOIN card_ledger l ON l.tenant_id=p.tenant_id AND l.id=p.card_ledger_id
WHERE p.store_id=sqlc.arg(store_id) AND p.order_id=sqlc.arg(order_id) AND p.method='card'
GROUP BY p.card_id;

-- name: ApprovedCardPrincipalRefunds :many
SELECT r.card_id,COALESCE(sum(r.amount),0)::text AS amount
FROM aftersale_refunds r JOIN aftersales a ON a.tenant_id=r.tenant_id AND a.id=r.aftersale_id
WHERE r.store_id=sqlc.arg(store_id) AND a.order_id=sqlc.arg(order_id) AND a.status='approved' AND r.method='card_principal'
GROUP BY r.card_id;

-- name: HasApprovedRefundAftersale :one
SELECT EXISTS(
 SELECT 1 FROM aftersales
 WHERE store_id=sqlc.arg(store_id) AND order_id=sqlc.arg(order_id) AND status='approved' AND type IN ('partial_refund','full_refund')
)::boolean AS exists;

-- name: OrderItemPerformanceEntries :many
SELECT * FROM performance_entries
WHERE store_id=sqlc.arg(store_id) AND order_id=sqlc.arg(order_id) AND kind='item' AND reverses_id IS NULL
ORDER BY id;

-- name: AddAftersalePerformanceEntry :exec
INSERT INTO performance_entries (tenant_id,store_id,kind,business_date,order_id,order_item_id,checkout_id,settings_snapshot_id,slot,position_id,staff_id,method,paid_amount,ratio,amount,reverses_id,aftersale_id,member)
VALUES (sqlc.arg(tenant_id),sqlc.arg(store_id),'item',sqlc.arg(business_date),sqlc.arg(order_id),sqlc.arg(order_item_id),sqlc.arg(checkout_id),sqlc.arg(settings_snapshot_id),sqlc.arg(slot),sqlc.arg(position_id),sqlc.arg(staff_id),sqlc.arg(method),sqlc.arg(paid_amount),sqlc.arg(ratio),sqlc.arg(amount),sqlc.arg(reverses_id),sqlc.arg(aftersale_id),sqlc.arg(member));

-- name: PriorAftersalePerformanceReversals :many
SELECT p.reverses_id,COALESCE(sum(p.paid_amount),0)::text AS paid,COALESCE(sum(p.amount),0)::text AS amount
FROM performance_entries p JOIN aftersales a ON a.tenant_id=p.tenant_id AND a.id=p.aftersale_id
WHERE p.store_id=sqlc.arg(store_id) AND p.order_id=sqlc.arg(order_id) AND p.aftersale_id IS NOT NULL AND p.reverses_id IS NOT NULL AND a.status='approved'
GROUP BY p.reverses_id;
