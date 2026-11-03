package commission

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/payrollmonth"
)

// Settle 保存结算月的全部提成及名称解析前的解释。
func Settle(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, settlement payrollmonth.Settlement) error {
	rows, err := liveCommissions(ctx, q, storeID, settlement.Month.First(), settlement.Month.Last(), nil)
	if err != nil {
		return err
	}
	date := pgtype.Date{Time: settlement.Month.First(), Valid: true}
	for _, c := range rows {
		explanation, err := json.Marshal(c.Explanation)
		if err != nil {
			return err
		}
		if err := q.AddSettledCommission(ctx, dbgen.AddSettledCommissionParams{TenantID: settlement.TenantID, StoreID: storeID, SettlementID: settlement.ID, Month: date, ID: c.ID, BusinessDate: pgtype.Date{Time: c.businessDate, Valid: true}, Source: c.Source, StaffID: c.Staff.ID, Amount: c.Amount, Reversal: c.Reversal, Explanation: explanation, UnitCreatedAt: pgtype.Timestamptz{Time: c.createdAt, Valid: true}}); err != nil {
			return err
		}
	}
	return nil
}
