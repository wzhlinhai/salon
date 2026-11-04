package attendance

import (
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/staff"
)

// ClockInInput 是本人打卡的空参数。
type ClockInInput struct{}

// AddPunchInput 是店长补卡的参数。
type AddPunchInput struct {
	StaffID uuid.UUID `json:"staff_id"`
	At      time.Time `json:"at"`
	Reason  string    `json:"reason" minLength:"1" maxLength:"100"`
}

// VoidPunchInput 是作废补卡的参数。
type VoidPunchInput struct {
	ID     uuid.UUID `json:"id"`
	Reason string    `json:"reason" minLength:"1" maxLength:"100"`
}

// ApproveAttendanceInput 是核准某员工某营业日全部异常的参数。
type ApproveAttendanceInput struct {
	StaffID      uuid.UUID `json:"staff_id"`
	BusinessDate string    `json:"business_date" format:"date"`
	Reason       string    `json:"reason" minLength:"1" maxLength:"100"`
}

// RevokeAttendanceApprovalInput 是撤销核准的参数。
type RevokeAttendanceApprovalInput struct {
	ID uuid.UUID `json:"id"`
}

// ListMyAttendanceInput 是本人考勤查询的月份。
type ListMyAttendanceInput struct {
	Month string `json:"month" pattern:"^[0-9]{4}-(0[1-9]|1[0-2])$"`
}

// GetStaffAttendanceInput 是店长查询员工考勤的参数。
type GetStaffAttendanceInput struct {
	Month   string    `json:"month" pattern:"^[0-9]{4}-(0[1-9]|1[0-2])$"`
	StaffID uuid.UUID `json:"staff_id"`
}

// ListAttendanceSummaryInput 是本店考勤统计的月份。
type ListAttendanceSummaryInput struct {
	Month string `json:"month" pattern:"^[0-9]{4}-(0[1-9]|1[0-2])$"`
}

// Punch 是保留作废信息的打卡记录。
type Punch struct {
	ID           uuid.UUID  `json:"id"`
	Staff        staff.Ref  `json:"staff"`
	At           time.Time  `json:"at"`
	BusinessDate string     `json:"business_date" format:"date"`
	Source       string     `json:"source" enum:"self,manual"`
	Reason       string     `json:"reason"`
	CreatedBy    staff.Ref  `json:"created_by"`
	CreatedAt    time.Time  `json:"created_at"`
	Voided       bool       `json:"voided"`
	VoidedBy     *staff.Ref `json:"voided_by"`
	VoidedAt     *time.Time `json:"voided_at"`
	VoidReason   *string    `json:"void_reason"`
}

// AttendanceApproval 是一个营业日全部异常的核准记录。
type AttendanceApproval struct {
	ID           uuid.UUID  `json:"id"`
	Staff        staff.Ref  `json:"staff"`
	BusinessDate string     `json:"business_date" format:"date"`
	Reason       string     `json:"reason"`
	ApprovedBy   staff.Ref  `json:"approved_by"`
	ApprovedAt   time.Time  `json:"approved_at"`
	RevokedBy    *staff.Ref `json:"revoked_by"`
	RevokedAt    *time.Time `json:"revoked_at"`
}

// AttendanceDay 是营业日的打卡、执行班次及原始异常。
type AttendanceDay struct {
	BusinessDate string              `json:"business_date" format:"date"`
	Shift        *Shift              `json:"shift"`
	Punches      []Punch             `json:"punches"`
	ClockIn      *time.Time          `json:"clock_in"`
	ClockOut     *time.Time          `json:"clock_out"`
	Late         bool                `json:"late"`
	LateMinutes  int                 `json:"late_minutes"`
	Early        bool                `json:"early"`
	EarlyMinutes int                 `json:"early_minutes"`
	Missing      bool                `json:"missing"`
	Approval     *AttendanceApproval `json:"approval"`
}

// AttendanceSummary 是一名员工的月度考勤统计。
type AttendanceSummary struct {
	Staff          staff.Ref `json:"staff"`
	Active         bool      `json:"active"`
	AttendanceDays int       `json:"attendance_days"`
	LateCount      int       `json:"late_count"`
	LateMinutes    int       `json:"late_minutes"`
	EarlyCount     int       `json:"early_count"`
	EarlyMinutes   int       `json:"early_minutes"`
	MissingCount   int       `json:"missing_count"`
	ApprovedCount  int       `json:"approved_count"`
}

// StaffAttendance 是一名员工的月度统计及每日明细。
type StaffAttendance struct {
	hasHistory bool
	Month      string            `json:"month"`
	Summary    AttendanceSummary `json:"summary"`
	Days       []AttendanceDay   `json:"days"`
}

// AttendanceSummaryList 是本店员工的月度考勤统计。
type AttendanceSummaryList struct {
	Entries []AttendanceSummary `json:"entries"`
}

func nullable(s *huma.Schema, fields ...string) *huma.Schema {
	for _, field := range fields {
		child := *s.Properties[field]
		child.Nullable = false
		s.Properties[field] = &huma.Schema{AnyOf: []*huma.Schema{&child, {Type: "null"}}}
	}
	return s
}

// TransformSchema 声明打卡尚未作废时为空的字段。
func (Punch) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	return nullable(s, "voided_by", "voided_at", "void_reason")
}

// TransformSchema 声明核准尚未撤销时为空的字段。
func (AttendanceApproval) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	return nullable(s, "revoked_by", "revoked_at")
}

// TransformSchema 声明明细可以没有班次、上班卡、下班卡或核准。
func (AttendanceDay) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	return nullable(s, "shift", "clock_in", "clock_out", "approval")
}
