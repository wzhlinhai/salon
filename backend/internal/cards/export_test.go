package cards

import (
	"context"
	"time"

	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

// OpenCardAtForTest 让接口测试在固定时刻执行同一开卡处理函数。
var OpenCardAtForTest = openCardAt

// RegisterRefundAtForTest 让 HTTP 测试以固定时刻执行退卡预览和确认。
func RegisterRefundAtForTest(r *ops.Registry, now func() time.Time) {
	ops.Register(r, ops.Operation[PreviewCardRefundInput, CardRefundQuote]{Name: "test_preview_card_refund", Kind: ops.Read, Permission: perm.CardAdjust, AI: ops.AIDirect, Handle: func(ctx context.Context, env ops.Env, in *PreviewCardRefundInput) (*CardRefundQuote, error) {
		return previewCardRefundAt(ctx, env, in, now())
	}})
	ops.Register(r, ops.Operation[RefundCardInput, CardRefund]{Name: "test_refund_card", Kind: ops.Write, Permission: perm.CardAdjust, AI: ops.AIDraft, Handle: func(ctx context.Context, env ops.Env, in *RefundCardInput) (*CardRefund, error) {
		return refundCardAt(ctx, env, in, now())
	}})
}
