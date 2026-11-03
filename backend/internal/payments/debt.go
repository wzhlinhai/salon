package payments

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/debts"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

func recordDebt(ctx context.Context, env ops.Env, order dbgen.Order, paymentID uuid.UUID, payment Input, day time.Time) error {
	return debts.RecordOrderDebt(ctx, env, order.CustomerID, order.ID, paymentID, payment.Amount, day)
}
func reverseDebt(ctx context.Context, env ops.Env, orderID uuid.UUID) error {
	// 欠款状态随原单作废，不另写冲回记录。
	return debts.LockOrderForVoid(ctx, env, orderID)
}
