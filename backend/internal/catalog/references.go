package catalog

import (
	"context"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

// LockReference 共享锁住本店项目并确认启用；不存在时返回查询的 ErrNoRows。
func LockReference(ctx context.Context, q *dbgen.Queries, storeID, id uuid.UUID) (dbgen.Item, error) {
	rows, err := LockItems(ctx, q, storeID, id)
	if err != nil {
		return dbgen.Item{}, err
	}
	row := rows[id]
	if !row.Active {
		return row, ops.Rule("项目已停用")
	}
	return row, nil
}

// LockItems 按 UUID 共享锁住本店项目，允许编辑卡类型时保留未改动的停用项目引用。
func LockItems(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, ids ...uuid.UUID) (map[uuid.UUID]dbgen.Item, error) {
	ordered := slices.Clone(ids)
	slices.SortFunc(ordered, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	out := make(map[uuid.UUID]dbgen.Item, len(ids))
	for _, id := range slices.Compact(ordered) {
		row, err := q.ShareItem(ctx, dbgen.ShareItemParams{StoreID: storeID, ID: id})
		if err != nil {
			return nil, err
		}
		out[id] = row
	}
	return out, nil
}
