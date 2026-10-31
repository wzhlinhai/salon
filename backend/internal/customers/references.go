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

// LockCardOpening 串行锁住本店顾客，检查查看范围和未合并，供开卡判定首购。
func LockCardOpening(ctx context.Context, env ops.Env, id uuid.UUID) (dbgen.Customer, error) {
	row, err := env.Q.LockCardOpeningCustomer(ctx, dbgen.LockCardOpeningCustomerParams{StoreID: env.Caller.StoreID, ID: id})
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

// CheckViewable 无锁按批检查每位顾客属于本店、在查看范围内且未合并，任一不符或不存在均返回未找到。
func CheckViewable(ctx context.Context, env ops.Env, ids ...uuid.UUID) error {
	rows, err := env.Q.GetCustomers(ctx, dbgen.GetCustomersParams{StoreID: env.Caller.StoreID, Ids: ids})
	if err != nil {
		return err
	}
	customers := make(map[uuid.UUID]dbgen.Customer, len(rows))
	for _, c := range rows {
		customers[c.ID] = c
	}
	for _, id := range ids {
		c, exists := customers[id]
		if !exists {
			return ops.ErrNotFound
		}
		if !CanView(env.Caller, c) || c.MergedInto != nil {
			return ops.ErrNotFound
		}
	}
	return nil
}
