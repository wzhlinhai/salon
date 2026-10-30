package orders_test

import (
	"context"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"testing"
	"time"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/orders"
)

func (s *fixture) refundCard(c map[string]any, want string) map[string]any {
	s.t.Helper()
	for _, ch := range s.Channels() {
		r := ch.Call(s.t, s.MustCaller(s.Owner), "preview_card_refund", map[string]any{"card_id": c["id"]})
		if r.Status != 200 || r.Body["amount"] != want {
			s.t.Fatal(ch.Name, r)
		}
	}
	return s.call("refund_card", map[string]any{"card_id": c["id"], "method": "cash", "amount": want}, 200)
}
func TestRefundUsedBonus(t *testing.T) {
	for _, principal := range []string{"500", "50"} {
		t.Run(principal, func(t *testing.T) {
			s := open(t)
			s.paymentSetting("card_deduction_order", "bonus_first")
			s.paymentSetting("card_refund_bonus", "deduct_used")
			ct := s.paymentType("卡", nil)
			c := s.paymentCard(ct, principal, "500")
			other := s.paymentCard(ct, "0", "0")
			line := s.checkoutLine()
			s.paid(s.customerOrder(line), cardPayment(c, "100"))
			s.void(s.paid(s.customerOrder(line), cardPayment(c, "100")))
			s.call("adjust_card", map[string]any{"card_id": c["id"], "bonus": "300", "reason": "调整赠送金"}, 200)
			s.call("transfer_card_balance", map[string]any{"from_card_id": c["id"], "to_card_id": other["id"], "principal": "0", "bonus": "100", "reason": "转赠送金"}, 200)
			want, deducted := "400.00", "100.00"
			if principal == "50" {
				want, deducted = "0.00", "50.00"
			}
			out := s.refundCard(c, want)
			if out["bonus_deducted"] != deducted || out["bonus"] != "200.00" {
				t.Fatal(out)
			}
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			var ok bool
			err := tx.QueryRow(context.Background(), `SELECT r.amount=$2 AND r.bonus_deducted=$3 AND l.bonus_after=0 AND l.principal_after=0 FROM card_refunds r JOIN card_ledger l ON l.refund_id=r.id WHERE r.card_id=$1`, c["id"], want, deducted).Scan(&ok)
			dbtest.Rollback(t, tx)
			if err != nil || !ok {
				t.Fatal(ok, err)
			}
		})
	}
}
func TestRefundCheckoutAndVoid(t *testing.T) {
	for _, kind := range []string{"stored_value", "count"} {
		t.Run(kind, func(t *testing.T) {
			s := open(t)
			line := s.checkoutLine()
			c := s.paymentCard(s.paymentType("储值", nil), "500", "0")
			if kind == "count" {
				c = s.countCard(line, "count", 3, 2)
			}
			paid := s.customerOrder(line)
			p := cardPayment(c, "100")
			if kind == "count" {
				p = countPayment(c, firstLine(paid), "100")
			}
			paid = s.paid(paid, p)
			quote := s.call("preview_card_refund", map[string]any{"card_id": c["id"]}, 200)
			s.refundCard(c, quote["amount"].(string))
			o := s.customerOrder(line)
			p = cardPayment(c, "100")
			if kind == "count" {
				p = countPayment(c, firstLine(o), "100")
			}
			before := s.voidSnapshot()
			r := s.call("checkout_orders", checkoutInput(checkoutOrder(o, p)), 409)
			if r["detail"] != "卡已退" || before != s.voidSnapshot() {
				t.Fatal(r)
			}
			for _, ch := range s.Channels() {
				r := ch.Call(t, s.MustCaller(s.Owner), "preview_checkout", orders.PreviewCheckoutInput{OrderID: id(o["id"]), CardID: new(id(c["id"]))})
				detail, _ := r.Body["detail"].(string)
				if r.Err != nil {
					detail = r.Err.Error()
				}
				if r.Status != 409 || detail != "卡已退" || before != s.voidSnapshot() {
					t.Fatal(ch.Name, r)
				}
			}
			s.voidRefused(s.MustCaller(s.Owner), voidInput(paid), 409, "卡已退，不能撤单退回")
		})
	}
}
func TestRefundSelection(t *testing.T) {
	s := open(t)
	s.paymentSetting("card_selection", "by_balance")
	ct := s.paymentType("卡", nil)
	c := s.paymentCard(ct, "50", "0")
	other := s.paymentCard(ct, "200", "0")
	s.refundCard(c, "50.00")
	o := s.customerOrder(s.checkoutLine())
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "preview_checkout", orders.PreviewCheckoutInput{OrderID: id(o["id"]), AutoCard: true})
		if r.Status != 200 || r.Body["pricing_card_id"] != other["id"] {
			t.Fatal(ch.Name, r)
		}
	}
	assertSelectionPayments(t, s.paid(o, payment("card", "100")), []map[string]any{other}, []string{"100.00"})
}
func TestRefundSelectionAfterListing(t *testing.T) {
	s := open(t)
	s.paymentSetting("card_selection", "by_balance")
	ct := s.paymentType("卡", nil)
	c := s.paymentCard(ct, "50", "0")
	other := s.paymentCard(ct, "200", "0")
	o := s.customerOrder(s.checkoutLine())
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "UPDATE cards SET status='refunded' WHERE id=$1", c["id"])
	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "checkout_orders", checkoutInput(checkoutOrder(o, payment("card", "100"))))
	}()
	dbtest.WaitForLockWait(t, s.Pool)
	optest.AssertWaiting(t, done)
	dbtest.Commit(t, tx)
	r := optest.WaitResult(t, done, 200)
	assertSelectionPayments(t, lines(r.Body)[0].(map[string]any), []map[string]any{other}, []string{"100.00"})
}

