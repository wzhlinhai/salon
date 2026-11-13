package aftersales

import (
	"context"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

func mulDiv(a, b, n int64) int64 {
	var product, denominator big.Int
	product.Mul(big.NewInt(a), big.NewInt(b))
	denominator.SetInt64(n)
	return product.Quo(&product, &denominator).Int64()
}

// deductPerformance 按累计退款占原单可退金额的比例冲回项目业绩，耗卡和手工费不冲回。
func deductPerformance(ctx context.Context, env ops.Env, orderID, aftersaleID uuid.UUID, day time.Time, refundTotal money.Total, state *refundState) error {
	if refundTotal.CmpTotal(money.Total{}) == 0 || state.baseFen == 0 {
		return nil
	}
	entries, err := env.Q.OrderItemPerformanceEntries(ctx, dbgen.OrderItemPerformanceEntriesParams{StoreID: env.Caller.StoreID, OrderID: &orderID})
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	prior, err := env.Q.PriorAftersalePerformanceReversals(ctx, dbgen.PriorAftersalePerformanceReversalsParams{StoreID: env.Caller.StoreID, OrderID: &orderID})
	if err != nil {
		return err
	}
	priorPaid := map[uuid.UUID]int64{}
	priorAmount := map[uuid.UUID]int64{}
	for _, row := range prior {
		if row.ReversesID == nil {
			continue
		}
		paid, err := parseAmount(row.Paid)
		if err != nil {
			return err
		}
		amount, err := parseAmount(row.Amount)
		if err != nil {
			return err
		}
		priorPaid[*row.ReversesID] += paid.Fen()
		priorAmount[*row.ReversesID] += amount.Fen()
	}
	cumulative := state.approved.AddTotal(refundTotal)
	cumulativeFen := cumulative.Fen()
	if cumulativeFen > state.baseFen {
		cumulativeFen = state.baseFen
	}
	for _, entry := range entries {
		targetPaidFen := mulDiv(entry.PaidAmount.Fen(), cumulativeFen, state.baseFen)
		if targetPaidFen > entry.PaidAmount.Fen() {
			targetPaidFen = entry.PaidAmount.Fen()
		}
		targetPaid, err := money.FromFen(targetPaidFen)
		if err != nil {
			return err
		}
		targetAmountFen := targetPaid.PercentOf(int(entry.Ratio)).Fen()
		deltaPaid := targetPaidFen - priorPaid[entry.ID]
		deltaAmount := targetAmountFen - priorAmount[entry.ID]
		if deltaPaid <= 0 {
			continue
		}
		if deltaAmount < 0 {
			deltaAmount = 0
		}
		if deltaAmount > deltaPaid {
			deltaAmount = deltaPaid
		}
		paid, err := money.FromFen(deltaPaid)
		if err != nil {
			return err
		}
		amount, err := money.FromFen(deltaAmount)
		if err != nil {
			return err
		}
		if err := env.Q.AddAftersalePerformanceEntry(ctx, dbgen.AddAftersalePerformanceEntryParams{
			TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID,
			BusinessDate: pgtype.Date{Time: day, Valid: true},
			OrderID:      entry.OrderID, OrderItemID: entry.OrderItemID, CheckoutID: entry.CheckoutID, SettingsSnapshotID: entry.SettingsSnapshotID,
			Slot: entry.Slot, PositionID: entry.PositionID, StaffID: entry.StaffID, Method: entry.Method,
			PaidAmount: paid, Ratio: entry.Ratio, Amount: amount,
			ReversesID: &entry.ID, AftersaleID: &aftersaleID, Member: entry.Member,
		}); err != nil {
			return err
		}
	}
	return nil
}
