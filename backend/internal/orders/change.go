package orders

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// change 按 UUID 锁住全部单并检查状态，再按操作声明的权限限制检查窗口和范围。
// edit 收到的顺序与 ids 相同；修改后的行状态也在此切片更新，返回全部单的改后视图。
func change(ctx context.Context, env ops.Env, action string, ids []uuid.UUID, edit func([]dbgen.Order) error) ([]Order, error) {
	ordered := slices.Clone(ids)
	slices.SortFunc(ordered, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	locked := make(map[uuid.UUID]dbgen.Order, len(ids))
	for _, id := range ordered {
		row, err := env.Q.LockOrder(ctx, dbgen.LockOrderParams{StoreID: env.Caller.StoreID, ID: id})
		if err != nil {
			return nil, rowError(err)
		}
		if row.Status == "merged" {
			return nil, ops.Rule("单已合并，请在合并后的单上操作")
		}
		if row.Status == "paid" {
			return nil, ops.Rule("单已结账")
		}
		if row.Status != "open" {
			return nil, ops.Rule("单当前状态不允许操作")
		}
		locked[id] = row
	}
	rows := make([]dbgen.Order, len(ids))
	now := time.Now()
	for i, id := range ids {
		rows[i] = locked[id]
		if err := checkEdit(ctx, env, rows[i], now); err != nil {
			return nil, err
		}
	}
	before, err := views(ctx, env, rows)
	if err != nil {
		return nil, err
	}
	if err := edit(rows); err != nil {
		return nil, err
	}
	after, err := views(ctx, env, rows)
	if err != nil {
		return nil, err
	}
	for i, row := range rows {
		if err := audit.Record(ctx, env, action, audit.OrderTarget(row.ID, row.CustomerID), before[i], after[i]); err != nil {
			return nil, err
		}
	}
	return after, nil
}

func checkEdit(ctx context.Context, env ops.Env, order dbgen.Order, now time.Time) error {
	if env.Grant.WindowDays != nil {
		today, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, now)
		if err != nil {
			return err
		}
		if order.BusinessDate.Time.Before(today.AddDate(0, 0, -*env.Grant.WindowDays)) {
			return ops.ErrForbidden
		}
	}
	if env.Grant.Scope == ops.ScopeSelf {
		involved, err := env.Q.OrderInvolvesStaff(ctx, dbgen.OrderInvolvesStaffParams{StoreID: env.Caller.StoreID, ID: order.ID, StaffID: env.Caller.StaffID})
		if err != nil {
			return err
		}
		if !involved {
			return ops.ErrForbidden
		}
	}
	return nil
}

func changeOne(ctx context.Context, env ops.Env, action string, id uuid.UUID, edit func(*dbgen.Order) error) (*Order, error) {
	after, err := change(ctx, env, action, []uuid.UUID{id}, func(rows []dbgen.Order) error { return edit(&rows[0]) })
	if err != nil {
		return nil, err
	}
	return &after[0], nil
}

// changeItem 在单锁和权限检查之后读取项目行，拒绝修改已去掉的行。
func changeItem(ctx context.Context, env ops.Env, action string, orderID, itemID uuid.UUID, edit func(dbgen.Order, dbgen.OrderItem) error) (*Order, error) {
	return changeOne(ctx, env, action, orderID, func(row *dbgen.Order) error {
		item, err := env.Q.GetOrderItem(ctx, dbgen.GetOrderItemParams{StoreID: env.Caller.StoreID, OrderID: row.ID, ID: itemID})
		if err != nil {
			return rowError(err)
		}
		if item.RemovedAt.Valid {
			return ops.Rule("项目已去掉")
		}
		return edit(*row, item)
	})
}
