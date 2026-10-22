// Package cards 管理储值卡类型、开卡、充值及只追加的卡流水。
package cards

import (
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/money"
)

// CardType 是门店定义的卡类型及有效期规则。
type CardType struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	Kind         string    `json:"kind"`
	ValidityKind string    `json:"validity_kind"`
	ValidityDate *string   `json:"validity_date,omitempty"`
	ValidityDays *int32    `json:"validity_days,omitempty"`
	Active       bool      `json:"active"`
	CreatedAt    time.Time `json:"created_at"`
}

// CreateCardTypeInput 是新建储值卡类型的参数。
type CreateCardTypeInput struct {
	Name         string  `json:"name" minLength:"1" maxLength:"40"`
	Kind         string  `json:"kind" enum:"stored_value"`
	ValidityKind string  `json:"validity_kind" enum:"none,fixed_date,days_after_open"`
	ValidityDate *string `json:"validity_date,omitempty"`
	ValidityDays *int32  `json:"validity_days,omitempty"`
}

// UpdateCardTypeInput 是编辑或停用卡类型的参数。
type UpdateCardTypeInput struct {
	CreateCardTypeInput
	ID     uuid.UUID `json:"id"`
	Active bool      `json:"active"`
}

// ListCardTypesInput 是列出本店卡类型的参数。
type ListCardTypesInput struct{}

// CardTypeList 是本店卡类型列表。
type CardTypeList struct {
	Items []CardType `json:"items"`
}

// Card 是顾客持有的卡，余额来自最后一条卡流水。
type Card struct {
	ID           uuid.UUID    `json:"id"`
	CustomerID   uuid.UUID    `json:"customer_id"`
	CardTypeID   uuid.UUID    `json:"card_type_id"`
	CardTypeName string       `json:"card_type_name"`
	OpenedAt     time.Time    `json:"opened_at"`
	ExpiresOn    *string      `json:"expires_on,omitempty"`
	Principal    money.Amount `json:"principal"`
	Bonus        money.Amount `json:"bonus"`
}

// TopUpInput 是本金、赠送金与业绩归属人。
type TopUpInput struct {
	Principal     money.Amount `json:"principal"`
	Bonus         money.Amount `json:"bonus"`
	SalesStaffIDs []uuid.UUID  `json:"sales_staff_ids" minItems:"1" maxItems:"10" uniqueItems:"true"`
}

// OpenCardInput 是为顾客开卡及首次充值的参数。
type OpenCardInput struct {
	TopUpInput
	CustomerID uuid.UUID `json:"customer_id"`
	CardTypeID uuid.UUID `json:"card_type_id"`
}

// RechargeInput 是给发行门店的卡充值的参数。
type RechargeInput struct {
	TopUpInput
	CardID uuid.UUID `json:"card_id"`
}

// ListCustomerCardsInput 指定要查看卡的顾客。
type ListCustomerCardsInput struct {
	CustomerID uuid.UUID `json:"customer_id"`
}

// CardList 是本店发行给顾客的卡列表。
type CardList struct {
	Items []Card `json:"items"`
}

// SalesStaff 是一笔开卡或充值的业绩归属人。
type SalesStaff struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// CardLedgerActor 是卡流水的操作员工。
type CardLedgerActor struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// CardLedgerEntry 是本金和赠送金的一次变动。
type CardLedgerEntry struct {
	ID              uuid.UUID       `json:"id"`
	Kind            string          `json:"kind"`
	PrincipalBefore money.Amount    `json:"principal_before"`
	PrincipalDelta  money.Amount    `json:"principal_delta"`
	PrincipalAfter  money.Amount    `json:"principal_after"`
	BonusBefore     money.Amount    `json:"bonus_before"`
	BonusDelta      money.Amount    `json:"bonus_delta"`
	BonusAfter      money.Amount    `json:"bonus_after"`
	BusinessDate    string          `json:"business_date"`
	At              time.Time       `json:"at"`
	Actor           CardLedgerActor `json:"actor"`
	SalesStaff      []SalesStaff    `json:"sales_staff"`
}

// ListCardLedgerInput 是卡流水的游标分页参数。
type ListCardLedgerInput struct {
	CardID   uuid.UUID  `json:"card_id"`
	BeforeID *uuid.UUID `json:"before_id,omitempty"`
	Limit    int32      `json:"limit,omitempty" minimum:"0" maximum:"200"`
}

// CardLedgerList 是由新到旧的卡流水。
type CardLedgerList struct {
	Entries []CardLedgerEntry `json:"entries"`
}
