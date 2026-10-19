-- name: CreateStore :one
INSERT INTO stores (tenant_id, name) VALUES ($1, $2)
RETURNING *;

-- name: GetStore :one
SELECT * FROM stores WHERE id = $1;

-- name: ListStores :many
SELECT * FROM stores ORDER BY created_at, id;

-- name: RenameStore :execrows
UPDATE stores SET name = $2 WHERE id = $1;

-- name: DeleteStore :execrows
DELETE FROM stores WHERE id = $1;
