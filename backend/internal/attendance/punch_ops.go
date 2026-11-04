package attendance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

func registerPunches(r *ops.Registry) {
	ops.Register(r, ops.Operation[ClockInInput, Punch]{Name: "clock_in", Summary: "本人打卡", Kind: ops.Write, Permission: perm.AttendanceClock, AI: ops.AINone, Handle: clockIn})
	ops.Register(r, ops.Operation[AddPunchInput, Punch]{Name: "add_punch", Summary: "给员工补卡", Kind: ops.Write, Permission: perm.AttendanceManage, AI: ops.AIDraft, Handle: addPunch})
	ops.Register(r, ops.Operation[VoidPunchInput, Punch]{Name: "void_punch", Summary: "作废补卡", Kind: ops.Write, Permission: perm.AttendanceManage, AI: ops.AIDraft, Handle: voidPunch})
	ops.Register(r, ops.Operation[ApproveAttendanceInput, AttendanceApproval]{Name: "approve_attendance", Summary: "核准当天全部考勤异常", Kind: ops.Write, Permission: perm.AttendanceManage, AI: ops.AIDraft, Handle: approveAttendance})
	ops.Register(r, ops.Operation[RevokeAttendanceApprovalInput, AttendanceApproval]{Name: "revoke_attendance_approval", Summary: "撤销考勤核准", Kind: ops.Write, Permission: perm.AttendanceManage, AI: ops.AIDraft, Handle: revokeAttendanceApproval})
	ops.Register(r, ops.Operation[ListMyAttendanceInput, StaffAttendance]{Name: "list_my_attendance", Summary: "查看本人月度考勤", Kind: ops.Read, Permission: perm.AttendanceClock, AI: ops.AIDirect, Handle: listMyAttendance})
	ops.Register(r, ops.Operation[GetStaffAttendanceInput, StaffAttendance]{Name: "get_staff_attendance", Summary: "查看员工月度考勤", Kind: ops.Read, Permission: perm.AttendanceManage, AI: ops.AIDirect, Handle: getStaffAttendance})
	ops.Register(r, ops.Operation[ListAttendanceSummaryInput, AttendanceSummaryList]{Name: "list_attendance_summary", Summary: "查看本店月度考勤统计", Kind: ops.Read, Permission: perm.AttendanceManage, AI: ops.AIDirect, Handle: listAttendanceSummary})
}

func lockAttendance(ctx context.Context, env ops.Env, id uuid.UUID) error {
	return env.Q.LockStaffAttendance(ctx, dbgen.LockStaffAttendanceParams{StoreID: env.Caller.StoreID, StaffID: id})
}

func punchResult(ctx context.Context, env ops.Env, row dbgen.Punch) (*Punch, error) {
	ids := []uuid.UUID{row.StaffID, row.CreatedBy}
	if row.VoidedBy != nil {
		ids = append(ids, *row.VoidedBy)
	}
	refs, err := names(ctx, env.Q, env.Caller.StoreID, ids)
	if err != nil {
		return nil, err
	}
	v := punchView(row, refs)
	return &v, nil
}

func approvalResult(ctx context.Context, env ops.Env, row dbgen.AttendanceApproval) (*AttendanceApproval, error) {
	ids := []uuid.UUID{row.StaffID, row.ApprovedBy}
	if row.RevokedBy != nil {
		ids = append(ids, *row.RevokedBy)
	}
	refs, err := names(ctx, env.Q, env.Caller.StoreID, ids)
	if err != nil {
		return nil, err
	}
	v := approvalView(row, refs)
	return &v, nil
}

func attendanceError(err error, label string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s不存在：%w", label, ops.ErrNotFound)
	}
	return err
}

func insertPunch(ctx context.Context, env ops.Env, id uuid.UUID, at time.Time, source, reason string) (*Punch, error) {
	day, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, at)
	if err != nil {
		return nil, err
	}
	row, err := env.Q.CreatePunch(ctx, dbgen.CreatePunchParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, StaffID: id, At: pgtype.Timestamptz{Time: at, Valid: true}, BusinessDate: dateValue(day), Source: source, Reason: reason, CreatedBy: env.Caller.StaffID})
	if err != nil {
		return nil, err
	}
	return punchResult(ctx, env, row)
}

func clockIn(ctx context.Context, env ops.Env, _ *ClockInInput) (*Punch, error) {
	if err := lockAttendance(ctx, env, env.Caller.StaffID); err != nil {
		return nil, err
	}
	row, err := env.Q.LatestPunch(ctx, dbgen.LatestPunchParams{StoreID: env.Caller.StoreID, StaffID: env.Caller.StaffID})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if err == nil && row.At.Time.After(env.Now.Add(-60*time.Second)) {
		return punchResult(ctx, env, row)
	}
	return insertPunch(ctx, env, env.Caller.StaffID, env.Now, "self", "")
}

