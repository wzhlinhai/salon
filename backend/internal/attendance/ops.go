package attendance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

// Register 声明班次与员工班次的五个管理操作。
func Register(r *ops.Registry) {
	ops.Register(r, ops.Operation[CreateShiftInput, Shift]{Name: "create_shift", Summary: "新建班次", Kind: ops.Write, Permission: perm.AttendanceManage, AI: ops.AIDraft, Handle: createShift})
	ops.Register(r, ops.Operation[DeactivateShiftInput, Shift]{Name: "deactivate_shift", Summary: "停用班次", Kind: ops.Write, Permission: perm.AttendanceManage, AI: ops.AIDraft, Handle: deactivateShift})
	ops.Register(r, ops.Operation[ListShiftsInput, ShiftList]{Name: "list_shifts", Summary: "列出本店全部班次", Kind: ops.Read, Permission: perm.AttendanceManage, AI: ops.AIDirect, Handle: listShifts})
	ops.Register(r, ops.Operation[SetStaffShiftInput, StaffShift]{Name: "set_staff_shift", Summary: "从营业日起指定员工班次", Kind: ops.Write, Permission: perm.AttendanceManage, AI: ops.AIDraft, Handle: setStaffShift})
	ops.Register(r, ops.Operation[ListStaffShiftsInput, StaffShiftList]{Name: "list_staff_shifts", Summary: "查看营业日每位员工执行的班次", Kind: ops.Read, Permission: perm.AttendanceManage, AI: ops.AIDirect, Handle: listStaffShifts})
}

func createShift(ctx context.Context, env ops.Env, in *CreateShiftInput) (*Shift, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, ops.Rule("班次名称不能为空")
	}
	if in.StartTime >= in.EndTime {
		return nil, ops.Rule("上班时间要早于下班时间")
	}
	start, err := parseTime(in.StartTime)
	if err != nil {
		return nil, err
	}
	end, err := parseTime(in.EndTime)
	if err != nil {
		return nil, err
	}
	row, err := env.Q.CreateShift(ctx, dbgen.CreateShiftParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, Name: name, StartTime: start, EndTime: end, CreatedBy: env.Caller.StaffID})
	if err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" {
			return nil, ops.Rule("班次名称已存在")
		}
		return nil, err
	}
	refs, err := names(ctx, env.Q, env.Caller.StoreID, []uuid.UUID{row.CreatedBy})
	if err != nil {
		return nil, err
	}
	after := shiftView(row, refs)
	if err := audit.Record(ctx, env, "create_shift", audit.ShiftTarget(row.ID), nil, after); err != nil {
		return nil, err
	}
	return &after, nil
}

func parseTime(s string) (pgtype.Time, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return pgtype.Time{}, err
	}
	return pgtype.Time{Microseconds: int64(t.Hour()*60+t.Minute()) * 60 * 1000000, Valid: true}, nil
}

func shiftError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("班次不存在：%w", ops.ErrNotFound)
	}
	return err
}

func deactivateShift(ctx context.Context, env ops.Env, in *DeactivateShiftInput) (*Shift, error) {
	row, err := env.Q.LockShift(ctx, dbgen.LockShiftParams{StoreID: env.Caller.StoreID, ID: in.ID})
	if err != nil {
		return nil, shiftError(err)
	}
	if !row.Active {
		return nil, ops.Rule("班次已停用")
	}
	refs, err := names(ctx, env.Q, env.Caller.StoreID, []uuid.UUID{row.CreatedBy})
	if err != nil {
		return nil, err
	}
	before := shiftView(row, refs)
	row, err = env.Q.DeactivateShift(ctx, dbgen.DeactivateShiftParams{StoreID: env.Caller.StoreID, ID: in.ID})
	if err != nil {
		return nil, err
	}
	after := shiftView(row, refs)
	if err := audit.Record(ctx, env, "deactivate_shift", audit.ShiftTarget(row.ID), before, after); err != nil {
		return nil, err
	}
	return &after, nil
}

func setStaffShift(ctx context.Context, env ops.Env, in *SetStaffShiftInput) (*StaffShift, error) {
	if err := env.Q.LockStaffShifts(ctx, env.Caller.StoreID); err != nil {
		return nil, err
	}
	from, err := time.Parse(settings.DateLayout, in.EffectiveFrom)
	if err != nil {
		return nil, err
	}
	today, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, env.Now)
	if err != nil {
		return nil, err
	}
	if from.Before(today) {
		return nil, ops.Rule("生效日期不能早于今天（" + today.Format(settings.DateLayout) + "）")
	}
	if err := staff.LockAssignable(ctx, env.Q, env.Caller.StoreID, in.StaffID); err != nil {
		if errors.Is(err, staff.ErrNotAssignable) {
			return nil, ops.Rule("员工必须是本店在职员工")
		}
		return nil, err
	}
	var shift *dbgen.Shift
	if in.ShiftID != nil {
		row, err := env.Q.ShareShift(ctx, dbgen.ShareShiftParams{StoreID: env.Caller.StoreID, ID: *in.ShiftID})
		if err != nil {
			return nil, shiftError(err)
		}
		if !row.Active {
			return nil, ops.Rule("班次已停用，不能指定给员工")
		}
		shift = &row
	}
	timelines, err := StaffShiftTimelines(ctx, env.Q, env.Caller.StoreID)
	if err != nil {
		return nil, err
	}
	var before any
	if c, ok := timelines[in.StaffID].At(from); ok {
		before = c.Value
	}
	row, err := env.Q.AddStaffShift(ctx, dbgen.AddStaffShiftParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, StaffID: in.StaffID, ShiftID: in.ShiftID, EffectiveFrom: pgtype.Date{Time: from, Valid: true}, SavedBy: env.Caller.StaffID})
	if err != nil {
		return nil, err
	}
	ids := []uuid.UUID{row.StaffID, row.SavedBy}
	if shift != nil {
		ids = append(ids, shift.CreatedBy)
	}
	refs, err := names(ctx, env.Q, env.Caller.StoreID, ids)
	if err != nil {
		return nil, err
	}
	after := StaffShift{Staff: refs[row.StaffID], EffectiveFrom: in.EffectiveFrom, SavedBy: refs[row.SavedBy], SavedAt: row.SavedAt.Time}
	if shift != nil {
		v := shiftView(*shift, refs)
		after.Shift = &v
	}
	if err := audit.Record(ctx, env, "set_staff_shift", audit.StaffShiftTarget(in.StaffID), before, after); err != nil {
		return nil, err
	}
	return &after, nil
}
