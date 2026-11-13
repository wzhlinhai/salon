package booking

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/catalog"
	"github.com/wzhlinhai/salon/backend/internal/customers"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

// Register 声明预约、私约占位及其查询操作。
func Register(r *ops.Registry) {
	ops.Register(r, ops.Operation[CreateAppointmentInput, Appointment]{Name: "create_appointment", Summary: "前台代顾客预约，直接已确认", Kind: ops.Write, Permission: perm.BookingManage, AI: ops.AIDraft, Handle: createAppointment})
	ops.Register(r, ops.Operation[RescheduleAppointmentInput, Appointment]{Name: "reschedule_appointment", Summary: "改约：修改预约时间或发型师", Kind: ops.Write, Permission: perm.BookingManage, AI: ops.AIDraft, Handle: rescheduleAppointment})
	ops.Register(r, ops.Operation[CancelAppointmentInput, Appointment]{Name: "cancel_appointment", Summary: "取消预约并选择原因", Kind: ops.Write, Permission: perm.BookingManage, AI: ops.AIDraft, Handle: cancelAppointment})
	ops.Register(r, ops.Operation[AppointmentStatusInput, Appointment]{Name: "confirm_appointment", Summary: "确认待确认的预约", Kind: ops.Write, Permission: perm.BookingManage, AI: ops.AIDraft, Handle: confirmAppointment})
	ops.Register(r, ops.Operation[CompleteAppointmentInput, Appointment]{Name: "complete_appointment", Summary: "完成已确认的预约，可关联同顾客的单", Kind: ops.Write, Permission: perm.BookingManage, AI: ops.AIDraft, Handle: completeAppointment})
	ops.Register(r, ops.Operation[AppointmentStatusInput, Appointment]{Name: "mark_no_show", Summary: "开始时间之后把已确认预约记为爽约", Kind: ops.Write, Permission: perm.BookingManage, AI: ops.AIDraft, Handle: markNoShow})
	ops.Register(r, ops.Operation[CreateBlockInput, Appointment]{Name: "create_block", Summary: "员工登记私约占位，该时段不参与轮牌分配", Kind: ops.Write, Permission: perm.BookingManage, AI: ops.AIDraft, Handle: createBlock})
	ops.Register(r, ops.Operation[CancelBlockInput, Appointment]{Name: "cancel_block", Summary: "取消私约占位", Kind: ops.Write, Permission: perm.BookingManage, AI: ops.AIDraft, Handle: cancelBlock})
	ops.Register(r, ops.Operation[ListAppointmentsInput, AppointmentList]{Name: "list_appointments", Summary: "按营业日范围查看预约和私约占位", Kind: ops.Read, Permission: perm.BookingManage, AI: ops.AIDirect, Handle: listAppointments})
	ops.Register(r, ops.Operation[GetAppointmentInput, Appointment]{Name: "get_appointment", Summary: "查看预约或私约占位的项目与全部变化记录", Kind: ops.Read, Permission: perm.BookingManage, AI: ops.AIDirect, Handle: getAppointment})
}

// rulesFor 取 at 所属营业日执行的设置，预约按预约日期的规则检查。
func rulesFor(ctx context.Context, env ops.Env, at time.Time) (settings.Rules, error) {
	day, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, at)
	if err != nil {
		return settings.Rules{}, err
	}
	return settings.Effective(ctx, env.Q, env.Caller.StoreID, day)
}

func loadAppointment(ctx context.Context, env ops.Env, row dbgen.Appointment) (*Appointment, error) {
	views, err := loadAppointments(ctx, env, []dbgen.Appointment{row}, false)
	if err != nil {
		return nil, err
	}
	return &views[0], nil
}

func target(row dbgen.Appointment) audit.Target {
	if row.Kind == KindBlock {
		return audit.AppointmentBlockTarget(row.ID)
	}
	return audit.AppointmentTarget(row.ID, *row.CustomerID)
}

