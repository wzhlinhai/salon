-- name: AddAuditLog :exec
INSERT INTO audit_log (tenant_id, store_id, action, target_id, before, after, actor_id, actor_customer_id, target_kind, customer_id, draft_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);

-- name: ListAuditLog :many
SELECT sqlc.embed(a), COALESCE(s.name, ac.name, '顾客本人')::text AS actor_name,
    CASE WHEN a.actor_customer_id IS NULL THEN 'staff' ELSE 'customer' END::text AS actor_kind
FROM audit_log a LEFT JOIN staff s ON s.id = a.actor_id
LEFT JOIN customers ac ON ac.id = a.actor_customer_id
LEFT JOIN customers c ON c.id = a.customer_id
WHERE a.store_id = @store_id AND (sqlc.narg(target_id)::uuid IS NULL OR a.target_id = sqlc.narg(target_id))
    AND (a.target_kind NOT IN ('base_salary','payroll_adjustment','payroll_month') OR sqlc.arg(payroll_manage)::boolean)
    AND (a.target_kind <> 'commission_scheme' OR sqlc.arg(settings_manage)::boolean)
    AND (a.customer_id IS NULL OR (sqlc.arg(customer_view)::boolean AND (sqlc.narg(customer_self_id)::uuid IS NULL OR c.responsible_staff_id = sqlc.narg(customer_self_id))))
    AND (sqlc.narg(before_id)::uuid IS NULL OR (a.at, a.id) < (SELECT b.at, b.id FROM audit_log b WHERE b.id = sqlc.narg(before_id) AND b.store_id = @store_id))
ORDER BY a.at DESC, a.id DESC
LIMIT sqlc.arg(row_limit);
