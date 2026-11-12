// Package closing 管理日结快照、封账和撤销封账。
package closing

import (
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/money"
)

// DayInput 指定一个营业日。
type DayInput struct {
	Date string `json:"date" format:"date" doc:"营业日 YYYY-MM-DD"`
}

// CloseDayInput 是日结时录入的现金清点数和各通道实际到账。
type CloseDayInput struct {
	Date           string       `json:"date" format:"date"`
	CashCounted    money.Amount `json:"cash_counted" doc:"现金清点数"`
	UnionpayActual money.Amount `json:"unionpay_actual" doc:"银联实际到账"`
	WechatActual   money.Amount `json:"wechat_actual" doc:"微信实际到账"`
	AlipayActual   money.Amount `json:"alipay_actual" doc:"支付宝实际到账"`
	Note           *string      `json:"note,omitempty" maxLength:"200"`
}

// UnsealDayInput 是撤销最后一个封账日的必填原因。
type UnsealDayInput struct {
	Reason string `json:"reason" minLength:"1" maxLength:"200"`
}

// ChannelSummary 是一个收款通道的营业日对账汇总。
type ChannelSummary struct {
	BusinessDate string            `json:"business_date"`
	Channel      string            `json:"channel"`
	Amounts      map[string]string `json:"amounts"`
	Counts       map[string]int    `json:"counts"`
}

// MethodAmount 是一种收款方式的系统金额。
type MethodAmount struct {
	Method string       `json:"method"`
	Label  string       `json:"label"`
	Amount money.Change `json:"amount"`
}

// UnrecordedSales 是没有收款方式的历史销售汇总。
type UnrecordedSales struct {
	Amount money.Change `json:"amount"`
	Count  int64        `json:"count"`
}

// SystemFigures 是一个营业日的系统应收汇总。
type SystemFigures struct {
	BusinessDate  string           `json:"business_date"`
	OnSite        []MethodAmount   `json:"on_site"`
	NonSite       []MethodAmount   `json:"non_site"`
	Unrecorded    UnrecordedSales  `json:"unrecorded"`
	GuestVisits   string           `json:"guest_visits"`
	CheckoutCount int64            `json:"checkout_count"`
	VoidCount     int64            `json:"void_count"`
	Channels      []ChannelSummary `json:"channels"`
}

// MethodDifference 是一种现场收款方式的实际、系统与差异。
type MethodDifference struct {
	Method     string       `json:"method"`
	Label      string       `json:"label"`
	System     money.Change `json:"system"`
	Actual     money.Amount `json:"actual"`
	Difference money.Change `json:"difference"`
}

// DayClose 是一次日结快照。
type DayClose struct {
	ID             uuid.UUID          `json:"id"`
	BusinessDate   string             `json:"business_date"`
	Version        int32              `json:"version"`
	System         SystemFigures      `json:"system"`
	Differences    []MethodDifference `json:"differences"`
	CashCounted    money.Amount       `json:"cash_counted"`
	UnionpayActual money.Amount       `json:"unionpay_actual"`
	WechatActual   money.Amount       `json:"wechat_actual"`
	AlipayActual   money.Amount       `json:"alipay_actual"`
	Note           *string            `json:"note" nullable:"true"`
	CreatedBy      uuid.UUID          `json:"created_by"`
	CreatedAt      time.Time          `json:"created_at"`
}

// DayCloseDetail 是某天的当前日结版本和全部历史版本。
type DayCloseDetail struct {
	Current  DayClose   `json:"current"`
	Versions []DayClose `json:"versions"`
}

// DayCloseListItem 是日结列表中的一天。
type DayCloseListItem struct {
	ID             uuid.UUID    `json:"id"`
	BusinessDate   string       `json:"business_date"`
	Version        int32        `json:"version"`
	CashCounted    money.Amount `json:"cash_counted"`
	UnionpayActual money.Amount `json:"unionpay_actual"`
	WechatActual   money.Amount `json:"wechat_actual"`
	AlipayActual   money.Amount `json:"alipay_actual"`
	Note           *string      `json:"note" nullable:"true"`
	Sealed         bool         `json:"sealed"`
	CreatedAt      time.Time    `json:"created_at"`
}

// DayCloseList 是日期范围内的当前日结版本。
type DayCloseList struct {
	SealedThrough *string            `json:"sealed_through" nullable:"true"`
	Items         []DayCloseListItem `json:"items"`
}

// SealResult 是封账或撤销封账后的门店状态。
type SealResult struct {
	SealedThrough *string   `json:"sealed_through" nullable:"true"`
	EventID       uuid.UUID `json:"event_id"`
	BusinessDate  string    `json:"business_date"`
	Kind          string    `json:"kind"`
}
