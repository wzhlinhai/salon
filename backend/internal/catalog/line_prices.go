package catalog

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

// LineStaff 是项目行一个岗位上的员工。
type LineStaff struct {
	Slot    int16
	StaffID uuid.UUID
}

// PreviousLinePrice 是决定是否重取价所需的原价格来源和完整岗位分配。
type PreviousLinePrice struct {
	Source string
	Staff  map[int16]*uuid.UUID
}

// QuoteLine 校验完整岗位分配，按项目、UUID 员工顺序加锁，再按第一个岗位报价。
// 手工改价或第一个岗位未换人时不重取价，返回的价格为 nil；仍校验全部新指派。
func QuoteLine(ctx context.Context, env ops.Env, itemID uuid.UUID, slots map[int16]bool, in []LineStaff, previous *PreviousLinePrice, rules settings.Rules) (map[int16]*uuid.UUID, *ItemPrice, error) {
	assigned := map[int16]*uuid.UUID{}
	ids := []uuid.UUID{}
	for _, s := range in {
		if !slots[s.Slot] {
			return nil, nil, ops.Rule("项目没有这个岗位")
		}
		if assigned[s.Slot] != nil {
			return nil, nil, ops.Rule("同一岗位不能重复填员工")
		}
		assigned[s.Slot] = new(s.StaffID)
		ids = append(ids, s.StaffID)
	}
	first := assigned[1]
	requote := previous == nil || previous.Source != "manual" && !sameStaff(previous.Staff[1], first)
	if requote {
		if _, err := LockReference(ctx, env.Q, env.Caller.StoreID, itemID); err != nil {
			return nil, nil, rowError(err)
		}
	}
	if err := staff.LockAssignable(ctx, env.Q, env.Caller.StoreID, ids...); err != nil {
		if errors.Is(err, staff.ErrNotAssignable) {
			return nil, nil, ops.Rule("岗位上的员工必须是本店在职员工")
		}
		return nil, nil, err
	}
	if !requote {
		return assigned, nil, nil
	}
	price, err := Quote(ctx, env.Q, env.Caller.StoreID, itemID, first, nil, rules)
	return assigned, price, err
}

func sameStaff(a, b *uuid.UUID) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
