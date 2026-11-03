package payrollmonth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// Settlement 是结算月的锁定记录。
type Settlement struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	Month     Month
	SettledBy uuid.UUID
	SettledAt time.Time
}

// Status 是结算月的锁定状态与可锁定标记。
type Status struct {
	Month      Month
	Settlement *Settlement
	Lockable   bool
}

// storeMonths 一次读取门店与营业日，首月为开店月，其余为传入时刻的结算月。
func storeMonths(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, times ...time.Time) (uuid.UUID, []Month, error) {
	store, err := q.GetStore(ctx, storeID)
	if err != nil {
		return uuid.Nil, nil, err
	}
	dates := append([]time.Time{store.CreatedAt.Time}, times...)
	days, err := settings.BusinessDates(ctx, q, storeID, dates)
	if err != nil {
		return uuid.Nil, nil, err
	}
	months := make([]Month, len(days))
	for i, day := range days {
		months[i] = Of(day)
	}
	return store.TenantID, months, nil
}

func check(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, m Month) (uuid.UUID, Month, error) {
	tenantID, months, err := storeMonths(ctx, q, storeID)
	if err != nil {
		return uuid.Nil, Month{}, err
	}
	return tenantID, months[0], floor(m, months[0])
}

func floor(m, opened Month) error {
	if m.Before(opened) {
		return ops.Rule("结算月不能早于开店的月份（" + opened.String() + "）")
	}
	return nil
}

// Check 解析结算月并检查开店月下限，允许未来月份。
func Check(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, s string) (Month, error) {
	m, err := Parse(s)
	if err != nil {
		return Month{}, err
	}
	_, _, err = check(ctx, q, storeID, m)
	return m, err
}

func settlement(row dbgen.CommissionSettlement) *Settlement {
	return &Settlement{ID: row.ID, TenantID: row.TenantID, Month: Of(row.Month.Time), SettledBy: row.SettledBy, SettledAt: row.SettledAt.Time}
}

// GetSettlement 读取结算月锁定记录，未锁定时返回 nil。
func GetSettlement(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, m Month) (*Settlement, error) {
	row, err := q.GetCommissionSettlement(ctx, dbgen.GetCommissionSettlementParams{StoreID: storeID, Month: pgtype.Date{Time: m.First(), Valid: true}})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return settlement(row), nil
}

// Guard 按咨询锁、开店月下限、未锁定的顺序保护结算月写入。
func Guard(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, m Month, action string) error {
	if err := q.LockPayroll(ctx, storeID); err != nil {
		return err
	}
	if _, _, err := check(ctx, q, storeID, m); err != nil {
		return err
	}
	locked, err := GetSettlement(ctx, q, storeID, m)
	if err != nil {
		return err
	}
	if locked != nil {
		return ops.Rule(m.String() + " 已经锁定，不能再" + action)
	}
	return nil
}

func lockable(m, opened, current Month, locked, previous bool) bool {
	return !locked && m.Before(current) && (m == opened || previous)
}

// List 从当前营业月倒序列出到开店月的结算状态。
func List(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID) ([]Status, error) {
	_, months, err := storeMonths(ctx, q, storeID, time.Now())
	if err != nil {
		return nil, err
	}
	opened, current := months[0], months[1]
	rows, err := q.ListCommissionSettlements(ctx, storeID)
	if err != nil {
		return nil, err
	}
	locked := map[Month]*Settlement{}
	for _, row := range rows {
		s := settlement(row)
		locked[s.Month] = s
	}
	out := []Status{}
	for m := current; !m.Before(opened); m = m.Prev() {
		out = append(out, Status{Month: m, Settlement: locked[m], Lockable: lockable(m, opened, current, locked[m] != nil, locked[m.Prev()] != nil)})
	}
	return out, nil
}

// Lock 检查锁定条件并写入结算月锁定记录，调用者在同一事务保存提成和工资表行。
func Lock(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, s string, by uuid.UUID) (*Settlement, error) {
	if err := q.LockPayroll(ctx, storeID); err != nil {
		return nil, err
	}
	m, err := Parse(s)
	if err != nil {
		return nil, err
	}
	tenantID, opened, err := check(ctx, q, storeID, m)
	if err != nil {
		return nil, err
	}
	existing, err := GetSettlement(ctx, q, storeID, m)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, ops.Rule(m.String() + " 已经锁定")
	}
	today, err := settings.BusinessDate(ctx, q, storeID, time.Now())
	if err != nil {
		return nil, err
	}
	if !today.After(m.Last()) {
		return nil, ops.Rule(m.String() + " 还没结束，不能锁定")
	}
	if m != opened {
		previous, err := GetSettlement(ctx, q, storeID, m.Prev())
		if err != nil {
			return nil, err
		}
		if previous == nil {
			return nil, ops.Rule("请先锁定 " + m.Prev().String())
		}
	}
	row, err := q.AddCommissionSettlement(ctx, dbgen.AddCommissionSettlementParams{TenantID: tenantID, StoreID: storeID, Month: pgtype.Date{Time: m.First(), Valid: true}, SettledBy: by})
	if err != nil {
		return nil, err
	}
	return settlement(row), nil
}
