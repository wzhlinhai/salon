-- name: AddCardRefund :one
INSERT INTO card_refunds (tenant_id,store_id,card_id,method,principal,bonus,counts_amount,bonus_policy,bonus_deducted,fee_percent,fee,amount,business_date,refunded_by)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING *;

-- name: MarkCardRefunded :exec
UPDATE cards SET status='refunded' WHERE id=$1;

-- name: CardUsedBonus :one
SELECT COALESCE(sum(-bonus_delta),0)::text AS used_bonus
FROM card_ledger WHERE card_id=$1 AND kind IN ('consume','void');
