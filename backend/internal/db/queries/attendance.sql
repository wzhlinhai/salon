-- name: LockStaffShifts :exec
-- 员工班次指定在此排队，使日志改前值接上前一次改后值。
SELECT pg_advisory_xact_lock(hashtextextended((@store_id::uuid)::text || '/staff_shift', 0));

-- name: CreateShift :one
INSERT INTO shifts (tenant_id,store_id,name,start_time,end_time,created_by)
VALUES ($1,$2,$3,$4,$5,$6) RETURNING *;

-- name: LockShift :one
SELECT * FROM shifts WHERE store_id = @store_id AND id = @id FOR NO KEY UPDATE;

-- name: ShareShift :one
SELECT * FROM shifts WHERE store_id = @store_id AND id = @id FOR SHARE;

-- name: DeactivateShift :one
UPDATE shifts SET active = false WHERE store_id = @store_id AND id = @id RETURNING *;

-- name: ListShifts :many
SELECT * FROM shifts WHERE store_id = $1 ORDER BY active DESC, start_time, name;

-- name: AddStaffShift :one
INSERT INTO staff_shifts (tenant_id,store_id,staff_id,shift_id,effective_from,saved_by)
VALUES ($1,$2,$3,$4,$5,$6) RETURNING *;

-- name: StaffShiftHistory :many
SELECT ss.*, s.name AS shift_name, s.start_time, s.end_time, s.active AS shift_active,
 s.created_by, s.created_at
FROM staff_shifts ss LEFT JOIN shifts s ON s.tenant_id = ss.tenant_id AND s.id = ss.shift_id
WHERE ss.store_id = $1;
