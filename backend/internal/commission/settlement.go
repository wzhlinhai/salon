package commission

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
)

func monthStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// Settlement 返回门店结算月的提成结算，未锁定时返回 nil。
func Settlement(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, month time.Time) (*dbgen.CommissionSettlement, error) {
	row, err := q.GetCommissionSettlement(ctx, dbgen.GetCommissionSettlementParams{StoreID: storeID, Month: pgtype.Date{Time: month, Valid: true}})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// Settle 在调用者已持有 LockPayroll 的事务中保存整月全部提成及名称解析前的解释。
func Settle(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, month time.Time, settledBy uuid.UUID) (*dbgen.CommissionSettlement, error) {
	rows, err := liveCommissions(ctx, q, storeID, month, month.AddDate(0, 1, -1), nil)
	if err != nil {
		return nil, err
	}
	store, err := q.GetStore(ctx, storeID)
	if err != nil {
		return nil, err
	}
	date := pgtype.Date{Time: month, Valid: true}
	settlement, err := q.AddCommissionSettlement(ctx, dbgen.AddCommissionSettlementParams{TenantID: store.TenantID, StoreID: storeID, Month: date, SettledBy: settledBy})
	if err != nil {
		return nil, err
	}
	for _, c := range rows {
		explanation, err := json.Marshal(c.Explanation)
		if err != nil {
			return nil, err
		}
		day, err := time.Parse(time.DateOnly, c.BusinessDate)
		if err != nil {
			return nil, err
		}
		if err := q.AddSettledCommission(ctx, dbgen.AddSettledCommissionParams{TenantID: store.TenantID, StoreID: storeID, SettlementID: settlement.ID, Month: date, ID: c.ID, BusinessDate: pgtype.Date{Time: day, Valid: true}, Source: c.Source, StaffID: c.Staff.ID, Amount: c.Amount, Reversal: c.Reversal, Explanation: explanation, UnitCreatedAt: pgtype.Timestamptz{Time: c.createdAt, Valid: true}}); err != nil {
			return nil, err
		}
	}
	return &settlement, nil
}
