// Package redpackets 管理独立于卡的一次性顾客红包。
package redpackets

import (
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/money"
)

// RedPacketSalesStaff 是卖红包的业绩归属人。
type RedPacketSalesStaff struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// RedPacket 是红包及其使用、撤回状态，不含顾客档案字段。
type RedPacket struct {
	ID           uuid.UUID             `json:"id"`
	Kind         string                `json:"kind" enum:"sold,gift"`
	FaceValue    money.Amount          `json:"face_value"`
	Price        *money.Amount         `json:"price" nullable:"true"`
	SalesStaff   []RedPacketSalesStaff `json:"sales_staff"`
	ExpiresOn    string                `json:"expires_on" format:"date"`
	BusinessDate string                `json:"business_date" format:"date"`
	IssuedBy     uuid.UUID             `json:"issued_by"`
	At           time.Time             `json:"at"`
	Status       string                `json:"status" enum:"available,used,revoked,expired"`
	OrderID      *uuid.UUID            `json:"order_id" nullable:"true"`
	UsedAmount   *money.Amount         `json:"used_amount" nullable:"true"`
	RevokeReason *string               `json:"revoke_reason" nullable:"true"`
	RevokedBy    *uuid.UUID            `json:"revoked_by" nullable:"true"`
	RevokedAt    *time.Time            `json:"revoked_at" nullable:"true"`
}

// IssueRedPacketInput 是卖或送红包的参数。
type IssueRedPacketInput struct {
	CustomerID    uuid.UUID     `json:"customer_id"`
	Kind          string        `json:"kind" enum:"sold,gift"`
	FaceValue     money.Amount  `json:"face_value"`
	ExpiresOn     string        `json:"expires_on" format:"date"`
	Price         *money.Amount `json:"price,omitempty"`
	SalesStaffIDs []uuid.UUID   `json:"sales_staff_ids,omitempty" maxItems:"10" uniqueItems:"true"`
}

// RevokeRedPacketInput 是撤回未用红包的原因。
type RevokeRedPacketInput struct {
	RedPacketID uuid.UUID `json:"red_packet_id"`
	Reason      string    `json:"reason" minLength:"1" maxLength:"200"`
}

// ListCustomerRedPacketsInput 是顾客红包列表的参数。
type ListCustomerRedPacketsInput struct {
	CustomerID uuid.UUID `json:"customer_id"`
}

// RedPacketList 是从新到旧的顾客红包。
type RedPacketList struct {
	Items []RedPacket `json:"items"`
}
