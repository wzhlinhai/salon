package cards

import (
	"context"
	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"time"
)

// HasValidCard 判断顾客在本店是否持有营业日当天仍可用的有效卡，不检查卡种和余额。
func HasValidCard(ctx context.Context, q *dbgen.Queries, storeID, customerID uuid.UUID, day time.Time) (bool, error) {
	rows, err := q.MemberCardCandidates(ctx, dbgen.MemberCardCandidatesParams{StoreID: storeID, CustomerID: customerID})
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if checkExpiry(row, day) == nil {
			return true, nil
		}
	}
	return false, nil
}
