package orders_test

import (
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
)

func TestCheckoutExplicitRejectionBeforePricingLocks(t *testing.T) {
	for _, table := range []string{"items", "card_types"} {
		for _, kind := range []string{"holder", "count-card"} {
			t.Run(table+"/"+kind, func(t *testing.T) {
				s := open(t)
				line := s.checkoutLine()
				var card map[string]any
				message := "不能用别人的卡"
				if kind == "count-card" {
					card = s.countCard(line, "count", 1, 0)
					message = "次卡不能用卡余额支付"
				} else {
					card = s.paymentCard(s.paymentType("余额卡", nil), "100", "0")
				}
				order := s.customerOrder(line)
				if kind == "holder" {
					order = s.call("open_order", s.opening(line), 200)
				}
				target := any(line.ItemID)
				if table == "card_types" {
					target = card["card_type_id"]
				}
				tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
				dbtest.Exec(t, tx, "SELECT id FROM "+table+" WHERE id=$1 FOR NO KEY UPDATE", target)
				done := make(chan optest.Result, 1)
				go func() {
					done <- s.HTTPWithToken(t, s.Owner, "checkout_orders", checkoutInput(checkoutOrder(order, cardPayment(card, "100"))))
				}()
				result := optest.WaitResult(t, done, 409)
				if result.Body["detail"] != message {
					t.Fatal(result)
				}
				dbtest.Rollback(t, tx)
			})
		}
	}
}
