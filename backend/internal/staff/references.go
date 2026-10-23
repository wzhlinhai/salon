package staff

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

// ErrNotAssignable 表示员工不存在、不在本店或已离职，不能用于新的指派。
var ErrNotAssignable = errors.New("员工必须是本店在职员工")

// LockReferences 按 UUID 共享锁住本店员工；允许离职员工用于交接来源等历史引用。
func LockReferences(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, ids ...uuid.UUID) (map[uuid.UUID]dbgen.Staff, error) {
	ordered := slices.Clone(ids)
	slices.SortFunc(ordered, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	out := make(map[uuid.UUID]dbgen.Staff, len(ids))
	for _, id := range slices.Compact(ordered) {
		row, err := q.ShareStaff(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && row.StoreID != storeID {
			return nil, ops.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		out[id] = row
	}
	return out, nil
}

// LockAssignable 按固定顺序共享锁住员工，并确认他们均为本店在职员工。
func LockAssignable(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, ids ...uuid.UUID) error {
	rows, err := LockReferences(ctx, q, storeID, ids...)
	if errors.Is(err, ops.ErrNotFound) {
		return ErrNotAssignable
	}
	if err != nil {
		return err
	}
	for _, row := range rows {
		if !row.Active {
			return ErrNotAssignable
		}
	}
	return nil
}
