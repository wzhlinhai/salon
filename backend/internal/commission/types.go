// Package commission 管理门店提成方案的不可变版本并从业绩计算提成。
package commission

import (
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

// CommissionScheme 是包含规则、覆盖比例和不计提成员工的整体设置。
type CommissionScheme struct {
	Rules         []Rule      `json:"rules"`
	Overrides     []Override  `json:"overrides"`
	ExcludedStaff []uuid.UUID `json:"excluded_staff"`
}

// Rule 是一个业绩来源按所选维度区分的提成规则表。
type Rule struct {
	Source       string `json:"source" enum:"item,consume,labor,sale"`
	Base         string `json:"base" enum:"line_price,paid,performance,cash_performance,card_performance"`
	ByPosition   bool   `json:"by_position"`
	ByDesignated bool   `json:"by_designated"`
	ByMember     bool   `json:"by_member"`
	Cells        []Cell `json:"cells"`
}

// Cell 是提成规则表的一格，未列出的格子不计提成。
type Cell struct {
	PositionID  *uuid.UUID     `json:"position_id,omitempty"`
	Designated  *bool          `json:"designated,omitempty"`
	Member      *bool          `json:"member,omitempty"`
	Percent     *money.Percent `json:"percent,omitempty"`
	FixedAmount *money.Amount  `json:"fixed_amount,omitempty"`
}

// Override 是员工、项目或员工与项目组合的覆盖比例。
type Override struct {
	Source      string         `json:"source" enum:"item,consume,labor,sale"`
	StaffID     *uuid.UUID     `json:"staff_id,omitempty"`
	ItemID      *uuid.UUID     `json:"item_id,omitempty"`
	Percent     *money.Percent `json:"percent,omitempty"`
	FixedAmount *money.Amount  `json:"fixed_amount,omitempty"`
}

// CommissionCellView 是含岗位名称的规则格子。
type CommissionCellView struct {
	Cell
	Position *staff.Ref `json:"position,omitempty"`
}

// CommissionRuleView 是含岗位名称的提成规则。
type CommissionRuleView struct {
	Source       string               `json:"source" enum:"item,consume,labor,sale"`
	Base         string               `json:"base" enum:"line_price,paid,performance,cash_performance,card_performance"`
	ByPosition   bool                 `json:"by_position"`
	ByDesignated bool                 `json:"by_designated"`
	ByMember     bool                 `json:"by_member"`
	Cells        []CommissionCellView `json:"cells"`
}

// CommissionOverrideView 是含员工和项目名称的覆盖比例。
type CommissionOverrideView struct {
	Override
	Staff *staff.Ref `json:"staff,omitempty"`
	Item  *staff.Ref `json:"item,omitempty"`
}

// CommissionExcludedStaffView 是不计提成员工的标识和名称。
type CommissionExcludedStaffView struct {
	StaffID uuid.UUID `json:"staff_id"`
	Staff   staff.Ref `json:"staff"`
}

// CommissionSchemeView 是解析引用名称后的提成方案。
type CommissionSchemeView struct {
	Rules         []CommissionRuleView          `json:"rules"`
	Overrides     []CommissionOverrideView      `json:"overrides"`
	ExcludedStaff []CommissionExcludedStaffView `json:"excluded_staff"`
}

// CommissionSchemeVersionSummary 是不含方案内容的版本摘要。
type CommissionSchemeVersionSummary struct {
	ID            uuid.UUID `json:"id"`
	Version       int32     `json:"version"`
	EffectiveFrom string    `json:"effective_from" format:"date"`
	SavedBy       staff.Ref `json:"saved_by"`
	SavedAt       time.Time `json:"saved_at"`
}

// CommissionSchemeVersionView 是带提成方案内容的版本。
type CommissionSchemeVersionView struct {
	CommissionSchemeVersionSummary
	Scheme CommissionSchemeView `json:"scheme"`
}

// SaveCommissionSchemeInput 是保存新版本的参数。
type SaveCommissionSchemeInput struct {
	EffectiveFrom string           `json:"effective_from" format:"date"`
	Scheme        CommissionScheme `json:"scheme"`
}

// ListCommissionSchemeVersionsInput 是按版本倒序翻页的参数。
type ListCommissionSchemeVersionsInput struct {
	Limit         int32  `json:"limit,omitempty" minimum:"0" maximum:"200"`
	BeforeVersion *int32 `json:"before_version,omitempty"`
}

// CommissionSchemeVersionList 是不含方案内容的版本分页。
type CommissionSchemeVersionList struct {
	Versions []CommissionSchemeVersionSummary `json:"versions"`
}

// GetCommissionSchemeInput 是按版本号或营业日二选一查询的参数。
type GetCommissionSchemeInput struct {
	Version *int32  `json:"version,omitempty"`
	Date    *string `json:"date,omitempty" format:"date"`
}
