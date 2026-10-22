-- name: ListCardTypes :many
SELECT * FROM card_types WHERE store_id=$1 ORDER BY active DESC, created_at, id;

-- name: CreateCardType :one
INSERT INTO card_types (tenant_id,store_id,name,kind,validity_kind,validity_date,validity_days)
VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING *;

-- name: GetCardType :one
SELECT * FROM card_types WHERE store_id=$1 AND id=$2;

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
SELECT sqlc.embed(c), t.name AS card_type_name, t.kind, l.principal_after, l.bonus_after
FROM cards c JOIN card_types t ON t.id=c.card_type_id
JOIN LATERAL (SELECT principal_after,bonus_after FROM card_ledger WHERE card_id=c.id ORDER BY at DESC,id DESC LIMIT 1) l ON true
WHERE c.store_id=$1 AND c.customer_id=$2 ORDER BY c.opened_at DESC,c.id;

-- name: CardView :one
SELECT sqlc.embed(c), t.name AS card_type_name, t.kind, l.principal_after, l.bonus_after
FROM cards c JOIN card_types t ON t.id=c.card_type_id
JOIN LATERAL (SELECT principal_after,bonus_after FROM card_ledger WHERE card_id=c.id ORDER BY at DESC,id DESC LIMIT 1) l ON true
WHERE c.store_id=$1 AND c.id=$2;

-- name: ListCardLedger :many
SELECT sqlc.embed(l), s.name AS actor_name, COALESCE(a.reason,'')::text AS reason FROM card_ledger l JOIN staff s ON s.id=l.actor_id LEFT JOIN card_adjustments a ON a.ledger_id=l.id
WHERE l.card_id=$1 AND (sqlc.narg(before_id)::uuid IS NULL OR (l.at,l.id) < (SELECT at,id FROM card_ledger WHERE id=sqlc.narg(before_id) AND card_id=$1))
ORDER BY l.at DESC,l.id DESC LIMIT sqlc.arg(row_limit);

-- name: ListCardLedgerSales :many
SELECT cs.ledger_id,s.id,s.name FROM card_ledger_sales cs JOIN staff s ON s.id=cs.staff_id
WHERE cs.ledger_id=ANY(sqlc.arg(ledger_ids)::uuid[]) ORDER BY cs.ledger_id,s.id;

-- name: MergeCustomerCards :exec
UPDATE cards SET customer_id=sqlc.arg(keep_id) WHERE customer_id=sqlc.arg(merged_id);

-- name: UpdateAdjustedCard :exec
UPDATE cards SET expires_on=$2,card_type_id=$3 WHERE id=$1;

-- name: AddCardAdjustment :one
INSERT INTO card_adjustments (tenant_id,store_id,card_id,reason,expires_before,expires_after,expiry_changed,card_type_before,card_type_after,ledger_id,business_date,actor_id,counts_changed)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING *;

-- name: ListCardAdjustments :many
SELECT sqlc.embed(a), s.name AS actor_name, tb.name AS type_before_name, ta.name AS type_after_name,
 COALESCE(l.principal_before,0) AS principal_before, COALESCE(l.principal_delta,0) AS principal_delta, COALESCE(l.principal_after,0) AS principal_after,
 COALESCE(l.bonus_before,0) AS bonus_before, COALESCE(l.bonus_delta,0) AS bonus_delta, COALESCE(l.bonus_after,0) AS bonus_after
FROM card_adjustments a JOIN staff s ON s.id=a.actor_id
LEFT JOIN card_types tb ON tb.id=a.card_type_before
LEFT JOIN card_types ta ON ta.id=a.card_type_after
LEFT JOIN card_ledger l ON l.id=a.ledger_id
WHERE a.card_id=$1 AND (sqlc.narg(before_id)::uuid IS NULL OR (a.at,a.id) < (SELECT at,id FROM card_adjustments WHERE id=sqlc.narg(before_id) AND card_id=$1))
ORDER BY a.at DESC,a.id DESC LIMIT sqlc.arg(row_limit);

-- name: ListCardTypeItems :many
SELECT sqlc.embed(ci), i.name AS item_name FROM card_type_items ci JOIN items i ON i.id=ci.item_id
WHERE ci.card_type_id=ANY(sqlc.arg(type_ids)::uuid[]) ORDER BY ci.card_type_id,ci.item_id;

-- name: ShareCatalogItem :one
SELECT * FROM items WHERE store_id=$1 AND id=$2 FOR SHARE;

-- name: CardTypeHasCards :one
SELECT EXISTS(SELECT 1 FROM cards WHERE card_type_id=$1);

-- name: AddCardTypeItem :exec
INSERT INTO card_type_items (tenant_id,store_id,card_type_id,item_id,paid_count,gift_count,unit_price) VALUES ($1,$2,$3,$4,$5,$6,$7);

-- name: UpdateCardTypeItem :exec
UPDATE card_type_items SET paid_count=$3,gift_count=$4,unit_price=$5 WHERE card_type_id=$1 AND item_id=$2;

-- name: DeleteCardTypeItem :exec
DELETE FROM card_type_items WHERE card_type_id=$1 AND item_id=$2;

-- name: AddCardItem :exec
INSERT INTO card_items (tenant_id,store_id,card_id,item_id,unit_price,paid_count_opened,gift_count_opened) VALUES ($1,$2,$3,$4,$5,$6,$7);

-- name: AddCardCountEntry :exec
INSERT INTO card_count_ledger (tenant_id,store_id,card_id,item_id,kind,paid_before,paid_delta,paid_after,gift_before,gift_delta,gift_after,adjustment_id,business_date,actor_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14);

-- name: RemainingCounts :many
SELECT sqlc.embed(ci), i.name AS item_name, l.paid_after, l.gift_after FROM card_items ci JOIN items i ON i.id=ci.item_id
JOIN LATERAL (SELECT paid_after,gift_after FROM card_count_ledger WHERE card_id=ci.card_id AND item_id=ci.item_id ORDER BY at DESC,id DESC LIMIT 1) l ON true
WHERE ci.card_id=ANY(sqlc.arg(card_ids)::uuid[]) ORDER BY ci.card_id,ci.item_id;

-- name: ListCardCounts :many
SELECT sqlc.embed(l), i.name AS item_name, s.name AS actor_name, COALESCE(a.reason,'')::text AS reason
FROM card_count_ledger l JOIN items i ON i.id=l.item_id JOIN staff s ON s.id=l.actor_id LEFT JOIN card_adjustments a ON a.id=l.adjustment_id
WHERE l.card_id=$1 AND (sqlc.narg(item_id)::uuid IS NULL OR l.item_id=sqlc.narg(item_id))
AND (sqlc.narg(before_id)::uuid IS NULL OR (l.at,l.id) < (SELECT at,id FROM card_count_ledger WHERE id=sqlc.narg(before_id) AND card_id=$1))
ORDER BY l.at DESC,l.id DESC LIMIT sqlc.arg(row_limit);

-- name: AdjustmentCounts :many
SELECT sqlc.embed(l), i.name AS item_name FROM card_count_ledger l JOIN items i ON i.id=l.item_id
WHERE l.adjustment_id=ANY(sqlc.arg(adjustment_ids)::uuid[]) ORDER BY l.adjustment_id,l.item_id;
