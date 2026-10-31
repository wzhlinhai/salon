-- name: LockCommissionScheme :exec
SELECT pg_advisory_xact_lock(hashtextextended(CAST(sqlc.arg(store_id)::uuid AS text) || '/commission_scheme', 0));

-- name: LatestCommissionScheme :one
SELECT * FROM commission_scheme_versions WHERE store_id = $1 ORDER BY version DESC LIMIT 1;

-- name: AddCommissionScheme :one
INSERT INTO commission_scheme_versions (tenant_id, store_id, version, effective_from, scheme, saved_by)
VALUES ($1,$2,$3,$4,$5,$6) RETURNING *;

-- name: GetCommissionSchemeVersion :one
SELECT * FROM commission_scheme_versions WHERE store_id = $1 AND version = $2;

-- name: EffectiveCommissionScheme :one
SELECT * FROM commission_scheme_versions
WHERE store_id = @store_id AND effective_from <= @business_date::date
 AND (sqlc.narg(saved_no_later_than)::timestamptz IS NULL OR saved_at <= sqlc.narg(saved_no_later_than))
ORDER BY effective_from DESC, version DESC LIMIT 1;

-- name: ListCommissionSchemeVersions :many
SELECT v.id, v.version, v.effective_from, v.saved_by, v.saved_at, s.name AS saved_by_name
FROM commission_scheme_versions v JOIN staff s ON s.id = v.saved_by
WHERE v.store_id = @store_id AND (sqlc.narg(before_version)::integer IS NULL OR v.version < sqlc.narg(before_version))
ORDER BY v.version DESC LIMIT sqlc.arg(row_limit);
