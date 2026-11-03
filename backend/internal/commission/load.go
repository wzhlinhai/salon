package commission

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wzhlinhai/salon/backend/internal/catalog"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/payrollmonth"
	"github.com/wzhlinhai/salon/backend/internal/performance"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

// Commissions 按门店、营业日范围及可选员工批量读取业绩并计算可解释的历史提成。
func Commissions(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, from, to time.Time, staffID *uuid.UUID) ([]Commission, error) {
	rows, err := liveCommissions(ctx, q, storeID, from, to, staffID)
	if err != nil {
		return nil, err
	}
	// 锁定标记和明细必须取自同一语句快照，不能分别读取后拼接。
	stored, err := q.SettledCommissions(ctx, dbgen.SettledCommissionsParams{StoreID: storeID, DateFrom: pgtype.Date{Time: payrollmonth.Of(from).First(), Valid: true}, DateTo: pgtype.Date{Time: payrollmonth.Of(to).First(), Valid: true}, StaffID: staffID})
	if err != nil {
		return nil, err
	}
	locked := map[payrollmonth.Month]bool{}
	for _, row := range stored {
		locked[payrollmonth.Of(row.SettlementMonth.Time)] = true
	}
	rows = slices.DeleteFunc(rows, func(c Commission) bool {
		return locked[payrollmonth.Of(c.businessDate)]
	})
	for _, row := range stored {
		if row.ID == nil {
			continue
		}
		c := Commission{ID: *row.ID, BusinessDate: row.BusinessDate.Time.Format(time.DateOnly), Source: *row.Source, Staff: staff.Ref{ID: *row.StaffID}, Reversal: *row.Reversal, createdAt: row.UnitCreatedAt.Time, businessDate: row.BusinessDate.Time}
		if err := c.Amount.Scan(row.Amount); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(row.Explanation, &c.Explanation); err != nil {
			return nil, err
		}
		rows = append(rows, c)
	}
	out := slices.DeleteFunc(rows, func(c Commission) bool {
		return c.BusinessDate < from.Format(time.DateOnly) || c.BusinessDate > to.Format(time.DateOnly)
	})
	sortCommissions(out)
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

func liveCommissions(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, from, to time.Time, staffID *uuid.UUID) ([]Commission, error) {
	monthFrom := payrollmonth.Of(from).First()
	monthTo := payrollmonth.Of(to).Last()
	units, err := performance.Units(ctx, q, storeID, monthFrom, monthTo, staffID)
	if err != nil {
		return nil, err
	}
	versions, err := q.AllCommissionSchemeVersions(ctx, storeID)
	if err != nil {
		return nil, err
	}
	out, err := calculate(units, versions)
	if err != nil {
		return nil, err
	}
	return out, nil
}
