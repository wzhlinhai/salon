// Package orders 管理开单、项目行和挂单。
package orders

import (
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/money"
)

// Order 是单据视图，只引用顾客 ID，不包含顾客姓名或手机号。
type Order struct {
	CheckoutID   *uuid.UUID     `json:"checkout_id" nullable:"true"`
	Payments     []OrderPayment `json:"payments"`
	ID           uuid.UUID      `json:"id"`
	CustomerID   uuid.UUID      `json:"customer_id"`
	Gender       string         `json:"gender" enum:"male,female"`
	Designated   bool           `json:"designated"`
	Status       string         `json:"status" enum:"open,merged,paid"`
	MergedInto   *uuid.UUID     `json:"merged_into" nullable:"true"`
	BusinessDate string         `json:"business_date" format:"date"`
	OpenedBy     uuid.UUID      `json:"opened_by"`
	OpenedAt     time.Time      `json:"opened_at"`
	Items        []OrderItem    `json:"items"`
	Discount     string         `json:"discount"`
	Net          string         `json:"net"`
	Receivable   string         `json:"receivable"`
}

// OrderItem 是只记录一次价格的项目行。
type OrderItem struct {
	CardTypeID    *uuid.UUID          `json:"card_type_id" nullable:"true"`
	Discount      money.Amount        `json:"discount"`
	ID            uuid.UUID           `json:"id"`
	ItemID        uuid.UUID           `json:"item_id"`
	ItemName      string              `json:"item_name"`
	Price         money.Amount        `json:"price"`
	PriceSource   string              `json:"price_source" enum:"standard,level,manual,member"`
	LevelID       *uuid.UUID          `json:"level_id" nullable:"true"`
	RemovedAt     *time.Time          `json:"removed_at" nullable:"true"`
	RemovedBy     *uuid.UUID          `json:"removed_by" nullable:"true"`
	RepriceReason *string             `json:"reprice_reason" nullable:"true"`
	AddedAt       time.Time           `json:"added_at"`
	Positions     []OrderItemPosition `json:"positions"`
}

// OrderItemPosition 是项目行保留的岗位及参与员工。
type OrderItemPosition struct {
	Slot       int16      `json:"slot"`
	PositionID uuid.UUID  `json:"position_id"`
	Required   bool       `json:"required"`
	StaffID    *uuid.UUID `json:"staff_id" nullable:"true"`
}

// OrderItemStaffInput 是一个项目岗位上的员工。
type OrderItemStaffInput struct {
	Slot    int16     `json:"slot"`
	StaffID uuid.UUID `json:"staff_id"`
}

// OrderItemInput 是添加项目及其岗位员工的参数。
type OrderItemInput struct {
	ItemID uuid.UUID             `json:"item_id"`
	Staff  []OrderItemStaffInput `json:"staff" maxItems:"20"`
}

// OpenOrderInput 是开单时选择的顾客、男客女客、指定和项目。
type OpenOrderInput struct {
	CustomerID *uuid.UUID       `json:"customer_id,omitempty"`
	Gender     string           `json:"gender" enum:"male,female"`
	Designated bool             `json:"designated"`
	Items      []OrderItemInput `json:"items" maxItems:"30"`
}

// AddOrderItemInput 是向单中添加一个项目的参数。
type AddOrderItemInput struct {
	OrderID uuid.UUID      `json:"order_id"`
	Item    OrderItemInput `json:"item"`
}

// ListOpenOrdersInput 是查看本店挂单的参数。
type ListOpenOrdersInput struct{}

// GetOrderInput 是查看一张单的参数。
type GetOrderInput struct {
	ID uuid.UUID `json:"id"`
}

// OrderList 是单据列表，结账时按输入顺序返回。
type OrderList struct {
	Items []Order `json:"items"`
}

// RemoveOrderItemInput 是去掉项目行的参数。
type RemoveOrderItemInput struct {
	OrderID     uuid.UUID `json:"order_id"`
	OrderItemID uuid.UUID `json:"order_item_id"`
}

// SetOrderItemStaffInput 是替换项目行全部岗位员工的参数。
type SetOrderItemStaffInput struct {
	OrderID     uuid.UUID             `json:"order_id"`
	OrderItemID uuid.UUID             `json:"order_item_id"`
	Staff       []OrderItemStaffInput `json:"staff" maxItems:"20"`
}

// RepriceOrderItemInput 是项目行改价的金额和原因。
type RepriceOrderItemInput struct {
	OrderID     uuid.UUID    `json:"order_id"`
	OrderItemID uuid.UUID    `json:"order_item_id"`
	Price       money.Amount `json:"price"`
	Reason      string       `json:"reason" minLength:"1" maxLength:"200"`
}

// MergeOrdersInput 是保留的单及并入的挂单。
type MergeOrdersInput struct {
	OrderID        uuid.UUID   `json:"order_id"`
	MergedOrderIDs []uuid.UUID `json:"merged_order_ids" minItems:"1" maxItems:"10"`
}

// OrderPayment 是单上的一笔支付，按登记顺序返回。
type OrderPayment struct {
	Card   *OrderPaymentCard `json:"card"`
	ID     uuid.UUID         `json:"id"`
	Method string            `json:"method" enum:"cash,unionpay,wechat,alipay,debt,card"`
	Amount money.Amount      `json:"amount"`
}

// PaymentInput 是支付方式和登记金额。
type PaymentInput struct {
	CardID *uuid.UUID   `json:"card_id,omitempty"`
	Method string       `json:"method" enum:"cash,unionpay,wechat,alipay,debt,card"`
	Amount money.Amount `json:"amount"`
}

// CheckoutOrderInput 是一张挂单及其支付。
type CheckoutOrderInput struct {
	OrderID  uuid.UUID      `json:"order_id"`
	Payments []PaymentInput `json:"payments" minItems:"1" maxItems:"10"`
}

// CheckoutOrdersInput 是一起结账的挂单，各单分别登记支付。
type CheckoutOrdersInput struct {
	Orders []CheckoutOrderInput `json:"orders" minItems:"1" maxItems:"10"`
}

// OrderPaymentCard 是一笔卡余额支付扣除的本金与赠送金。
type OrderPaymentCard struct {
	CardID    uuid.UUID    `json:"card_id"`
	Principal money.Amount `json:"principal"`
	Bonus     money.Amount `json:"bonus"`
}

// PreviewCheckoutInput 是试算一张挂单及可选计价卡的参数。
type PreviewCheckoutInput struct {
	OrderID uuid.UUID  `json:"order_id"`
	CardID  *uuid.UUID `json:"card_id,omitempty"`
}

// TransformSchema 声明支付卡对象或空值，兼容 Huma 的对象引用格式。
func (OrderPayment) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["card"] = &huma.Schema{AnyOf: []*huma.Schema{s.Properties["card"], {Type: "null"}}}
	return s
}
