package cards

import (
	"cmp"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

// CardSnapshot 是结账规划使用的卡值快照，余额为本金与赠送金合计。
type CardSnapshot struct {
	ID, CustomerID, CardTypeID uuid.UUID
	Kind, Status               string
	ExpiresOn                  *time.Time
	OpenedAt                   time.Time
	Balance                    money.Total
}

// Snapshot 返回已锁定或只读加载的卡值快照。
func (c *PaymentCard) Snapshot() CardSnapshot {
	var expiry *time.Time
	if c.row.ExpiresOn.Valid {
		expiry = new(c.row.ExpiresOn.Time)
	}
	return CardSnapshot{ID: c.row.ID, CustomerID: c.row.CustomerID, CardTypeID: c.row.CardTypeID, Kind: c.before.Kind, Status: c.row.Status, ExpiresOn: expiry, OpenedAt: c.row.OpenedAt.Time, Balance: c.Balance()}
}

// CheckHolder 按单的规则快照检查是否允许用别人的卡。
func (c CardSnapshot) CheckHolder(customerID uuid.UUID, othersCardPayment string) error {
	if c.CustomerID != customerID && othersCardPayment != "allowed" {
		return ops.Rule("不能用别人的卡")
	}
	return nil
}

// CheckBalancePayment 检查卡是否可以用于卡余额支付或作为计价卡。
func (c CardSnapshot) CheckBalancePayment() error {
	if c.Kind == CountCard {
		return ops.Rule("次卡不能用卡余额支付")
	}
	return nil
}

// CheckSelection 检查锁后候选卡仍属于顾客且可用于卡余额支付。
func (c CardSnapshot) CheckSelection(customerID uuid.UUID, day time.Time) error {
	if c.CustomerID != customerID {
		return ops.Rule("不能用别人的卡")
	}
	if c.Status == "refunded" {
		return ops.Rule("卡已退")
	}
	if c.ExpiresOn != nil && c.ExpiresOn.Before(day) {
		return ops.Rule("卡已过期")
	}
	if err := c.CheckBalancePayment(); err != nil {
		return err
	}
	if c.Balance.Cmp(money.Amount{}) <= 0 {
		return ops.Rule("卡余额不足")
	}
	return nil
}

// CompareSelectionTie 按到期日（不限最后）、开卡时间、UUID 比较选卡的同序卡。
func (c CardSnapshot) CompareSelectionTie(other CardSnapshot) int {
	if (c.ExpiresOn != nil) != (other.ExpiresOn != nil) {
		if c.ExpiresOn != nil {
			return -1
		}
		return 1
	}
	if c.ExpiresOn != nil {
		if n := (*c.ExpiresOn).Compare((*other.ExpiresOn)); n != 0 {
			return n
		}
	}
	if n := c.OpenedAt.Compare(other.OpenedAt); n != 0 {
		return n
	}
	return cmp.Compare(c.ID.String(), other.ID.String())
}
