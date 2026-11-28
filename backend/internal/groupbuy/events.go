package groupbuy

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/storeai"
)

func revocableUntil(platform string, redeemedAt time.Time) *time.Time {
	if platform != "douyin" {
		return nil
	}
	until := redeemedAt.Add(storeai.DouyinRevokeWindow)
	return &until
}

func couponSubject(id uuid.UUID) storeai.Subject {
	return storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: id}
}

func emitCouponEvent(ctx context.Context, q *dbgen.Queries, tenantID, storeID uuid.UUID, now time.Time, kind, sourceKey string, id uuid.UUID, payload map[string]any) error {
	inbox, err := storeai.EnsureInbox(ctx, q, storeID, nil)
	if err != nil {
		return err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = storeai.Emit(ctx, q, now, storeai.EmitInput{
		TenantID: tenantID, StoreID: storeID, InboxID: inbox.ID,
		Kind: kind, SourceKey: sourceKey, Subject: couponSubject(id), Payload: body,
	})
	return err
}

func emitCouponRedeemed(ctx context.Context, q *dbgen.Queries, now time.Time, row dbgen.CouponRedemption) error {
	payload := map[string]any{
		"redemption_id":   row.ID,
		"platform":        row.Platform,
		"redeemed_by":     row.RedeemedBy,
		"redeemed_at":     row.RedeemedAt.Time,
		"revocable_until": revocableUntil(row.Platform, row.RedeemedAt.Time),
	}
	return emitCouponEvent(ctx, q, row.TenantID, row.StoreID, now, storeai.EventCouponRedeemed,
		"coupon_redeemed:"+row.ID.String(), row.ID, payload)
}

func emitCouponRevoked(ctx context.Context, q *dbgen.Queries, now time.Time, row dbgen.CouponRedemption) error {
	if err := storeai.ResolveSubject(ctx, q, now, row.StoreID, couponSubject(row.ID)); err != nil {
		return err
	}
	payload := map[string]any{
		"redemption_id": row.ID,
		"revoked_at":    row.RevokedAt.Time,
	}
	return emitCouponEvent(ctx, q, row.TenantID, row.StoreID, now, storeai.EventCouponRevoked,
		"coupon_revoked:"+row.ID.String(), row.ID, payload)
}

func emitCouponLinked(ctx context.Context, q *dbgen.Queries, now time.Time, row dbgen.CouponRedemption) error {
	if err := storeai.ResolveSubject(ctx, q, now, row.StoreID, couponSubject(row.ID)); err != nil {
		return err
	}
	payload := map[string]any{
		"redemption_id": row.ID,
		"order_id":      row.OrderID,
		"linked_at":     row.LinkedAt.Time,
	}
	return emitCouponEvent(ctx, q, row.TenantID, row.StoreID, now, storeai.EventCouponLinked,
		fmt.Sprintf("coupon_linked:%s:%d", row.ID, row.LinkedAt.Time.UnixNano()), row.ID, payload)
}

func emitCouponUnlinked(ctx context.Context, q *dbgen.Queries, tenantID, storeID uuid.UUID, now time.Time, id, orderID uuid.UUID) error {
	return storeai.EmitCouponUnlinked(ctx, q, tenantID, storeID, now, id, orderID)
}
