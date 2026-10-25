-- name: ListItems :many
SELECT * FROM items WHERE store_id = $1 ORDER BY active DESC, created_at, id;

-- name: GetItem :one
SELECT * FROM items WHERE store_id = $1 AND id = $2;

-- name: LockItem :one
-- 改名时 PostgreSQL 会在 UPDATE 执行时自行取得键锁。
SELECT * FROM items WHERE store_id = $1 AND id = $2 FOR NO KEY UPDATE;

-- name: CreateItem :one
INSERT INTO items (tenant_id, store_id, name, price, discountable, labor_fee_amount, labor_fee_percent, kind)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING *;

-- name: UpdateItem :one
UPDATE items SET name=$2, price=$3, discountable=$4, labor_fee_amount=$5, labor_fee_percent=$6, active=$7, kind=$8
WHERE id=$1 RETURNING *;

-- name: ListItemLevelPrices :many
SELECT * FROM item_level_prices WHERE store_id=$1 AND item_id = ANY(sqlc.arg(item_ids)::uuid[]) ORDER BY item_id, level_id;

-- name: ListItemPositions :many
SELECT * FROM item_positions WHERE store_id=$1 AND item_id = ANY(sqlc.arg(item_ids)::uuid[]) ORDER BY item_id, slot;

-- name: AddItemLevelPrice :exec
INSERT INTO item_level_prices (tenant_id,store_id,item_id,level_id,price) VALUES ($1,$2,$3,$4,$5);

-- name: UpdateItemLevelPrice :exec
UPDATE item_level_prices SET price=$3 WHERE item_id=$1 AND level_id=$2;

-- name: DeleteItemLevelPrice :exec
DELETE FROM item_level_prices WHERE item_id=$1 AND level_id=$2;

-- name: AddItemPosition :exec
INSERT INTO item_positions (tenant_id,store_id,item_id,slot,position_id,required) VALUES ($1,$2,$3,$4,$5,$6);

-- name: UpdateItemPosition :exec
UPDATE item_positions SET position_id=$3, required=$4 WHERE item_id=$1 AND slot=$2;

-- name: DeleteItemPosition :exec
DELETE FROM item_positions WHERE item_id=$1 AND slot=$2;

-- name: GetItemLevelPrice :one
SELECT * FROM item_level_prices WHERE item_id=$1 AND level_id=$2;

-- name: ListItemMemberPrices :many
SELECT * FROM item_member_prices WHERE store_id=$1 AND item_id=ANY(sqlc.arg(item_ids)::uuid[]) ORDER BY item_id,card_type_id;

-- name: GetItemMemberPrice :one
SELECT * FROM item_member_prices WHERE item_id=$1 AND card_type_id=$2;

-- name: AddItemMemberPrice :exec
INSERT INTO item_member_prices (tenant_id,store_id,item_id,card_type_id,price) VALUES ($1,$2,$3,$4,$5);

-- name: UpdateItemMemberPrice :exec
UPDATE item_member_prices SET price=$3 WHERE item_id=$1 AND card_type_id=$2;

-- name: DeleteItemMemberPrice :exec
DELETE FROM item_member_prices WHERE item_id=$1 AND card_type_id=$2;

-- name: ShareItem :one
SELECT * FROM items WHERE store_id=$1 AND id=$2 FOR SHARE;

-- name: GetItems :many
SELECT * FROM items WHERE store_id=sqlc.arg(store_id) AND id=ANY(sqlc.arg(ids)::uuid[]);
