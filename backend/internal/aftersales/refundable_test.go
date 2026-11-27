package aftersales_test

import (
	"context"
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/optest"
)

func refundableCall(t *testing.T, f *fixture, ch optest.Channel, orderID any) optest.Result {
	t.Helper()
	return ch.Call(t, f.MustCaller(f.Owner), "get_order_refundable", map[string]any{"order_id": orderID})
}

func requireRefundable(t *testing.T, name string, body map[string]any, refundable, base, approved string) {
	t.Helper()
	if body["refundable"] != refundable || body["refundable_base"] != base || body["approved_refunded"] != approved {
		t.Fatal(name, body)
	}
}

func requireCard(t *testing.T, name string, body map[string]any, index int, cardType, paid, refunded, remaining string) {
	t.Helper()
	cards := body["cards"].([]any)
	if len(cards) <= index {
		t.Fatal(name, body)
	}
	card := cards[index].(map[string]any)
	if card["card_type_name"] != cardType || card["paid"] != paid || card["refunded"] != refunded || card["remaining"] != remaining {
		t.Fatal(name, card)
	}
}

func TestOrderRefundableMixedPayment(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	ct := f.cardType("储值卡")
	card := f.card(ct, "100.00", "0.00", payment("cash", "100"))
	order := f.order(1)
	f.checkout(order, payment("cash", "40"), cardPayment("card", "60", id(card["id"]), nil))
	f.now("2026-10-02", 12)
	for _, ch := range f.Channels() {
		r := refundableCall(t, f, ch, order["id"])
		if r.Status != 200 || r.Body["order_id"] != order["id"] || len(r.Body["cards"].([]any)) != 1 || r.Body["pending_count"] != float64(0) {
			t.Fatal(ch.Name, r)
		}
		requireRefundable(t, ch.Name, r.Body, "100.00", "100.00", "0.00")
		requireCard(t, ch.Name, r.Body, 0, "储值卡", "60.00", "0.00", "60.00")
	}
	part := f.create(id(order["id"]), "partial_refund", "退部分", payment("cash", "10"), map[string]any{"method": "card_principal", "amount": "20", "card_id": card["id"]})
	for _, ch := range f.Channels() {
		if r := refundableCall(t, f, ch, order["id"]); r.Body["pending_count"] != float64(1) {
			t.Fatal(ch.Name, "pending", r.Body)
		}
	}
	f.approve(id(part["id"]), nil)
	for _, ch := range f.Channels() {
		r := refundableCall(t, f, ch, order["id"])
		if r.Status != 200 || r.Body["pending_count"] != float64(0) {
			t.Fatal(ch.Name, r)
		}
		requireRefundable(t, ch.Name, r.Body, "70.00", "100.00", "30.00")
		requireCard(t, ch.Name, r.Body, 0, "储值卡", "60.00", "20.00", "40.00")
	}
}

func TestOrderRefundableMatchesCreateAftersale(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	order := f.paidOrder(payment("cash", "100"))
	first := f.create(id(order["id"]), "partial_refund", "退一部分", payment("cash", "30.50"))
	f.approve(id(first["id"]), nil)
	got := refundableCall(t, f, f.Channels()[0], order["id"])
	refundable := got.Body["refundable"].(string)
	if refundable != "69.50" {
		t.Fatal(got)
	}
	f.refused(f.MustCaller(f.Owner), "create_aftersale", map[string]any{"order_id": order["id"], "type": "full_refund", "reason": "多一分", "refund_lines": []any{payment("cash", "69.51")}}, 409, "全额退款合计必须等于可退金额 69.50")
	f.refused(f.MustCaller(f.Owner), "create_aftersale", map[string]any{"order_id": order["id"], "type": "partial_refund", "reason": "多一分", "refund_lines": []any{payment("cash", "69.51")}}, 409, "退款合计不能超过可退金额 69.50")
	if done := f.create(id(order["id"]), "full_refund", "按可退数", payment("cash", refundable)); done["status"] != "pending" {
		t.Fatal(done)
	}
}