func lockAppointmentRow(ctx context.Context, env ops.Env, id uuid.UUID) (dbgen.Appointment, error) {
	row, err := env.Q.LockAppointment(ctx, dbgen.LockAppointmentParams{StoreID: env.Caller.StoreID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, ops.ErrNotFound
	}
	if err != nil {
		return row, err
	}
	if err := requireStaffScope(env, row.StaffID); err != nil {
		return row, err
	}
	return row, nil
}

func checkOverlap(ctx context.Context, env ops.Env, staffID uuid.UUID, startsAt, endsAt time.Time, exclude *uuid.UUID) error {
	row, err := env.Q.FindOverlappingAppointment(ctx, dbgen.FindOverlappingAppointmentParams{
		StoreID: env.Caller.StoreID, StaffID: staffID, StartsAt: timestamptz(startsAt), EndsAt: timestamptz(endsAt), ExcludeID: exclude,
	})
	if err == nil {
		return conflictRule(&row)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}

func addEvent(ctx context.Context, env ops.Env, appointmentID uuid.UUID, kind string, fromStart, fromEnd *time.Time, toStart, toEnd time.Time, fromStaff *uuid.UUID, toStaff uuid.UUID, reason, note *string) error {
	return env.Q.AddAppointmentEvent(ctx, dbgen.AddAppointmentEventParams{
		TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, AppointmentID: appointmentID, Kind: kind,
		FromStartsAt: optionalTime(fromStart), FromEndsAt: optionalTime(fromEnd), ToStartsAt: timestamptz(toStart), ToEndsAt: timestamptz(toEnd),
		FromStaffID: fromStaff, ToStaffID: toStaff, Reason: reason, Note: note, ActorStaffID: env.Caller.StaffID,
	})
}

func optionalTime(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return timestamptz(*t)
}

func resolveCustomer(ctx context.Context, env ops.Env, in *CreateAppointmentInput) (uuid.UUID, error) {
	if in.CustomerID != nil {
		row, err := customers.LockReference(ctx, env, *in.CustomerID)
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ops.ErrNotFound
		}
		if err != nil {
			return uuid.Nil, err
		}
		return row.ID, nil
	}
	if in.CustomerPhone == nil && in.CustomerName == nil {
		return uuid.Nil, ops.Rule("预约必须选择顾客或填写手机号")
	}
	row, _, err := customers.FindOrCreate(ctx, env, in.CustomerName, in.CustomerPhone, nil)
	if err != nil {
		return uuid.Nil, err
	}
	return row.ID, nil
}

func selectedItems(ctx context.Context, env ops.Env, in []AppointmentItemInput) ([]dbgen.Item, int, error) {
	if len(in) == 0 {
		return nil, 0, nil
	}
	ids := make([]uuid.UUID, 0, len(in))
	seen := map[uuid.UUID]bool{}
	for _, item := range in {
		if seen[item.ItemID] {
			return nil, 0, ops.Rule("项目不能重复")
		}
		seen[item.ItemID] = true
		ids = append(ids, item.ItemID)
	}
	rows, err := catalog.LockItems(ctx, env.Q, env.Caller.StoreID, ids...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, ops.Rule("项目不存在")
	}
	if err != nil {
		return nil, 0, err
	}
	items := make([]dbgen.Item, 0, len(ids))
	total := 0
	for _, id := range ids {
		item := rows[id]
		if !item.Active {
			return nil, 0, ops.Rule("项目已停用")
		}
		if !item.Bookable {
			return nil, 0, ops.Rule("该项目不可预约")
		}
		items = append(items, item)
		total += int(item.DurationMinutes)
	}
	return items, total, nil
}

func createAppointment(ctx context.Context, env ops.Env, in *CreateAppointmentInput) (*Appointment, error) {
	if err := requireStaffScope(env, in.StaffID); err != nil {
		return nil, err
	}
	rules, err := rulesFor(ctx, env, in.StartsAt)
	if err != nil {
		return nil, err
	}
	customerID, err := resolveCustomer(ctx, env, in)
	if err != nil {
		return nil, err
	}
	if err := staff.LockAssignable(ctx, env.Q, env.Caller.StoreID, in.StaffID); err != nil {
		if errors.Is(err, staff.ErrNotAssignable) {
			return nil, ops.Rule("员工必须是本店在职员工")
		}
		return nil, err
	}
	items, total, err := selectedItems(ctx, env, in.Items)
	if err != nil {
		return nil, err
	}
	endsAt := in.StartsAt.Add(time.Duration(rules.BookingDefaultMinutes()) * time.Minute)
	if total > 0 {
		endsAt = in.StartsAt.Add(time.Duration(total) * time.Minute)
	}
	if in.EndsAt != nil {
		endsAt = *in.EndsAt
	}
	if err := checkSchedule(ctx, env.Q, env.Caller.StoreID, rules, env.Now, in.StartsAt, endsAt); err != nil {
		return nil, err
	}
	if err := checkOverlap(ctx, env, in.StaffID, in.StartsAt, endsAt, nil); err != nil {
		return nil, err
	}
	row, err := env.Q.CreateAppointment(ctx, dbgen.CreateAppointmentParams{
		TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, StaffID: in.StaffID, CustomerID: &customerID,
		Kind: KindAppointment, StartsAt: timestamptz(in.StartsAt), EndsAt: timestamptz(endsAt), Status: StatusConfirmed,
		Note: trimNote(in.Note), CreatedBy: env.Caller.StaffID,
	})
	if err != nil {
		if isExclusionViolation(err) {
			return nil, conflictRule(nil)
		}
		return nil, err
	}
	for i, item := range items {
		if err := env.Q.AddAppointmentItem(ctx, dbgen.AddAppointmentItemParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, AppointmentID: row.ID, ItemID: item.ID, Position: int32(i + 1)}); err != nil {
			return nil, err
		}
	}
	if err := addEvent(ctx, env, row.ID, "create", nil, nil, row.StartsAt.Time, row.EndsAt.Time, nil, row.StaffID, nil, nil); err != nil {
		return nil, err
	}
	out, err := loadAppointment(ctx, env, row)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "create_appointment", target(row), nil, out); err != nil {
		return nil, err
	}
	return out, nil
}

