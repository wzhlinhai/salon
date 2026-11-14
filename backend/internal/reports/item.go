package reports

import (
	"context"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

type itemAccum struct {
	item        ItemReportRow
	linePrice   money.SignedTotal
	paid        money.SignedTotal
	cardPaid    money.SignedTotal
	countPaid   money.SignedTotal
	groupbuy    money.SignedTotal
	performance money.SignedTotal
}

func getItemReport(ctx context.Context, env ops.Env, in *ItemReportInput) (*ItemReport, error) {
	q, err := dateRange(env.Caller, perm.ReportView, in.From, in.To)
	if err != nil {
		return nil, err
	}
	out := &ItemReport{Rows: []ItemReportRow{}, Totals: ItemReportRow{ItemName: "合计"}}
	if q.Empty {
		return out, nil
	}
	rows, err := env.Q.ItemReport(ctx, dbgen.ItemReportParams{StoreID: env.Caller.StoreID, DateFrom: pgDate(q.From), DateTo: pgDate(q.To)})
	if err != nil {
		return nil, err
	}
	total := &itemAccum{item: ItemReportRow{ItemName: "合计"}}
	for _, row := range rows {
		acc := &itemAccum{item: ItemReportRow{ItemID: row.ItemID, ItemName: row.ItemName, ItemKind: row.ItemKind, Times: row.Times, Designated: row.Designated}}
		if err := addSigned(&acc.linePrice, row.LinePrice); err != nil {
			return nil, err
		}
		if err := addSigned(&acc.paid, row.Paid); err != nil {
			return nil, err
		}
		if err := addSigned(&acc.cardPaid, row.CardPaid); err != nil {
			return nil, err
		}
		if err := addSigned(&acc.countPaid, row.CountPaid); err != nil {
			return nil, err
		}
		if err := addSigned(&acc.groupbuy, row.GroupbuyPaid); err != nil {
			return nil, err
		}
		if err := addSigned(&acc.performance, row.Performance); err != nil {
			return nil, err
		}
		out.Rows = append(out.Rows, finishItemRow(acc))
		total.add(acc)
	}
	out.Totals = finishItemRow(total)
	return out, nil
}

func (a *itemAccum) add(other *itemAccum) {
	a.item.Times += other.item.Times
	a.item.Designated += other.item.Designated
	a.linePrice = a.linePrice.Add(other.linePrice.Change())
	a.paid = a.paid.Add(other.paid.Change())
	a.cardPaid = a.cardPaid.Add(other.cardPaid.Change())
	a.countPaid = a.countPaid.Add(other.countPaid.Change())
	a.groupbuy = a.groupbuy.Add(other.groupbuy.Change())
	a.performance = a.performance.Add(other.performance.Change())
}

func finishItemRow(acc *itemAccum) ItemReportRow {
	if acc == nil {
		acc = &itemAccum{}
	}
	row := acc.item
	row.LinePrice = acc.linePrice.Change()
	row.Paid = acc.paid.Change()
	row.CardPaid = acc.cardPaid.Change()
	row.CountPaid = acc.countPaid.Change()
	row.GroupbuyPaid = acc.groupbuy.Change()
	row.Performance = acc.performance.Change()
	return row
}
