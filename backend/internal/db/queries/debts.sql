-- name: CreateDebt :one
INSERT INTO debts (tenant_id,store_id,customer_id,amount,business_date,order_id,order_payment_id,note,recorded_by)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING *;

-- name: LockDebt :one
SELECT * FROM debts WHERE store_id=$1 AND id=$2 FOR NO KEY UPDATE;

-- name: ListCustomerDebts :many
SELECT * FROM debts WHERE store_id=$1 AND customer_id=$2 ORDER BY id DESC;

-- name: ListDebtRepayments :many
SELECT * FROM debt_repayments WHERE store_id=$1 AND debt_id=ANY(sqlc.arg(debt_ids)::uuid[]) ORDER BY id;

-- name: CreateDebtRepayment :exec
INSERT INTO debt_repayments (tenant_id,store_id,debt_id,amount,method,business_date,repaid_by)
VALUES ($1,$2,$3,$4,$5,$6,$7);

-- name: MergeCustomerDebts :exec
UPDATE debts SET customer_id=sqlc.arg(keep_id) WHERE customer_id=sqlc.arg(merged_id);

-- name: DebtSummary :many
SELECT (d.order_id IS NOT NULL)::boolean AS is_order,
 sum(d.amount)::text AS amount, sum(COALESCE(r.repaid,0))::text AS repaid,
 sum(d.amount-COALESCE(r.repaid,0))::text AS outstanding,
 count(*) FILTER (WHERE d.amount>COALESCE(r.repaid,0))::bigint AS outstanding_count
FROM debts d LEFT JOIN (
 SELECT debt_id,sum(amount) AS repaid FROM debt_repayments GROUP BY debt_id
) r ON r.debt_id=d.id WHERE d.store_id=$1 GROUP BY (d.order_id IS NOT NULL);
