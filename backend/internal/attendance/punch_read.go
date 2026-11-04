package attendance

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/payrollmonth"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

func punchView(row dbgen.Punch, refs map[uuid.UUID]staff.Ref) Punch {
	p := Punch{ID: row.ID, Staff: refs[row.StaffID], At: row.At.Time, BusinessDate: row.BusinessDate.Time.Format(settings.DateLayout), Source: row.Source, Reason: row.Reason, CreatedBy: refs[row.CreatedBy], CreatedAt: row.CreatedAt.Time, Voided: row.VoidedAt.Valid, VoidReason: row.VoidReason}
	if row.VoidedBy != nil {
		ref := refs[*row.VoidedBy]
		p.VoidedBy = &ref
	}
	if row.VoidedAt.Valid {
		p.VoidedAt = &row.VoidedAt.Time
	}
	return p
}

func approvalView(row dbgen.AttendanceApproval, refs map[uuid.UUID]staff.Ref) AttendanceApproval {
	a := AttendanceApproval{ID: row.ID, Staff: refs[row.StaffID], BusinessDate: row.BusinessDate.Time.Format(settings.DateLayout), Reason: row.Reason, ApprovedBy: refs[row.ApprovedBy], ApprovedAt: row.ApprovedAt.Time}
	if row.RevokedBy != nil {
		ref := refs[*row.RevokedBy]
		a.RevokedBy = &ref
	}
	if row.RevokedAt.Valid {
		a.RevokedAt = &row.RevokedAt.Time
	}
	return a
}

func dateValue(t time.Time) pgtype.Date { return pgtype.Date{Time: t, Valid: true} }

// loadAttendance 所有读取共用固定次数批查询；即使月份早于开店也可查看。
func loadAttendance(ctx context.Context, env ops.Env, month payrollmonth.Month) ([]StaffAttendance, error) {
	punches, err := env.Q.AttendancePunches(ctx, dbgen.AttendancePunchesParams{StoreID: env.Caller.StoreID, FirstDay: dateValue(month.First()), LastDay: dateValue(month.Last())})
	if err != nil {
		return nil, err
	}
	approvals, err := env.Q.AttendanceApprovals(ctx, dbgen.AttendanceApprovalsParams{StoreID: env.Caller.StoreID, FirstDay: dateValue(month.First()), LastDay: dateValue(month.Last())})
	if err != nil {
		return nil, err
	}
	timelines, err := StaffShiftTimelines(ctx, env.Q, env.Caller.StoreID)
	if err != nil {
		return nil, err
	}
	days := []time.Time{}
	for day := month.First(); !day.After(month.Last()); day = day.AddDate(0, 0, 1) {
		days = append(days, day)
	}
	rules, err := settings.EffectiveDays(ctx, env.Q, env.Caller.StoreID, days)
	if err != nil {
		return nil, err
	}
	grace := map[string]int{}
	for i, day := range days {
		grace[day.Format(settings.DateLayout)] = rules[i].AttendanceGraceMinutes()
	}
	today, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, env.Now)
	if err != nil {
		return nil, err
	}
	rows, err := env.Q.ListPayrollStaff(ctx, env.Caller.StoreID)
	if err != nil {
		return nil, err
	}
	refs := map[uuid.UUID]staff.Ref{}
	for _, row := range rows {
		refs[row.ID] = staff.Ref{ID: row.ID, Name: row.Name}
	}
	pp := map[uuid.UUID][]Punch{}
	aa := map[uuid.UUID][]AttendanceApproval{}
	for _, p := range punches {
		pp[p.StaffID] = append(pp[p.StaffID], punchView(p, refs))
	}
	for _, a := range approvals {
		aa[a.StaffID] = append(aa[a.StaffID], approvalView(a, refs))
	}
	out := make([]StaffAttendance, 0, len(rows))
	for _, row := range rows {
		v := calculate(AttendanceSummary{Staff: refs[row.ID], Active: row.Active}, pp[row.ID], aa[row.ID], timelines[row.ID], grace, today)
		v.Month = month.String()
		out = append(out, v)
	}
	return out, nil
}

func staffAttendance(ctx context.Context, env ops.Env, month string, id uuid.UUID) (*StaffAttendance, error) {
	m, err := payrollmonth.Parse(month)
	if err != nil {
		return nil, err
	}
	rows, err := loadAttendance(ctx, env, m)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.Summary.Staff.ID == id {
			return &row, nil
		}
	}
	return nil, fmt.Errorf("员工不存在：%w", ops.ErrNotFound)
}

func listMyAttendance(ctx context.Context, env ops.Env, in *ListMyAttendanceInput) (*StaffAttendance, error) {
	return staffAttendance(ctx, env, in.Month, env.Caller.StaffID)
}

func getStaffAttendance(ctx context.Context, env ops.Env, in *GetStaffAttendanceInput) (*StaffAttendance, error) {
	return staffAttendance(ctx, env, in.Month, in.StaffID)
}

func listAttendanceSummary(ctx context.Context, env ops.Env, in *ListAttendanceSummaryInput) (*AttendanceSummaryList, error) {
	m, err := payrollmonth.Parse(in.Month)
	if err != nil {
		return nil, err
	}
	rows, err := loadAttendance(ctx, env, m)
	if err != nil {
		return nil, err
	}
	out := &AttendanceSummaryList{Entries: []AttendanceSummary{}}
	for _, row := range rows {
		// 撤销的核准仍是月内历史，因此也保留该离职员工。
		if row.Summary.Active || row.hasHistory {
			out.Entries = append(out.Entries, row.Summary)
		}
	}
	return out, nil
}