func rescheduleAppointment(ctx context.Context, env ops.Env, in *RescheduleAppointmentInput) (*Appointment, error) {
	if in.StaffID == nil && in.StartsAt == nil && in.EndsAt == nil {
		return nil, ops.Rule("没有要修改的内容")
	}
	row, err := lockAppointmentRow(ctx, env, in.ID)
	if err != nil {
		return nil, err
	}
	if row.Kind == KindBlock {
		return nil, ops.Rule("私约占位不能改约")
	}
	if row.Status != StatusPending && row.Status != StatusConfirmed {
		return nil, ops.Rule("只有待确认或已确认的预约可以改约")
	}
	staffID := row.StaffID
	if in.StaffID != nil {
		if err := requireStaffScope(env, *in.StaffID); err != nil {
			return nil, err
		}
		if err := staff.LockAssignable(ctx, env.Q, env.Caller.StoreID, *in.StaffID); err != nil {
			if errors.Is(err, staff.ErrNotAssignable) {
				return nil, ops.Rule("员工必须是本店在职员工")
			}
			return nil, err
		}
		staffID = *in.StaffID
	} else if err := staff.LockAssignable(ctx, env.Q, env.Caller.StoreID, row.StaffID); err != nil {
		if errors.Is(err, staff.ErrNotAssignable) {
			return nil, ops.Rule("员工必须是本店在职员工")
		}
		return nil, err
	}
	startsAt := row.StartsAt.Time
	endsAt := row.EndsAt.Time
	if in.StartsAt != nil {
		startsAt = *in.StartsAt
		if in.EndsAt == nil {
			endsAt = startsAt.Add(row.EndsAt.Time.Sub(row.StartsAt.Time))
		}
	}
	if in.EndsAt != nil {
		endsAt = *in.EndsAt
	}
	rules, err := rulesFor(ctx, env, startsAt)
	if err != nil {
		return nil, err
	}
	if err := checkSchedule(ctx, env.Q, env.Caller.StoreID, rules, env.Now, startsAt, endsAt); err != nil {
		return nil, err
	}
	if err := checkOverlap(ctx, env, staffID, startsAt, endsAt, &row.ID); err != nil {
		return nil, err
	}
	before, err := loadAppointment(ctx, env, row)
	if err != nil {
		return nil, err
	}
	row, err = env.Q.RescheduleAppointment(ctx, dbgen.RescheduleAppointmentParams{StoreID: env.Caller.StoreID, ID: row.ID, StaffID: staffID, StartsAt: timestamptz(startsAt), EndsAt: timestamptz(endsAt)})
	if err != nil {
		if isExclusionViolation(err) {
			return nil, conflictRule(nil)
		}
		return nil, err
	}
	reason := CancelReasonReschedule
	if err := addEvent(ctx, env, row.ID, "reschedule", &before.StartsAt, &before.EndsAt, startsAt, endsAt, &before.Staff.ID, staffID, &reason, trimNote(in.Reason)); err != nil {
		return nil, err
	}
	out, err := loadAppointment(ctx, env, row)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "reschedule_appointment", target(row), before, out); err != nil {
		return nil, err
	}
	return out, nil
}

