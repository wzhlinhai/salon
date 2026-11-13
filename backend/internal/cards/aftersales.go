package cards

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

// AddAftersaleRefund 在调用方已锁卡的事务里把售后退款加回卡本金。
func AddAftersaleRefund(ctx context.Context, env ops.Env, cardID uuid.UUID, principal, bonus, amount money.Amount, day time.Time, aftersaleID uuid.UUID) (dbgen.CardLedger, error) {
	after, err := principal.Apply(amount.Sub(money.Amount{}))
	if err != nil {
		return dbgen.CardLedger{}, ops.Rule("卡余额超过上限")
	}
	return env.Q.AddAftersaleCardLedger(ctx, dbgen.AddAftersaleCardLedgerParams{
		TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CardID: cardID,
		PrincipalBefore: principal, PrincipalDelta: amount.Sub(money.Amount{}), PrincipalAfter: after,
		BonusBefore: bonus, BonusDelta: money.Amount{}.Sub(money.Amount{}), BonusAfter: bonus,
		BusinessDate: pgtype.Date{Time: day, Valid: true}, ActorID: env.Caller.StaffID, AftersaleID: &aftersaleID,
	})
}
