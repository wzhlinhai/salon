package reports

import (
	"context"
	"time"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/paymethod"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

type businessAccum struct {
	values      map[string]money.SignedTotal
	cards       map[string]money.SignedTotal
	service     money.SignedTotal
	sales       money.SignedTotal
	newDebt     money.SignedTotal
	debtRepay   money.SignedTotal
	aftersale   money.SignedTotal
	guestTotal  int64
	guestDesig  int64
	guestNot    int64
	guestMale   int64
	guestFemale int64
	checkout    int64
	void        int64
}

func newBusinessAccum() *businessAccum {
	return &businessAccum{values: map[string]money.SignedTotal{}, cards: map[string]money.SignedTotal{}}
}

func (a *businessAccum) add(other *businessAccum) {
	for key, value := range other.values {
		a.values[key] = a.values[key].Add(value.Change())
	}
	for key, value := range other.cards {
		a.cards[key] = a.cards[key].Add(value.Change())
	}
	a.service = a.service.Add(other.service.Change())
	a.sales = a.sales.Add(other.sales.Change())
	a.newDebt = a.newDebt.Add(other.newDebt.Change())
	a.debtRepay = a.debtRepay.Add(other.debtRepay.Change())
	a.aftersale = a.aftersale.Add(other.aftersale.Change())
	a.guestTotal += other.guestTotal
	a.guestDesig += other.guestDesig
	a.guestNot += other.guestNot
	a.guestMale += other.guestMale
	a.guestFemale += other.guestFemale
	a.checkout += other.checkout
	a.void += other.void
}

func (a *businessAccum) addValue(method, value string) error {
	return addAmount(a.values, method, value)
}

func (a *businessAccum) subValue(method, value string) error {
	delta, err := change(value)
	if err != nil {
		return err
	}
	a.values[method] = a.values[method].Add(delta.Negate())
	return nil
}

func (a *businessAccum) addCard(method, value string) error {
	return addAmount(a.cards, method, value)
}

func (a *businessAccum) subCard(method, value string) error {
	delta, err := change(value)
	if err != nil {
		return err
	}
	a.cards[method] = a.cards[method].Add(delta.Negate())
	return nil
}

func getBusinessReport(ctx context.Context, env ops.Env, in *BusinessReportInput) (*BusinessReport, error) {
	granularity := in.Granularity
	if granularity != "day" && granularity != "month" {
		// rule-untested: granularity 的 schema enum 在调用前拒绝其他值
		return nil, ops.Rule("请选择日报或月报")
	}
	var from, to time.Time
	var err error
	if granularity == "day" {
		var q struct {
			From, To time.Time
		}
		q, err = parseDayRange(env.Caller, in.From, in.To)
		if err != nil {
			return nil, err
		}
		from, to = q.From, q.To
	} else {
		from, to, err = monthRange(in.From, in.To)
		if err != nil {
			return nil, err
		}
	}
	days, err := loadBusinessDays(ctx, env, from, to)
	if err != nil {
		return nil, err
	}
	out := &BusinessReport{Granularity: granularity, Rows: []BusinessReportRow{}}
	months := map[string]*businessAccum{}
	for day := from; !day.After(to); day = day.AddDate(0, 0, 1) {
		key := dayKey(day)
		acc := days[key]
		if acc == nil {
			acc = newBusinessAccum()
		}
		if granularity == "month" {
			monthKey := day.Format("2006-01")
			if months[monthKey] == nil {
				months[monthKey] = newBusinessAccum()
			}
			months[monthKey].add(acc)
		}
	}
	if granularity == "day" {
		for day := from; !day.After(to); day = day.AddDate(0, 0, 1) {
			acc := days[dayKey(day)]
			if acc == nil {
				acc = newBusinessAccum()
			}
			out.Rows = append(out.Rows, finishBusinessRow(dayKey(day), acc))
		}
	} else {
		for month := from; !month.After(to); month = month.AddDate(0, 1, 0) {
			key := month.Format("2006-01")
			out.Rows = append(out.Rows, finishBusinessRow(key, months[key]))
		}
	}
	total := newBusinessAccum()
	for _, acc := range days {
		total.add(acc)
	}
	out.Totals = finishBusinessRow("total", total)
	return out, nil
}

func parseDayRange(caller ops.Caller, from, to string) (struct{ From, To time.Time }, error) {
	q, err := dateRange(caller, perm.ReportView, from, to)
	if err != nil {
		return struct{ From, To time.Time }{}, err
	}
	return struct{ From, To time.Time }{From: q.From, To: q.To}, nil
}

func loadBusinessDays(ctx context.Context, env ops.Env, from, to time.Time) (map[string]*businessAccum, error) {
	days := map[string]*businessAccum{}
	get := func(day time.Time) *businessAccum {
		key := dayKey(day)
		if days[key] == nil {
			days[key] = newBusinessAccum()
		}
		return days[key]
	}
	store := env.Caller.StoreID
	dates := dbgen.BusinessCheckoutPaymentsParams{StoreID: store, DateFrom: pgDate(from), DateTo: pgDate(to)}
	payments, err := env.Q.BusinessCheckoutPayments(ctx, dates)
	if err != nil {
		return nil, err
	}
	for _, row := range payments {
		if err := get(row.BusinessDate.Time).addValue(row.Method, row.Amount); err != nil {
			return nil, err
		}
	}
	splits, err := env.Q.BusinessCheckoutCardSplit(ctx, dbgen.BusinessCheckoutCardSplitParams{StoreID: store, DateFrom: pgDate(from), DateTo: pgDate(to)})
	if err != nil {
		return nil, err
	}
	for _, row := range splits {
		if err := get(row.BusinessDate.Time).addCard(string(paymethod.CardPrincipal), row.Principal); err != nil {
			return nil, err
		}
		if err := get(row.BusinessDate.Time).addCard(string(paymethod.CardBonus), row.Bonus); err != nil {
			return nil, err
		}
	}
	repayments, err := env.Q.BusinessDebtRepayments(ctx, dbgen.BusinessDebtRepaymentsParams{StoreID: store, DateFrom: pgDate(from), DateTo: pgDate(to)})
	if err != nil {
		return nil, err
	}
	for _, row := range repayments {
		acc := get(row.BusinessDate.Time)
		if err := acc.addValue(row.Method, row.Amount); err != nil {
			return nil, err
		}
		if err := addSigned(&acc.debtRepay, row.Amount); err != nil {
			return nil, err
		}
	}
	sales, err := env.Q.BusinessSalePayments(ctx, dbgen.BusinessSalePaymentsParams{StoreID: store, DateFrom: pgDate(from), DateTo: pgDate(to)})
	if err != nil {
		return nil, err
	}
	for _, row := range sales {
		acc := get(row.BusinessDate.Time)
		if err := acc.addValue(row.Method, row.Amount); err != nil {
			return nil, err
		}
		if err := addSigned(&acc.sales, row.Amount); err != nil {
			return nil, err
		}
	}
	newDebts, err := env.Q.BusinessNewDebts(ctx, dbgen.BusinessNewDebtsParams{StoreID: store, DateFrom: pgDate(from), DateTo: pgDate(to)})
	if err != nil {
		return nil, err
	}
	for _, row := range newDebts {
		if err := addSigned(&get(row.BusinessDate.Time).newDebt, row.Amount); err != nil {
			return nil, err
		}
	}
	refunds, err := env.Q.BusinessCardRefunds(ctx, dbgen.BusinessCardRefundsParams{StoreID: store, DateFrom: pgDate(from), DateTo: pgDate(to)})
	if err != nil {
		return nil, err
	}
	for _, row := range refunds {
		if err := get(row.BusinessDate.Time).subValue(row.Method, row.Amount); err != nil {
			return nil, err
		}
	}
	aftersales, err := env.Q.BusinessAftersaleRefunds(ctx, dbgen.BusinessAftersaleRefundsParams{StoreID: store, DateFrom: pgDate(from), DateTo: pgDate(to)})
	if err != nil {
		return nil, err
	}
	for _, row := range aftersales {
		acc := get(row.BusinessDate.Time)
		if err := addSigned(&acc.aftersale, row.Amount); err != nil {
			return nil, err
		}
		if row.Method == string(paymethod.CardPrincipal) {
			if err := acc.subCard(string(paymethod.CardPrincipal), row.Amount); err != nil {
				return nil, err
			}
			continue
		}
		if err := acc.subValue(row.Method, row.Amount); err != nil {
			return nil, err
		}
	}
	voids, err := env.Q.BusinessVoidPayments(ctx, dbgen.BusinessVoidPaymentsParams{StoreID: store, DateFrom: pgDate(from), DateTo: pgDate(to)})
	if err != nil {
		return nil, err
	}
	for _, row := range voids {
		if err := get(row.BusinessDate.Time).subValue(row.Method, row.Amount); err != nil {
			return nil, err
		}
	}
	voidCards, err := env.Q.BusinessVoidCardSplit(ctx, dbgen.BusinessVoidCardSplitParams{StoreID: store, DateFrom: pgDate(from), DateTo: pgDate(to)})
	if err != nil {
		return nil, err
	}
	for _, row := range voidCards {
		acc := get(row.BusinessDate.Time)
		if err := acc.subCard(string(paymethod.CardPrincipal), row.Principal); err != nil {
			return nil, err
		}
		if err := acc.subCard(string(paymethod.CardBonus), row.Bonus); err != nil {
			return nil, err
		}
	}
	service, err := env.Q.BusinessServiceRevenue(ctx, dbgen.BusinessServiceRevenueParams{StoreID: store, DateFrom: pgDate(from), DateTo: pgDate(to)})
	if err != nil {
		return nil, err
	}
	for _, row := range service {
		if err := addSigned(&get(row.BusinessDate.Time).service, row.Amount); err != nil {
			return nil, err
		}
	}
	guests, err := env.Q.BusinessGuestVisits(ctx, dbgen.BusinessGuestVisitsParams{StoreID: store, DateFrom: pgDate(from), DateTo: pgDate(to)})
	if err != nil {
		return nil, err
	}
	for _, row := range guests {
		acc := get(row.BusinessDate.Time)
		total, err := parseShare(row.Total)
		if err != nil {
			return nil, err
		}
		designated, err := parseShare(row.Designated)
		if err != nil {
			return nil, err
		}
		notDesignated, err := parseShare(row.NotDesignated)
		if err != nil {
			return nil, err
		}
		male, err := parseShare(row.Male)
		if err != nil {
			return nil, err
		}
		female, err := parseShare(row.Female)
		if err != nil {
			return nil, err
		}
		acc.guestTotal += total
		acc.guestDesig += designated
		acc.guestNot += notDesignated
		acc.guestMale += male
		acc.guestFemale += female
	}
	checkouts, err := env.Q.BusinessCheckoutCounts(ctx, dbgen.BusinessCheckoutCountsParams{StoreID: store, DateFrom: pgDate(from), DateTo: pgDate(to)})
	if err != nil {
		return nil, err
	}
	for _, row := range checkouts {
		get(row.BusinessDate.Time).checkout += row.Count
	}
	voidCounts, err := env.Q.BusinessVoidCounts(ctx, dbgen.BusinessVoidCountsParams{StoreID: store, DateFrom: pgDate(from), DateTo: pgDate(to)})
	if err != nil {
		return nil, err
	}
	for _, row := range voidCounts {
		get(row.BusinessDate.Time).void += row.Count
	}
	return days, nil
}

func finishBusinessRow(period string, acc *businessAccum) BusinessReportRow {
	if acc == nil {
		acc = newBusinessAccum()
	}
	out := BusinessReportRow{
		Period: period, ServiceRevenue: acc.service.Change(), SalesRevenue: acc.sales.Change(),
		CardPrincipal: acc.cards[string(paymethod.CardPrincipal)].Change(), CardBonus: acc.cards[string(paymethod.CardBonus)].Change(),
		CountAmount: acc.values[string(paymethod.Count)].Change(), GroupbuyDeduct: acc.values[string(paymethod.Groupbuy)].Change(),
		NewDebt: acc.newDebt.Change(), DebtRepayment: acc.debtRepay.Change(), AftersaleRefund: acc.aftersale.Change(),
		GuestTotal: formatShare(acc.guestTotal), GuestDesignated: formatShare(acc.guestDesig), GuestNotDesignated: formatShare(acc.guestNot),
		GuestMale: formatShare(acc.guestMale), GuestFemale: formatShare(acc.guestFemale),
		CheckoutCount: acc.checkout, VoidCount: acc.void,
	}
	for _, method := range paymethod.OnSite() {
		out.OnSite = append(out.OnSite, ReportMethodAmount{Method: string(method.Code), Label: method.Label, Amount: acc.values[string(method.Code)].Change()})
	}
	known := map[string]bool{}
	for _, method := range paymethod.All {
		known[string(method.Code)] = true
	}
	for _, method := range paymethod.NonSite() {
		amount := acc.values[string(method.Code)].Change()
		if method.Code == paymethod.CardPrincipal || method.Code == paymethod.CardBonus {
			amount = acc.cards[string(method.Code)].Change()
		}
		out.NonSite = append(out.NonSite, ReportMethodAmount{Method: string(method.Code), Label: method.Label, Amount: amount})
	}
	for _, code := range sortedKeys(acc.values) {
		if !known[code] {
			out.NonSite = append(out.NonSite, ReportMethodAmount{Method: code, Label: paymethod.Label(code), Amount: acc.values[code].Change()})
		}
	}
	return out
}
