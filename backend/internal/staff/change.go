package staff

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

// staffLock 是员工行的锁级别，由调用处按会不会改唯一约束列写明。
type staffLock int

const (
	plainColumns  staffLock = iota // 只改普通列或别的表：FOR NO KEY UPDATE
	uniqueColumns                  // 可能改编号、登录账号等唯一约束列：FOR UPDATE
)

// change 先锁老板角色排队，再锁员工行，避免持员工 FOR UPDATE 时等老板角色而与日志外键检查形成死锁。
// 随后检查可管理、读取改前视图、修改、检查老板底线并记录改前改后日志。
func change(ctx context.Context, env ops.Env, action string, id uuid.UUID, lock staffLock, edit func(current dbgen.Staff) error) (*Staff, error) {
	if err := lockOwners(ctx, env); err != nil {
		return nil, err
	}
	var current dbgen.Staff
	var err error
	if lock == uniqueColumns {
		current, err = env.Q.LockStaff(ctx, id)
	} else {
		var row dbgen.LockStaffNoKeyRow
		row, err = env.Q.LockStaffNoKey(ctx, id)
		current = row.Staff
	}
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && current.StoreID != env.Caller.StoreID) {
		return nil, ops.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := ensureCanManage(ctx, env, id); err != nil {
		return nil, err
	}
	before, err := view(ctx, env, id)
	if err != nil {
		return nil, err
	}
	if err := edit(current); err != nil {
		return nil, err
	}
	if err := ensureOwnerRemains(ctx, env); err != nil {
		return nil, err
	}
	after, err := view(ctx, env, id)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, action, audit.StaffTarget(id), before, after); err != nil {
		return nil, err
	}
	return after, nil
}

// created 为刚新建的员工取视图并记日志，改前为 nil。
func created(ctx context.Context, env ops.Env, action string, id uuid.UUID) (*Staff, error) {
	after, err := view(ctx, env, id)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, action, audit.StaffTarget(id), nil, after); err != nil {
		return nil, err
	}
	return after, nil
}
