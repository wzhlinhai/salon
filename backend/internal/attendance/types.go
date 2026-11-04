// Package attendance 保存班次和按营业日生效的员工班次。
package attendance

import (
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

// CreateShiftInput 是新建班次的参数。
type CreateShiftInput struct {
	Name      string `json:"name" minLength:"1" maxLength:"20"`
	StartTime string `json:"start_time" pattern:"^([01][0-9]|2[0-3]):[0-5][0-9]$"`
	EndTime   string `json:"end_time" pattern:"^([01][0-9]|2[0-3]):[0-5][0-9]$"`
}

// Shift 是时间不可修改的班次视图。
type Shift struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	StartTime string    `json:"start_time" pattern:"^([01][0-9]|2[0-3]):[0-5][0-9]$"`
	EndTime   string    `json:"end_time" pattern:"^([01][0-9]|2[0-3]):[0-5][0-9]$"`
	Active    bool      `json:"active"`
	CreatedBy staff.Ref `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

// DeactivateShiftInput 是要停用的班次标识。
type DeactivateShiftInput struct {
	ID uuid.UUID `json:"id"`
}

// ListShiftsInput 是无需参数的班次查询。
type ListShiftsInput struct{}

// ShiftList 是本店全部班次。
type ShiftList struct {
	Entries []Shift `json:"entries"`
}

// SetStaffShiftInput 是追加员工班次的参数，空班次表示从该日起无班次。
type SetStaffShiftInput struct {
	StaffID       uuid.UUID  `json:"staff_id"`
	ShiftID       *uuid.UUID `json:"shift_id,omitempty" nullable:"true"`
	EffectiveFrom string     `json:"effective_from" format:"date" doc:"从哪一个营业日开始执行，不能早于今天"`
}

// StaffShift 是一次员工班次指定的完整视图。
type StaffShift struct {
	Staff         staff.Ref `json:"staff"`
	Shift         *Shift    `json:"shift"`
	EffectiveFrom string    `json:"effective_from" format:"date"`
	SavedBy       staff.Ref `json:"saved_by"`
	SavedAt       time.Time `json:"saved_at"`
}

// ListStaffShiftsInput 是要查看的营业日。
type ListStaffShiftsInput struct {
	Date string `json:"date" format:"date"`
}

// StaffShiftEntry 是员工当天执行的班次及以后的修改。
type StaffShiftEntry struct {
	Staff    staff.Ref    `json:"staff"`
	Active   bool         `json:"active"`
	Current  *StaffShift  `json:"current"`
	Upcoming []StaffShift `json:"upcoming"`
}

// StaffShiftList 是本店全部在职及离职员工的班次。
type StaffShiftList struct {
	Entries []StaffShiftEntry `json:"entries"`
}

// TransformSchema 声明员工从某日起可以没有班次。
func (StaffShift) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["shift"] = &huma.Schema{AnyOf: []*huma.Schema{s.Properties["shift"], {Type: "null"}}}
	return s
}

// TransformSchema 声明从未指定班次时没有当前记录。
func (StaffShiftEntry) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["current"] = &huma.Schema{AnyOf: []*huma.Schema{s.Properties["current"], {Type: "null"}}}
	return s
}
