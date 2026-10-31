package catalog

import (
	"context"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

// References 批量读取本店项目的标识和名称，允许任何状态，不加行锁。
func References(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]staff.Ref, error) {
	rows, err := q.GetItems(ctx, dbgen.GetItemsParams{StoreID: storeID, Ids: ids})
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]staff.Ref, len(rows))
	for _, row := range rows {
		out[row.ID] = staff.Ref{ID: row.ID, Name: row.Name}
	}
	for _, id := range ids {
		if _, ok := out[id]; !ok {
			return nil, ops.ErrNotFound
		}
	}
	return out, nil
}
