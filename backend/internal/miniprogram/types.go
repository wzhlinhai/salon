package miniprogram

import (
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/booking"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

// MyProfile 是顾客本人看到的资料。
type MyProfile struct {
	Name        *string `json:"name" nullable:"true"`
	Phone       *string `json:"phone" nullable:"true" doc:"打码手机号"`
	PhoneMasked bool    `json:"phone_masked"`
	StoreName   string  `json:"store_name"`
}

// MyCardItem 是卡上某个项目的剩余购买和赠送次数。
type MyCardItem struct {
	ItemID        uuid.UUID    `json:"item_id"`
	ItemName      string       `json:"item_name"`
	UnitPrice     money.Amount `json:"unit_price"`
	PaidRemaining int32        `json:"paid_remaining"`
	GiftRemaining int32        `json:"gift_remaining"`
}

// MyCard 是顾客本人的一张卡。
type MyCard struct {
	Kind         string       `json:"kind"`
	Status       string       `json:"status"`
	ID           uuid.UUID    `json:"id"`
	CardTypeName string       `json:"card_type_name"`
	ExpiresOn    *string      `json:"expires_on" nullable:"true"`
	Principal    money.Amount `json:"principal"`
	Bonus        money.Amount `json:"bonus"`
	Balance      money.Amount `json:"balance"`
	Points       int32        `json:"points"`
	OpenedAt     time.Time    `json:"opened_at"`
	Items        []MyCardItem `json:"items"`
}

// MyCards 是顾客本人的卡列表。
type MyCards struct {
	Items []MyCard `json:"items"`
}

// MyCardLedgerInput 是查看一张本人卡的流水参数。
type MyCardLedgerInput struct {
	CardID   uuid.UUID  `json:"card_id"`
	BeforeID *uuid.UUID `json:"before_id,omitempty" doc:"上一页最后一条的 ID"`
	Limit    int32      `json:"limit,omitempty" minimum:"0" maximum:"200"`
}

// MyCardLedgerEntry 是顾客可见的卡余额变动。
type MyCardLedgerEntry struct {
	OrderID        *uuid.UUID   `json:"order_id" nullable:"true"`
	ID             uuid.UUID    `json:"id"`
	Kind           string       `json:"kind"`
	BusinessDate   string       `json:"business_date"`
	PrincipalDelta money.Change `json:"principal_delta"`
	PrincipalAfter money.Amount `json:"principal_after"`
	BonusDelta     money.Change `json:"bonus_delta"`
	BonusAfter     money.Amount `json:"bonus_after"`
}

// MyCardLedger 是由新到旧的本人卡流水。
type MyCardLedger struct {
	Entries []MyCardLedgerEntry `json:"entries"`
}

// MyOrdersInput 是查看本人消费明细的游标分页参数。
type MyOrdersInput struct {
	BeforeID *uuid.UUID `json:"before_id,omitempty" doc:"上一页最后一条的 ID"`
	Limit    int32      `json:"limit,omitempty" minimum:"0" maximum:"200"`
}

// MyOrderItem 是消费明细里的一个项目行。
type MyOrderItem struct {
	ItemName   string       `json:"item_name"`
	Price      money.Amount `json:"price"`
	StaffNames []string     `json:"staff_names" doc:"服务岗位员工姓名"`
}

// MyOrderPayment 是一笔支付的顾客可见信息。
type MyOrderPayment struct {
	CardName *string      `json:"card_name" nullable:"true"`
	Method   string       `json:"method"`
	Amount   money.Amount `json:"amount"`
}

// MyOrder 是顾客本人的一张已结账或已撤单的单。
type MyOrder struct {
	ID           uuid.UUID        `json:"id"`
	BusinessDate string           `json:"business_date"`
	Status       string           `json:"status"`
	Voided       bool             `json:"voided"`
	PaidAmount   money.Amount     `json:"paid_amount"`
	Items        []MyOrderItem    `json:"items"`
	Payments     []MyOrderPayment `json:"payments"`
}

// MyOrders 是由新到旧的消费明细。
type MyOrders struct {
	Entries []MyOrder `json:"entries"`
}

// MyAppointment 是顾客本人的一条预约。
type MyAppointment struct {
	ID       uuid.UUID                 `json:"id"`
	Status   string                    `json:"status"`
	StartsAt time.Time                 `json:"starts_at"`
	EndsAt   time.Time                 `json:"ends_at"`
	Staff    staff.Ref                 `json:"staff"`
	Items    []booking.AppointmentItem `json:"items"`
}

// MyAppointments 是顾客本人的预约列表。
type MyAppointments struct {
	Entries []MyAppointment `json:"entries"`
}
