// Package payroll 保存员工按月执行的底薪与加减项。
package payroll

import (
	"time"

	"github.com/danielgtaylor/huma/v2"
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

// PayrollTotals 是工资表各金额列的全店合计。
type PayrollTotals struct {
	AppliedAmount     money.Amount `json:"applied_amount"`
	BasePay           money.Change `json:"base_pay"`
	ItemCommission    money.Change `json:"item_commission"`
	ConsumeCommission money.Change `json:"consume_commission"`
	LaborCommission   money.Change `json:"labor_commission"`
	SaleCommission    money.Change `json:"sale_commission"`
	CommissionTotal   money.Change `json:"commission_total"`
	GuaranteeTopUp    money.Amount `json:"guarantee_top_up"`
	AdjustmentsTotal  money.Change `json:"adjustments_total"`
	Total             money.Change `json:"total"`
}

// PayrollLine 是一位员工的工资表行。
type PayrollLine struct {
	Staff  staff.Ref `json:"staff"`
	Active bool      `json:"active"`
	Mode   string    `json:"mode" enum:"base,guarantee"`
	PayrollTotals
}

// Payroll 是结算月的工资表及锁定状态。
type Payroll struct {
	Month    string        `json:"month"`
	Locked   bool          `json:"locked"`
	LockedBy *staff.Ref    `json:"locked_by"`
	LockedAt *time.Time    `json:"locked_at"`
	Lines    []PayrollLine `json:"lines"`
	Totals   PayrollTotals `json:"totals"`
}

// PayrollMonth 是一个结算月的锁定状态及当前是否允许锁定。
type PayrollMonth struct {
	Month    string     `json:"month"`
	Locked   bool       `json:"locked"`
	LockedBy *staff.Ref `json:"locked_by"`
	LockedAt *time.Time `json:"locked_at"`
	Lockable bool       `json:"lockable"`
}

// PayrollMonthList 是从当前营业月倒序到开店月的结算状态。
type PayrollMonthList struct {
	Entries []PayrollMonth `json:"entries"`
}

// ListPayrollMonthsInput 是无需参数的结算状态查询。
type ListPayrollMonthsInput struct{}

// TransformSchema 声明开放工资表的锁定人可以为空。
func (Payroll) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["locked_by"] = &huma.Schema{AnyOf: []*huma.Schema{s.Properties["locked_by"], {Type: "null"}}}
	return s
}

// TransformSchema 声明开放结算月的锁定人可以为空。
func (PayrollMonth) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["locked_by"] = &huma.Schema{AnyOf: []*huma.Schema{s.Properties["locked_by"], {Type: "null"}}}
	return s
}
