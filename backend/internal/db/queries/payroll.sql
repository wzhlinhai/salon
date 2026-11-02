-- name: LockPayroll :exec
-- 底薪、加减项和第二步的锁月在同一门店咨询锁上排队。
SELECT pg_advisory_xact_lock(hashtextextended(CAST(sqlc.arg(store_id)::uuid AS text) || '/payroll', 0));

-- name: BaseSalariesInForce :many
SELECT DISTINCT ON (staff_id) * FROM staff_base_salaries
WHERE store_id = @store_id AND effective_month <= @month::date
ORDER BY staff_id, effective_month DESC, saved_at DESC, id DESC;

-- name: AddBaseSalary :one
INSERT INTO staff_base_salaries (tenant_id, store_id, staff_id, effective_month, mode, amount, saved_by)
VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING *;

-- name: AddPayrollAdjustment :one
INSERT INTO payroll_adjustments (tenant_id, store_id, month, staff_id, name, amount, note, created_by)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING *;

-- name: GetPayrollAdjustment :one
SELECT * FROM payroll_adjustments WHERE store_id = @store_id AND id = @id;

-- name: DeletePayrollAdjustment :exec
DELETE FROM payroll_adjustments WHERE store_id = @store_id AND id = @id;

-- name: ListPayrollStaff :many
SELECT * FROM staff WHERE store_id = $1 ORDER BY number, id;

-- name: ListPayrollAdjustments :many
SELECT * FROM payroll_adjustments a
WHERE a.store_id = @store_id AND a.month = @month::date
AND (sqlc.narg(staff_id)::uuid IS NULL OR a.staff_id = sqlc.narg(staff_id))
AND (sqlc.narg(before_id)::uuid IS NULL OR (a.created_at, a.id) < (
 SELECT b.created_at, b.id FROM payroll_adjustments b WHERE b.store_id = @store_id AND b.id = sqlc.narg(before_id)))
ORDER BY a.created_at DESC, a.id DESC LIMIT @row_limit;
