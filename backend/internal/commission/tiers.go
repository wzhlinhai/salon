package commission

import (
	"cmp"
	"slices"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
)

type cumulativeKey struct {
	staff         uuid.UUID
	source, month string
}
type cumulativePosition struct{ before, after, total int64 }
type cumulative struct {
	units     []unit
	originals map[uuid.UUID]unit
	facts     map[lineKey]lineFact
	kinds     map[string]map[uuid.UUID]cumulativePosition
}

func newCumulative(all []unit, facts map[lineKey]lineFact) *cumulative {
	ordered := slices.Clone(all)
	slices.SortFunc(ordered, func(a, b unit) int {
		if n := a.first.BusinessDate.Time.Compare(b.first.BusinessDate.Time); n != 0 {
			return n
		}
		if n := a.createdAt.Compare(b.createdAt); n != 0 {
			return n
		}
		return cmp.Compare(a.first.ID.String(), b.first.ID.String())
	})
	c := &cumulative{units: ordered, originals: map[uuid.UUID]unit{}, facts: facts, kinds: map[string]map[uuid.UUID]cumulativePosition{}}
	for _, u := range all {
		if u.first.ReversesID == nil {
			for _, e := range u.entries {
				c.originals[e.ID] = u
			}
		}
	}
	return c
}

func groupKey(e dbgen.PerformanceEntry) cumulativeKey {
	return cumulativeKey{staff: *e.StaffID, source: e.Kind, month: e.BusinessDate.Time.Format("2006-01")}
}

func (c *cumulative) position(target unit, kind string) (cumulativePosition, error) {
	if cached, ok := c.kinds[kind]; ok {
		return cached[target.first.ID], nil
	}
	positions := map[uuid.UUID]cumulativePosition{}
	totals := map[cumulativeKey]int64{}
	for _, u := range c.units {
		original := u
		sign := int64(1)
		if u.first.ReversesID != nil {
			original = c.originals[*u.first.ReversesID]
			sign = -1
		}
		if len(original.entries) == 0 {
			continue
		}
		e := original.first
		base, err := unitBase(original, kind, c.facts[lineKey{optional(e.OrderItemID), optional(e.CheckoutID)}])
		if err != nil {
			return cumulativePosition{}, err
		}
		key := groupKey(u.first)
		before := totals[key]
		after := before + sign*base.Amount.Fen()
		positions[u.first.ID] = cumulativePosition{before: before, after: after}
		totals[key] = after
	}
	for _, u := range c.units {
		p := positions[u.first.ID]
		p.total = totals[groupKey(u.first)]
		positions[u.first.ID] = p
	}
	c.kinds[kind] = positions
	return positions[target.first.ID], nil
}

func reached(t Tiers, cumulative int64) int {
	level := 0
	for i, l := range t.Levels {
		if cumulative >= l.From.Fen() {
			level = i
		}
	}
	return level
}

func calculateTier(t Tiers, month, kind string, p cumulativePosition) (*CommissionTier, money.Change) {
	out := &CommissionTier{Mode: t.Mode, Month: month, BaseKind: kind, Parts: []CommissionTierPart{}}
	var amount int64
	part := func(index int, base int64) {
		b := money.ChangeFromFen(base)
		a := b.Share(t.Levels[index].Percent)
		out.Parts = append(out.Parts, CommissionTierPart{TierLevel: t.Levels[index], Base: b, Amount: a})
		amount += a.Fen()
	}
	level := reached(t, p.after)
	if t.Mode == "whole" {
		out.Cumulative = new(money.ChangeFromFen(p.total))
		level = reached(t, p.total)
		part(level, p.after-p.before)
	} else {
		out.Before, out.After = new(money.ChangeFromFen(p.before)), new(money.ChangeFromFen(p.after))
		low, high, sign := p.before, p.after, int64(1)
		if high < low {
			low, high, sign = high, low, -1
		}
		for low < high {
			i := reached(t, low)
			end := high
			if i+1 < len(t.Levels) {
				end = min(end, t.Levels[i+1].From.Fen())
			}
			part(i, (end-low)*sign)
			low = end
		}
	}
	out.Level = CommissionTierLevel{Index: level, TierLevel: t.Levels[level]}
	return out, money.ChangeFromFen(amount)
}
