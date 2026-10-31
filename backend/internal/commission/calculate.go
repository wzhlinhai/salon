package commission

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

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

func units(entries []dbgen.PerformanceEntry) []unit {
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
		k := unitKey{kind: e.Kind, checkout: optional(e.CheckoutID), void: optional(e.VoidID), line: optional(e.OrderItemID), slot: optional(e.Slot)}
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

func calculate(entries, originals []dbgen.PerformanceEntry, facts map[lineKey]lineFact, versions []dbgen.CommissionSchemeVersion) ([]Commission, error) {
	originalUnits := units(append(slices.Clone(originals), entries...))
	byEntry := map[uuid.UUID]Commission{}
	// 原笔先计算；冲回仅复制原笔结果，因此不会使用撤单日的新版本或新规则。
	for _, u := range originalUnits {
		if u.first.ReversesID != nil {
			continue
		}
		c, err := calculateOriginal(u, facts, versions)
		if err != nil {
			return nil, err
		}
		if c == nil {
			continue
		}
		for _, e := range u.entries {
			byEntry[e.ID] = *c
		}
	}
	out := []Commission{}
	for _, u := range units(entries) {
		e := u.first
		key := e.ID
		if e.ReversesID != nil {
			key = *e.ReversesID
		}
		c, ok := byEntry[key]
		if !ok {
			continue
		}
		if e.ReversesID != nil {
			originalID := c.ID
			c.Explanation.Reverses = &originalID
			c.Amount = c.Amount.Negate()
			c.Reversal = true
			c.Explanation.Origin.VoidID = e.VoidID
			c.Explanation.Entries = entryViews(u.entries)
		}
		c.ID, c.BusinessDate, c.createdAt = e.ID, e.BusinessDate.Time.Format(time.DateOnly), u.createdAt
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b Commission) int {
		if n := cmp.Compare(b.BusinessDate, a.BusinessDate); n != 0 {
			return n
		}
		if n := b.createdAt.Compare(a.createdAt); n != 0 {
			return n
		}
		return cmp.Compare(b.ID.String(), a.ID.String())
	})
	return out, nil
}

func calculateOriginal(u unit, facts map[lineKey]lineFact, versions []dbgen.CommissionSchemeVersion) (*Commission, error) {
	e := u.first
	v, err := selectVersion(versions, e.BusinessDate.Time, &u.createdAt)
	if err != nil {
		return nil, nil
	}
	var s CommissionScheme
	if err := json.Unmarshal(v.Scheme, &s); err != nil {
		return nil, err
	}
	if slices.Contains(s.ExcludedStaff, *e.StaffID) {
		return nil, nil
	}
	fact := facts[lineKey{optional(e.OrderItemID), optional(e.CheckoutID)}]
	for _, r := range s.Rules {
		if r.Source != e.Kind {
			continue
		}
		match, value := matchRule(r, s.Overrides, e, fact)
		if value == nil {
			return nil, nil
		}
		base, err := unitBase(u, r.Base, fact)
		if err != nil {
			return nil, err
		}
		amount := money.Amount{}
		if value.FixedAmount != nil {
			amount = *value.FixedAmount
		} else {
			amount = base.Amount.Share(*value.Percent)
		}
		origin := CommissionOrigin{OrderID: e.OrderID, OrderItemID: e.OrderItemID, Slot: e.Slot, CardLedgerID: e.CardLedgerID, RedPacketID: e.RedPacketID, CheckoutID: e.CheckoutID}
		var designated *bool
		if e.Kind != "sale" {
			origin.Item = &staff.Ref{ID: fact.itemID}
			origin.Position = &staff.Ref{ID: optional(e.PositionID)}
			designated = &fact.designated
		}
		return &Commission{ID: e.ID, Source: e.Kind, Staff: staff.Ref{ID: *e.StaffID}, Amount: amount.Sub(money.Amount{}), Explanation: CommissionExplanation{Origin: origin, Entries: entryViews(u.entries), Designated: designated, Member: e.Member, Base: base, Match: match, Version: CommissionVersion{ID: v.ID, Version: v.Version, EffectiveFrom: v.EffectiveFrom.Time.Format(time.DateOnly)}}}, nil
	}
	return nil, nil
}

func matchRule(r Rule, overrides []Override, e dbgen.PerformanceEntry, f lineFact) (CommissionMatch, *Cell) {
	// 逐层查找，不依赖覆盖在方案中的保存顺序。
	for _, kind := range []string{"override_staff_item", "override_item", "override_staff"} {
		for _, o := range overrides {
			if o.Source != e.Kind {
				continue
			}
			if o.ItemID != nil && (e.Kind == "sale" || *o.ItemID != f.itemID) {
				continue
			}
			if o.StaffID != nil && *o.StaffID != *e.StaffID {
				continue
			}
			target := "override_staff"
			if o.ItemID != nil {
				target = "override_item"
				if o.StaffID != nil {
					target = "override_staff_item"
				}
			}
			if target != kind {
				continue
			}
			return CommissionMatch{Kind: kind, Override: &o}, &Cell{Percent: o.Percent, FixedAmount: o.FixedAmount}
		}
	}
	for _, c := range r.Cells {
		if r.ByPosition && optional(c.PositionID) != optional(e.PositionID) {
			continue
		}
		if r.ByDesignated && optional(c.Designated) != f.designated {
			continue
		}
		if r.ByMember && optional(c.Member) != optional(e.Member) {
			continue
		}
		return CommissionMatch{Kind: "cell", Cell: &c}, &c
	}
	return CommissionMatch{}, nil
}

func entryViews(entries []dbgen.PerformanceEntry) []CommissionPerformanceEntry {
	out := make([]CommissionPerformanceEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, CommissionPerformanceEntry{ID: e.ID, Method: e.Method, PaidAmount: e.PaidAmount, Ratio: e.Ratio, Amount: e.Amount})
	}
	return out
}

func unitBase(u unit, kind string, f lineFact) (CommissionBase, error) {
	var paid, performance, cash, card int64
	for _, e := range u.entries {
		paid += e.PaidAmount.Fen()
		performance += e.Amount.Fen()
		switch e.Method {
		case "cash", "unionpay", "wechat", "alipay", "debt":
			cash += e.Amount.Fen()
		case "card_principal", "card_bonus":
			card += e.Amount.Fen()
		}
	}
	n := performance
	base := CommissionBase{Kind: kind}
	if u.first.Kind == "item" {
		switch kind {
		case "paid":
			n = paid
		case "cash_performance":
			n = cash
		case "card_performance":
			n = card
		case "line_price":
			var denominator money.Total
			if err := denominator.Scan(f.paid); err != nil {
				return base, err
			}
			share, err := money.FromFen(paid)
			if err != nil {
				return base, err
			}
			n = f.price.Prorate(share, denominator).Fen()
			base.LinePrice, base.UnitPaid, base.LinePaid = &f.price, &share, &f.paid
		}
	}
	var err error
	base.Amount, err = money.FromFen(n)
	if err != nil {
		return base, fmt.Errorf("commission base: %w", err)
	}
	return base, nil
}
