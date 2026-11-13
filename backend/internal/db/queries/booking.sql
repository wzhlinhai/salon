-- name: CreateAppointment :one
INSERT INTO appointments (tenant_id, store_id, staff_id, customer_id, kind, starts_at, ends_at, status, note, created_by)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
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
INSERT INTO appointment_events (tenant_id, store_id, appointment_id, kind, from_starts_at, from_ends_at, to_starts_at, to_ends_at, from_staff_id, to_staff_id, reason, note, actor_staff_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13);

-- name: ListAppointmentEvents :many
SELECT e.*, actor.name AS actor_name, fs.name AS from_staff_name, ts.name AS to_staff_name
FROM appointment_events e
JOIN staff actor ON actor.id=e.actor_staff_id
LEFT JOIN staff fs ON fs.id=e.from_staff_id
JOIN staff ts ON ts.id=e.to_staff_id
WHERE e.store_id=$1 AND e.appointment_id=ANY(sqlc.arg(appointment_ids)::uuid[])
ORDER BY e.appointment_id, e.created_at, e.id;

-- name: OccupiedStaff :many
SELECT DISTINCT staff_id FROM appointments
WHERE store_id=sqlc.arg(store_id) AND staff_id=ANY(sqlc.arg(staff_ids)::uuid[])
 AND status IN ('pending','confirmed')
 AND starts_at <= sqlc.arg(at) AND ends_at > sqlc.arg(at);

-- name: OrderHasCompletedAppointment :one
SELECT EXISTS(SELECT 1 FROM appointments WHERE store_id=sqlc.arg(store_id) AND order_id=sqlc.arg(order_id)::uuid AND status='completed') AS found;
