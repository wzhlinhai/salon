-- name: CreateRotationQueue :one
INSERT INTO rotation_queues (tenant_id, store_id, name, created_by)
VALUES ($1,$2,$3,$4) RETURNING *;

-- name: LockRotationQueue :one
SELECT * FROM rotation_queues WHERE store_id = @store_id AND id = @id FOR NO KEY UPDATE;

-- name: GetRotationQueue :one
SELECT * FROM rotation_queues WHERE store_id = @store_id AND id = @id;

-- name: LockRotationQueueForUpdate :one
SELECT * FROM rotation_queues WHERE store_id = @store_id AND id = @id FOR UPDATE;

-- name: LockAllRotationQueues :many
SELECT * FROM rotation_queues WHERE store_id = @store_id ORDER BY id FOR NO KEY UPDATE;

-- name: UpdateRotationQueue :one
UPDATE rotation_queues SET name = @name, active = @active WHERE store_id = @store_id AND id = @id RETURNING *;

-- name: ListRotationQueues :many
SELECT * FROM rotation_queues WHERE store_id = $1 ORDER BY active DESC, name, id;

-- name: DeleteRotationMember :exec
DELETE FROM rotation_members WHERE store_id = @store_id AND queue_id = @queue_id AND staff_id = @staff_id;

-- name: ShiftRotationMemberOrder :exec
UPDATE rotation_members SET sort_order = sort_order + 1000000
WHERE store_id = @store_id AND queue_id = @queue_id AND staff_id = @staff_id;

-- name: UpdateRotationMemberOrder :exec
UPDATE rotation_members SET sort_order = @sort_order
WHERE store_id = @store_id AND queue_id = @queue_id AND staff_id = @staff_id;

-- name: AddRotationMember :exec
INSERT INTO rotation_members (tenant_id, store_id, queue_id, staff_id, sort_order)
VALUES ($1,$2,$3,$4,$5);

-- name: ListRotationMembers :many
SELECT m.*, s.name AS staff_name, s.number AS staff_number
FROM rotation_members m JOIN staff s ON s.tenant_id = m.tenant_id AND s.id = m.staff_id
WHERE m.store_id = @store_id AND m.queue_id = @queue_id
ORDER BY m.sort_order, m.id;

-- name: ListAllRotationMembers :many
SELECT m.*, s.name AS staff_name, s.number AS staff_number
FROM rotation_members m JOIN staff s ON s.tenant_id = m.tenant_id AND s.id = m.staff_id
WHERE m.store_id = $1
ORDER BY m.queue_id, m.sort_order, m.id;

-- name: GetRotationMember :one
SELECT * FROM rotation_members WHERE store_id = @store_id AND queue_id = @queue_id AND staff_id = @staff_id;

-- name: MaxRotationPosition :one
SELECT COALESCE(MAX(position), 0)::int FROM rotation_turns
WHERE store_id = @store_id AND queue_id = @queue_id AND business_date = @business_date;

-- name: InsertRotationTurn :one
INSERT INTO rotation_turns (tenant_id, store_id, queue_id, business_date, staff_id, position, status)
VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING *;

-- name: InsertRotationTurns :exec
INSERT INTO rotation_turns (tenant_id,store_id,queue_id,business_date,staff_id,position,status)
SELECT @tenant_id, @store_id, @queue_id, @business_date, t.staff_id, t.position::int, 'off'
FROM unnest(sqlc.arg(staff_ids)::uuid[]) WITH ORDINALITY AS t(staff_id, position);

-- name: LockRotationTurns :many
SELECT t.*, s.name AS staff_name, s.number AS staff_number
FROM rotation_turns t JOIN staff s ON s.tenant_id = t.tenant_id AND s.id = t.staff_id
WHERE t.store_id = @store_id AND t.queue_id = @queue_id AND t.business_date = @business_date
ORDER BY t.position, t.staff_id
FOR UPDATE OF t;

-- name: ListRotationTurns :many
SELECT t.*, s.name AS staff_name, s.number AS staff_number
FROM rotation_turns t JOIN staff s ON s.tenant_id = t.tenant_id AND s.id = t.staff_id
WHERE t.store_id = @store_id AND t.queue_id = @queue_id AND t.business_date = @business_date
ORDER BY t.position, t.staff_id;

-- name: ListPreviousRotationTurns :many
SELECT t.* FROM rotation_turns t
WHERE t.store_id = @store_id AND t.queue_id = @queue_id
 AND t.business_date = (
   SELECT max(p.business_date) FROM rotation_turns p
   WHERE p.store_id = @store_id AND p.queue_id = @queue_id AND p.business_date < @business_date
 )
ORDER BY t.position, t.staff_id;

-- name: ListServingRotationTurns :many
SELECT t.* FROM rotation_turns t
WHERE t.store_id = @store_id AND t.business_date = @business_date
 AND t.status = 'serving' AND t.staff_id = ANY(sqlc.arg(staff_ids)::uuid[])
