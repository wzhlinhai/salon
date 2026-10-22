// Package cards 管理卡类型、开卡、充值及只追加的余额和次数流水。
package cards

import (
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/money"
)

// CardType 是门店定义的卡类型及有效期规则。
type CardType struct {
	Items        []CardTypeItem `json:"items"`
	ID           uuid.UUID      `json:"id"`
	Name         string         `json:"name"`
	Kind         string         `json:"kind"`
	ValidityKind string         `json:"validity_kind"`
	ValidityDate *string        `json:"validity_date,omitempty"`
	ValidityDays *int32         `json:"validity_days,omitempty"`
	Active       bool           `json:"active"`
	CreatedAt    time.Time      `json:"created_at"`
}

// CreateCardTypeInput 是新建卡类型的参数。
type CreateCardTypeInput struct {
	Items        []CardTypeItemInput `json:"items,omitempty" maxItems:"20"`
	Name         string              `json:"name" minLength:"1" maxLength:"40"`
	Kind         string              `json:"kind" enum:"stored_value,count,package"`
	ValidityKind string              `json:"validity_kind" enum:"none,fixed_date,days_after_open"`
	ValidityDate *string             `json:"validity_date,omitempty"`
	ValidityDays *int32              `json:"validity_days,omitempty"`
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
	Kind         string            `json:"kind"`
	Items        []RemainingCounts `json:"items"`
	CountsPrice  string            `json:"counts_price"`
	ID           uuid.UUID         `json:"id"`
	CustomerID   uuid.UUID         `json:"customer_id"`
	CardTypeID   uuid.UUID         `json:"card_type_id"`
	CardTypeName string            `json:"card_type_name"`
	OpenedAt     time.Time         `json:"opened_at"`
	ExpiresOn    *string           `json:"expires_on,omitempty"`
	Principal    money.Amount      `json:"principal"`
	Bonus        money.Amount      `json:"bonus"`
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
	Reason          string          `json:"reason,omitempty"`
	PrincipalBefore money.Amount    `json:"principal_before"`
	PrincipalDelta  money.Change    `json:"principal_delta"`
	PrincipalAfter  money.Amount    `json:"principal_after"`
	BonusBefore     money.Amount    `json:"bonus_before"`
	BonusDelta      money.Change    `json:"bonus_delta"`
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

// CardExpiry 是调整后的有效期，空日期表示不限。
type CardExpiry struct {
	ExpiresOn *string `json:"expires_on" format:"date"`
}

// AdjustCardInput 是以目标值更正卡的调整单参数。
type AdjustCardInput struct {
	Counts     []CardCountTarget `json:"counts,omitempty" maxItems:"20"`
	CardID     uuid.UUID         `json:"card_id"`
	Principal  *money.Amount     `json:"principal,omitempty"`
	Bonus      *money.Amount     `json:"bonus,omitempty"`
	Expiry     *CardExpiry       `json:"expiry,omitempty"`
	CardTypeID *uuid.UUID        `json:"card_type_id,omitempty"`
	Reason     string            `json:"reason" minLength:"1"`
}

// ListCardAdjustmentsInput 是调整单的游标分页参数。
type ListCardAdjustmentsInput struct {
	CardID   uuid.UUID  `json:"card_id"`
	BeforeID *uuid.UUID `json:"before_id,omitempty"`
	Limit    int32      `json:"limit,omitempty" minimum:"0" maximum:"200"`
}

// CardAdjustmentType 是调整单保存的卡类型。
type CardAdjustmentType struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// CardAdjustmentBalance 是调整单关联的余额变动。
type CardAdjustmentBalance struct {
	PrincipalBefore money.Amount `json:"principal_before"`
	PrincipalDelta  money.Change `json:"principal_delta"`
	PrincipalAfter  money.Amount `json:"principal_after"`
	BonusBefore     money.Amount `json:"bonus_before"`
	BonusDelta      money.Change `json:"bonus_delta"`
	BonusAfter      money.Amount `json:"bonus_after"`
}

// CardAdjustment 是只追加的更正记录及原因。
type CardAdjustment struct {
	Counts         []CardCountEntry       `json:"counts"`
	ID             uuid.UUID              `json:"id"`
	Reason         string                 `json:"reason"`
	ExpiryBefore   *CardExpiry            `json:"expiry_before,omitempty"`
	ExpiryAfter    *CardExpiry            `json:"expiry_after,omitempty"`
	CardTypeBefore *CardAdjustmentType    `json:"card_type_before,omitempty"`
	CardTypeAfter  *CardAdjustmentType    `json:"card_type_after,omitempty"`
	Balance        *CardAdjustmentBalance `json:"balance,omitempty"`
	BusinessDate   string                 `json:"business_date"`
	At             time.Time              `json:"at"`
	Actor          CardLedgerActor        `json:"actor"`
}

// CardAdjustmentList 是由新到旧的调整单。
type CardAdjustmentList struct {
	Entries []CardAdjustment `json:"entries"`
}

// StoredValue 是储值卡的种类。
const StoredValue = "stored_value"

// CountCard 是只含项目次数的次卡种类。
const CountCard = "count"

// Package 是兼含储值和项目次数的套餐种类。
const Package = "package"

// CardTypeItemInput 是卡类型内项目的购买次数、赠送次数和单次金额。
type CardTypeItemInput struct {
	ItemID    uuid.UUID    `json:"item_id"`
	PaidCount int32        `json:"paid_count" minimum:"1" maximum:"9999"`
	GiftCount int32        `json:"gift_count" minimum:"0" maximum:"9999"`
	UnitPrice money.Amount `json:"unit_price"`
}

// CardTypeItem 是卡类型内的项目定义。
type CardTypeItem struct {
	CardTypeItemInput
	ItemName string `json:"item_name"`
}

// RemainingCounts 是卡上一个项目由次数流水推得的剩余次数。
type RemainingCounts struct {
	ItemID        uuid.UUID    `json:"item_id"`
	ItemName      string       `json:"item_name"`
	UnitPrice     money.Amount `json:"unit_price"`
	PaidRemaining int32        `json:"paid_remaining"`
	GiftRemaining int32        `json:"gift_remaining"`
}

// CardCountTarget 是调整单中一个项目的目标剩余次数。
type CardCountTarget struct {
	ItemID uuid.UUID `json:"item_id"`
	Paid   int32     `json:"paid" minimum:"0" maximum:"9999"`
	Gift   int32     `json:"gift" minimum:"0" maximum:"9999"`
}

// CardCountItem 是次数流水关联的项目。
type CardCountItem struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// CardCountEntry 是购买次数与赠送次数的一次变动。
type CardCountEntry struct {
	ID           uuid.UUID       `json:"id"`
	Item         CardCountItem   `json:"item"`
	Kind         string          `json:"kind"`
	PaidBefore   int32           `json:"paid_before"`
	PaidDelta    int32           `json:"paid_delta"`
	PaidAfter    int32           `json:"paid_after"`
	GiftBefore   int32           `json:"gift_before"`
	GiftDelta    int32           `json:"gift_delta"`
	GiftAfter    int32           `json:"gift_after"`
	Reason       string          `json:"reason,omitempty"`
	BusinessDate string          `json:"business_date"`
	At           time.Time       `json:"at"`
	Actor        CardLedgerActor `json:"actor"`
}

// ListCardCountsInput 是次数流水的项目筛选和游标分页参数。
type ListCardCountsInput struct {
	CardID   uuid.UUID  `json:"card_id"`
	ItemID   *uuid.UUID `json:"item_id,omitempty"`
	BeforeID *uuid.UUID `json:"before_id,omitempty"`
	Limit    int32      `json:"limit,omitempty" minimum:"0" maximum:"200"`
}

// CardCountList 是由新到旧的次数流水。
type CardCountList struct {
	Entries []CardCountEntry `json:"entries"`
}
