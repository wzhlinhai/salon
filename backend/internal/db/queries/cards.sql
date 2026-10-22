-- name: ListCardTypes :many
SELECT * FROM card_types WHERE store_id=$1 ORDER BY active DESC, created_at, id;

-- name: CreateCardType :one
INSERT INTO card_types (tenant_id,store_id,name,kind,validity_kind,validity_date,validity_days)
VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING *;

-- name: LockCardType :one
SELECT * FROM card_types WHERE store_id=$1 AND id=$2 FOR NO KEY UPDATE;

-- name: ShareCardType :one
SELECT * FROM card_types WHERE store_id=$1 AND id=$2 FOR SHARE;

-- name: UpdateCardType :one
UPDATE card_types SET name=$2,kind=$3,validity_kind=$4,validity_date=$5,validity_days=$6,active=$7 WHERE id=$1 RETURNING *;

-- name: ShareCardCustomer :one
SELECT * FROM customers WHERE store_id=$1 AND id=$2 FOR SHARE;

-- name: CreateCard :one
INSERT INTO cards (tenant_id,store_id,customer_id,card_type_id,opened_at,expires_on,created_by)
VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING *;

-- name: LockCard :one
SELECT * FROM cards WHERE store_id=$1 AND id=$2 FOR NO KEY UPDATE;

-- name: GetCard :one
SELECT * FROM cards WHERE store_id=$1 AND id=$2;

-- name: LatestCardLedger :one
SELECT * FROM card_ledger WHERE card_id=$1 ORDER BY at DESC,id DESC LIMIT 1;

-- name: AddCardLedger :one
INSERT INTO card_ledger (tenant_id,store_id,card_id,kind,principal_before,principal_delta,principal_after,bonus_before,bonus_delta,bonus_after,business_date,actor_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING *;

-- name: AddCardLedgerSales :exec
INSERT INTO card_ledger_sales (tenant_id,store_id,ledger_id,staff_id) VALUES ($1,$2,$3,$4);

-- name: ListCustomerCards :many
SELECT sqlc.embed(c), t.name AS card_type_name, l.principal_after, l.bonus_after
FROM cards c JOIN card_types t ON t.id=c.card_type_id
JOIN LATERAL (SELECT principal_after,bonus_after FROM card_ledger WHERE card_id=c.id ORDER BY at DESC,id DESC LIMIT 1) l ON true
WHERE c.store_id=$1 AND c.customer_id=$2 ORDER BY c.opened_at DESC,c.id;

-- name: CardView :one
SELECT sqlc.embed(c), t.name AS card_type_name, l.principal_after, l.bonus_after
FROM cards c JOIN card_types t ON t.id=c.card_type_id
JOIN LATERAL (SELECT principal_after,bonus_after FROM card_ledger WHERE card_id=c.id ORDER BY at DESC,id DESC LIMIT 1) l ON true
WHERE c.store_id=$1 AND c.id=$2;

-- name: ListCardLedger :many
SELECT sqlc.embed(l), s.name AS actor_name FROM card_ledger l JOIN staff s ON s.id=l.actor_id
WHERE l.card_id=$1 AND (sqlc.narg(before_id)::uuid IS NULL OR (l.at,l.id) < (SELECT at,id FROM card_ledger WHERE id=sqlc.narg(before_id) AND card_id=$1))
ORDER BY l.at DESC,l.id DESC LIMIT sqlc.arg(row_limit);

-- name: ListCardLedgerSales :many
SELECT cs.ledger_id,s.id,s.name FROM card_ledger_sales cs JOIN staff s ON s.id=cs.staff_id
WHERE cs.ledger_id=ANY(sqlc.arg(ledger_ids)::uuid[]) ORDER BY cs.ledger_id,s.id;

-- name: MergeCustomerCards :exec
UPDATE cards SET customer_id=sqlc.arg(keep_id) WHERE customer_id=sqlc.arg(merged_id);
