-- name: ListItems :many
SELECT * FROM items WHERE store_id = $1 ORDER BY active DESC, created_at, id;

-- name: GetItem :one
SELECT * FROM items WHERE store_id = $1 AND id = $2;

-- name: LockItem :one
-- 改名时 PostgreSQL 会在 UPDATE 执行时自行取得键锁。
SELECT * FROM items WHERE store_id = $1 AND id = $2 FOR NO KEY UPDATE;

-- name: CreateItem :one
INSERT INTO items (tenant_id, store_id, name, price, discountable, labor_fee_amount, labor_fee_percent, kind, duration_minutes, bookable, category_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING *;

-- name: UpdateItem :one
UPDATE items SET name=$2, price=$3, discountable=$4, labor_fee_amount=$5, labor_fee_percent=$6, active=$7, kind=$8, duration_minutes=$9, bookable=$10, category_id=$11
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
INSERT INTO item_positions (tenant_id,store_id,item_id,slot,position_id,required,share_percent) VALUES ($1,$2,$3,$4,$5,$6,$7);

-- name: UpdateItemPosition :exec
UPDATE item_positions SET position_id=$3, required=$4, share_percent=$5 WHERE item_id=$1 AND slot=$2;

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

-- name: ListBookableItems :many
SELECT id, name, duration_minutes FROM items
WHERE store_id = $1 AND active AND bookable
ORDER BY name, id;

-- name: ListItemCategories :many
SELECT * FROM item_categories WHERE store_id = $1 ORDER BY active DESC, name, id;

-- name: GetItemCategories :many
SELECT * FROM item_categories WHERE store_id = $1 AND id = ANY(sqlc.arg(ids)::uuid[]);

-- name: ShareItemCategory :one
SELECT * FROM item_categories WHERE store_id = $1 AND id = $2 FOR SHARE;

-- name: CreateItemCategory :one
INSERT INTO item_categories (tenant_id, store_id, name, performance_ratio, active)
VALUES ($1, $2, $3, $4, $5) RETURNING *;

-- name: GetItemCategory :one
SELECT * FROM item_categories WHERE store_id = $1 AND id = $2;

-- name: LockItemCategory :one
SELECT * FROM item_categories WHERE store_id = $1 AND id = $2 FOR NO KEY UPDATE;

-- name: UpdateItemCategory :one
UPDATE item_categories SET name = $2, performance_ratio = $3, active = $4
WHERE store_id = $1 AND id = $5 RETURNING *;
