-- name: BaseSalaryHistory :many
-- 本店全部底薪记录；某月执行哪一条由 inforce 选择。
SELECT * FROM staff_base_salaries WHERE store_id = @store_id;

-- name: AddBaseSalary :one
INSERT INTO staff_base_salaries (tenant_id, store_id, staff_id, effective_month, mode, amount, saved_by)
VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING *;

-- name: AddPayrollAdjustment :one
INSERT INTO payroll_adjustments (tenant_id, store_id, month, staff_id, name, amount, note, created_by)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING *;

-- name: GetPayrollAdjustment :one
SELECT * FROM payroll_adjustments WHERE store_id = @store_id AND id = @id;

-- name: DeletePayrollAdjustment :execrows
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

-- name: PayrollLines :many
SELECT p.* FROM payroll_lines p JOIN staff s ON s.id = p.staff_id
WHERE p.store_id = $1 AND p.settlement_id = $2 ORDER BY s.number, s.id;

-- name: AddPayrollLine :exec
INSERT INTO payroll_lines (tenant_id,store_id,settlement_id,staff_id,mode,applied_amount,base_pay,item_commission,consume_commission,labor_commission,sale_commission,commission_total,guarantee_top_up,adjustments_total,total)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15);

-- name: PayrollMonthAdjustments :many
SELECT * FROM payroll_adjustments WHERE store_id = $1 AND month = $2;
