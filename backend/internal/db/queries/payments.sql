-- name: VoidBalanceConsumes :many
SELECT l.* FROM card_ledger l JOIN order_payments p ON p.card_ledger_id=l.id WHERE p.store_id=$1 AND p.order_id=$2 ORDER BY p.id;

-- name: VoidCountConsumes :many
SELECT l.* FROM card_count_ledger l JOIN order_payments p ON p.card_count_ledger_id=l.id WHERE p.store_id=$1 AND p.order_id=$2 ORDER BY p.id;

-- name: CreateOrderPayment :one
INSERT INTO order_payments (tenant_id,store_id,checkout_id,order_id,method,amount,card_id,card_ledger_id,order_item_id,card_count_ledger_id,red_packet_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id;
