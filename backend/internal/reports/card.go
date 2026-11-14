package reports

import (
	"context"
	"sort"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

type cardAccum struct {
	row               CardReportRow
	openPrincipal     money.SignedTotal
	openBonus         money.SignedTotal
	rechargePrincipal money.SignedTotal
	rechargeBonus     money.SignedTotal
	upgrade           money.SignedTotal
	consumedPrincipal money.SignedTotal
	consumedBonus     money.SignedTotal
	countAmount       money.SignedTotal
	refundAmount      money.SignedTotal
	endingBalance     money.SignedTotal
}

func getCardReport(ctx context.Context, env ops.Env, in *CardReportInput) (*CardReport, error) {
	q, err := dateRange(env.Caller, perm.ReportView, in.From, in.To)
	if err != nil {
		return nil, err
	}
	out := &CardReport{Rows: []CardReportRow{}, Totals: CardReportRow{CardType: CardTypeRef{Name: "合计"}}}
	if q.Empty {
		return out, nil
	}
	types, err := env.Q.ReportCardTypes(ctx, env.Caller.StoreID)
	if err != nil {
		return nil, err
	}
	rows := map[uuid.UUID]*cardAccum{}
	order := []uuid.UUID{}
	for _, typ := range types {
		rows[typ.ID] = &cardAccum{row: CardReportRow{CardType: CardTypeRef{ID: typ.ID, Name: typ.Name}}}
		order = append(order, typ.ID)
	}
	get := func(id uuid.UUID) *cardAccum {
		if rows[id] == nil {
			rows[id] = &cardAccum{row: CardReportRow{CardType: CardTypeRef{ID: id}}}
			order = append(order, id)
		}
		return rows[id]
	}
	from, to := pgDate(q.From), pgDate(q.To)
	sales, err := env.Q.ReportCardSales(ctx, dbgen.ReportCardSalesParams{StoreID: env.Caller.StoreID, DateFrom: from, DateTo: to})
	if err != nil {
		return nil, err
	}
	for _, row := range sales {
		acc := get(row.CardTypeID)
		acc.row.OpenCount += row.OpenCount
		if err := addSigned(&acc.openPrincipal, row.OpenPrincipal); err != nil {
			return nil, err
		}
		if err := addSigned(&acc.openBonus, row.OpenBonus); err != nil {
			return nil, err
		}
		if err := addSigned(&acc.rechargePrincipal, row.RechargePrincipal); err != nil {
			return nil, err
		}
		if err := addSigned(&acc.rechargeBonus, row.RechargeBonus); err != nil {
			return nil, err
		}
		if err := addSigned(&acc.upgrade, row.Upgrade); err != nil {
			return nil, err
		}
	}
	consumption, err := env.Q.ReportCardConsumption(ctx, dbgen.ReportCardConsumptionParams{StoreID: env.Caller.StoreID, DateFrom: from, DateTo: to})
	if err != nil {
		return nil, err
	}
	for _, row := range consumption {
		acc := get(row.CardTypeID)
		if err := addSigned(&acc.consumedPrincipal, row.ConsumedPrincipal); err != nil {
			return nil, err
		}
		if err := addSigned(&acc.consumedBonus, row.ConsumedBonus); err != nil {
			return nil, err
		}
	}
	counts, err := env.Q.ReportCardCounts(ctx, dbgen.ReportCardCountsParams{StoreID: env.Caller.StoreID, DateFrom: from, DateTo: to})
	if err != nil {
		return nil, err
	}
	for _, row := range counts {
		acc := get(row.CardTypeID)
		acc.row.CountUsed += row.CountUsed
		if err := addSigned(&acc.countAmount, row.CountAmount); err != nil {
			return nil, err
		}
	}
	refunds, err := env.Q.ReportCardRefunds(ctx, dbgen.ReportCardRefundsParams{StoreID: env.Caller.StoreID, DateFrom: from, DateTo: to})
	if err != nil {
		return nil, err
	}
	for _, row := range refunds {
		acc := get(row.CardTypeID)
		acc.row.RefundCount += row.RefundCount
		if err := addSigned(&acc.refundAmount, row.RefundAmount); err != nil {
			return nil, err
		}
	}
	balances, err := env.Q.ReportCardBalances(ctx, dbgen.ReportCardBalancesParams{StoreID: env.Caller.StoreID, DateTo: to})
	if err != nil {
		return nil, err
	}
	for _, row := range balances {
		if err := addSigned(&get(row.CardTypeID).endingBalance, row.EndingBalance); err != nil {
			return nil, err
		}
	}
	remaining, err := env.Q.ReportCardRemainingCounts(ctx, dbgen.ReportCardRemainingCountsParams{StoreID: env.Caller.StoreID, DateTo: to})
	if err != nil {
		return nil, err
	}
	for _, row := range remaining {
		get(row.CardTypeID).row.EndingCounts += row.EndingCounts
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := rows[order[i]].row.CardType, rows[order[j]].row.CardType
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.ID.String() < b.ID.String()
	})
	total := &cardAccum{}
	for _, id := range order {
		acc := rows[id]
		total.add(acc)
		if acc.empty() {
			continue
		}
		out.Rows = append(out.Rows, finishCardRow(acc))
	}
	out.Totals = finishCardRow(total)
	return out, nil
}

