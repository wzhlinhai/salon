-- name: CreateAppointment :one
INSERT INTO appointments (tenant_id, store_id, staff_id, customer_id, kind, starts_at, ends_at, status, note, created_by, created_by_customer)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
RETURNING *;

-- name: GetAppointment :one
SELECT * FROM appointments WHERE store_id=$1 AND id=$2;

-- name: LockAppointment :one
SELECT * FROM appointments WHERE store_id=$1 AND id=$2 FOR NO KEY UPDATE;

-- name: FindOverlappingAppointment :one
SELECT * FROM appointments
WHERE store_id=sqlc.arg(store_id) AND staff_id=sqlc.arg(staff_id) AND status IN ('pending','confirmed')
 AND starts_at < sqlc.arg(ends_at) AND ends_at > sqlc.arg(starts_at)
 AND (sqlc.narg(exclude_id)::uuid IS NULL OR id <> sqlc.narg(exclude_id))
ORDER BY starts_at, id
LIMIT 1;

-- name: ListAppointments :many
SELECT * FROM appointments
WHERE store_id=sqlc.arg(store_id)
 AND starts_at >= sqlc.arg(from_at) AND starts_at < sqlc.arg(to_at)
 AND (sqlc.narg(staff_id)::uuid IS NULL OR staff_id = sqlc.narg(staff_id))
ORDER BY starts_at, id;

-- name: ListCustomerAppointments :many
SELECT * FROM appointments
WHERE store_id=sqlc.arg(store_id) AND customer_id=sqlc.arg(customer_id) AND kind='appointment'
 AND (
  (status IN ('pending','confirmed') AND ends_at >= sqlc.arg(now))
  OR (status IN ('completed','cancelled','no_show') AND starts_at >= sqlc.arg(now) - interval '90 days')
 )
ORDER BY (status NOT IN ('pending','confirmed')), CASE WHEN status IN ('pending','confirmed') THEN starts_at END, starts_at DESC, id DESC
LIMIT sqlc.arg(row_limit);

-- name: RescheduleAppointment :one
UPDATE appointments SET staff_id=$3, starts_at=$4, ends_at=$5
WHERE store_id=$1 AND id=$2
RETURNING *;

-- name: SetAppointmentStatus :one
UPDATE appointments SET status=$3
WHERE store_id=$1 AND id=$2
RETURNING *;

-- name: CompleteAppointment :one
UPDATE appointments SET status='completed', order_id=$3
WHERE store_id=$1 AND id=$2
RETURNING *;

-- name: AddAppointmentItem :exec
INSERT INTO appointment_items (tenant_id, store_id, appointment_id, item_id, position)
VALUES ($1,$2,$3,$4,$5);

-- name: ListAppointmentItems :many
SELECT ai.id, ai.appointment_id, ai.item_id, ai.position, i.name, i.duration_minutes
FROM appointment_items ai
JOIN items i ON i.tenant_id=ai.tenant_id AND i.id=ai.item_id
WHERE ai.store_id=$1 AND ai.appointment_id=ANY(sqlc.arg(appointment_ids)::uuid[])
ORDER BY ai.appointment_id, ai.position;

-- name: AddAppointmentEvent :exec
INSERT INTO appointment_events (tenant_id, store_id, appointment_id, kind, from_starts_at, from_ends_at, to_starts_at, to_ends_at, from_staff_id, to_staff_id, reason, note, actor_staff_id, actor_customer_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14);

-- name: ListAppointmentEvents :many
SELECT e.*, actor.name AS actor_name, fs.name AS from_staff_name, ts.name AS to_staff_name
FROM appointment_events e
LEFT JOIN staff actor ON actor.id=e.actor_staff_id
LEFT JOIN staff fs ON fs.id=e.from_staff_id
LEFT JOIN staff ts ON ts.id=e.to_staff_id
WHERE e.store_id=$1 AND e.appointment_id=ANY(sqlc.arg(appointment_ids)::uuid[])
ORDER BY e.appointment_id, e.created_at, e.id;

-- name: OccupiedStaff :many
SELECT DISTINCT staff_id FROM appointments
WHERE store_id=sqlc.arg(store_id) AND staff_id=ANY(sqlc.arg(staff_ids)::uuid[])
 AND status IN ('pending','confirmed')
 AND starts_at <= sqlc.arg(at) AND ends_at > sqlc.arg(at);

-- name: OrderHasCompletedAppointment :one
SELECT EXISTS(SELECT 1 FROM appointments WHERE store_id=sqlc.arg(store_id) AND order_id=sqlc.arg(order_id)::uuid AND status='completed') AS found;

-- name: ListStaffOccupied :many
SELECT id, starts_at, ends_at FROM appointments
WHERE store_id=sqlc.arg(store_id) AND staff_id=sqlc.arg(staff_id)
 AND status IN ('pending','confirmed')
 AND starts_at < sqlc.arg(day_end) AND ends_at > sqlc.arg(day_start)
ORDER BY starts_at, id;

-- name: CountCustomerSelfBookings :one
SELECT count(*) FROM appointments
WHERE store_id=sqlc.arg(store_id) AND customer_id=sqlc.arg(customer_id)
 AND created_by_customer IS NOT NULL
 AND status IN ('pending','confirmed')
 AND starts_at >= sqlc.arg(now);

-- name: LockCustomerSelfBooking :exec
SELECT pg_advisory_xact_lock(hashtextextended((sqlc.arg(store_id)::uuid)::text || '/self_booking/' || (sqlc.arg(customer_id)::uuid)::text, 0));

-- name: ListWalkinLines :many
SELECT o.id AS order_id, o.customer_id, o.opened_at,
       oi.id AS line_id, oi.added_at, oi.removed_at, oi.item_kind,
       i.duration_minutes, p.staff_id
FROM orders o
JOIN order_items oi ON oi.tenant_id=o.tenant_id AND oi.order_id=o.id
JOIN items i ON i.tenant_id=oi.tenant_id AND i.id=oi.item_id
LEFT JOIN LATERAL (
 SELECT pos.staff_id
 FROM order_item_positions pos
 WHERE pos.tenant_id=oi.tenant_id AND pos.order_item_id=oi.id
 ORDER BY pos.slot
 LIMIT 1
) p ON true
WHERE o.store_id=sqlc.arg(store_id)
 AND o.status='open'
 AND NOT o.backfill
 AND o.business_date=sqlc.arg(business_date)::date
 AND (sqlc.narg(staff_id)::uuid IS NULL OR p.staff_id=sqlc.narg(staff_id))
ORDER BY o.id, oi.added_at, oi.id;

-- name: ListWalkinConflictCandidates :many
SELECT a.id AS appointment_id, a.kind, a.staff_id, a.customer_id, a.order_id,
       a.starts_at, a.ends_at, s.name AS staff_name
FROM appointments a
JOIN staff s ON s.tenant_id=a.tenant_id AND s.id=a.staff_id
WHERE a.store_id=sqlc.arg(store_id)
 AND a.status IN ('pending','confirmed')
 AND a.staff_id=ANY(sqlc.arg(staff_ids)::uuid[])
 AND a.starts_at < sqlc.arg(to_at) AND a.ends_at > sqlc.arg(from_at)
ORDER BY a.starts_at, a.id;
