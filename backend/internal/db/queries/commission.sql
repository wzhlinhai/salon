-- name: LockCommissionScheme :exec
SELECT pg_advisory_xact_lock(hashtextextended(CAST(sqlc.arg(store_id)::uuid AS text) || '/commission_scheme', 0));

-- name: LatestCommissionScheme :one
SELECT * FROM commission_scheme_versions WHERE store_id = $1 ORDER BY version DESC LIMIT 1;

-- name: AddCommissionScheme :one
INSERT INTO commission_scheme_versions (tenant_id, store_id, version, effective_from, scheme, saved_by)
VALUES ($1,$2,$3,$4,$5,$6) RETURNING *;

-- name: GetCommissionSchemeVersion :one
SELECT * FROM commission_scheme_versions WHERE store_id = $1 AND version = $2;

-- name: AllCommissionSchemeVersions :many
SELECT * FROM commission_scheme_versions WHERE store_id = $1 ORDER BY version;

-- name: ListCommissionSchemeVersions :many
SELECT v.id, v.version, v.effective_from, v.saved_by, v.saved_at, s.name AS saved_by_name
FROM commission_scheme_versions v JOIN staff s ON s.id = v.saved_by
WHERE v.store_id = @store_id AND (sqlc.narg(before_version)::integer IS NULL OR v.version < sqlc.narg(before_version))
ORDER BY v.version DESC LIMIT sqlc.arg(row_limit);

-- name: CommissionPerformance :many
SELECT * FROM performance_entries
WHERE store_id = @store_id AND business_date BETWEEN @date_from::date AND @date_to::date
AND staff_id IS NOT NULL AND (sqlc.narg(staff_id)::uuid IS NULL OR staff_id = sqlc.narg(staff_id));

-- name: CommissionOriginalEntries :many
SELECT * FROM performance_entries WHERE store_id = @store_id AND id = ANY(@ids::uuid[]);

-- name: CommissionLineFacts :many
SELECT i.id, i.item_id, i.price, o.designated, e.checkout_id,
COALESCE(sum(e.paid_amount) FILTER (WHERE e.kind = 'item'),0)::text AS line_paid
FROM performance_entries e JOIN order_items i ON i.id = e.order_item_id JOIN orders o ON o.id = e.order_id
WHERE e.store_id = @store_id AND e.order_item_id = ANY(@ids::uuid[]) AND e.reverses_id IS NULL
GROUP BY i.id, i.item_id, i.price, o.designated, e.checkout_id;
