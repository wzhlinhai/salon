// Package perm 是权限目录和默认角色（ADR-0009）。
//
// 每项权限对应一个业务动作或一类数据。业务操作声明所需权限时使用这里的常量；
// 角色设置页面按这里的顺序和名称列出权限。
package perm

import "github.com/wzhlinhai/salon/backend/internal/ops"

const (
	OrderCreate       ops.Permission = "order.create"
	OrderEdit         ops.Permission = "order.edit"
	OrderVoid         ops.Permission = "order.void"
	OrderReprice      ops.Permission = "order.reprice"
	PaymentCollect    ops.Permission = "payment.collect"
	CardSell          ops.Permission = "card.sell"
	CardAdjust        ops.Permission = "card.adjust"
	GroupBuyRedeem    ops.Permission = "groupbuy.redeem"
	AfterSalesManage  ops.Permission = "aftersales.manage"
	QueueOperate      ops.Permission = "queue.operate"
	QueueView         ops.Permission = "queue.view"
	BookingManage     ops.Permission = "booking.manage"
	AttendanceClock   ops.Permission = "attendance.clock"
	AttendanceManage  ops.Permission = "attendance.manage"
	CustomerView      ops.Permission = "customer.view"
	CustomerFullPhone ops.Permission = "customer.full_phone"
	PerformanceView   ops.Permission = "performance.view"
	ReportView        ops.Permission = "report.view"
	ClosingDaily      ops.Permission = "closing.daily"
	ClosingSeal       ops.Permission = "closing.seal"
	PayrollManage     ops.Permission = "payroll.manage"
	StaffManage       ops.Permission = "staff.manage"
	AuditLogView      ops.Permission = "audit_log.view"
	RoleManage        ops.Permission = "role.manage"
	SettingsManage    ops.Permission = "settings.manage"
	DataImportExport  ops.Permission = "data.import_export"
)

// Def 描述一项权限。
type Def struct {
	Name  ops.Permission `json:"name"`
	Label string         `json:"label"`
	// Scoped：可以限制为本人或全店。
	Scoped bool `json:"scoped"`
	// Windowed：可以限制为 N 天内的单。
	Windowed bool `json:"windowed"`
}

// Catalogue 是全部权限，顺序即设置页面的显示顺序。
var Catalogue = []Def{
	{OrderCreate, "开单（含加项、挂单）", false, false},
	{OrderEdit, "改单", true, true},
	{OrderVoid, "撤单", false, true},
	{OrderReprice, "改价、打折", false, false},
	{PaymentCollect, "收款结算", false, false},
	{CardSell, "开卡、充值、卖套餐", false, false},
	{CardAdjust, "调整单、退卡", false, false},
	{GroupBuyRedeem, "团购核销", false, false},
	{AfterSalesManage, "售后单", false, false},
	{QueueOperate, "轮牌操作（点客、跳牌）", false, false},
	{QueueView, "看轮牌", false, false},
	{BookingManage, "预约管理", true, false},
	{AttendanceClock, "打卡", false, false},
	{AttendanceManage, "补卡、考勤审核", false, false},
	{CustomerView, "看顾客档案、卡余额", true, false},
	{CustomerFullPhone, "看完整手机号", false, false},
	{PerformanceView, "看业绩、提成", true, false},
	{ReportView, "营业报表", false, false},
	{ClosingDaily, "日结", false, false},
	{ClosingSeal, "封账", false, false},
	{PayrollManage, "提成结算、工资表", false, false},
	{StaffManage, "员工档案管理", false, false},
	{AuditLogView, "操作日志", false, false},
	{RoleManage, "角色权限管理", false, false},
	{SettingsManage, "门店设置（提成、卡、价格等规则）", false, false},
	{DataImportExport, "导入导出", false, false},
}

// Lookup 返回权限 p 的定义。
func Lookup(p ops.Permission) (Def, bool) {
	for _, d := range Catalogue {
		if d.Name == p {
			return d, true
		}
	}
	return Def{}, false
}

// Normalize 去掉 g 中该权限不支持的限制，并给支持数据范围的权限补上默认的全店范围。
func Normalize(p ops.Permission, g ops.Grant) ops.Grant {
	d, _ := Lookup(p)
	if !d.Scoped {
		g.Scope = ""
	} else if g.Scope == "" {
		g.Scope = ops.ScopeStore
	}
	if !d.Windowed {
		g.WindowDays = nil
	}
	return g
}

// All 返回老板的权限：目录中的每一项，限制最宽。
func All() map[ops.Permission]ops.Grant {
	out := make(map[ops.Permission]ops.Grant, len(Catalogue))
	for _, d := range Catalogue {
		out[d.Name] = Normalize(d.Name, ops.Grant{})
	}
	return out
}

// DefaultRole 是开店时创建的一个默认角色。
type DefaultRole struct {
	Name   string
	Grants map[ops.Permission]ops.Grant
}

func days(n int) *int { return &n }

var (
	store = ops.Grant{Scope: ops.ScopeStore}
	self  = ops.Grant{Scope: ops.ScopeSelf}
	plain = ops.Grant{}
)

// OwnerRoleName 是内置老板角色的名称。
const OwnerRoleName = "老板"

// DefaultRoles 是四个默认角色的初始权限（ADR-0009 的表格）。老板是内置角色，不在此列。
var DefaultRoles = []DefaultRole{
	{"店长", map[ops.Permission]ops.Grant{
		OrderCreate:       plain,
		OrderEdit:         {Scope: ops.ScopeStore, WindowDays: days(7)},
		OrderVoid:         {WindowDays: days(7)},
		OrderReprice:      plain,
		PaymentCollect:    plain,
		CardSell:          plain,
		CardAdjust:        plain,
		GroupBuyRedeem:    plain,
		AfterSalesManage:  plain,
		QueueOperate:      plain,
		QueueView:         plain,
		BookingManage:     store,
		AttendanceClock:   plain,
		AttendanceManage:  plain,
		CustomerView:      store,
		CustomerFullPhone: plain,
		PerformanceView:   store,
		ReportView:        plain,
		ClosingDaily:      plain,
		ClosingSeal:       plain,
		PayrollManage:     plain,
		StaffManage:       plain,
		AuditLogView:      plain,
	}},
	{"发型师", map[ops.Permission]ops.Grant{
		OrderCreate:     plain,
		OrderEdit:       {Scope: ops.ScopeSelf, WindowDays: days(0)},
		QueueView:       plain,
		BookingManage:   self,
		AttendanceClock: plain,
		CustomerView:    store,
		PerformanceView: self,
	}},
	{"助理", map[ops.Permission]ops.Grant{
		QueueView:       plain,
		AttendanceClock: plain,
		CustomerView:    store,
		PerformanceView: self,
	}},
	{"前台", map[ops.Permission]ops.Grant{
		OrderCreate:       plain,
		OrderEdit:         {Scope: ops.ScopeStore, WindowDays: days(0)},
		PaymentCollect:    plain,
		CardSell:          plain,
		GroupBuyRedeem:    plain,
		QueueOperate:      plain,
		QueueView:         plain,
		BookingManage:     store,
		AttendanceClock:   plain,
		CustomerView:      store,
		CustomerFullPhone: plain,
		ClosingDaily:      plain,
	}},
}
