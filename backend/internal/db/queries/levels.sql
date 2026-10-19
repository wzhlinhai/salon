-- name: CreateLevel :one
INSERT INTO levels (tenant_id, store_id, name) VALUES ($1, $2, $3) RETURNING *;

-- name: ListLevels :many
SELECT * FROM levels WHERE store_id = $1 ORDER BY active DESC, created_at, id;

-- name: UpdateLevel :one
UPDATE levels SET name = $2, active = $3 WHERE id = $1 RETURNING *;

-- name: CreatePosition :one
INSERT INTO positions (tenant_id, store_id, name) VALUES ($1, $2, $3) RETURNING *;

-- name: ListPositions :many
SELECT * FROM positions WHERE store_id = $1 ORDER BY active DESC, created_at, id;

-- name: UpdatePosition :one
UPDATE positions SET name = $2, active = $3 WHERE id = $1 RETURNING *;
