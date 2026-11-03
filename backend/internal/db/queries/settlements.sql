-- name: SettledCommissions :many
-- 同一语句读取结算月及其明细，空月份或没有该员工的明细也保留锁定标记。
SELECT s.month AS settlement_month, c.id, c.business_date, c.source, c.staff_id,
 COALESCE(c.amount::text, '0.00') AS amount, c.reversal, c.explanation, c.unit_created_at
FROM commission_settlements s
LEFT JOIN settled_commissions c
 ON c.tenant_id = s.tenant_id AND c.store_id = s.store_id AND c.settlement_id = s.id
 AND (sqlc.narg(staff_id)::uuid IS NULL OR c.staff_id = sqlc.narg(staff_id))
WHERE s.store_id = @store_id AND s.month BETWEEN @date_from::date AND @date_to::date;

-- name: AddSettledCommission :exec
INSERT INTO settled_commissions (tenant_id,store_id,settlement_id,month,id,business_date,source,staff_id,amount,reversal,explanation,unit_created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12);
