package performance

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

// Sale 是开卡、充值、补差价或卖红包的销售来源，归属人保留输入顺序。
type Sale struct {
	Method       string
	Value        money.Total
	StaffIDs     []uuid.UUID
	BusinessDate time.Time
	CardLedgerID *uuid.UUID
	RedPacketID  *uuid.UUID
	Purchase     string
}

type saleShare struct {
	staffID uuid.UUID
	amount  money.Total
}

func splitSale(value money.Total, staffIDs []uuid.UUID) []saleShare {
	out := []saleShare{}
	if len(staffIDs) == 0 {
		return out
	}
	share, remainder := value.Split(len(staffIDs))
	for i, id := range staffIDs {
		paid := share
		if i == 0 {
			paid = paid.AddTotal(remainder)
		}
		if paid.Cmp(money.Amount{}) == 0 {
			continue
		}
		out = append(out, saleShare{staffID: id, amount: paid})
	}
	return out
}

// RecordSale 在来源事务内按归属人均分销售业绩，不应用支付方式比例。
func RecordSale(ctx context.Context, env ops.Env, sale Sale) error {
	for _, share := range splitSale(sale.Value, sale.StaffIDs) {
		var paid money.Amount
		if err := paid.Scan(share.amount.Format()); err != nil {
			return ops.Rule("销售业绩金额超过上限")
		}
		if err := env.Q.AddSalePerformance(ctx, dbgen.AddSalePerformanceParams{
			TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID,
			BusinessDate: pgtype.Date{Time: sale.BusinessDate, Valid: true},
			StaffID:      &share.staffID, Method: sale.Method, PaidAmount: paid,
			CardLedgerID: sale.CardLedgerID, RedPacketID: sale.RedPacketID, Purchase: &sale.Purchase,
		}); err != nil {
			return err
		}
	}
	return nil
}
