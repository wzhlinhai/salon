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
