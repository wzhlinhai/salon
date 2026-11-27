package reports

import (
	"context"
	"fmt"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

// valueColumn 是储值日报里的一列流水。card_ledger 的每个 kind 归进且只归进一列。
type valueColumn int

const (
	columnAdded valueColumn = iota
	columnUpgrade
	columnConsumed
	columnRefunded
	columnAdjusted
	columnCount
)

func storedValueColumn(kind string) (valueColumn, error) {
	switch kind {
	case "open", "recharge":
		return columnAdded, nil
	case "upgrade":
		return columnUpgrade, nil
	case "consume", "void", "aftersale_refund":
		return columnConsumed, nil
	case "refund":
		return columnRefunded, nil
	case "adjust", "transfer_in", "transfer_out", "import":
		return columnAdjusted, nil
	}
	return 0, fmt.Errorf("卡流水类型 %q 没有归入储值日报的列", kind)
}

// valuePair 是一笔钱的本金和赠送金，带符号。
type valuePair struct{ principal, bonus money.SignedTotal }

func (p valuePair) plus(o valuePair) valuePair {
	return valuePair{p.principal.Add(o.principal.Change()), p.bonus.Add(o.bonus.Change())}
}

type valueColumns [columnCount]valuePair

func (c valueColumns) sum() valuePair {
	var out valuePair
	for _, p := range c {
		out = out.plus(p)
	}
	return out
}

func (c valueColumns) plus(o valueColumns) valueColumns {
	for i := range c {
		c[i] = c[i].plus(o[i])
	}
	return c
}

func getStoredValueReport(ctx context.Context, env ops.Env, in *StoredValueReportInput) (*StoredValueReport, error) {
	q, err := dateRange(env.Caller, perm.ReportView, in.From, in.To)
	if err != nil {
		return nil, err
	}
	out := &StoredValueReport{Rows: []StoredValueDay{}}
	if q.Empty {
		return out, nil
	}
	from, to := pgDate(q.From), pgDate(q.To)
	var opening valuePair
	start, err := env.Q.ReportStoredValueOpening(ctx, dbgen.ReportStoredValueOpeningParams{StoreID: env.Caller.StoreID, DateFrom: from})
	if err != nil {
		return nil, err
	}
	if err := addSigned(&opening.principal, start.Principal); err != nil {
		return nil, err
	}
	if err := addSigned(&opening.bonus, start.Bonus); err != nil {
		return nil, err
	}
	flows, err := env.Q.ReportStoredValueFlows(ctx, dbgen.ReportStoredValueFlowsParams{StoreID: env.Caller.StoreID, DateFrom: from, DateTo: to})
	if err != nil {
		return nil, err
	}
	byDay := map[string]*valueColumns{}
	for _, row := range flows {
		column, err := storedValueColumn(row.Kind)
		if err != nil {
			return nil, err
		}
		key := dayKey(row.BusinessDate.Time)
		if byDay[key] == nil {
			byDay[key] = &valueColumns{}
		}
		cell := &byDay[key][column]
		if err := addSigned(&cell.principal, row.PrincipalDelta); err != nil {
			return nil, err
		}
		if err := addSigned(&cell.bonus, row.BonusDelta); err != nil {
			return nil, err
		}
	}
	var all valueColumns
	running := opening
	for day := q.From; !day.After(q.To); day = day.AddDate(0, 0, 1) {
		var cols valueColumns
		if found := byDay[dayKey(day)]; found != nil {
			cols = *found
		}
		closing := running.plus(cols.sum())
		out.Rows = append(out.Rows, storedValueDay(dayKey(day), running, cols, closing))
		all = all.plus(cols)
		running = closing
	}
	out.Totals = storedValueTotals(opening, all, running)
	return out, nil
}

func storedValueDay(date string, opening valuePair, c valueColumns, closing valuePair) StoredValueDay {
	return StoredValueDay{
		BusinessDate:      date,
		OpeningPrincipal:  opening.principal.Change(),
		OpeningBonus:      opening.bonus.Change(),
		AddedPrincipal:    c[columnAdded].principal.Change(),
		AddedBonus:        c[columnAdded].bonus.Change(),
		UpgradePrincipal:  c[columnUpgrade].principal.Change(),
		UpgradeBonus:      c[columnUpgrade].bonus.Change(),
		ConsumedPrincipal: c[columnConsumed].principal.Change().Negate(),
		ConsumedBonus:     c[columnConsumed].bonus.Change().Negate(),
		RefundedPrincipal: c[columnRefunded].principal.Change().Negate(),
		RefundedBonus:     c[columnRefunded].bonus.Change().Negate(),
		AdjustedPrincipal: c[columnAdjusted].principal.Change(),
		AdjustedBonus:     c[columnAdjusted].bonus.Change(),
		ClosingPrincipal:  closing.principal.Change(),
		ClosingBonus:      closing.bonus.Change(),
	}
}

func storedValueTotals(opening valuePair, c valueColumns, closing valuePair) StoredValueTotals {
	d := storedValueDay("", opening, c, closing)
	return StoredValueTotals{
		OpeningPrincipal: d.OpeningPrincipal, OpeningBonus: d.OpeningBonus,
		AddedPrincipal: d.AddedPrincipal, AddedBonus: d.AddedBonus,
		UpgradePrincipal: d.UpgradePrincipal, UpgradeBonus: d.UpgradeBonus,
		ConsumedPrincipal: d.ConsumedPrincipal, ConsumedBonus: d.ConsumedBonus,
		RefundedPrincipal: d.RefundedPrincipal, RefundedBonus: d.RefundedBonus,
		AdjustedPrincipal: d.AdjustedPrincipal, AdjustedBonus: d.AdjustedBonus,
		ClosingPrincipal: d.ClosingPrincipal, ClosingBonus: d.ClosingBonus,
	}
}
