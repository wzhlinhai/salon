package orders_test

import (
	"context"
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
)

func TestPaymentRedPacketAudit(t *testing.T) {
	s := open(t)
	packet := s.redPacket(s.customer, "100")
	paid := s.paid(s.customerOrder(s.checkoutLine()), packetPayment(packet, "100"))
	assertAudit := func(action, before, after string, want int) {
		t.Helper()
		for _, ch := range s.Channels() {
			r := ch.Call(t, s.MustCaller(s.Owner), "list_audit_log", map[string]any{"target_id": packet["id"]})
			if r.Status != 200 {
				t.Fatal(r)
			}
			entries := r.Body["entries"].([]any)
			found := 0
			for _, raw := range entries {
				entry := raw.(map[string]any)
				if entry["action"] != action {
					continue
				}
				found++
				prev, next := entry["before"].(map[string]any), entry["after"].(map[string]any)
				if entry["target_kind"] != "red_packet" || entry["target_id"] != packet["id"] || prev["id"] != packet["id"] || next["id"] != packet["id"] || prev["status"] != before || next["status"] != after || prev["face_value"] != "100.00" || next["face_value"] != "100.00" {
					t.Fatal(entry)
				}
				used, available := next, prev
				if action == "void_order" {
					used, available = prev, next
				}
				if used["order_id"] != paid["id"] || used["used_amount"] != "100.00" || available["order_id"] != nil || available["used_amount"] != nil {
					t.Fatal(entry)
				}
			}
			if found != 1 || len(entries) != want {
				t.Fatal(r)
			}
		}
	}
	assertAudit("checkout_orders", "available", "used", 2)
	s.void(paid)
	assertAudit("checkout_orders", "available", "used", 3)
	assertAudit("void_order", "used", "available", 3)
}

func TestPaymentCardCheckPrecedence(t *testing.T) {
	s := open(t)
	ct := s.paymentType("检查顺序", nil)
	low, high := s.paymentCard(ct, "100", "0"), s.paymentCard(ct, "100", "0")
	if low["id"].(string) > high["id"].(string) {
		low, high = high, low
	}
	s.refundCard(low, "100.00")
	order := s.customerOrder(s.checkoutLine())
	input := checkoutInput(checkoutOrder(order, cardPayment(high, "50"), cardPayment(low, "50")))
	before := s.cardSnapshot()
	r := s.HTTPWithToken(t, s.Owner, "checkout_orders", input)
	if r.Status != 409 || r.Detail() != "卡已退" || before != s.cardSnapshot() {
		t.Fatal(r)
	}
	s.call("adjust_card", map[string]any{"card_id": high["id"], "expiry": map[string]any{"expires_on": "2000-01-01"}, "reason": "检查过期卡优先级"}, 200)
	before = s.cardSnapshot()
	r = s.HTTPWithToken(t, s.Owner, "checkout_orders", input)
	if r.Status != 409 || r.Detail() != "卡已退" || before != s.cardSnapshot() {
		t.Fatal(r)
	}
	missing := map[string]any{"id": "ffffffff-ffff-4fff-bfff-ffffffffffff"}
	before = s.cardSnapshot()
	r = s.HTTPWithToken(t, s.Owner, "checkout_orders", checkoutInput(checkoutOrder(order, cardPayment(low, "50"), cardPayment(missing, "50"))))
	if r.Status != 404 || r.Detail() != "not found" || before != s.cardSnapshot() {
		t.Fatal(r)
	}
}

func TestPaymentReverseRejectionRollback(t *testing.T) {
	for _, method := range []string{"debt", "card"} {
		t.Run(method, func(t *testing.T) {
			s := open(t)
			line := s.checkoutLine()
			order := s.customerOrder(line)
			detail := "这张单的挂账已有还款，不能撤单"
			if method == "debt" {
				order = s.paid(order, payment("debt", "100"))
				debtID := order["payments"].([]any)[0].(map[string]any)["debt_id"]
				s.call("repay_debt", map[string]any{"debt_id": debtID, "amount": "1", "method": "cash"}, 200)
			} else {
				card := s.paymentCard(s.paymentType("撤单拒绝", nil), "200", "0")
				order = s.paid(order, cardPayment(card, "100"))
				s.refundCard(card, "100.00")
				detail = "卡已退，不能撤单退回"
			}
			before := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "order_voids", "performance_entries", "guest_visits", "orders", "audit_log")
			r := s.HTTPWithToken(t, s.Owner, "void_order", voidInput(order))
			if r.Status != 409 || r.Detail() != detail || before != dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "order_voids", "performance_entries", "guest_visits", "orders", "audit_log") {
				t.Fatal(r)
			}
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			var status string
			var voids, reversals int
			err := tx.QueryRow(context.Background(), `SELECT status,(SELECT count(*) FROM order_voids WHERE order_id=$1),(SELECT count(*) FROM performance_entries WHERE order_id=$1 AND reverses_id IS NOT NULL) FROM orders WHERE id=$1`, order["id"]).Scan(&status, &voids, &reversals)
			dbtest.Rollback(t, tx)
			if err != nil || status != "paid" || voids != 0 || reversals != 0 {
				t.Fatal(status, voids, reversals, err)
			}
		})
	}
}
