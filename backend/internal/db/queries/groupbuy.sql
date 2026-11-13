-- name: LockCouponIdentity :exec
-- 同一门店的平台券先排队，再检查是否已有未结束的核销。
SELECT pg_advisory_xact_lock(hashtextextended((@store_id::uuid)::text || '/coupon/' || length(@platform::text)::text || ':' || @platform::text || @coupon_code::text, 0));

-- name: HasOtherActiveRedemption :one
SELECT EXISTS (
  SELECT 1 FROM coupon_redemptions
  WHERE store_id = @store_id AND platform = @platform AND coupon_code = @coupon_code
    AND idempotency_key <> @idempotency_key AND status IN ('pending', 'unknown', 'redeemed')
);

-- name: CreateRedemption :execrows
INSERT INTO coupon_redemptions (tenant_id, store_id, platform, coupon_code, idempotency_key, status, redeemed_by, created_at)
VALUES (@tenant_id, @store_id, @platform, @coupon_code, @idempotency_key, 'pending', @redeemed_by, @created_at)
ON CONFLICT (tenant_id, store_id, idempotency_key) DO NOTHING;

-- name: LockRedemption :one
SELECT * FROM coupon_redemptions WHERE store_id = @store_id AND idempotency_key = @idempotency_key FOR NO KEY UPDATE;

-- name: GetRedemption :one
SELECT * FROM coupon_redemptions WHERE store_id = @store_id AND idempotency_key = @idempotency_key;

-- name: UpdateRedemption :one
UPDATE coupon_redemptions SET status = @status, redeemed_at = @redeemed_at, revoked_at = @revoked_at
WHERE store_id = @store_id AND idempotency_key = @idempotency_key RETURNING *;

-- name: CreateGroupbuyDeal :one
INSERT INTO groupbuy_deals (tenant_id, store_id, platform, name, purchase_price, item_id, active, created_by)
VALUES ($1,$2,$3,$4,$5,$6,true,$7) RETURNING *;

-- name: GetGroupbuyDeal :one
SELECT * FROM groupbuy_deals WHERE store_id = $1 AND id = $2;

-- name: LockGroupbuyDeal :one
SELECT * FROM groupbuy_deals WHERE store_id = $1 AND id = $2 FOR NO KEY UPDATE;

-- name: UpdateGroupbuyDeal :one
UPDATE groupbuy_deals
SET name = @name, purchase_price = @purchase_price, item_id = @item_id, active = @active
WHERE store_id = @store_id AND id = @id RETURNING *;

-- name: ListGroupbuyDeals :many
SELECT d.*, i.name AS item_name, s.name AS created_by_name
FROM groupbuy_deals d
JOIN items i ON i.tenant_id = d.tenant_id AND i.id = d.item_id
JOIN staff s ON s.tenant_id = d.tenant_id AND s.id = d.created_by
WHERE d.store_id = @store_id
ORDER BY d.active DESC, d.platform, d.name, d.id;

-- name: GetRedemptionByID :one
SELECT * FROM coupon_redemptions WHERE store_id = $1 AND id = $2;

-- name: LockRedemptionByID :one
SELECT * FROM coupon_redemptions WHERE store_id = $1 AND id = $2 FOR NO KEY UPDATE;

-- name: LinkRedemption :one
UPDATE coupon_redemptions
SET order_id = @order_id, deal_id = @deal_id, purchase_price = @purchase_price,
    store_price = @store_price, linked_at = @linked_at, order_payment_id = NULL
WHERE store_id = @store_id AND id = @id RETURNING *;

-- name: UnlinkRedemption :one
UPDATE coupon_redemptions
SET order_id = NULL, linked_at = NULL, order_payment_id = NULL
WHERE store_id = @store_id AND id = @id RETURNING *;

-- name: SetRedemptionPayment :exec
UPDATE coupon_redemptions SET order_payment_id = @order_payment_id
WHERE store_id = @store_id AND id = @id;

-- name: ReleaseRedemption :one
UPDATE coupon_redemptions
SET order_id = NULL, linked_at = NULL, order_payment_id = NULL
WHERE store_id = @store_id AND id = @id RETURNING *;

-- name: RevokeRedemption :one
UPDATE coupon_redemptions
SET status = 'revoked', revoked_at = @revoked_at, order_id = NULL, linked_at = NULL, order_payment_id = NULL
WHERE store_id = @store_id AND id = @id RETURNING *;

-- name: ListOrderRedemptions :many
SELECT r.*, d.name AS deal_name
FROM coupon_redemptions r
JOIN groupbuy_deals d ON d.tenant_id = r.tenant_id AND d.id = r.deal_id
WHERE r.store_id = @store_id AND r.order_id = ANY(sqlc.arg(order_ids)::uuid[])
  AND r.deal_id IS NOT NULL AND r.status = 'redeemed'
ORDER BY r.order_id, r.linked_at, r.id;

-- name: LockOrderRedemptions :many
SELECT r.*, d.name AS deal_name
FROM coupon_redemptions r
JOIN groupbuy_deals d ON d.tenant_id = r.tenant_id AND d.id = r.deal_id
WHERE r.store_id = @store_id AND r.order_id = ANY(sqlc.arg(order_ids)::uuid[])
  AND r.deal_id IS NOT NULL AND r.status = 'redeemed'
ORDER BY r.order_id, r.linked_at, r.id
FOR NO KEY UPDATE OF r;

-- name: ListMonthRedemptions :many
SELECT r.id, r.platform, r.status, r.redeemed_at, r.order_id, r.purchase_price, r.store_price,
       o.status AS order_status
FROM coupon_redemptions r
LEFT JOIN orders o ON o.tenant_id = r.tenant_id AND o.id = r.order_id
WHERE r.store_id = @store_id AND r.platform = @platform
  AND r.status IN ('redeemed','revoked')
  AND r.redeemed_at >= @from_time AND r.redeemed_at < @to_time
ORDER BY r.redeemed_at, r.id;

-- name: CreateGroupbuySettlement :one
INSERT INTO groupbuy_settlements (tenant_id, store_id, platform, month, amount, note, entered_by)
VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING *;

-- name: ListGroupbuySettlements :many
SELECT s.*, st.name AS entered_by_name
FROM groupbuy_settlements s
JOIN staff st ON st.tenant_id = s.tenant_id AND st.id = s.entered_by
WHERE s.store_id = @store_id AND s.platform = @platform AND s.month = @month
ORDER BY s.created_at DESC, s.id DESC;

-- name: MoveOrderRedemptions :exec
UPDATE coupon_redemptions SET order_id = sqlc.arg(order_id)
WHERE store_id = sqlc.arg(store_id) AND order_id = ANY(sqlc.arg(merged_order_ids)::uuid[]) AND status = 'redeemed';
