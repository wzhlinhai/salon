package performance

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
)

// Unit 是按来源、结账、冲回、项目行和岗位归并的业绩单位。
type Unit struct {
	ID                                                                     uuid.UUID
	Kind                                                                   string
	StaffID                                                                uuid.UUID
	BusinessDate                                                           time.Time
	CreatedAt                                                              time.Time
	OrderID, OrderItemID                                                   *uuid.UUID
	ItemID                                                                 uuid.UUID
	Slot                                                                   *int16
	PositionID, CheckoutID, CardLedgerID, RedPacketID, VoidID, AftersaleID *uuid.UUID
	Member                                                                 *bool
	Designated                                                             bool
	Entries                                                                []UnitEntry
	Base                                                                   UnitBase
	Reverses                                                               *Unit
}

// UnitEntry 是业绩单位内按 ID 排序的支付方式记录。
type UnitEntry struct {
	ID         uuid.UUID
	Method     string
	PaidAmount money.Amount
	Ratio      int16
	Amount     money.Amount
}

// UnitBase 留存各类基数的分值、原价事实及按需报告的原价计算错误。
type UnitBase struct {
	Paid, Performance, CashPerformance, CardPerformance int64
	LinePrice                                           money.Amount
	LinePaid                                            string
	ProratedLinePrice                                   int64
	LinePriceError                                      error
}

type lineKey struct{ item, checkout uuid.UUID }
type lineFact struct {
	itemID     uuid.UUID
	price      money.Amount
	paid       string
	designated bool
}
type unitKey struct {
	kind                       string
	checkout, void, line, sale uuid.UUID
	aftersale                  uuid.UUID
	slot                       int16
}
type unit struct {
	entries   []dbgen.PerformanceEntry
	first     dbgen.PerformanceEntry
	createdAt time.Time
}

func optional[T any](p *T) (out T) {
	if p != nil {
		return *p
	}
	return
}

func groupEntries(entries []dbgen.PerformanceEntry) []unit {
	groups := map[unitKey]*unit{}
	seen := map[uuid.UUID]bool{}
	for _, e := range entries {
		if seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		if e.StaffID == nil {
			continue
		}
		k := unitKey{kind: e.Kind, checkout: optional(e.CheckoutID), void: optional(e.VoidID), line: optional(e.OrderItemID), aftersale: optional(e.AftersaleID), slot: optional(e.Slot)}
		if e.Kind == "sale" {
			k.sale = e.ID
		}
		u := groups[k]
		if u == nil {
			u = &unit{first: e, createdAt: e.CreatedAt.Time}
			groups[k] = u
		}
		u.entries = append(u.entries, e)
		if e.ID.String() < u.first.ID.String() {
			u.first = e
		}
		if e.CreatedAt.Time.Before(u.createdAt) {
			u.createdAt = e.CreatedAt.Time
		}
	}
	out := make([]unit, 0, len(groups))
	for _, u := range groups {
		slices.SortFunc(u.entries, func(a, b dbgen.PerformanceEntry) int { return cmp.Compare(a.ID.String(), b.ID.String()) })
		out = append(out, *u)
	}
	return out
}

// Units 按门店、营业日范围和员工批量加载含原笔及基数构成的业绩单位。
func Units(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, from, to time.Time, staffID *uuid.UUID) ([]Unit, error) {
	entries, err := q.PerformanceUnitEntries(ctx, dbgen.PerformanceUnitEntriesParams{StoreID: storeID, DateFrom: pgtype.Date{Time: from, Valid: true}, DateTo: pgtype.Date{Time: to, Valid: true}, StaffID: staffID})
	if err != nil {
		return nil, err
	}
	ids, lines := []uuid.UUID{}, []uuid.UUID{}
	for _, e := range entries {
		if e.ReversesID != nil {
			ids = append(ids, *e.ReversesID)
		}
		if e.OrderItemID != nil {
			lines = append(lines, *e.OrderItemID)
		}
	}
	originals, err := q.PerformanceOriginalEntries(ctx, dbgen.PerformanceOriginalEntriesParams{StoreID: storeID, Ids: ids})
	if err != nil {
		return nil, err
	}
	for _, e := range originals {
		if e.OrderItemID != nil {
			lines = append(lines, *e.OrderItemID)
		}
	}
	rows, err := q.PerformanceLineFacts(ctx, dbgen.PerformanceLineFactsParams{StoreID: storeID, Ids: lines})
	if err != nil {
		return nil, err
	}
	facts := map[lineKey]lineFact{}
	for _, r := range rows {
		facts[lineKey{r.ID, optional(r.CheckoutID)}] = lineFact{itemID: r.ItemID, price: r.Price, paid: r.LinePaid, designated: r.Designated}
	}
	return composeUnits(entries, originals, facts), nil
}

func compose(u unit, f lineFact) Unit {
	e := u.first
	out := Unit{ID: e.ID, Kind: e.Kind, StaffID: *e.StaffID, BusinessDate: e.BusinessDate.Time, CreatedAt: u.createdAt,
		OrderID: e.OrderID, OrderItemID: e.OrderItemID, ItemID: f.itemID, Slot: e.Slot, PositionID: e.PositionID,
		CheckoutID: e.CheckoutID, CardLedgerID: e.CardLedgerID, RedPacketID: e.RedPacketID, VoidID: e.VoidID, AftersaleID: e.AftersaleID,
		Member: e.Member, Designated: f.designated, Entries: make([]UnitEntry, 0, len(u.entries))}
	for _, row := range u.entries {
		out.Entries = append(out.Entries, UnitEntry{ID: row.ID, Method: row.Method, PaidAmount: row.PaidAmount, Ratio: row.Ratio, Amount: row.Amount})
		out.Base.Paid += row.PaidAmount.Fen()
		out.Base.Performance += row.Amount.Fen()
		for _, method := range paymentMethods {
			if method.name != row.Method {
				continue
			}
			switch method.group {
			case "cash":
				out.Base.CashPerformance += row.Amount.Fen()
			case "card":
				out.Base.CardPerformance += row.Amount.Fen()
			}
		}
	}
	if e.Kind == "item" {
		out.Base.LinePrice, out.Base.LinePaid = f.price, f.paid
		var denominator money.Total
		out.Base.LinePriceError = denominator.Scan(f.paid)
		if out.Base.LinePriceError == nil {
			share, err := money.FromFen(out.Base.Paid)
			out.Base.LinePriceError = err
			if err == nil {
				out.Base.ProratedLinePrice = f.price.Prorate(share, denominator).Fen()
			}
		}
	}
	return out
}

func composeUnits(entries, originals []dbgen.PerformanceEntry, facts map[lineKey]lineFact) []Unit {
	all := groupEntries(append(slices.Clone(originals), entries...))
	byEntry := map[uuid.UUID]*Unit{}
	for _, raw := range all {
		if raw.first.ReversesID != nil {
			continue
		}
		u := compose(raw, facts[lineKey{optional(raw.first.OrderItemID), optional(raw.first.CheckoutID)}])
		for _, e := range raw.entries {
			byEntry[e.ID] = &u
		}
	}
	out := make([]Unit, 0)
	for _, raw := range groupEntries(entries) {
		u := compose(raw, facts[lineKey{optional(raw.first.OrderItemID), optional(raw.first.CheckoutID)}])
		if raw.first.ReversesID != nil {
			u.Reverses = byEntry[*raw.first.ReversesID]
			if u.Reverses == nil {
				continue
			}
		}
		out = append(out, u)
	}
	return out
}
