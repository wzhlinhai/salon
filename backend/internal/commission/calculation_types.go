package commission

import (
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

// Commission 是由业绩与历史方案算出的单笔提成及解释。
type Commission struct {
	ID           uuid.UUID             `json:"id"`
	BusinessDate string                `json:"business_date" format:"date"`
	Source       string                `json:"source" enum:"item,consume,labor,sale"`
	Staff        staff.Ref             `json:"staff"`
	Amount       money.Change          `json:"amount"`
	Reversal     bool                  `json:"reversal"`
	Explanation  CommissionExplanation `json:"explanation"`
	createdAt    time.Time
}

// CommissionExplanation 留存计算使用的来源事实、基数、命中项和方案版本。
type CommissionExplanation struct {
	Origin     CommissionOrigin             `json:"origin"`
	Entries    []CommissionPerformanceEntry `json:"entries"`
	Designated *bool                        `json:"designated" nullable:"true"`
	Member     *bool                        `json:"member" nullable:"true"`
	Base       CommissionBase               `json:"base"`
	Match      CommissionMatch              `json:"match"`
	Version    CommissionVersion            `json:"version"`
	Reverses   *uuid.UUID                   `json:"reverses,omitempty"`
}

// CommissionOrigin 标识结账、项目岗位或销售流水来源，不含顾客资料。
type CommissionOrigin struct {
	OrderID      *uuid.UUID `json:"order_id,omitempty"`
	OrderItemID  *uuid.UUID `json:"order_item_id,omitempty"`
	Item         *staff.Ref `json:"item,omitempty"`
	Slot         *int16     `json:"slot,omitempty"`
	Position     *staff.Ref `json:"position,omitempty"`
	CardLedgerID *uuid.UUID `json:"card_ledger_id,omitempty"`
	RedPacketID  *uuid.UUID `json:"red_packet_id,omitempty"`
	CheckoutID   *uuid.UUID `json:"checkout_id,omitempty"`
	VoidID       *uuid.UUID `json:"void_id,omitempty"`
}

// CommissionPerformanceEntry 是一笔提成所包含的支付方式业绩记录。
type CommissionPerformanceEntry struct {
	ID         uuid.UUID    `json:"id"`
	Method     string       `json:"method"`
	PaidAmount money.Amount `json:"paid_amount"`
	Ratio      int16        `json:"ratio"`
	Amount     money.Amount `json:"amount"`
}

// CommissionBase 说明计提基数及项目行价格按实收份额的分摊。
type CommissionBase struct {
	Kind      string        `json:"kind" enum:"line_price,paid,performance,cash_performance,card_performance"`
	Amount    money.Amount  `json:"amount"`
	LinePrice *money.Amount `json:"line_price,omitempty"`
	UnitPaid  *money.Amount `json:"unit_paid,omitempty"`
	LinePaid  *string       `json:"line_paid,omitempty"`
}

// CommissionMatch 说明命中的格子键或覆盖目标以及比例或固定金额。
type CommissionMatch struct {
	Kind     string    `json:"kind" enum:"cell,override_staff_item,override_item,override_staff"`
	Cell     *Cell     `json:"cell,omitempty"`
	Override *Override `json:"override,omitempty"`
}

// CommissionVersion 是本笔提成使用的不可变方案版本。
type CommissionVersion struct {
	ID            uuid.UUID `json:"id"`
	Version       int32     `json:"version"`
	EffectiveFrom string    `json:"effective_from" format:"date"`
}
