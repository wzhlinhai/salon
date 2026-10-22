// Package orders 管理开单、项目行和挂单。
package orders

import (
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/money"
)

// Order 是单据视图，只引用顾客 ID，不包含顾客姓名或手机号。
type Order struct {
	ID           uuid.UUID   `json:"id"`
	CustomerID   uuid.UUID   `json:"customer_id"`
	Gender       string      `json:"gender" enum:"male,female"`
	Designated   bool        `json:"designated"`
	Status       string      `json:"status" enum:"open"`
	BusinessDate string      `json:"business_date" format:"date"`
	OpenedBy     uuid.UUID   `json:"opened_by"`
	OpenedAt     time.Time   `json:"opened_at"`
	Items        []OrderItem `json:"items"`
	Receivable   string      `json:"receivable"`
}

// OrderItem 是只记录一次价格的项目行。
type OrderItem struct {
	ID          uuid.UUID           `json:"id"`
	ItemID      uuid.UUID           `json:"item_id"`
	ItemName    string              `json:"item_name"`
	Price       money.Amount        `json:"price"`
	PriceSource string              `json:"price_source" enum:"standard,level"`
	LevelID     *uuid.UUID          `json:"level_id" nullable:"true"`
	AddedAt     time.Time           `json:"added_at"`
	Positions   []OrderItemPosition `json:"positions"`
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

// OrderList 是按开单时间排列的挂单列表。
type OrderList struct {
	Items []Order `json:"items"`
}
