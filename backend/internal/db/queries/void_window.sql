-- name: CreateVoidWindow :one
INSERT INTO void_windows (tenant_id, store_id, staff_id, opened_at, expires_at)
VALUES ($1, $2, $3, $4, $5) RETURNING *;

-- name: HasVoidWindow :one
SELECT EXISTS (SELECT 1 FROM void_windows
WHERE store_id = $1 AND staff_id = $2 AND expires_at > @now::timestamptz) AS allowed;
