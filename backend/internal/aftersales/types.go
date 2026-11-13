// Package aftersales 管理售后单：关联原单、审批、退款、业绩与提成冲回。
package aftersales

import (
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

// RefundLine 是一笔售后退款的方式、金额和可选卡。
type RefundLine struct {
	CardID *uuid.UUID   `json:"card_id,omitempty" nullable:"true"`
	Method string       `json:"method" enum:"cash,unionpay,wechat,alipay,card_principal"`
	Amount money.Amount `json:"amount"`
}

// CreateAftersaleInput 是创建售后单的参数。
type CreateAftersaleInput struct {
	OrderID     uuid.UUID    `json:"order_id"`
	Type        string       `json:"type" enum:"repair,redo,partial_refund,full_refund"`
	Reason      string       `json:"reason" minLength:"1" maxLength:"200"`
	RefundLines []RefundLine `json:"refund_lines,omitempty" maxItems:"20"`
}

// ApproveAftersaleInput 是审批售后单的参数；两个扣减开关不填时用门店设置。
type ApproveAftersaleInput struct {
	ID                uuid.UUID `json:"id"`
	DeductPerformance *bool     `json:"deduct_performance,omitempty"`
	DeductCommission  *bool     `json:"deduct_commission,omitempty"`
}

// RejectAftersaleInput 是拒绝售后单的参数。
type RejectAftersaleInput struct {
	ID   uuid.UUID `json:"id"`
	Note *string   `json:"note,omitempty" maxLength:"200"`
}

// GetAftersaleInput 是查看一张售后单的参数。
type GetAftersaleInput struct {
	ID uuid.UUID `json:"id"`
}

// ListAftersalesInput 是按营业日范围查看售后单的参数。
type ListAftersalesInput struct {
	DateFrom string     `json:"date_from" format:"date"`
	DateTo   string     `json:"date_to" format:"date"`
	Status   *string    `json:"status,omitempty" enum:"pending,approved,rejected"`
	OrderID  *uuid.UUID `json:"order_id,omitempty"`
	Limit    int32      `json:"limit,omitempty" minimum:"0" maximum:"200"`
	BeforeID *uuid.UUID `json:"before_id,omitempty"`
}

// AftersaleRefund 是一笔已生效的退款记录。
type AftersaleRefund struct {
	ID           uuid.UUID    `json:"id"`
	Method       string       `json:"method"`
	Amount       money.Amount `json:"amount"`
	CardID       *uuid.UUID   `json:"card_id,omitempty" nullable:"true"`
	BusinessDate string       `json:"business_date" format:"date"`
	CreatedAt    time.Time    `json:"created_at"`
}

// AftersaleEvent 是售后单状态变化的一条只追加记录。
type AftersaleEvent struct {
	ID    uuid.UUID `json:"id"`
	Kind  string    `json:"kind" enum:"created,approved,rejected"`
	Note  *string   `json:"note,omitempty" nullable:"true"`
	Actor staff.Ref `json:"actor"`
	At    time.Time `json:"at"`
}

// Aftersale 是售后单视图，只引用原单 ID，不含顾客姓名或手机号。
type Aftersale struct {
	ApprovedBy        *staff.Ref        `json:"approved_by,omitempty"`
	ApprovedAt        *time.Time        `json:"approved_at,omitempty"`
	RejectedBy        *staff.Ref        `json:"rejected_by,omitempty"`
	RejectedAt        *time.Time        `json:"rejected_at,omitempty"`
	RejectNote        *string           `json:"reject_note,omitempty"`
	ID                uuid.UUID         `json:"id"`
	OrderID           uuid.UUID         `json:"order_id"`
	Type              string            `json:"type" enum:"repair,redo,partial_refund,full_refund"`
	Reason            string            `json:"reason"`
	Status            string            `json:"status" enum:"pending,approved,rejected"`
	BusinessDate      string            `json:"business_date" format:"date"`
	CreatedBy         staff.Ref         `json:"created_by"`
	CreatedAt         time.Time         `json:"created_at"`
	DeductPerformance bool              `json:"deduct_performance"`
	DeductCommission  bool              `json:"deduct_commission"`
	RefundLines       []RefundLine      `json:"refund_lines"`
	Refunds           []AftersaleRefund `json:"refunds"`
	Events            []AftersaleEvent  `json:"events"`
}

// AftersaleList 是按创建时间倒序的售后单列表。
type AftersaleList struct {
	Items []Aftersale `json:"items"`
}

// TransformSchema 声明尚未审批或拒绝时没有审批人和拒绝人。
func (Aftersale) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["approved_by"] = &huma.Schema{AnyOf: []*huma.Schema{s.Properties["approved_by"], {Type: "null"}}}
	s.Properties["rejected_by"] = &huma.Schema{AnyOf: []*huma.Schema{s.Properties["rejected_by"], {Type: "null"}}}
	return s
}
