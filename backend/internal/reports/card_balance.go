package reports

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// cardBalanceRowLimit 是套餐结余报表最多返回的行数，合计仍按全部符合条件的卡算。
var cardBalanceRowLimit int32 = 1000

func getCardBalanceReport(ctx context.Context, env ops.Env, in *CardBalanceReportInput) (*CardBalanceReport, error) {
	today, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, env.Now)
	if err != nil {
		return nil, err
	}
	var expiringBefore pgtype.Date
	if in.ExpiringWithinDays != nil {
		expiringBefore = pgDate(today.AddDate(0, 0, *in.ExpiringWithinDays))
	}
	totals, err := env.Q.ReportCardBalanceTotals(ctx, dbgen.ReportCardBalanceTotalsParams{StoreID: env.Caller.StoreID, Kind: in.Kind, ExpiringBefore: expiringBefore, Today: pgDate(today)})
	if err != nil {
		return nil, err
	}
	out := &CardBalanceReport{Rows: []CardBalanceRow{}, Truncated: totals.Cards > int64(cardBalanceRowLimit)}
	if out.Totals.RemainingValue, err = change(totals.RemainingValue); err != nil {
		return nil, err
	}
	if out.Totals.Principal, err = change(totals.Principal); err != nil {
		return nil, err
	}
	if out.Totals.Bonus, err = change(totals.Bonus); err != nil {
		return nil, err
	}
	out.Totals.Cards = totals.Cards
	out.Totals.RemainingCounts = totals.RemainingCounts
	out.Totals.ExpiringIn30Days = totals.ExpiringIn30Days

	rows, err := env.Q.ReportCardBalanceRows(ctx, dbgen.ReportCardBalanceRowsParams{StoreID: env.Caller.StoreID, Kind: in.Kind, ExpiringBefore: expiringBefore, RowLimit: cardBalanceRowLimit})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return out, nil
	}
	cardIDs := make([]uuid.UUID, 0, len(rows))
	customerIDs := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		cardIDs = append(cardIDs, row.CardID)
		customerIDs = append(customerIDs, row.CustomerID)
	}
	customerRows, err := env.Q.GetCustomers(ctx, dbgen.GetCustomersParams{StoreID: env.Caller.StoreID, Ids: customerIDs})
	if err != nil {
		return nil, err
	}
	customerNames := make(map[uuid.UUID]*string, len(customerRows))
	for _, c := range customerRows {
		customerNames[c.ID] = c.Name
	}
	itemRows, err := env.Q.ReportCardBalanceItems(ctx, dbgen.ReportCardBalanceItemsParams{StoreID: env.Caller.StoreID, CardIds: cardIDs})
	if err != nil {
		return nil, err
	}
	items := map[uuid.UUID][]CardBalanceItem{}
	for _, row := range itemRows {
		price, err := change(row.UnitPrice)
		if err != nil {
			return nil, err
		}
		items[row.CardID] = append(items[row.CardID], CardBalanceItem{ItemID: row.ItemID, ItemName: row.ItemName, PaidRemaining: int64(row.PaidAfter), GiftRemaining: int64(row.GiftAfter), UnitPrice: price})
	}
	for _, row := range rows {
		view := CardBalanceRow{
			CardID:          row.CardID,
			Customer:        CardBalanceCustomer{ID: row.CustomerID, Name: customerNames[row.CustomerID]},
			CardType:        CardBalanceType{ID: row.CardTypeID, Name: row.CardTypeName, Kind: row.CardTypeKind},
			Items:           items[row.CardID],
			RemainingCounts: row.RemainingCounts,
		}
		if view.Items == nil {
			view.Items = []CardBalanceItem{}
		}
		opened := settings.Date(row.OpenedAt.Time)
		if row.OpenedOn.Valid {
			opened = row.OpenedOn.Time
		}
		view.OpenedOn = dayKey(opened)
		if row.ExpiresOn.Valid {
			expires := dayKey(row.ExpiresOn.Time)
			left := int64(daysBetween(today, row.ExpiresOn.Time))
			view.ExpiresOn, view.DaysLeft = &expires, &left
		}
		if view.Principal, err = change(row.Principal); err != nil {
			return nil, err
		}
		if view.Bonus, err = change(row.Bonus); err != nil {
			return nil, err
		}
		if view.RemainingValue, err = change(row.RemainingValue); err != nil {
			return nil, err
		}
		out.Rows = append(out.Rows, view)
	}
	return out, nil
}

// daysBetween 返回两个日期之间相差的整天数，to 晚于 from 为正。
func daysBetween(from, to time.Time) int {
	a := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
	b := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, time.UTC)
	return int(b.Sub(a).Hours() / 24)
}
