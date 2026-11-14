package reports

import (
	"context"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

func getCustomerSourceReport(ctx context.Context, env ops.Env, in *CustomerSourceReportInput) (*CustomerSourceReport, error) {
	q, err := dateRange(env.Caller, perm.ReportView, in.From, in.To)
	if err != nil {
		return nil, err
	}
	out := &CustomerSourceReport{Rows: []CustomerSourceReportRow{}, Totals: CustomerSourceReportRow{}}
	if q.Empty {
		return out, nil
	}
	from, to := pgDate(q.From), pgDate(q.To)
	rows, err := env.Q.CustomerSourceReport(ctx, dbgen.CustomerSourceReportParams{StoreID: env.Caller.StoreID, DateFrom: from, DateTo: to})
	if err != nil {
		return nil, err
	}
	var revenue money.SignedTotal
	for _, row := range rows {
		value, err := change(row.Revenue)
		if err != nil {
			return nil, err
		}
		var sourceID *uuid.UUID
		var sourceName *string
		if row.SourceID != uuid.Nil {
			sourceID = &row.SourceID
			sourceName = &row.SourceName
		}
		out.Rows = append(out.Rows, CustomerSourceReportRow{SourceID: sourceID, SourceName: sourceName, NewCustomers: row.NewCustomers, Revenue: value, Returning: row.Returning})
		out.Totals.NewCustomers += row.NewCustomers
		out.Totals.Returning += row.Returning
		revenue = revenue.Add(value)
	}
	out.Totals.Revenue = revenue.Change()
	out.UnidentifiedOrders, err = env.Q.UnidentifiedCheckoutOrders(ctx, dbgen.UnidentifiedCheckoutOrdersParams{StoreID: env.Caller.StoreID, DateFrom: from, DateTo: to})
	if err != nil {
		return nil, err
	}
	return out, nil
}
