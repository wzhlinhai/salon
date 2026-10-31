package staff

import (
	"context"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

// References 批量读取本店员工的标识和名称，允许任何状态，不加行锁。
func References(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]Ref, error) {
	rows, err := q.StaffNames(ctx, dbgen.StaffNamesParams{StoreID: storeID, Ids: ids})
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]Ref, len(rows))
	for _, row := range rows {
		out[row.ID] = Ref{ID: row.ID, Name: row.Name}
	}
	for _, id := range ids {
		if _, ok := out[id]; !ok {
			return nil, ops.ErrNotFound
		}
	}
	return out, nil
}

// PositionReferences 批量读取本店岗位的标识和名称，允许任何状态，不加行锁。
func PositionReferences(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]Ref, error) {
	rows, err := q.PositionNames(ctx, dbgen.PositionNamesParams{StoreID: storeID, Ids: ids})
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]Ref, len(rows))
	for _, row := range rows {
		out[row.ID] = Ref{ID: row.ID, Name: row.Name}
	}
	for _, id := range ids {
		if _, ok := out[id]; !ok {
			return nil, ops.ErrNotFound
		}
	}
	return out, nil
}
