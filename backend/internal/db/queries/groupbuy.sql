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