func TestOrderRefundableRefusals(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	open1 := f.order(1)
	voided := f.paidOrder(payment("cash", "100"))
	f.call("void_order", map[string]any{"order_id": voided["id"], "reason": "录入错误"}, 200)
	keep, dropped := f.order(1), f.order(1)
	f.call("merge_orders", map[string]any{"order_id": keep["id"], "merged_order_ids": []any{dropped["id"]}}, 200)
	denied := f.MustCaller(f.Owner)
	delete(denied.Permissions, "aftersales.manage")
	paid := f.paidOrder(payment("cash", "100"))
	for _, ch := range f.Channels() {
		for _, tc := range []struct {
			id     any
			status int
			detail string
		}{
			{voided["id"], 409, "单已撤"},
			{dropped["id"], 409, "单已合并，请在合并后的单上操作"},
			{open1["id"], 409, "只能为已结账的单创建售后单"},
			{"00000000-0000-4000-8000-000000000001", 404, "not found"},
		} {
			r := refundableCall(t, f, ch, tc.id)
			if r.Status != tc.status || r.Detail() != tc.detail {
				t.Fatal(ch.Name, tc, r)
			}
		}
		r := ch.Call(t, denied, "get_order_refundable", map[string]any{"order_id": paid["id"]})
		if r.Status != 403 || r.Detail() != "permission denied" {
			t.Fatal(ch.Name, r)
		}
	}
}

func TestOrderRefundableWindow(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	order := f.paidOrder(payment("cash", "100"))
	for _, tc := range []struct {
		day    string
		within bool
	}{{"2026-10-31", true}, {"2026-11-01", false}} {
		f.now(tc.day, 12)
		for _, ch := range f.Channels() {
			r := refundableCall(t, f, ch, order["id"])
			if r.Status != 200 || r.Body["window_ends_on"] != "2026-10-31" || r.Body["within_window"] != tc.within {
				t.Fatal(ch.Name, tc, r)
			}
		}
	}
	f.setting("aftersale_window_days", 60)
	for _, ch := range f.Channels() {
		r := refundableCall(t, f, ch, order["id"])
		if r.Body["window_ends_on"] != "2026-11-30" || r.Body["within_window"] != true {
			t.Fatal(ch.Name, r)
		}
	}
}

func TestOrderRefundableQueryCount(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	ct := f.cardType("储值卡")
	oneCard := f.card(ct, "100.00", "0.00", payment("cash", "100"))
	threeCards := []map[string]any{}
	for range 3 {
		c := f.card(ct, "100.00", "0.00", payment("cash", "100"))
		threeCards = append(threeCards, cardPayment("card", "10", id(c["id"]), nil))
	}
	one := f.checkout(f.order(1), cardPayment("card", "10", id(oneCard["id"]), nil), payment("cash", "90"))
	many := f.checkout(f.order(1), append(threeCards, payment("cash", "70"))...)
	counter := &aftersaleQueryCounter{}
	config := f.Pool.Config().Copy()
	config.ConnConfig.Tracer = counter
	pool, err := db.NewPoolWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	app := optest.New(t, pool)
	caller := f.MustCaller(f.Owner)
	counts := map[string]int64{}
	for i, orderID := range []any{one["id"], many["id"]} {
		for _, ch := range app.Channels() {
			counter.count.Store(0)
			r := ch.Call(t, caller, "get_order_refundable", map[string]any{"order_id": orderID})
			if want := []int{1, 3}[i]; r.Status != 200 || len(r.Body["cards"].([]any)) != want {
				t.Fatal(ch.Name, r)
			}
			got := counter.count.Load()
			if i == 0 {
				counts[ch.Name] = got
			} else if got != counts[ch.Name] {
				t.Fatalf("%s queries grew: %d -> %d", ch.Name, counts[ch.Name], got)
			}
		}
	}
}