func cancelAppointment(ctx context.Context, env ops.Env, in *CancelAppointmentInput) (*Appointment, error) {
	row, err := lockAppointmentRow(ctx, env, in.ID)
	if err != nil {
		return nil, err
	}
	if row.Kind == KindBlock {
		return nil, ops.Rule("私约占位请在占位操作中取消")
	}
	if err := requireTransition(row, StatusCancelled); err != nil {
		return nil, err
	}
	note, err := normalizeCancelNote(in.Reason, in.Note)
	if err != nil {
		return nil, err
	}
	before, err := loadAppointment(ctx, env, row)
	if err != nil {
		return nil, err
	}
	row, err = env.Q.SetAppointmentStatus(ctx, dbgen.SetAppointmentStatusParams{StoreID: env.Caller.StoreID, ID: row.ID, Status: StatusCancelled})
	if err != nil {
		return nil, err
	}
	reason := in.Reason
	if err := addEvent(ctx, env, row.ID, "cancel", nil, nil, before.StartsAt, before.EndsAt, nil, before.Staff.ID, &reason, note); err != nil {
		return nil, err
	}
	out, err := loadAppointment(ctx, env, row)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "cancel_appointment", target(row), before, out); err != nil {
		return nil, err
	}
	return out, nil
}

func confirmAppointment(ctx context.Context, env ops.Env, in *AppointmentStatusInput) (*Appointment, error) {
	row, err := lockAppointmentRow(ctx, env, in.ID)
	if err != nil {
		return nil, err
	}
	if row.Kind == KindBlock {
		return nil, ops.Rule("私约占位不能确认")
	}
	if err := requireTransition(row, StatusConfirmed); err != nil {
		return nil, err
	}
	before, err := loadAppointment(ctx, env, row)
	if err != nil {
		return nil, err
	}
	row, err = env.Q.SetAppointmentStatus(ctx, dbgen.SetAppointmentStatusParams{StoreID: env.Caller.StoreID, ID: row.ID, Status: StatusConfirmed})
	if err != nil {
		return nil, err
	}
	if err := addEvent(ctx, env, row.ID, "confirm", nil, nil, before.StartsAt, before.EndsAt, nil, before.Staff.ID, nil, nil); err != nil {
		return nil, err
	}
	out, err := loadAppointment(ctx, env, row)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "confirm_appointment", target(row), before, out); err != nil {
		return nil, err
	}
	return out, nil
}

func completeAppointment(ctx context.Context, env ops.Env, in *CompleteAppointmentInput) (*Appointment, error) {
	row, err := lockAppointmentRow(ctx, env, in.ID)
	if err != nil {
		return nil, err
	}
	if row.Kind == KindBlock {
		return nil, ops.Rule("私约占位不能完成")
	}
	if err := requireTransition(row, StatusCompleted); err != nil {
		return nil, err
	}
	if in.OrderID != nil {
		order, err := env.Q.GetOrder(ctx, dbgen.GetOrderParams{StoreID: env.Caller.StoreID, ID: *in.OrderID})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ops.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		if order.CustomerID != *row.CustomerID {
			return nil, ops.Rule("只能关联同一顾客的单")
		}
	}
	before, err := loadAppointment(ctx, env, row)
	if err != nil {
		return nil, err
	}
	row, err = env.Q.CompleteAppointment(ctx, dbgen.CompleteAppointmentParams{StoreID: env.Caller.StoreID, ID: row.ID, OrderID: in.OrderID})
	if err != nil {
		return nil, err
	}
	if err := addEvent(ctx, env, row.ID, "complete", nil, nil, before.StartsAt, before.EndsAt, nil, before.Staff.ID, nil, nil); err != nil {
		return nil, err
	}
	out, err := loadAppointment(ctx, env, row)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "complete_appointment", target(row), before, out); err != nil {
		return nil, err
	}
	return out, nil
}

