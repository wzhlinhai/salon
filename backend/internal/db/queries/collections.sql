-- name: CreateCollection :one
INSERT INTO collections (tenant_id,store_id,order_id,order_payment_id,channel,amount,status,external_ref,created_by,created_at,updated_at)
VALUES ($1,$2,$3,$4,$5,$6,'pending',$7,$8,$9,$9) RETURNING *;

-- name: GetCollection :one
SELECT * FROM collections WHERE store_id=sqlc.arg(store_id) AND id=sqlc.arg(id);

-- name: LockCollection :one
SELECT * FROM collections WHERE store_id=sqlc.arg(store_id) AND id=sqlc.arg(id) FOR NO KEY UPDATE;

-- name: ListCollections :many
SELECT * FROM collections WHERE store_id=sqlc.arg(store_id) AND order_id=sqlc.arg(order_id) ORDER BY id;

-- name: UpdateCollection :one
UPDATE collections SET status=$3,updated_at=$4,external_ref=$5 WHERE store_id=$1 AND id=$2 RETURNING *;

-- name: AddCollectionEvent :exec
INSERT INTO collection_events (tenant_id,store_id,collection_id,from_status,to_status,actor_id,at,reason)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8);

-- name: ReconcileCollections :many
SELECT * FROM collections WHERE store_id=sqlc.arg(store_id) AND channel=sqlc.arg(channel);
