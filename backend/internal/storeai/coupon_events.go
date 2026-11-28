package storeai

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
)

// EmitCouponUnlinked 写一条团购券取消关联事件。关联撤单释放券和 unlink_coupon_order 共用它，
// 保证两处的 payload 和 source_key 一致。
func EmitCouponUnlinked(ctx context.Context, q *dbgen.Queries, tenantID, storeID uuid.UUID, now time.Time, redemptionID, orderID uuid.UUID) error {
	inbox, err := EnsureInbox(ctx, q, storeID, nil)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{
		"redemption_id": redemptionID,
		"order_id":      orderID,
		"unlinked_at":   now,
	})
	if err != nil {
		return err
	}
	_, err = Emit(ctx, q, now, EmitInput{
		TenantID: tenantID, StoreID: storeID, InboxID: inbox.ID,
		Kind:      EventCouponUnlinked,
		SourceKey: fmt.Sprintf("coupon_unlinked:%s:%d", redemptionID, now.UnixNano()),
		Subject:   Subject{Kind: SubjectCouponRedemption, ID: redemptionID}, Payload: payload,
	})
	return err
}
