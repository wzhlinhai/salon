// Package reports 提供营业、员工、项目、卡项和新客来源报表。
package reports

import (
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

// BusinessReportInput 是营业日报或月报的查询范围。
type BusinessReportInput struct {
	From        string `json:"from" doc:"营业日 YYYY-MM-DD，或月份 YYYY-MM"`
	To          string `json:"to" doc:"营业日 YYYY-MM-DD，或月份 YYYY-MM"`
	Granularity string `json:"granularity" enum:"day,month" doc:"day 为日报，month 为月报"`
}

// ReportMethodAmount 是一种收款方式的报表金额。
type ReportMethodAmount struct {
	Method string       `json:"method"`
	Label  string       `json:"label"`
	Amount money.Change `json:"amount"`
}

// BusinessReportRow 是一个营业日或月份的营业汇总。
type BusinessReportRow struct {
	Period             string               `json:"period" doc:"日报为 YYYY-MM-DD，月报为 YYYY-MM"`
	OnSite             []ReportMethodAmount `json:"on_site"`
	NonSite            []ReportMethodAmount `json:"non_site"`
	ServiceRevenue     money.Change         `json:"service_revenue" doc:"结账单实收合计，撤单在撤单日冲回"`
	SalesRevenue       money.Change         `json:"sales_revenue" doc:"开卡、充值、补差价和卖红包的收款合计"`
	CardPrincipal      money.Change         `json:"card_principal" doc:"耗卡本金"`
	CardBonus          money.Change         `json:"card_bonus" doc:"耗卡赠送金"`
	CountAmount        money.Change         `json:"count_amount" doc:"扣次金额"`
	GroupbuyDeduct     money.Change         `json:"groupbuy_deduct" doc:"团购抵扣"`
	NewDebt            money.Change         `json:"new_debt" doc:"新增挂账"`
	DebtRepayment      money.Change         `json:"debt_repayment" doc:"挂账还款"`
	AftersaleRefund    money.Change         `json:"aftersale_refund" doc:"售后退款"`
	GuestTotal         string               `json:"guest_total" doc:"四位小数的客数净额"`
	GuestDesignated    string               `json:"guest_designated"`
	GuestNotDesignated string               `json:"guest_not_designated"`
	GuestMale          string               `json:"guest_male"`
	GuestFemale        string               `json:"guest_female"`
	CheckoutCount      int64                `json:"checkout_count"`
	VoidCount          int64                `json:"void_count"`
}

// BusinessReport 是按营业日或月份排列的营业汇总及合计。
type BusinessReport struct {
	Granularity string              `json:"granularity" enum:"day,month"`
	Rows        []BusinessReportRow `json:"rows"`
	Totals      BusinessReportRow   `json:"totals"`
}

// StaffReportInput 是按营业日查询员工业绩报表的参数。
type StaffReportInput struct {
	From    string     `json:"from" format:"date"`
	To      string     `json:"to" format:"date"`
	StaffID *uuid.UUID `json:"staff_id,omitempty"`
}

// StaffReportRow 是一位员工或未分配业绩的报表行。
type StaffReportRow struct {
	Staff              *staff.Ref   `json:"staff"`
	ItemPerformance    money.Change `json:"item_performance"`
	ConsumePerformance money.Change `json:"consume_performance"`
	LaborPerformance   money.Change `json:"labor_performance"`
	SalePerformance    money.Change `json:"sale_performance"`
	CashPerformance    money.Change `json:"cash_performance"`
	CardPerformance    money.Change `json:"card_performance"`
	GuestTotal         string       `json:"guest_total"`
	GuestDesignated    string       `json:"guest_designated"`
	GuestNotDesignated string       `json:"guest_not_designated"`
	GuestMale          string       `json:"guest_male"`
	GuestFemale        string       `json:"guest_female"`
	ServiceItemLines   int64        `json:"service_item_lines"`
	CommissionTotal    money.Change `json:"commission_total"`
}

// StaffReport 是按员工排列的业绩报表及合计。
type StaffReport struct {
	Rows   []StaffReportRow `json:"rows"`
	Totals StaffReportRow   `json:"totals"`
}

// ItemReportInput 是按营业日查询项目分析的参数。
type ItemReportInput struct {
	From string `json:"from" format:"date"`
	To   string `json:"to" format:"date"`
}

// ItemReportRow 是一个项目或商品的报表行。
type ItemReportRow struct {
	ItemID       uuid.UUID    `json:"item_id"`
	ItemName     string       `json:"item_name"`
	ItemKind     string       `json:"item_kind" enum:"service,goods"`
	Times        int64        `json:"times"`
	LinePrice    money.Change `json:"line_price"`
	Paid         money.Change `json:"paid"`
	CardPaid     money.Change `json:"card_paid"`
	CountPaid    money.Change `json:"count_paid"`
	GroupbuyPaid money.Change `json:"groupbuy_paid"`
	Designated   int64        `json:"designated"`
	Performance  money.Change `json:"performance"`
}

// ItemReport 是按项目排列的分析报表及合计。
type ItemReport struct {
	Rows   []ItemReportRow `json:"rows"`
	Totals ItemReportRow   `json:"totals"`
}

// CardReportInput 是按营业日查询卡项分析的参数。
type CardReportInput struct {
	From string `json:"from" format:"date"`
	To   string `json:"to" format:"date"`
}

// CardTypeRef 是报表中的卡类型引用。
type CardTypeRef struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// CardReportRow 是一个卡类型的分析报表行。
type CardReportRow struct {
	CardType          CardTypeRef  `json:"card_type"`
	OpenCount         int64        `json:"open_count"`
	OpenPrincipal     money.Change `json:"open_principal"`
	OpenBonus         money.Change `json:"open_bonus"`
	RechargePrincipal money.Change `json:"recharge_principal"`
	RechargeBonus     money.Change `json:"recharge_bonus"`
	Upgrade           money.Change `json:"upgrade"`
	ConsumedPrincipal money.Change `json:"consumed_principal"`
	ConsumedBonus     money.Change `json:"consumed_bonus"`
	CountUsed         int64        `json:"count_used"`
	CountAmount       money.Change `json:"count_amount"`
	RefundCount       int64        `json:"refund_count"`
	RefundAmount      money.Change `json:"refund_amount"`
	EndingBalance     money.Change `json:"ending_balance"`
	EndingCounts      int64        `json:"ending_counts"`
}

// CardReport 是按卡类型排列的分析报表及合计。
type CardReport struct {
	Rows   []CardReportRow `json:"rows"`
	Totals CardReportRow   `json:"totals"`
}

// CustomerSourceReportInput 是按营业日查询新客来源报表的参数。
type CustomerSourceReportInput struct {
	From string `json:"from" format:"date"`
	To   string `json:"to" format:"date"`
}

// CustomerSourceReportRow 是一个顾客来源的新客汇总。
type CustomerSourceReportRow struct {
	SourceID     *uuid.UUID   `json:"source_id" nullable:"true"`
	SourceName   *string      `json:"source_name" nullable:"true" doc:"未填写来源时为空"`
	NewCustomers int64        `json:"new_customers"`
	Revenue      money.Change `json:"revenue"`
	Returning    int64        `json:"returning"`
}

// CustomerSourceReport 是新客来源报表、合计及未识别单数。
type CustomerSourceReport struct {
	Rows               []CustomerSourceReportRow `json:"rows"`
	Totals             CustomerSourceReportRow   `json:"totals"`
	UnidentifiedOrders int64                     `json:"unidentified_orders"`
}

func dayKey(day time.Time) string { return day.Format("2006-01-02") }
