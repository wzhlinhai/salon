-- name: AddAuditLog :exec
INSERT INTO audit_log (tenant_id, store_id, action, target_id, before, after, actor_id)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ListAuditLog :many
SELECT sqlc.embed(a), s.name AS actor_name
FROM audit_log a JOIN staff s ON s.id = a.actor_id
WHERE a.store_id = @store_id AND (sqlc.narg(target_id)::uuid IS NULL OR a.target_id = sqlc.narg(target_id))
ORDER BY a.at DESC, a.id DESC;
