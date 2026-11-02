// Package payroll 保存员工按月执行的底薪与加减项。
package payroll

import (
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

// MonthInput 是结算月查询参数。
type MonthInput struct {
	Month string `json:"month" pattern:"^[0-9]{4}-(0[1-9]|1[0-2])$"`
}

// SetBaseSalaryInput 是追加底薪设置的参数。
type SetBaseSalaryInput struct {
	MonthInput
	StaffID uuid.UUID    `json:"staff_id"`
	Mode    string       `json:"mode" enum:"base,guarantee"`
	Amount  money.Amount `json:"amount"`
}

// BaseSalary 是一次底薪设置的完整视图。
type BaseSalary struct {
	Staff          staff.Ref    `json:"staff"`
	EffectiveMonth string       `json:"effective_month" pattern:"^[0-9]{4}-(0[1-9]|1[0-2])$"`
	Mode           string       `json:"mode" enum:"base,guarantee"`
	Amount         money.Amount `json:"amount"`
	SavedBy        staff.Ref    `json:"saved_by"`
	SavedAt        time.Time    `json:"saved_at"`
}

// StaffBaseSalary 是员工在指定结算月执行的底薪，未设置时为空。
type StaffBaseSalary struct {
	Staff      staff.Ref   `json:"staff"`
	Active     bool        `json:"active"`
	BaseSalary *BaseSalary `json:"base_salary"`
}

// BaseSalaryList 是本店全部员工的当月底薪。
type BaseSalaryList struct {
	Entries []StaffBaseSalary `json:"entries"`
}

// AddPayrollAdjustmentInput 是新增加减项的参数。
type AddPayrollAdjustmentInput struct {
	MonthInput
	StaffID uuid.UUID    `json:"staff_id"`
	Name    string       `json:"name" minLength:"1" maxLength:"20"`
	Amount  money.Change `json:"amount"`
	Note    string       `json:"note,omitempty" maxLength:"100"`
}

// PayrollAdjustment 是一条月度员工加减项。
type PayrollAdjustment struct {
	ID        uuid.UUID    `json:"id"`
	Month     string       `json:"month" pattern:"^[0-9]{4}-(0[1-9]|1[0-2])$"`
	Staff     staff.Ref    `json:"staff"`
	Name      string       `json:"name"`
	Amount    money.Change `json:"amount"`
	Note      string       `json:"note"`
	CreatedBy staff.Ref    `json:"created_by"`
	CreatedAt time.Time    `json:"created_at"`
}

// DeletePayrollAdjustmentInput 是要删除的加减项标识。
type DeletePayrollAdjustmentInput struct {
	ID uuid.UUID `json:"id"`
}

// ListPayrollAdjustmentsInput 是月度加减项的筛选和分页参数。
type ListPayrollAdjustmentsInput struct {
	MonthInput
	StaffID  *uuid.UUID `json:"staff_id,omitempty"`
	Limit    int32      `json:"limit,omitempty" minimum:"0" maximum:"200"`
	BeforeID *uuid.UUID `json:"before_id,omitempty"`
}

// PayrollAdjustmentList 是按创建时间倒序排列的加减项页。
type PayrollAdjustmentList struct {
	Entries []PayrollAdjustment `json:"entries"`
}