ORDER BY t.queue_id, t.position;

-- name: LockServingRotationTurnsByOrder :many
SELECT t.* FROM rotation_turns t
WHERE t.store_id = @store_id AND t.status = 'serving' AND t.serving_order_id = sqlc.arg(order_id)::uuid
ORDER BY t.queue_id, t.business_date, t.position, t.staff_id
FOR UPDATE;

-- name: ShiftRotationTurnPositions :exec
UPDATE rotation_turns SET position = position + 1000000
WHERE store_id = @store_id AND queue_id = @queue_id AND business_date = @business_date;

-- name: UpdateRotationTurn :exec
UPDATE rotation_turns
SET position = @position, status = @status, half_pending = @half_pending,
 rest_until = @rest_until, serving_order_id = @serving_order_id, serving_designated = @serving_designated,
 updated_at = clock_timestamp()
WHERE store_id = @store_id AND queue_id = @queue_id AND business_date = @business_date AND staff_id = @staff_id;

-- name: AddRotationEvent :one
INSERT INTO rotation_events
 (tenant_id, store_id, queue_id, business_date, staff_id, kind, reason, note,
  from_position, to_position, minutes, order_id, designated, actor_staff_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING *;

-- name: AddRotationEventAt :one
INSERT INTO rotation_events
 (tenant_id, store_id, queue_id, business_date, staff_id, kind, reason, note,
  from_position, to_position, minutes, order_id, designated, actor_staff_id, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15) RETURNING *;

-- name: ListRotationEvents :many
SELECT e.*, s.name AS staff_name, s.number AS staff_number, a.name AS actor_name
FROM rotation_events e
LEFT JOIN staff s ON s.tenant_id = e.tenant_id AND s.id = e.staff_id
JOIN staff a ON a.tenant_id = e.tenant_id AND a.id = e.actor_staff_id
WHERE e.store_id = @store_id AND e.business_date BETWEEN @from_date AND @to_date
 AND (sqlc.narg(queue_id)::uuid IS NULL OR e.queue_id = sqlc.narg(queue_id))
ORDER BY e.business_date, e.created_at, e.id;

-- name: RotationStats :many
WITH served AS (
 SELECT e.staff_id, e.order_id, bool_or(e.designated) AS designated
 FROM rotation_events e
 WHERE e.store_id = @store_id AND e.business_date BETWEEN @from_date AND @to_date
  AND e.staff_id IS NOT NULL AND e.order_id IS NOT NULL
  AND e.kind IN ('advance_whole','advance_half','advance_designated_no_turn')
 GROUP BY e.staff_id, e.order_id
)
SELECT served.staff_id, s.name AS staff_name, s.number AS staff_number,
 count(*) FILTER (WHERE served.designated)::int AS designated_count,
 count(*) FILTER (WHERE NOT served.designated)::int AS rotation_count
FROM served JOIN staff s ON s.tenant_id = @tenant_id AND s.id = served.staff_id
GROUP BY served.staff_id, s.name, s.number
ORDER BY s.number, served.staff_id;

-- name: DeleteRotationHalfItem :exec
DELETE FROM rotation_half_items WHERE store_id = @store_id AND item_id = @item_id;

-- name: AddRotationHalfItem :exec
INSERT INTO rotation_half_items (tenant_id, store_id, item_id) VALUES ($1,$2,$3);

-- name: ListRotationHalfItems :many
SELECT h.item_id, i.name AS item_name FROM rotation_half_items h
JOIN items i ON i.tenant_id = h.tenant_id AND i.id = h.item_id
WHERE h.store_id = $1 ORDER BY i.name, h.item_id;

-- name: OrderServiceStaff :many
SELECT DISTINCT p.staff_id
FROM order_item_positions p JOIN order_items oi ON oi.tenant_id = p.tenant_id AND oi.id = p.order_item_id
WHERE p.store_id = @store_id AND oi.order_id = @order_id AND oi.removed_at IS NULL
 AND oi.item_kind = 'service' AND p.staff_id IS NOT NULL
ORDER BY p.staff_id;

-- name: OrderServiceItems :many
SELECT oi.item_id, oi.price, oi.discount, COALESCE(h.item_id IS NOT NULL, false)::boolean AS half_item
FROM order_items oi
LEFT JOIN rotation_half_items h ON h.tenant_id = oi.tenant_id AND h.store_id = oi.store_id AND h.item_id = oi.item_id
WHERE oi.store_id = @store_id AND oi.order_id = @order_id AND oi.removed_at IS NULL
 AND oi.item_kind = 'service'
ORDER BY oi.id;

-- name: LockRotationHalfItems :exec
SELECT pg_advisory_xact_lock(hashtextextended((@store_id::uuid)::text || '/rotation_half_items', 0));
