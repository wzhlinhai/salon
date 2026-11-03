-- name: LockPayroll :exec
-- 底薪、加减项和第二步的锁月在同一门店咨询锁上排队。
SELECT pg_advisory_xact_lock(hashtextextended(CAST(sqlc.arg(store_id)::uuid AS text) || '/payroll', 0));

-- name: GetCommissionSettlement :one
SELECT * FROM commission_settlements WHERE store_id = $1 AND month = $2;

-- name: ListCommissionSettlements :many
SELECT * FROM commission_settlements WHERE store_id = $1 ORDER BY month DESC;

-- name: AddCommissionSettlement :one
INSERT INTO commission_settlements (tenant_id,store_id,month,settled_by) VALUES ($1,$2,$3,$4) RETURNING *;

