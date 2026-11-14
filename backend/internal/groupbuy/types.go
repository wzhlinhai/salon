package groupbuy

import (
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/orders"
)

// Deal 是本店为某个平台商品登记的门店标价对应项目和平台购买价。
type Deal struct {
	ID            uuid.UUID    `json:"id"`
	Platform      string       `json:"platform"`
	Name          string       `json:"name"`
	PurchasePrice money.Amount `json:"purchase_price"`
	ItemID        uuid.UUID    `json:"item_id"`
	ItemName      string       `json:"item_name"`
	Active        bool         `json:"active"`
	CreatedBy     uuid.UUID    `json:"created_by"`
	CreatedByName string       `json:"created_by_name"`
	CreatedAt     time.Time    `json:"created_at"`
}

// DealList 是本店全部团购商品。
type DealList struct {
	Items []Deal `json:"items"`
}

// CreateDealInput 是新建团购商品。
type CreateDealInput struct {
	Platform      string       `json:"platform" minLength:"1" maxLength:"100"`
	Name          string       `json:"name" minLength:"1" maxLength:"200"`
	PurchasePrice money.Amount `json:"purchase_price"`
	ItemID        uuid.UUID    `json:"item_id"`
}

// UpdateDealInput 是替换团购商品的名称、平台购买价、项目和启用状态。
type UpdateDealInput struct {
	ID            uuid.UUID    `json:"id"`
	Name          string       `json:"name" minLength:"1" maxLength:"200"`
	PurchasePrice money.Amount `json:"purchase_price"`
	ItemID        uuid.UUID    `json:"item_id"`
	Active        bool         `json:"active"`
}

// ListDealsInput 是查看本店团购商品的参数。
type ListDealsInput struct{}

// LinkCouponOrderInput 把一张已核销的团购券关联到一张挂单。
type LinkCouponOrderInput struct {
	RedemptionID uuid.UUID `json:"redemption_id"`
	DealID       uuid.UUID `json:"deal_id"`
	OrderID      uuid.UUID `json:"order_id"`
}

// UnlinkCouponOrderInput 取消团购券与挂单的关联。
type UnlinkCouponOrderInput struct {
	RedemptionID uuid.UUID `json:"redemption_id"`
}

// Settlement 是一次手工录入的平台月结算额。
type Settlement struct {
	ID            uuid.UUID    `json:"id"`
	Platform      string       `json:"platform"`
	Month         string       `json:"month"`
	Amount        money.Amount `json:"amount"`
	Note          string       `json:"note"`
	EnteredBy     uuid.UUID    `json:"entered_by"`
	EnteredByName string       `json:"entered_by_name"`
	CreatedAt     time.Time    `json:"created_at"`
}

// RecordSettlementInput 是录入平台月结算额的参数。
type RecordSettlementInput struct {
	Platform string       `json:"platform" minLength:"1" maxLength:"100"`
	Month    string       `json:"month" pattern:"^[0-9]{4}-(0[1-9]|1[0-2])$"`
	Amount   money.Amount `json:"amount"`
	Note     string       `json:"note,omitempty" maxLength:"500"`
}

// GroupbuyMonthInput 是按平台和结算月查询团购核销与对账的参数。
type GroupbuyMonthInput struct {
	Platform string `json:"platform" minLength:"1" maxLength:"100"`
	Month    string `json:"month" pattern:"^[0-9]{4}-(0[1-9]|1[0-2])$"`
}

// GroupbuyMonthCount 是一类核销券的笔数和平台购买价合计。
type GroupbuyMonthCount struct {
	Count         int          `json:"count"`
	PurchaseTotal money.Amount `json:"purchase_total"`
}

// GroupbuyMonth 是某平台某结算月的核销分类、结算额、差额和录入历史。
type GroupbuyMonth struct {
	Platform       string             `json:"platform"`
	Month          string             `json:"month"`
	RedeemedCount  int                `json:"redeemed_count"`
	LinkedPaid     GroupbuyMonthCount `json:"linked_paid"`
	LinkedOpen     GroupbuyMonthCount `json:"linked_open"`
	Unlinked       GroupbuyMonthCount `json:"unlinked"`
	UnlinkedNoDeal int                `json:"unlinked_no_deal"`
	PurchaseTotal  money.Amount       `json:"purchase_total"`
	Settlement     *Settlement        `json:"settlement"`
	Difference     *money.Change      `json:"difference"`
	History        []Settlement       `json:"history"`
}

// TransformSchema 声明结算额和差额可以为空。
func (GroupbuyMonth) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["settlement"] = &huma.Schema{AnyOf: []*huma.Schema{s.Properties["settlement"], {Type: "null"}}}
	s.Properties["difference"] = &huma.Schema{AnyOf: []*huma.Schema{s.Properties["difference"], {Type: "null"}}}
	return s
}

// ListRedemptionsInput 是按营业日范围查看本店团购核销记录的参数。
type ListRedemptionsInput struct {
	From         string `json:"from" format:"date"`
	To           string `json:"to" format:"date"`
	Status       string `json:"status,omitempty"`
	UnlinkedOnly bool   `json:"unlinked_only,omitempty"`
}

// RedemptionList 是本店某段营业日内的团购核销记录。
type RedemptionList struct {
	Items []Redemption `json:"items"`
}

// OpenGroupbuyOrderInput 是核销记录、团购商品和开单项目的参数。
type OpenGroupbuyOrderInput struct {
	CustomerID   *uuid.UUID              `json:"customer_id,omitempty"`
	RedemptionID uuid.UUID               `json:"redemption_id"`
	DealID       uuid.UUID               `json:"deal_id"`
	Gender       string                  `json:"gender" enum:"male,female"`
	Designated   bool                    `json:"designated"`
	Items        []orders.OrderItemInput `json:"items" maxItems:"30"`
}

// OpenGroupbuyOrderOutput 是团购开单后的单据和已关联的核销记录。
type OpenGroupbuyOrderOutput struct {
	Order      orders.Order `json:"order"`
	Redemption Redemption   `json:"redemption"`
}