func addPunch(ctx context.Context, env ops.Env, in *AddPunchInput) (*Punch, error) {
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return nil, ops.Rule("补卡要写原因")
	}
	if err := lockAttendance(ctx, env, in.StaffID); err != nil {
		return nil, err
	}
	if in.At.After(env.Now) {
		return nil, ops.Rule("补卡时间不能晚于现在")
	}
	if _, err := staff.LockReferences(ctx, env.Q, env.Caller.StoreID, in.StaffID); err != nil {
		return nil, err
	}
	after, err := insertPunch(ctx, env, in.StaffID, in.At, "manual", reason)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "add_punch", audit.PunchTarget(after.ID), nil, after); err != nil {
		return nil, err
	}
	return after, nil
}

func voidPunch(ctx context.Context, env ops.Env, in *VoidPunchInput) (*Punch, error) {
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return nil, ops.Rule("作废要写原因")
	}
	row, err := env.Q.GetPunch(ctx, dbgen.GetPunchParams{StoreID: env.Caller.StoreID, ID: in.ID})
	if err != nil {
		return nil, attendanceError(err, "打卡")
	}
	if err := lockAttendance(ctx, env, row.StaffID); err != nil {
		return nil, err
	}
	row, err = env.Q.LockPunch(ctx, dbgen.LockPunchParams{StoreID: env.Caller.StoreID, ID: in.ID})
	if err != nil {
		return nil, err
	}
	if row.Source == "self" {
		return nil, ops.Rule("员工自己打的卡不能作废")
	}
	if row.VoidedAt.Valid {
		return nil, ops.Rule("这次补卡已作废")
	}
	before, err := punchResult(ctx, env, row)
	if err != nil {
		return nil, err
	}
	row, err = env.Q.VoidPunch(ctx, dbgen.VoidPunchParams{StoreID: env.Caller.StoreID, ID: in.ID, VoidedBy: &env.Caller.StaffID, VoidedAt: pgtype.Timestamptz{Time: env.Now, Valid: true}, VoidReason: &reason})
	if err != nil {
		return nil, err
	}
	after, err := punchResult(ctx, env, row)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "void_punch", audit.PunchTarget(row.ID), before, after); err != nil {
		return nil, err
	}
	return after, nil
}

func approveAttendance(ctx context.Context, env ops.Env, in *ApproveAttendanceInput) (*AttendanceApproval, error) {
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return nil, ops.Rule("核准要写原因")
	}
	if err := lockAttendance(ctx, env, in.StaffID); err != nil {
		return nil, err
	}
	if _, err := staff.LockReferences(ctx, env.Q, env.Caller.StoreID, in.StaffID); err != nil {
		return nil, err
	}
	day, err := time.Parse(settings.DateLayout, in.BusinessDate)
	if err != nil {
		return nil, err
	}
	view, err := staffAttendance(ctx, env, day.Format("2006-01"), in.StaffID)
	if err != nil {
		return nil, err
	}
	anomaly := false
	for _, d := range view.Days {
		if d.BusinessDate != in.BusinessDate {
			continue
		}
		if d.Approval != nil {
			return nil, ops.Rule("这一天已经核准过")
		}
		anomaly = d.Late || d.Early || d.Missing
	}
	if !anomaly {
		return nil, ops.Rule("这一天没有异常，不需要核准")
	}
	row, err := env.Q.CreateAttendanceApproval(ctx, dbgen.CreateAttendanceApprovalParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, StaffID: in.StaffID, BusinessDate: dateValue(day), Reason: reason, ApprovedBy: env.Caller.StaffID})
	if err != nil {
		return nil, err
	}
	after, err := approvalResult(ctx, env, row)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "approve_attendance", audit.AttendanceApprovalTarget(row.ID), nil, after); err != nil {
		return nil, err
	}
	return after, nil
}

func revokeAttendanceApproval(ctx context.Context, env ops.Env, in *RevokeAttendanceApprovalInput) (*AttendanceApproval, error) {
	row, err := env.Q.GetAttendanceApproval(ctx, dbgen.GetAttendanceApprovalParams{StoreID: env.Caller.StoreID, ID: in.ID})
	if err != nil {
		return nil, attendanceError(err, "核准")
	}
	if err := lockAttendance(ctx, env, row.StaffID); err != nil {
		return nil, err
	}
	row, err = env.Q.LockAttendanceApproval(ctx, dbgen.LockAttendanceApprovalParams{StoreID: env.Caller.StoreID, ID: in.ID})
	if err != nil {
		return nil, err
	}
	if row.RevokedAt.Valid {
		return nil, ops.Rule("这次核准已撤销")
	}
	before, err := approvalResult(ctx, env, row)
	if err != nil {
		return nil, err
	}
	row, err = env.Q.RevokeAttendanceApproval(ctx, dbgen.RevokeAttendanceApprovalParams{StoreID: env.Caller.StoreID, ID: in.ID, RevokedBy: &env.Caller.StaffID, RevokedAt: pgtype.Timestamptz{Time: env.Now, Valid: true}})
	if err != nil {
		return nil, err
	}
	after, err := approvalResult(ctx, env, row)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "revoke_attendance_approval", audit.AttendanceApprovalTarget(row.ID), before, after); err != nil {
		return nil, err
	}
	return after, nil
}