func TestRefundBonusAfterVoidAll(t *testing.T) {
	s := open(t)
	s.paymentSetting("card_refund_bonus", "deduct_used")
	s.paymentSetting("card_deduction_order", "bonus_first")
	c := s.paymentCard(s.paymentType("卡", nil), "50", "100")
	s.void(s.paid(s.customerOrder(s.checkoutLine()), cardPayment(c, "100")))
	s.refundCard(c, "50.00")
}

func TestRefundLargeIntermediate(t *testing.T) {
	s := open(t)
	s.paymentSetting("card_refund_bonus", "deduct_used")
	s.paymentSetting("card_deduction_order", "bonus_first")
	line := s.checkoutLine()
	c := s.countCard(line, "package", 1, 2)
	s.call("adjust_card", map[string]any{"card_id": c["id"], "principal": "99999999.99", "bonus": "200", "reason": "导入卡余额"}, 200)
	for range 2 {
		s.paid(s.customerOrder(line), cardPayment(c, "100"))
	}
	out := s.refundCard(c, "99999899.99")
	if out["principal"] != "99999999.99" || out["counts_amount"] != "100.00" || out["bonus_deducted"] != "200.00" {
		t.Fatal(out)
	}
}

func TestRefundLargeGrossFee(t *testing.T) {
	s := open(t)
	s.call("change_setting", map[string]any{"key": "card_refund_fee_percent", "value": 50, "effective_from": settings.Date(time.Now()).Format(time.DateOnly)}, 200)
	c := s.countCard(s.checkoutLine(), "package", 1, 0)
	s.call("adjust_card", map[string]any{"card_id": c["id"], "principal": "99999999.99", "reason": "导入卡余额"}, 200)
	out := s.refundCard(c, "50000049.99")
	if out["fee"] != "50000050.00" || out["counts_amount"] != "100.00" {
		t.Fatal(out)
	}
}

func TestRefundLifetimeBonus(t *testing.T) {
	s := open(t)
	s.paymentSetting("card_refund_bonus", "deduct_used")
	c := s.paymentCard(s.paymentType("卡", nil), "50", "0")
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	for range 2 {
		dbtest.Exec(t, tx, `INSERT INTO card_ledger (tenant_id,store_id,card_id,kind,principal_before,principal_delta,principal_after,bonus_before,bonus_delta,bonus_after,business_date,actor_id) VALUES ($1,$2,$3,'consume',50,0,50,60000000,-60000000,0,CURRENT_DATE,$4)`, s.Res.TenantID, s.Res.StoreID, c["id"], s.Res.OwnerID)
	}
	dbtest.Commit(t, tx)
	out := s.refundCard(c, "0.00")
	if out["bonus_deducted"] != "50.00" {
		t.Fatal(out)
	}
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var valid bool
	err := tx.QueryRow(context.Background(), `SELECT r.bonus_deducted=50 AND r.amount=0 AND l.principal_after=0 AND l.bonus_after=0 FROM card_refunds r JOIN card_ledger l ON l.refund_id=r.id WHERE r.card_id=$1`, c["id"]).Scan(&valid)
	dbtest.Rollback(t, tx)
	if err != nil || !valid {
		t.Fatal(valid, err)
	}
}
