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
	entries, err := checkoutEntries(ctx, env, in.OrderID, in.Lines, in.Rules)
	if err != nil {
		return err
	}
	for _, e := range entries {
		err := env.Q.AddPerformanceEntry(ctx, dbgen.AddPerformanceEntryParams{
			TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID,
			BusinessDate: pgtype.Date{Time: in.BusinessDate, Valid: true},
			OrderID:      &in.OrderID, OrderItemID: &e.orderItemID, CheckoutID: &in.CheckoutID, SettingsSnapshotID: &in.SettingsSnapshotID,
			Slot: e.slot, PositionID: e.positionID, StaffID: e.staffID,
			Member: &in.Member, Kind: e.kind, Method: e.method, PaidAmount: e.paidAmount, CommissionPaidAmount: e.commissionPaidAmount,
			Ratio: e.ratio, CategoryRatio: e.categoryRatio, Amount: e.amount, CommissionAmount: e.commissionAmount,
		})
		if err != nil {
			return err
		}
	}
	return recordGuestVisits(ctx, env, in)
}

// checkoutEntries 按结账单已登记的支付和规则快照计算项目业绩、耗卡业绩和手工费。
func checkoutEntries(ctx context.Context, env ops.Env, orderID uuid.UUID, lines []Line, rules settings.Rules) ([]allocation, error) {
	recordedPayments, err := env.Q.ListOrderPayments(ctx, dbgen.ListOrderPaymentsParams{StoreID: env.Caller.StoreID, OrderIds: []uuid.UUID{orderID}})
	if err != nil {
		return nil, err
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
	allocationLines := make([]allocationLine, 0, len(lines))
	for _, line := range lines {
		allocationLines = append(allocationLines, allocationLine{Line: line, Count: consumptions[line.OrderItemID]})
	}
	ratios := map[string]int{}
	for _, method := range paymentMethods {
		ratios[method.name] = rules.PerformanceRatio(method.ratioKey)
	}
	split, commissionSplit := rules.PerformanceSplit(), rules.CommissionSplit()
	entries := allocate(allocationLines, categories, split, commissionSplit, ratios)
	return append(entries, consumeAndLabor(allocationLines, split, commissionSplit, ratios)...), nil
}

// ReverseOrder 在撤单已持有单锁的事务内追加尚未冲回的原业绩的反向记录。
func ReverseOrder(ctx context.Context, env ops.Env, orderID, voidID uuid.UUID, day time.Time) error {
	if err := env.Q.ReversePerformanceOrder(ctx, dbgen.ReversePerformanceOrderParams{StoreID: env.Caller.StoreID, OrderID: &orderID, VoidID: &voidID, Day: pgtype.Date{Time: day, Valid: true}}); err != nil {
		return err
	}
	return env.Q.ReverseGuestVisits(ctx, dbgen.ReverseGuestVisitsParams{StoreID: env.Caller.StoreID, OrderID: orderID, VoidID: &voidID, Day: pgtype.Date{Time: day, Valid: true}})
}
