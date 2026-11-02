package payroll

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

const monthLayout = "2006-01"

// CheckMonth 解析结算月并检查不早于门店开店的营业月，允许未来月份。
func CheckMonth(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, month string) (time.Time, error) {
	m, err := time.Parse(settings.DateLayout, month+"-01")
	if err != nil {
		// rule-untested: 所有月份输入均由 schema pattern 在调用前校验
		return time.Time{}, ops.Rule("结算月的格式是 YYYY-MM")
	}
	store, err := q.GetStore(ctx, storeID)
	if err != nil {
		return time.Time{}, err
	}
	opened, err := settings.BusinessDate(ctx, q, storeID, store.CreatedAt.Time)
	if err != nil {
		return time.Time{}, err
	}
	if month < opened.Format(monthLayout) {
		return time.Time{}, ops.Rule("结算月不能早于开店的月份（" + opened.Format(monthLayout) + "）")
	}
	return m, nil
}

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
	return BaseSalary{Staff: refs[row.StaffID], EffectiveMonth: row.EffectiveMonth.Time.Format(monthLayout), Mode: row.Mode, Amount: row.Amount, SavedBy: refs[row.SavedBy], SavedAt: row.SavedAt.Time}
}

// BaseSalariesInForce 批量读取本店所有员工在结算月执行的底薪，先取最大生效月，再取最后保存的一条。
func BaseSalariesInForce(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, month time.Time) (map[uuid.UUID]BaseSalary, error) {
	rows, err := q.BaseSalariesInForce(ctx, dbgen.BaseSalariesInForceParams{StoreID: storeID, Month: pgtype.Date{Time: month, Valid: true}})
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(rows)*2)
	for _, row := range rows {
		ids = append(ids, row.StaffID, row.SavedBy)
	}
	refs, err := names(ctx, q, storeID, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]BaseSalary, len(rows))
	for _, row := range rows {
		out[row.StaffID] = salaryView(row, refs)
	}
	return out, nil
}

func listBaseSalaries(ctx context.Context, env ops.Env, in *MonthInput) (*BaseSalaryList, error) {
	month, err := CheckMonth(ctx, env.Q, env.Caller.StoreID, in.Month)
	if err != nil {
		return nil, err
	}
	salaries, err := BaseSalariesInForce(ctx, env.Q, env.Caller.StoreID, month)
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
		out = append(out, PayrollAdjustment{ID: row.ID, Month: row.Month.Time.Format(monthLayout), Staff: refs[row.StaffID], Name: row.Name, Amount: row.Amount, Note: row.Note, CreatedBy: refs[row.CreatedBy], CreatedAt: row.CreatedAt.Time})
	}
	return out, nil
}

func listPayrollAdjustments(ctx context.Context, env ops.Env, in *ListPayrollAdjustmentsInput) (*PayrollAdjustmentList, error) {
	month, err := CheckMonth(ctx, env.Q, env.Caller.StoreID, in.Month)
	if err != nil {
		return nil, err
	}
	limit := in.Limit
	if limit == 0 {
		limit = 50
	}
	rows, err := env.Q.ListPayrollAdjustments(ctx, dbgen.ListPayrollAdjustmentsParams{StoreID: env.Caller.StoreID, Month: pgtype.Date{Time: month, Valid: true}, StaffID: in.StaffID, BeforeID: in.BeforeID, RowLimit: limit})
	if err != nil {
		return nil, err
	}
	entries, err := adjustmentViews(ctx, env, rows)
	return &PayrollAdjustmentList{Entries: entries}, err
}
