package payments

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/cards"
	"github.com/wzhlinhai/salon/backend/internal/collections"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/redpackets"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// Input 是一笔计划支付及其支付方式所需字段。
type Input struct {
	ExternalRef                      *string
	RedPacketID, OrderItemID, CardID *uuid.UUID
	CouponRedemptionID               *uuid.UUID
	Method                           string
	Amount                           money.Amount
}

// Validate 按输入顺序检查各单支付方式的字段规则。
func Validate(orders [][]Input) error {
	packetSeen := map[uuid.UUID]bool{}
	countLines := map[uuid.UUID]bool{}
	for _, order := range orders {
		unspecified := 0
		for _, payment := range order {
			if payment.Method == "groupbuy" || payment.CouponRedemptionID != nil {
				// rule-untested: 客户支付类型没有 groupbuy 或 coupon_redemption_id 字段，HTTP 和 AI schema 在到达这里前已拒绝
				return ops.Rule("团购支付由系统按关联的团购券生成，不能手动登记")
			}
			if payment.Method != "red_packet" && payment.RedPacketID != nil {
				return ops.Rule("只有红包支付要选红包")
			}
			if payment.Method == "red_packet" {
				if payment.RedPacketID == nil {
					return ops.Rule("红包支付要选红包")
				}
				if packetSeen[*payment.RedPacketID] {
					return ops.Rule("同一个红包只能用一次")
				}
				packetSeen[*payment.RedPacketID] = true
			}
			if payment.Method != "count" && payment.OrderItemID != nil {
				return ops.Rule("只有扣次要选项目行")
			}
			if payment.Method == "count" {
				if payment.OrderItemID == nil {
					return ops.Rule("扣次要选项目行")
				}
				if payment.CardID == nil {
					return ops.Rule("扣次要选卡")
				}
				if countLines[*payment.OrderItemID] {
					return ops.Rule("同一项目行不能重复扣次")
				}
				countLines[*payment.OrderItemID] = true
			}
			if payment.Method != "card" && payment.Method != "count" && payment.CardID != nil {
				return ops.Rule("只有卡余额支付和扣次要选卡")
			}
			if payment.Method == "card" && payment.CardID == nil {
				unspecified++
				if unspecified > 1 {
					return ops.Rule("一张单只能有一笔不指定卡的卡余额支付")
				}
			}
		}
	}
	return nil
}

// Checkout 是已锁定卡和红包的结账支付句柄。
type Checkout struct {
	env     ops.Env
	day     time.Time
	cards   map[uuid.UUID]*cards.PaymentCard
	packets map[uuid.UUID]dbgen.RedPacket
}

// Lock 在已有单锁之后依次锁卡和红包。
func Lock(ctx context.Context, env ops.Env, explicit, candidates, packetIDs []uuid.UUID, day time.Time) (*Checkout, error) {
	used, err := cards.LockPayments(ctx, env, explicit, candidates, day, false)
	if err != nil {
		return nil, err
	}
	packets, err := redpackets.LockPayments(ctx, env, packetIDs)
	if err != nil {
		return nil, err
	}
	return &Checkout{env: env, day: day, cards: used, packets: packets}, nil
}

// Snapshots 返回结账计划所需的卡状态副本。
func (p *Checkout) Snapshots() map[uuid.UUID]cards.CardSnapshot {
	out := make(map[uuid.UUID]cards.CardSnapshot, len(p.cards))
	for id, card := range p.cards {
		out[id] = card.Snapshot()
	}
	return out
}

// Record 按计划顺序登记一张单的支付及各方式的效果。
func (p *Checkout) Record(ctx context.Context, checkoutID uuid.UUID, order dbgen.Order, items map[uuid.UUID]uuid.UUID, rules settings.Rules, planned []Input) error {
	env, day := p.env, p.day
	for _, payment := range planned {
		if payment.Method == "red_packet" {
			if err := p.checkRedPacket(ctx, payment, order.CustomerID); err != nil {
				return err
			}
		}
		var ledgerID, countLedgerID *uuid.UUID
		if payment.Method == "count" {
			id, err := p.recordCount(ctx, payment, items[*payment.OrderItemID], rules)
			if err != nil {
				return err
			}
			countLedgerID = &id
		}
		if payment.Method == "card" {
			id, err := p.recordCard(ctx, payment, rules)
			if err != nil {
				return err
			}
			ledgerID = &id
		}
		paymentID, err := env.Q.CreateOrderPayment(ctx, dbgen.CreateOrderPaymentParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, CheckoutID: checkoutID, OrderID: order.ID, Method: payment.Method, Amount: payment.Amount, CardID: payment.CardID, CardLedgerID: ledgerID, OrderItemID: payment.OrderItemID, CardCountLedgerID: countLedgerID, RedPacketID: payment.RedPacketID, CouponRedemptionID: payment.CouponRedemptionID})
		if err != nil {
			return err
		}
		if payment.Method == "groupbuy" {
			if err := env.Q.SetRedemptionPayment(ctx, dbgen.SetRedemptionPaymentParams{StoreID: env.Caller.StoreID, ID: *payment.CouponRedemptionID, OrderPaymentID: &paymentID}); err != nil {
				return err
			}
		}
		if payment.Method == "red_packet" {
			if err := p.recordRedPacket(ctx, payment, paymentID); err != nil {
				return err
			}
		}
		if code, ok := collections.ForPayment(payment.Method); ok {
			if _, err := collections.Initiate(ctx, env, code, order.ID, &paymentID, payment.Amount, payment.ExternalRef); err != nil {
				return err
			}
		}
		if payment.Method == "debt" {
			if err := recordDebt(ctx, env, order, paymentID, payment, day); err != nil {
				return err
			}
		}
	}
	return nil
}

