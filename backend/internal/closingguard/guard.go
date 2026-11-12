// Package closingguard 提供封账写入护栏：业务写入取共享咨询锁，封账和撤销封账取排他锁。
package closingguard

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

// SealedThrough 读取门店当前封账到哪一天；从未封账返回 nil。
func SealedThrough(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID) (*time.Time, error) {
	row, err := q.GetStoreSealing(ctx, storeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !row.SealedThrough.Valid {
		return nil, nil
	}
	day := row.SealedThrough.Time
	return &day, nil
}

// Guard 先取门店共享咨询锁，再拒绝已封账营业日（businessDate ≤ 封账日）的写入。
func Guard(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, businessDate time.Time) error {
	if err := q.LockClosingShared(ctx, storeID); err != nil {
		return err
	}
	sealed, err := SealedThrough(ctx, q, storeID)
	if err != nil {
		return err
	}
	if sealed != nil && !businessDate.After(*sealed) {
		return ops.Rule("该营业日已封账（" + businessDate.Format(time.DateOnly) + "）")
	}
	return nil
}
