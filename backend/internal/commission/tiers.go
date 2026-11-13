package commission

import (
	"cmp"
	"slices"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/payrollmonth"
	"github.com/wzhlinhai/salon/backend/internal/performance"
)

type cumulativeKey struct {
	staff  uuid.UUID
	source string
	month  payrollmonth.Month
}
type cumulativePosition struct{ before, after, total int64 }
type cumulative struct {
	units []performance.Unit
	kinds map[string]map[uuid.UUID]cumulativePosition
}

func newCumulative(all []performance.Unit) *cumulative {
	ordered := slices.Clone(all)
	slices.SortFunc(ordered, func(a, b performance.Unit) int {
		if n := a.BusinessDate.Compare(b.BusinessDate); n != 0 {
			return n
		}
		if n := a.CreatedAt.Compare(b.CreatedAt); n != 0 {
			return n
		}
		return cmp.Compare(a.ID.String(), b.ID.String())
	})
	return &cumulative{units: ordered, kinds: map[string]map[uuid.UUID]cumulativePosition{}}
}

func groupKey(u performance.Unit) cumulativeKey {
	return cumulativeKey{staff: u.StaffID, source: u.Kind, month: payrollmonth.Of(u.BusinessDate)}
}

func (c *cumulative) position(target performance.Unit, kind string) (cumulativePosition, error) {
	if cached, ok := c.kinds[kind]; ok {
		return cached[target.ID], nil
	}
	positions := map[uuid.UUID]cumulativePosition{}
	totals := map[cumulativeKey]int64{}
	for _, u := range c.units {
		sign := int64(1)
		if u.Reverses != nil {
			sign = -1
		}
		base, err := unitBase(u, kind)
		if err != nil {
			return cumulativePosition{}, err
		}
		key := groupKey(u)
		before := totals[key]
		after := before + sign*base.Amount.Fen()
		positions[u.ID] = cumulativePosition{before: before, after: after}
		totals[key] = after
	}
	for _, u := range c.units {
		p := positions[u.ID]
		p.total = totals[groupKey(u)]
		positions[u.ID] = p
	}
	c.kinds[kind] = positions
	return positions[target.ID], nil
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

func calculateTier(t Tiers, month payrollmonth.Month, kind string, p cumulativePosition) (*CommissionTier, money.Change) {
	out := &CommissionTier{Mode: t.Mode, Month: month.String(), BaseKind: kind, Parts: []CommissionTierPart{}}
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
