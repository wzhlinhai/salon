package commission

import (
	"cmp"
	"encoding/json"
	"fmt"
	"math/big"
	"slices"
	"time"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/payrollmonth"
	"github.com/wzhlinhai/salon/backend/internal/performance"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

func optional[T any](p *T) (out T) {
	if p != nil {
		return *p
	}
	return
}

func calculate(units []performance.Unit, versions []dbgen.CommissionSchemeVersion) ([]Commission, error) {
	cumulative := newCumulative(units)
	out := []Commission{}
	for _, u := range units {
		original := u
		if u.Reverses != nil {
			original = *u.Reverses
		}
		c, err := calculateOriginal(original, u, versions)
		if err != nil {
			return nil, err
		}
		if c == nil {
			continue
		}
		if u.Reverses != nil {
			originalID := c.ID
			c.Explanation.Reverses = &originalID
			c.Amount = c.Amount.Negate()
			c.Reversal = true
			c.Explanation.Origin.VoidID = u.VoidID
			c.Explanation.Origin.AftersaleID = u.AftersaleID
			c.Explanation.Entries = entryViews(u.Entries)
		}
		if cell := c.Explanation.Match.Cell; cell != nil && cell.Tiers != nil {
			position, err := cumulative.position(u, c.Explanation.Base.Kind)
			if err != nil {
				return nil, err
			}
			c.Explanation.Tier, c.Amount = calculateTier(*cell.Tiers, payrollmonth.Of(u.BusinessDate), c.Explanation.Base.Kind, position)
		}
		c.ID, c.BusinessDate, c.businessDate, c.createdAt = u.ID, u.BusinessDate.Format(time.DateOnly), u.BusinessDate, u.CreatedAt
		out = append(out, *c)
	}
	sortCommissions(out)
	return out, nil
}

// calculateOriginal 用原始业绩命中方案和版本，用 base 的金额计算提成；
// 售后按比例冲回时 base 是缩放后的冲回单位，撤单时两者相同。
func calculateOriginal(u, baseUnit performance.Unit, versions []dbgen.CommissionSchemeVersion) (*Commission, error) {
	e := u
	v, err := selectVersion(versions, e.BusinessDate, &u.CreatedAt)
	if err != nil {
		return nil, nil
	}
	var s CommissionScheme
	if err := json.Unmarshal(v.Scheme, &s); err != nil {
		return nil, err
	}
	if slices.Contains(s.ExcludedStaff, e.StaffID) {
		return nil, nil
	}
	for _, r := range s.Rules {
		if r.Source != e.Kind {
			continue
		}
		match, value := matchRule(r, s.Overrides, e)
		if value == nil {
			return nil, nil
		}
		base, err := unitBase(baseUnit, r.Base)
		if err != nil {
			return nil, err
		}
		amount := money.Amount{}
		if value.FixedAmount != nil {
			amount = *value.FixedAmount
			if baseUnit.Reverses != nil && baseUnit.AftersaleID != nil {
				amount = prorateFixedFloor(amount, baseUnit.Base.Paid, u.Base.Paid)
			}
		} else if value.Percent != nil {
			amount = base.Amount.Share(*value.Percent)
		}
		origin := CommissionOrigin{OrderID: e.OrderID, OrderItemID: e.OrderItemID, Slot: e.Slot, CardLedgerID: e.CardLedgerID, RedPacketID: e.RedPacketID, CheckoutID: e.CheckoutID}
		var designated *bool
		if e.Kind != "sale" {
			origin.Item = &staff.Ref{ID: e.ItemID}
			origin.Position = &staff.Ref{ID: optional(e.PositionID)}
			designated = &e.Designated
		}
		return &Commission{ID: e.ID, Source: e.Kind, Staff: staff.Ref{ID: e.StaffID}, Amount: amount.Sub(money.Amount{}), Explanation: CommissionExplanation{Origin: origin, Entries: entryViews(u.Entries), Designated: designated, Member: e.Member, Base: base, Match: match, Version: CommissionVersion{ID: v.ID, Version: v.Version, EffectiveFrom: v.EffectiveFrom.Time.Format(time.DateOnly)}}}, nil
	}
	return nil, nil
}

// prorateFixedFloor 按比例向下取整到分，用于部分售后退款冲回固定提成。
func prorateFixedFloor(fixed money.Amount, numerator, denominator int64) money.Amount {
	if numerator <= 0 || denominator <= 0 {
		return money.Amount{}
	}
	var product big.Int
	product.Mul(big.NewInt(fixed.Fen()), big.NewInt(numerator))
	product.Quo(&product, big.NewInt(denominator))
	out, err := money.FromFen(product.Int64())
	if err != nil {
		return money.Amount{}
	}
	if out.Cmp(fixed) > 0 {
		return fixed
	}
	return out
}

func matchRule(r Rule, overrides []Override, e performance.Unit) (CommissionMatch, *Cell) {
	// 逐层查找，不依赖覆盖在方案中的保存顺序。
	for _, kind := range []string{"override_staff_item", "override_item", "override_staff"} {
		for _, o := range overrides {
			if o.Source != e.Kind {
				continue
			}
			if o.ItemID != nil && (e.Kind == "sale" || *o.ItemID != e.ItemID) {
				continue
			}
			if o.StaffID != nil && *o.StaffID != e.StaffID {
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
		if r.ByDesignated && optional(c.Designated) != e.Designated {
			continue
		}
		if r.ByMember && optional(c.Member) != optional(e.Member) {
			continue
		}
		return CommissionMatch{Kind: "cell", Cell: &c}, &c
	}
	return CommissionMatch{}, nil
}

func entryViews(entries []performance.UnitEntry) []CommissionPerformanceEntry {
	out := make([]CommissionPerformanceEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, CommissionPerformanceEntry{ID: e.ID, Method: e.Method, PaidAmount: e.PaidAmount, Ratio: e.Ratio, Amount: e.Amount})
	}
	return out
}

func unitBase(u performance.Unit, kind string) (CommissionBase, error) {
	n := u.Base.Performance
	base := CommissionBase{Kind: kind}
	if u.Kind == "item" {
		switch kind {
		case "paid":
			n = u.Base.Paid
		case "cash_performance":
			n = u.Base.CashPerformance
		case "card_performance":
			n = u.Base.CardPerformance
		case "line_price":
			if u.Base.LinePriceError != nil {
				return base, u.Base.LinePriceError
			}
			share, err := money.FromFen(u.Base.Paid)
			if err != nil {
				return base, err
			}
			n = u.Base.ProratedLinePrice
			base.LinePrice, base.UnitPaid, base.LinePaid = &u.Base.LinePrice, &share, &u.Base.LinePaid
		}
	}
	var err error
	base.Amount, err = money.FromFen(n)
	if err != nil {
		return base, fmt.Errorf("commission base: %w", err)
	}
	return base, nil
}

func sortCommissions(out []Commission) {
	slices.SortFunc(out, func(a, b Commission) int {
		if n := cmp.Compare(b.BusinessDate, a.BusinessDate); n != 0 {
			return n
		}
		if n := b.createdAt.Compare(a.createdAt); n != 0 {
			return n
		}
		return cmp.Compare(b.ID.String(), a.ID.String())
	})
}
