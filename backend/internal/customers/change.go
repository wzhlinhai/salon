package customers

import (
	"context"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

// selection 选出并锁住要改的顾客；只改普通列（手机号仅有部分唯一索引），两种选择方式都用 FOR NO KEY UPDATE。
type selection func(context.Context, ops.Env) ([]dbgen.Customer, error)

// byIDs 按 ID 排序加锁并检查查看范围和未合并，返回顺序与传入顺序相同。
func byIDs(ids ...uuid.UUID) selection {
	return func(ctx context.Context, env ops.Env) ([]dbgen.Customer, error) {
		ordered := slices.Clone(ids)
		slices.SortFunc(ordered, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
		locked := make(map[uuid.UUID]dbgen.Customer, len(ids))
		for _, id := range ordered {
			row, err := env.Q.LockCustomer(ctx, id)
			if err != nil {
				return nil, rowError(err)
			}
			if !canView(env.Caller, row) {
				return nil, ops.ErrNotFound
			}
			if row.MergedInto != nil {
				return nil, ops.Rule("该顾客已合并到另一位顾客")
			}
			locked[id] = row
		}
		rows := make([]dbgen.Customer, 0, len(ids))
		for _, id := range ids {
			rows = append(rows, locked[id])
		}
		return rows, nil
	}
}

// responsibleOf 锁住员工负责的全部未合并顾客，任一顾客不可查看时拒绝交接。
func responsibleOf(staffID uuid.UUID) selection {
	return func(ctx context.Context, env ops.Env) ([]dbgen.Customer, error) {
		rows, err := env.Q.LockResponsibleCustomers(ctx, &staffID)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if !canView(env.Caller, row) {
				return nil, ops.Rule("这位员工负责的顾客里有你无权查看的，不能交接")
			}
		}
		return rows, nil
	}
}

// change 选中并修改顾客，批量构造改前改后日志，返回按权限打码的视图。
func change(ctx context.Context, env ops.Env, action string, sel selection, edit func([]dbgen.Customer) error) ([]*Customer, error) {
	rows, err := sel(ctx, env)
	if err != nil {
		return nil, err
	}
	before, err := views(ctx, env, rows)
	if err != nil {
		return nil, err
	}
	if err := edit(rows); err != nil {
		return nil, err
	}
	for _, row := range rows {
		if err := save(ctx, env, row); err != nil {
			return nil, rowError(err)
		}
	}
	after, err := views(ctx, env, rows)
	if err != nil {
		return nil, err
	}
	for i, c := range after {
		if err := audit.Record(ctx, env, action, audit.CustomerTarget(rows[i].ID), before[i], c); err != nil {
			return nil, err
		}
		mask(env, c)
	}
	return after, nil
}

// changeOne 修改一位顾客并返回按权限打码的视图。
func changeOne(ctx context.Context, env ops.Env, action string, id uuid.UUID, edit func(*dbgen.Customer) error) (*Customer, error) {
	items, err := change(ctx, env, action, byIDs(id), func(rows []dbgen.Customer) error {
		return edit(&rows[0])
	})
	if err != nil {
		return nil, err
	}
	return items[0], nil
}

// created 为新建顾客记录完整日志，返回按权限打码的视图。
func created(ctx context.Context, env ops.Env, action string, row dbgen.Customer) (*Customer, error) {
	c, err := view(ctx, env, row)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, action, audit.CustomerTarget(row.ID), nil, c); err != nil {
		return nil, err
	}
	return mask(env, c), nil
}
