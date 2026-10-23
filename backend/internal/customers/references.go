package customers

import (
	"context"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

// LockReference 共享锁住本店顾客并检查查看范围和未合并；不存在时返回查询的 ErrNoRows。
func LockReference(ctx context.Context, env ops.Env, id uuid.UUID) (dbgen.Customer, error) {
	row, err := env.Q.ShareCustomer(ctx, dbgen.ShareCustomerParams{StoreID: env.Caller.StoreID, ID: id})
	if err != nil {
		return row, err
	}
	return row, checkReference(env, row)
}

func checkReference(env ops.Env, row dbgen.Customer) error {
	if !canView(env.Caller, row) {
		return ops.ErrNotFound
	}
	if row.MergedInto != nil {
		return ops.Rule("该顾客已合并到另一位顾客")
	}
	return nil
}