// Finish 为结账用过的卡记录改前改后日志。
func (p *Checkout) Finish(ctx context.Context) error {
	return cards.Audit(ctx, p.env, "checkout_orders", p.cards)
}

// Reverse 锁欠款、卡和红包，检查可撤单后冲回整单支付并记日志。
func Reverse(ctx context.Context, env ops.Env, orderID, voidID uuid.UUID, day time.Time) error {
	// 调用方已锁单；与顾客合并、结账同序：单 → 欠款 → 卡 → 红包；各组按 UUID，持锁后不回锁顾客。
	if err := reverseDebt(ctx, env, orderID); err != nil {
		return err
	}
	recorded, err := env.Q.ListOrderPayments(ctx, dbgen.ListOrderPaymentsParams{StoreID: env.Caller.StoreID, OrderIds: []uuid.UUID{orderID}})
	if err != nil {
		return err
	}
	cardIDs, packetIDs := []uuid.UUID{}, []uuid.UUID{}
	for _, payment := range recorded {
		if payment.CardID != nil {
			cardIDs = append(cardIDs, *payment.CardID)
		}
		if payment.RedPacketID != nil {
			packetIDs = append(packetIDs, *payment.RedPacketID)
		}
	}
	used, err := cards.LockPayments(ctx, env, cardIDs, nil, day, true)
	if err != nil {
		return err
	}
	packets, err := redpackets.LockPayments(ctx, env, packetIDs)
	if err != nil {
		return err
	}
	if err := reverseCard(ctx, env, used, orderID, voidID, day); err != nil {
		return err
	}
	if err := reverseCount(ctx, env, used, orderID, voidID, day); err != nil {
		return err
	}
	for _, id := range packetIDs {
		if err := reverseRedPacket(ctx, env, packets[id], day); err != nil {
			return err
		}
	}
	if err := releaseGroupbuy(ctx, env, orderID); err != nil {
		return err
	}
	if err := collections.CancelOrder(ctx, env, orderID); err != nil {
		return err
	}
	return cards.Audit(ctx, env, "void_order", used)
}

// redemptionAudit 是团购券释放前后的日志视图，键名与其他团购日志一致。
type redemptionAudit struct {
	ID             uuid.UUID     `json:"id"`
	Platform       string        `json:"platform"`
	CouponCode     string        `json:"coupon_code"`
	Status         string        `json:"status"`
	OrderID        *uuid.UUID    `json:"order_id" nullable:"true"`
	DealID         *uuid.UUID    `json:"deal_id" nullable:"true"`
	PurchasePrice  *money.Amount `json:"purchase_price" nullable:"true"`
	StorePrice     *money.Amount `json:"store_price" nullable:"true"`
	LinkedAt       *time.Time    `json:"linked_at" nullable:"true"`
	OrderPaymentID *uuid.UUID    `json:"order_payment_id" nullable:"true"`
}

func redemptionAuditView(row dbgen.CouponRedemption) redemptionAudit {
	out := redemptionAudit{ID: row.ID, Platform: row.Platform, CouponCode: row.CouponCode, Status: row.Status, OrderID: row.OrderID, DealID: row.DealID, PurchasePrice: row.PurchasePrice, StorePrice: row.StorePrice, OrderPaymentID: row.OrderPaymentID}
	if row.LinkedAt.Valid {
		out.LinkedAt = &row.LinkedAt.Time
	}
	return out
}

// releaseGroupbuy 撤单时锁住单上关联的团购券并清空关联和当前支付指针，券可再次关联或撤销。
func releaseGroupbuy(ctx context.Context, env ops.Env, orderID uuid.UUID) error {
	rows, err := env.Q.LockOrderRedemptions(ctx, dbgen.LockOrderRedemptionsParams{StoreID: env.Caller.StoreID, OrderIds: []uuid.UUID{orderID}})
	if err != nil {
		return err
	}
	for _, row := range rows {
		before := redemptionAudit{ID: row.ID, Platform: row.Platform, CouponCode: row.CouponCode, Status: row.Status, OrderID: row.OrderID, DealID: row.DealID, PurchasePrice: row.PurchasePrice, StorePrice: row.StorePrice, OrderPaymentID: row.OrderPaymentID}
		if row.LinkedAt.Valid {
			before.LinkedAt = &row.LinkedAt.Time
		}
		released, err := env.Q.ReleaseRedemption(ctx, dbgen.ReleaseRedemptionParams{StoreID: env.Caller.StoreID, ID: row.ID})
		if err != nil {
			return err
		}
		after := redemptionAuditView(released)
		if err := audit.Record(ctx, env, "void_order", audit.CouponRedemptionTarget(row.ID), before, after); err != nil {
			return err
		}
	}
	return nil
}