func (a *cardAccum) add(other *cardAccum) {
	a.row.OpenCount += other.row.OpenCount
	a.row.CountUsed += other.row.CountUsed
	a.row.RefundCount += other.row.RefundCount
	a.row.EndingCounts += other.row.EndingCounts
	a.openPrincipal = a.openPrincipal.Add(other.openPrincipal.Change())
	a.openBonus = a.openBonus.Add(other.openBonus.Change())
	a.rechargePrincipal = a.rechargePrincipal.Add(other.rechargePrincipal.Change())
	a.rechargeBonus = a.rechargeBonus.Add(other.rechargeBonus.Change())
	a.upgrade = a.upgrade.Add(other.upgrade.Change())
	a.consumedPrincipal = a.consumedPrincipal.Add(other.consumedPrincipal.Change())
	a.consumedBonus = a.consumedBonus.Add(other.consumedBonus.Change())
	a.countAmount = a.countAmount.Add(other.countAmount.Change())
	a.refundAmount = a.refundAmount.Add(other.refundAmount.Change())
	a.endingBalance = a.endingBalance.Add(other.endingBalance.Change())
}

func (a *cardAccum) empty() bool {
	return a.row.OpenCount == 0 && a.row.CountUsed == 0 && a.row.RefundCount == 0 && a.row.EndingCounts == 0 &&
		a.openPrincipal.Change().Fen() == 0 && a.openBonus.Change().Fen() == 0 &&
		a.rechargePrincipal.Change().Fen() == 0 && a.rechargeBonus.Change().Fen() == 0 &&
		a.upgrade.Change().Fen() == 0 && a.consumedPrincipal.Change().Fen() == 0 &&
		a.consumedBonus.Change().Fen() == 0 && a.countAmount.Change().Fen() == 0 &&
		a.refundAmount.Change().Fen() == 0 && a.endingBalance.Change().Fen() == 0
}

func finishCardRow(acc *cardAccum) CardReportRow {
	if acc == nil {
		acc = &cardAccum{}
	}
	row := acc.row
	row.OpenPrincipal = acc.openPrincipal.Change()
	row.OpenBonus = acc.openBonus.Change()
	row.RechargePrincipal = acc.rechargePrincipal.Change()
	row.RechargeBonus = acc.rechargeBonus.Change()
	row.Upgrade = acc.upgrade.Change()
	row.ConsumedPrincipal = acc.consumedPrincipal.Change()
	row.ConsumedBonus = acc.consumedBonus.Change()
	row.CountAmount = acc.countAmount.Change()
	row.RefundAmount = acc.refundAmount.Change()
	row.EndingBalance = acc.endingBalance.Change()
	return row
}
