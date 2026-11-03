package performance

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// CheckoutInput 是结账事务内按单提供的业绩来源和规则快照。
type CheckoutInput struct {
	CheckoutID         uuid.UUID
	BusinessDate       time.Time
	OrderID            uuid.UUID
	SettingsSnapshotID uuid.UUID
	Rules              settings.Rules
	Lines              []Line
	Member             bool
	Designated         bool
	Gender             string
}

// RecordCheckout 在结账已持有单锁的事务内追加项目业绩、耗卡业绩和手工费。
func RecordCheckout(ctx context.Context, env ops.Env, in CheckoutInput) error {
	recordedPayments, err := env.Q.ListOrderPayments(ctx, dbgen.ListOrderPaymentsParams{StoreID: env.Caller.StoreID, OrderIds: []uuid.UUID{in.OrderID}})
	if err != nil {
		return err
	}
	consumptions := map[uuid.UUID]*consumption{}
	categories := []category{}
	for _, p := range recordedPayments {
		switch p.Method {
		case "count":
			method := "count_paid"
			if p.Gift > 0 {
				method = "count_gift"
			}
			consumptions[*p.OrderItemID] = &consumption{Method: method, UnitPrice: *p.CountUnitPrice}
		case "card":
			categories = append(categories, category{Method: "card_principal", Amount: *p.Principal}, category{Method: "card_bonus", Amount: *p.Bonus})
		default:
			categories = append(categories, category{Method: p.Method, Amount: p.Amount})
		}
	}
	lines := make([]allocationLine, 0, len(in.Lines))
	for _, line := range in.Lines {
		lines = append(lines, allocationLine{Line: line, Count: consumptions[line.OrderItemID]})
	}
	ratios := map[string]int{}
	for _, method := range paymentMethods {
		ratios[method.name] = in.Rules.PerformanceRatio(method.ratioKey)
	}
	entries := allocate(lines, categories, in.Rules.PerformanceSplit(), ratios)
	entries = append(entries, consumeAndLabor(lines, in.Rules.PerformanceSplit(), ratios)...)
	for _, e := range entries {
		err := env.Q.AddPerformanceEntry(ctx, dbgen.AddPerformanceEntryParams{
			TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID,
			BusinessDate: pgtype.Date{Time: in.BusinessDate, Valid: true},
			OrderID:      &in.OrderID, OrderItemID: &e.orderItemID, CheckoutID: &in.CheckoutID, SettingsSnapshotID: &in.SettingsSnapshotID,
			Slot: e.slot, PositionID: e.positionID, StaffID: e.staffID,
			Member: &in.Member, Kind: e.kind, Method: e.method, PaidAmount: e.paidAmount, Ratio: e.ratio, Amount: e.amount,
		})
		if err != nil {
			return err
		}
	}
	return recordGuestVisits(ctx, env, in)
}

// ReverseOrder 在撤单已持有单锁的事务内追加尚未冲回的原业绩的反向记录。
func ReverseOrder(ctx context.Context, env ops.Env, orderID, voidID uuid.UUID, day time.Time) error {
	if err := env.Q.ReversePerformanceOrder(ctx, dbgen.ReversePerformanceOrderParams{StoreID: env.Caller.StoreID, OrderID: &orderID, VoidID: &voidID, Day: pgtype.Date{Time: day, Valid: true}}); err != nil {
		return err
	}
	return env.Q.ReverseGuestVisits(ctx, dbgen.ReverseGuestVisitsParams{StoreID: env.Caller.StoreID, OrderID: orderID, VoidID: &voidID, Day: pgtype.Date{Time: day, Valid: true}})
}
