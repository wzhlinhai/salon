package commission

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wzhlinhai/salon/backend/internal/catalog"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

// Commissions 按门店、营业日范围及可选员工批量读取业绩并计算可解释的历史提成。
func Commissions(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, from, to time.Time, staffID *uuid.UUID) ([]Commission, error) {
	entries, err := q.CommissionPerformance(ctx, dbgen.CommissionPerformanceParams{StoreID: storeID, DateFrom: pgtype.Date{Time: from, Valid: true}, DateTo: pgtype.Date{Time: to, Valid: true}, StaffID: staffID})
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
	originals, err := q.CommissionOriginalEntries(ctx, dbgen.CommissionOriginalEntriesParams{StoreID: storeID, Ids: ids})
	if err != nil {
		return nil, err
	}
	rows, err := q.CommissionLineFacts(ctx, dbgen.CommissionLineFactsParams{StoreID: storeID, Ids: lines})
	if err != nil {
		return nil, err
	}
	facts := map[lineKey]lineFact{}
	for _, r := range rows {
		facts[lineKey{r.ID, optional(r.CheckoutID)}] = lineFact{itemID: r.ItemID, price: r.Price, paid: r.LinePaid, designated: r.Designated}
	}
	versions, err := q.AllCommissionSchemeVersions(ctx, storeID)
	if err != nil {
		return nil, err
	}
	out, err := calculate(entries, originals, facts, versions)
	if err != nil {
		return nil, err
	}
	staffIDs, positions, items := []uuid.UUID{}, []uuid.UUID{}, []uuid.UUID{}
	for _, c := range out {
		staffIDs = append(staffIDs, c.Staff.ID)
		if c.Explanation.Origin.Position != nil {
			positions = append(positions, c.Explanation.Origin.Position.ID)
		}
		if c.Explanation.Origin.Item != nil {
			items = append(items, c.Explanation.Origin.Item.ID)
		}
	}
	staffRefs, err := staff.References(ctx, q, storeID, staffIDs)
	if err != nil {
		return nil, err
	}
	positionRefs, err := staff.PositionReferences(ctx, q, storeID, positions)
	if err != nil {
		return nil, err
	}
	itemRefs, err := catalog.References(ctx, q, storeID, items)
	if err != nil {
		return nil, err
	}
	for i := range out {
		c := &out[i]
		c.Staff = staffRefs[c.Staff.ID]
		if c.Explanation.Origin.Position != nil {
			c.Explanation.Origin.Position = new(positionRefs[c.Explanation.Origin.Position.ID])
		}
		if c.Explanation.Origin.Item != nil {
			ref := itemRefs[c.Explanation.Origin.Item.ID]
			c.Explanation.Origin.Item = &staff.Ref{ID: ref.ID, Name: ref.Name}
		}
	}
	return out, nil
}
