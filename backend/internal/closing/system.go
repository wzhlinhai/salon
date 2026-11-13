package closing

import (
	"context"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/collections"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/paymethod"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

type amounts map[paymethod.Code]money.SignedTotal

func (a amounts) add(code paymethod.Code, value string, sign int) error {
	var delta money.Change
	if err := delta.Scan(value); err != nil {
		return err
	}
	if sign < 0 {
		delta = delta.Negate()
	}
	a[code] = a[code].Add(delta)
	return nil
}

func systemFigures(ctx context.Context, env ops.Env, day time.Time) (*SystemFigures, error) {
	q := env.Q
	storeID := env.Caller.StoreID
	values := amounts{}
	card := amounts{}

	checkout, err := q.CheckoutPaymentsByMethod(ctx, dbgen.CheckoutPaymentsByMethodParams{StoreID: storeID, Day: pgDate(day)})
	if err != nil {
		return nil, err
	}
	for _, row := range checkout {
		if err := values.add(paymethod.Code(row.Method), row.Amount, 1); err != nil {
			return nil, err
		}
	}
	split, err := q.CheckoutCardSplit(ctx, dbgen.CheckoutCardSplitParams{StoreID: storeID, Day: pgDate(day)})
	if err != nil {
		return nil, err
	}
	if err := card.add(paymethod.CardPrincipal, split.Principal, 1); err != nil {
		return nil, err
	}
	if err := card.add(paymethod.CardBonus, split.Bonus, 1); err != nil {
		return nil, err
	}

	repayments, err := q.DebtRepaymentsByMethod(ctx, dbgen.DebtRepaymentsByMethodParams{StoreID: storeID, Day: pgDate(day)})
	if err != nil {
		return nil, err
	}
	for _, row := range repayments {
		if err := values.add(paymethod.Code(row.Method), row.Amount, 1); err != nil {
			return nil, err
		}
	}
	sales, err := q.SalePaymentsByMethod(ctx, dbgen.SalePaymentsByMethodParams{StoreID: storeID, Day: pgDate(day)})
	if err != nil {
		return nil, err
	}
	for _, row := range sales {
		if err := values.add(paymethod.Code(row.Method), row.Amount, 1); err != nil {
			return nil, err
		}
	}
	refunds, err := q.CardRefundsByMethod(ctx, dbgen.CardRefundsByMethodParams{StoreID: storeID, Day: pgDate(day)})
	if err != nil {
		return nil, err
	}
	for _, row := range refunds {
		if err := values.add(paymethod.Code(row.Method), row.Amount, -1); err != nil {
			return nil, err
		}
	}
	aftersales, err := q.AftersaleRefundsByMethod(ctx, dbgen.AftersaleRefundsByMethodParams{StoreID: storeID, Day: pgDate(day)})
	if err != nil {
		return nil, err
	}
	for _, row := range aftersales {
		if row.Method == string(paymethod.CardPrincipal) {
			if err := card.add(paymethod.CardPrincipal, row.Amount, -1); err != nil {
				return nil, err
			}
			continue
		}
		if err := values.add(paymethod.Code(row.Method), row.Amount, -1); err != nil {
			return nil, err
		}
	}
	voids, err := q.VoidPaymentsByMethod(ctx, dbgen.VoidPaymentsByMethodParams{StoreID: storeID, Day: pgDate(day)})
	if err != nil {
		return nil, err
	}
	for _, row := range voids {
		if err := values.add(paymethod.Code(row.Method), row.Amount, -1); err != nil {
			return nil, err
		}
	}
	voidCard, err := q.VoidCardSplit(ctx, dbgen.VoidCardSplitParams{StoreID: storeID, Day: pgDate(day)})
	if err != nil {
		return nil, err
	}
	if err := card.add(paymethod.CardPrincipal, voidCard.Principal, -1); err != nil {
		return nil, err
	}
	if err := card.add(paymethod.CardBonus, voidCard.Bonus, -1); err != nil {
		return nil, err
	}

	unrecorded, err := unrecordedSales(ctx, env, day)
	if err != nil {
		return nil, err
	}
	visitTotal, err := q.GuestVisitTotal(ctx, dbgen.GuestVisitTotalParams{StoreID: storeID, Day: pgDate(day)})
	if err != nil {
		return nil, err
	}
	checkouts, err := q.DayCheckoutCount(ctx, dbgen.DayCheckoutCountParams{StoreID: storeID, Day: pgDate(day)})
	if err != nil {
		return nil, err
	}
	voidCount, err := q.DayVoidCount(ctx, dbgen.DayVoidCountParams{StoreID: storeID, Day: pgDate(day)})
	if err != nil {
		return nil, err
	}
	summaries, err := collections.ReconcileAll(ctx, env, day)
	if err != nil {
		return nil, err
	}
	channels := make([]ChannelSummary, 0, len(summaries))
	for _, summary := range summaries {
		channels = append(channels, ChannelSummary{BusinessDate: summary.BusinessDate, Channel: summary.Channel, Amounts: summary.Amounts, Counts: summary.Counts})
	}

	out := &SystemFigures{BusinessDate: day.Format(time.DateOnly), Unrecorded: unrecorded, GuestVisits: visitTotal, CheckoutCount: checkouts, VoidCount: voidCount, Channels: channels}
	for _, method := range paymethod.OnSite() {
		out.OnSite = append(out.OnSite, MethodAmount{Method: string(method.Code), Label: method.Label, Amount: values[method.Code].Change()})
	}
	for _, method := range paymethod.NonSite() {
		amount := values[method.Code]
		if method.Code == paymethod.CardPrincipal || method.Code == paymethod.CardBonus {
			amount = card[method.Code]
		}
		out.NonSite = append(out.NonSite, MethodAmount{Method: string(method.Code), Label: method.Label, Amount: amount.Change()})
	}
	known := map[paymethod.Code]bool{}
	for _, m := range paymethod.All {
		known[m.Code] = true
	}
	extra := []paymethod.Code{}
	for code := range values {
		if !known[code] {
			extra = append(extra, code)
		}
	}
	sort.Slice(extra, func(i, j int) bool { return extra[i] < extra[j] })
	for _, code := range extra {
		out.NonSite = append(out.NonSite, MethodAmount{Method: string(code), Label: paymethod.Label(string(code)), Amount: values[code].Change()})
	}
	return out, nil
}

func hasBusiness(ctx context.Context, env ops.Env, day time.Time) (bool, error) {
	q, storeID := env.Q, env.Caller.StoreID
	has, err := q.DayHasBusiness(ctx, dbgen.DayHasBusinessParams{StoreID: storeID, Day: pgDate(day)})
	if err != nil {
		return false, err
	}
	if has != nil && *has {
		return true, nil
	}
	times := []time.Time{}
	explicit := []time.Time{}
	collections, err := q.CollectionCreatedAts(ctx, storeID)
	if err != nil {
		return false, err
	}
	for _, row := range collections {
		if row.BusinessDate.Valid {
			explicit = append(explicit, row.BusinessDate.Time)
		} else {
			times = append(times, row.CreatedAt.Time)
		}
	}
	events, err := q.CollectionEventAts(ctx, storeID)
	if err != nil {
		return false, err
	}
	for _, row := range events {
		if row.BusinessDate.Valid && row.Initial {
			continue
		}
		times = append(times, row.At.Time)
	}
	coupons, err := q.CouponRedemptionAts(ctx, storeID)
	if err != nil {
		return false, err
	}
	for _, at := range coupons {
		times = append(times, at.Time)
	}
	days, err := settings.BusinessDates(ctx, q, storeID, times)
	if err != nil {
		return false, err
	}
	days = append(days, explicit...)
	for _, d := range days {
		if d.Equal(day) {
			return true, nil
		}
	}
	return false, nil
}

func unrecordedSales(ctx context.Context, env ops.Env, day time.Time) (UnrecordedSales, error) {
	var out UnrecordedSales
	var total money.SignedTotal
	cards, err := env.Q.UnrecordedCardSales(ctx, dbgen.UnrecordedCardSalesParams{StoreID: env.Caller.StoreID, Day: pgDate(day)})
	if err != nil {
		return out, err
	}
	for _, row := range cards {
		var delta money.Change
		if err := delta.Scan(row.Amount); err != nil {
			return out, err
		}
		total = total.Add(delta)
		out.Count++
	}
	packets, err := env.Q.UnrecordedRedPacketSales(ctx, dbgen.UnrecordedRedPacketSalesParams{StoreID: env.Caller.StoreID, Day: pgDate(day)})
	if err != nil {
		return out, err
	}
	for _, row := range packets {
		var delta money.Change
		if err := delta.Scan(row.Amount); err != nil {
			return out, err
		}
		total = total.Add(delta)
		out.Count++
	}
	out.Amount = total.Change()
	return out, nil
}

func differences(system *SystemFigures, in *CloseDayInput) []MethodDifference {
	actual := map[string]money.Amount{
		string(paymethod.Cash): in.CashCounted, string(paymethod.Unionpay): in.UnionpayActual,
		string(paymethod.Wechat): in.WechatActual, string(paymethod.Alipay): in.AlipayActual,
	}
	out := []MethodDifference{}
	for _, line := range system.OnSite {
		a := actual[line.Method]
		out = append(out, MethodDifference{Method: line.Method, Label: line.Label, System: line.Amount, Actual: a, Difference: a.SubChange(line.Amount)})
	}
	return out
}

func pgDate(day time.Time) pgtype.Date { return pgtype.Date{Time: day, Valid: true} }
