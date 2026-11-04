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

-- name: LockStaffAttendance :exec
-- 同一员工的所有考勤写入先排队，稳定去重与核准检查所读的打卡集合。
SELECT pg_advisory_xact_lock(hashtextextended((@store_id::uuid)::text || '/attendance/' || (@staff_id::uuid)::text, 0));

-- name: LatestPunch :one
SELECT * FROM punches WHERE store_id = @store_id AND staff_id = @staff_id AND voided_at IS NULL ORDER BY at DESC, id DESC LIMIT 1;

-- name: CreatePunch :one
INSERT INTO punches (tenant_id,store_id,staff_id,at,business_date,source,reason,created_by)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING *;

-- name: GetPunch :one
SELECT * FROM punches WHERE store_id = @store_id AND id = @id;

-- name: LockPunch :one
SELECT * FROM punches WHERE store_id = @store_id AND id = @id FOR NO KEY UPDATE;

-- name: VoidPunch :one
UPDATE punches SET voided_by = @voided_by, voided_at = @voided_at, void_reason = @void_reason WHERE store_id = @store_id AND id = @id RETURNING *;

-- name: AttendancePunches :many
SELECT * FROM punches WHERE store_id = @store_id AND business_date BETWEEN @first_day AND @last_day ORDER BY at, id;

-- name: AttendanceApprovals :many
SELECT * FROM attendance_approvals WHERE store_id = @store_id AND business_date BETWEEN @first_day AND @last_day;

-- name: CreateAttendanceApproval :one
INSERT INTO attendance_approvals (tenant_id,store_id,staff_id,business_date,reason,approved_by)
VALUES ($1,$2,$3,$4,$5,$6) RETURNING *;

-- name: GetAttendanceApproval :one
SELECT * FROM attendance_approvals WHERE store_id = @store_id AND id = @id;

-- name: LockAttendanceApproval :one
SELECT * FROM attendance_approvals WHERE store_id = @store_id AND id = @id FOR NO KEY UPDATE;

-- name: RevokeAttendanceApproval :one
UPDATE attendance_approvals SET revoked_by = @revoked_by, revoked_at = @revoked_at WHERE store_id = @store_id AND id = @id RETURNING *;
