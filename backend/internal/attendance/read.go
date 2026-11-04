package attendance

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/inforce"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

func names(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]staff.Ref, error) {
	rows, err := q.StaffNames(ctx, dbgen.StaffNamesParams{StoreID: storeID, Ids: ids})
	if err != nil {
		return nil, err
	}
	refs := make(map[uuid.UUID]staff.Ref, len(rows))
	for _, row := range rows {
		refs[row.ID] = staff.Ref{ID: row.ID, Name: row.Name}
	}
	return refs, nil
}

func clockTime(t pgtype.Time) string {
	minutes := t.Microseconds / (60 * 1000000)
	return fmt.Sprintf("%02d:%02d", minutes/60, minutes%60)
}

func shiftView(row dbgen.Shift, refs map[uuid.UUID]staff.Ref) Shift {
	return Shift{ID: row.ID, Name: row.Name, StartTime: clockTime(row.StartTime), EndTime: clockTime(row.EndTime), Active: row.Active, CreatedBy: refs[row.CreatedBy], CreatedAt: row.CreatedAt.Time}
}

// StaffShiftTimelines 批量加载本店全部员工班次及班次时间，按员工构造可查询任意营业日的生效历史。
func StaffShiftTimelines(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID) (map[uuid.UUID]inforce.Timeline[StaffShift], error) {
	rows, err := q.StaffShiftHistory(ctx, storeID)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(rows)*3)
	for _, row := range rows {
		ids = append(ids, row.StaffID, row.SavedBy)
		if row.CreatedBy != nil {
			ids = append(ids, *row.CreatedBy)
		}
	}
	refs, err := names(ctx, q, storeID, ids)
	if err != nil {
		return nil, err
	}
	changes := make([]inforce.Change[StaffShift], 0, len(rows))
	for _, row := range rows {
		view := StaffShift{Staff: refs[row.StaffID], EffectiveFrom: row.EffectiveFrom.Time.Format(settings.DateLayout), SavedBy: refs[row.SavedBy], SavedAt: row.SavedAt.Time}
		if row.ShiftID != nil {
			view.Shift = &Shift{ID: *row.ShiftID, Name: *row.ShiftName, StartTime: clockTime(row.StartTime), EndTime: clockTime(row.EndTime), Active: *row.ShiftActive, CreatedBy: refs[*row.CreatedBy], CreatedAt: row.CreatedAt.Time}
		}
		changes = append(changes, inforce.Change[StaffShift]{From: row.EffectiveFrom.Time, SavedAt: row.SavedAt.Time, ID: row.ID, Value: view})
	}
	return inforce.Group(changes, func(c inforce.Change[StaffShift]) uuid.UUID { return c.Value.Staff.ID }), nil
}

func listShifts(ctx context.Context, env ops.Env, _ *ListShiftsInput) (*ShiftList, error) {
	rows, err := env.Q.ListShifts(ctx, env.Caller.StoreID)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.CreatedBy)
	}
	refs, err := names(ctx, env.Q, env.Caller.StoreID, ids)
	if err != nil {
		return nil, err
	}
	out := &ShiftList{Entries: []Shift{}}
	for _, row := range rows {
		out.Entries = append(out.Entries, shiftView(row, refs))
	}
	return out, nil
}

func listStaffShifts(ctx context.Context, env ops.Env, in *ListStaffShiftsInput) (*StaffShiftList, error) {
	day, err := time.Parse(settings.DateLayout, in.Date)
	if err != nil {
		return nil, err
	}
	timelines, err := StaffShiftTimelines(ctx, env.Q, env.Caller.StoreID)
	if err != nil {
		return nil, err
	}
	rows, err := env.Q.ListPayrollStaff(ctx, env.Caller.StoreID)
	if err != nil {
		return nil, err
	}
	out := &StaffShiftList{Entries: []StaffShiftEntry{}}
	for _, row := range rows {
		entry := StaffShiftEntry{Staff: staff.Ref{ID: row.ID, Name: row.Name}, Active: row.Active, Upcoming: []StaffShift{}}
		timeline := timelines[row.ID]
		if c, ok := timeline.At(day); ok {
			entry.Current = &c.Value
		}
		for _, c := range timeline.After(day) {
			entry.Upcoming = append(entry.Upcoming, c.Value)
		}
		out.Entries = append(out.Entries, entry)
	}
	return out, nil
}
