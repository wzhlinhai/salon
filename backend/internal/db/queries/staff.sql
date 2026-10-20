-- name: CreateStaff :one
INSERT INTO staff (tenant_id, store_id, name, number, level_id, position_id, login_account, password_hash)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetStaff :one
SELECT * FROM staff WHERE id = $1;

-- name: LockStaff :one
SELECT * FROM staff WHERE id = $1 FOR UPDATE;

-- name: ListStaff :many
SELECT * FROM staff WHERE store_id = $1 ORDER BY active DESC, number, id;

-- name: UpdateStaffProfile :one
UPDATE staff SET name = $2, number = $3, level_id = $4, position_id = $5
WHERE id = $1
RETURNING *;

-- name: SetStaffLogin :execrows
UPDATE staff SET login_account = $2, password_hash = $3 WHERE id = $1;

-- name: SetStaffPassword :execrows
UPDATE staff SET password_hash = $2 WHERE id = $1 AND login_account IS NOT NULL;

-- name: DeactivateStaff :execrows
UPDATE staff SET active = false, deactivated_at = now() WHERE id = $1 AND active;

-- name: ReactivateStaff :execrows
UPDATE staff SET active = true, deactivated_at = NULL WHERE id = $1 AND NOT active;
