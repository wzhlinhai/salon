package payroll

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/inforce"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/payrollmonth"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

func names(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]staff.Ref, error) {
	rows, err := q.StaffNames(ctx, dbgen.StaffNamesParams{StoreID: storeID, Ids: ids})
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]staff.Ref, len(rows))
	for _, row := range rows {
		out[row.ID] = staff.Ref{ID: row.ID, Name: row.Name}
	}
	return out, nil
}

func salaryView(row dbgen.StaffBaseSalary, refs map[uuid.UUID]staff.Ref) BaseSalary {
	return BaseSalary{Staff: refs[row.StaffID], EffectiveMonth: payrollmonth.Of(row.EffectiveMonth.Time).String(), Mode: row.Mode, Amount: row.Amount, SavedBy: refs[row.SavedBy], SavedAt: row.SavedAt.Time}
}

// BaseSalariesInForce 批量读取本店所有员工在结算月执行的底薪：生效月不晚于结算月的最大者，同月取最后保存的一条。
func BaseSalariesInForce(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, month time.Time) (map[uuid.UUID]BaseSalary, error) {
	rows, err := q.BaseSalaryHistory(ctx, storeID)
	if err != nil {
		return nil, err
	}
	changes := make([]inforce.Change[dbgen.StaffBaseSalary], 0, len(rows))
	for _, row := range rows {
		changes = append(changes, inforce.Change[dbgen.StaffBaseSalary]{From: row.EffectiveMonth.Time, SavedAt: row.SavedAt.Time, ID: row.ID, Value: row})
	}
	var inForce []dbgen.StaffBaseSalary
	for _, timeline := range inforce.Group(changes, func(c inforce.Change[dbgen.StaffBaseSalary]) uuid.UUID { return c.Value.StaffID }) {
		if c, ok := timeline.At(month); ok {
			inForce = append(inForce, c.Value)
		}
	}
	ids := make([]uuid.UUID, 0, len(inForce)*2)
	for _, row := range inForce {
		ids = append(ids, row.StaffID, row.SavedBy)
	}
	refs, err := names(ctx, q, storeID, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]BaseSalary, len(inForce))
	for _, row := range inForce {
		out[row.StaffID] = salaryView(row, refs)
	}
	return out, nil
}

func listBaseSalaries(ctx context.Context, env ops.Env, in *MonthInput) (*BaseSalaryList, error) {
	month, err := payrollmonth.Check(ctx, env.Q, env.Caller.StoreID, in.Month)
	if err != nil {
		return nil, err
	}
	salaries, err := BaseSalariesInForce(ctx, env.Q, env.Caller.StoreID, month.First())
	if err != nil {
		return nil, err
	}
	rows, err := env.Q.ListPayrollStaff(ctx, env.Caller.StoreID)
	if err != nil {
		return nil, err
	}
	out := &BaseSalaryList{Entries: []StaffBaseSalary{}}
	for _, row := range rows {
		entry := StaffBaseSalary{Staff: staff.Ref{ID: row.ID, Name: row.Name}, Active: row.Active}
		if salary, ok := salaries[row.ID]; ok {
			entry.BaseSalary = &salary
		}
		out.Entries = append(out.Entries, entry)
	}
	return out, nil
}

func adjustmentViews(ctx context.Context, env ops.Env, rows []dbgen.PayrollAdjustment) ([]PayrollAdjustment, error) {
	ids := make([]uuid.UUID, 0, len(rows)*2)
	for _, row := range rows {
		ids = append(ids, row.StaffID, row.CreatedBy)
	}
	refs, err := names(ctx, env.Q, env.Caller.StoreID, ids)
	if err != nil {
		return nil, err
	}
	out := make([]PayrollAdjustment, 0, len(rows))
	for _, row := range rows {
		out = append(out, PayrollAdjustment{ID: row.ID, Month: payrollmonth.Of(row.Month.Time).String(), Staff: refs[row.StaffID], Name: row.Name, Amount: row.Amount, Note: row.Note, CreatedBy: refs[row.CreatedBy], CreatedAt: row.CreatedAt.Time})
	}
	return out, nil
}

func listPayrollAdjustments(ctx context.Context, env ops.Env, in *ListPayrollAdjustmentsInput) (*PayrollAdjustmentList, error) {
	month, err := payrollmonth.Check(ctx, env.Q, env.Caller.StoreID, in.Month)
	if err != nil {
		return nil, err
	}
	limit := in.Limit
	if limit == 0 {
		limit = 50
	}
	rows, err := env.Q.ListPayrollAdjustments(ctx, dbgen.ListPayrollAdjustmentsParams{StoreID: env.Caller.StoreID, Month: pgtype.Date{Time: month.First(), Valid: true}, StaffID: in.StaffID, BeforeID: in.BeforeID, RowLimit: limit})
	if err != nil {
		return nil, err
	}
	entries, err := adjustmentViews(ctx, env, rows)
	return &PayrollAdjustmentList{Entries: entries}, err
}