func markNoShow(ctx context.Context, env ops.Env, in *AppointmentStatusInput) (*Appointment, error) {
	row, err := lockAppointmentRow(ctx, env, in.ID)
	if err != nil {
		return nil, err
	}
	if row.Kind == KindBlock {
		return nil, ops.Rule("私约占位不能记爽约")
	}
	if err := requireTransition(row, StatusNoShow); err != nil {
		return nil, err
	}
	if env.Now.Before(row.StartsAt.Time) {
		return nil, ops.Rule("只有开始时间之后才能记爽约")
	}
	before, err := loadAppointment(ctx, env, row)
	if err != nil {
		return nil, err
	}
	row, err = env.Q.SetAppointmentStatus(ctx, dbgen.SetAppointmentStatusParams{StoreID: env.Caller.StoreID, ID: row.ID, Status: StatusNoShow})
	if err != nil {
		return nil, err
	}
	if err := addEvent(ctx, env, row.ID, "no_show", nil, nil, before.StartsAt, before.EndsAt, nil, before.Staff.ID, nil, nil); err != nil {
		return nil, err
	}
	out, err := loadAppointment(ctx, env, row)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "mark_no_show", target(row), before, out); err != nil {
		return nil, err
	}
	return out, nil
}

func createBlock(ctx context.Context, env ops.Env, in *CreateBlockInput) (*Appointment, error) {
	if err := requireStaffScope(env, in.StaffID); err != nil {
		return nil, err
	}
	if err := staff.LockAssignable(ctx, env.Q, env.Caller.StoreID, in.StaffID); err != nil {
		if errors.Is(err, staff.ErrNotAssignable) {
			return nil, ops.Rule("员工必须是本店在职员工")
		}
		return nil, err
	}
	if !in.EndsAt.After(in.StartsAt) {
		return nil, ops.Rule("结束时间要晚于开始时间")
	}
	if in.EndsAt.Sub(in.StartsAt) > 24*time.Hour {
		return nil, ops.Rule("私约占位最长 24 小时")
	}
	if !in.EndsAt.After(env.Now) {
		return nil, ops.Rule("私约占位结束时间要在当前时刻之后")
	}
	if err := checkOverlap(ctx, env, in.StaffID, in.StartsAt, in.EndsAt, nil); err != nil {
		return nil, err
	}
	row, err := env.Q.CreateAppointment(ctx, dbgen.CreateAppointmentParams{
		TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, StaffID: in.StaffID, Kind: KindBlock,
		StartsAt: timestamptz(in.StartsAt), EndsAt: timestamptz(in.EndsAt), Status: StatusConfirmed, Note: trimNote(in.Note), CreatedBy: env.Caller.StaffID,
	})
	if err != nil {
		if isExclusionViolation(err) {
			return nil, conflictRule(nil)
		}
		return nil, err
	}
	if err := addEvent(ctx, env, row.ID, "create", nil, nil, row.StartsAt.Time, row.EndsAt.Time, nil, row.StaffID, nil, nil); err != nil {
		return nil, err
	}
	out, err := loadAppointment(ctx, env, row)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "create_block", target(row), nil, out); err != nil {
		return nil, err
	}
	return out, nil
}

func cancelBlock(ctx context.Context, env ops.Env, in *CancelBlockInput) (*Appointment, error) {
	row, err := lockAppointmentRow(ctx, env, in.ID)
	if err != nil {
		return nil, err
	}
	if row.Kind != KindBlock {
		return nil, ops.Rule("只有私约占位可以这样取消")
	}
	if err := requireTransition(row, StatusCancelled); err != nil {
		return nil, err
	}
	before, err := loadAppointment(ctx, env, row)
	if err != nil {
		return nil, err
	}
	row, err = env.Q.SetAppointmentStatus(ctx, dbgen.SetAppointmentStatusParams{StoreID: env.Caller.StoreID, ID: row.ID, Status: StatusCancelled})
	if err != nil {
		return nil, err
	}
	if err := addEvent(ctx, env, row.ID, "cancel", nil, nil, before.StartsAt, before.EndsAt, nil, before.Staff.ID, nil, nil); err != nil {
		return nil, err
	}
	out, err := loadAppointment(ctx, env, row)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "cancel_block", target(row), before, out); err != nil {
		return nil, err
	}
	return out, nil
}
