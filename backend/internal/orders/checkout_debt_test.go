package orders_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/debts"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/orders"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func TestCheckoutDebt(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	a := s.customerOrder(line)
	b := s.call("open_order", s.opening(line), 200)
	s.call("add_customer_phone", map[string]any{"id": b["customer_id"], "phone": "13800000002"}, 200)
	day := settings.Date(time.Now()).AddDate(0, 0, 2)
	s.call("change_setting", map[string]any{"key": "business_day_end", "value": "02:00", "effective_from": day.Format(time.DateOnly)}, 200)
	at := time.Date(day.Year(), day.Month(), day.Day()+1, 1, 30, 0, 0, settings.Location)
	caller := s.MustCaller(s.Owner)
	err := db.InTenant(context.Background(), s.Pool, s.Res.TenantID, func(q *dbgen.Queries) error {
		_, err := orders.CheckoutOrdersAtForTest(context.Background(), ops.Env{Caller: caller, Q: q}, new(checkoutInput(checkoutOrder(a, payment("debt", "100")), checkoutOrder(b, payment("cash", "40"), payment("debt", "60")))), at)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range s.Channels() {
		for i, o := range []map[string]any{a, b} {
			r := ch.Call(t, caller, "get_order", orders.GetOrderInput{ID: id(o["id"])})
			if r.Status != 200 {
				t.Fatal(r)
			}
			ps := r.Body["payments"].([]any)
			p := ps[len(ps)-1].(map[string]any)
			listed := ch.Call(t, caller, "list_customer_debts", debts.ListCustomerDebtsInput{CustomerID: id(o["customer_id"])})
			if listed.Status != 200 {
				t.Fatal(listed)
			}
			ds := listed.Body["items"].([]any)
			if len(ds) != 1 {
				t.Fatal(ds)
			}
			d := ds[0].(map[string]any)
			if p["debt_id"] != d["id"] || d["order_id"] != o["id"] || d["kind"] != "order" || d["amount"] != []string{"100.00", "60.00"}[i] || d["business_date"] != day.Format(time.DateOnly) || d["recorded_by"] != caller.StaffID.String() {
				t.Fatal(p, d)
			}
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			var paymentID uuid.UUID
			if err := tx.QueryRow(context.Background(), "SELECT order_payment_id FROM debts WHERE id=$1", d["id"]).Scan(&paymentID); err != nil || paymentID != id(p["id"]) {
				t.Fatal(paymentID, err)
			}
			dbtest.Rollback(t, tx)
		}
	}
	s.call("register_historical_debt", map[string]any{"customer_id": s.customer, "amount": "40", "incurred_on": "2020-01-01"}, 200)
	listed := s.call("list_customer_debts", debts.ListCustomerDebtsInput{CustomerID: s.customer}, 200)
	d := listed["items"].([]any)[1].(map[string]any)
	s.call("repay_debt", map[string]any{"debt_id": d["id"], "amount": "30", "method": "wechat"}, 200)
	for _, ch := range s.Channels() {
		r := ch.Call(t, caller, "debt_summary", debts.DebtSummaryInput{})
		if r.Status != 200 || r.Body["amount"] != "200.00" || r.Body["repaid"] != "30.00" || r.Body["outstanding"] != "170.00" || r.Body["outstanding_count"] != float64(3) {
			t.Fatal(r)
		}
		order := r.Body["order"].(map[string]any)
		hist := r.Body["historical"].(map[string]any)
		if order["amount"] != "160.00" || order["repaid"] != "30.00" || order["outstanding"] != "130.00" || hist["amount"] != "40.00" || hist["repaid"] != "0.00" || hist["outstanding"] != "40.00" {
			t.Fatal(r)
		}
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		var total string
		if err := tx.QueryRow(context.Background(), "SELECT sum(amount)::text FROM order_payments WHERE store_id=$1 AND method='debt'", s.Res.StoreID).Scan(&total); err != nil || total != order["amount"] {
			t.Fatal(total, err, r)
		}
		dbtest.Rollback(t, tx)
	}
}

func TestUnidentifiedCheckoutDebt(t *testing.T) {
	s := open(t)
	card := s.paymentCard(s.paymentType("卡", nil), "200", "0")
	line := s.checkoutLine()
	a := s.customerOrder(line)
	b := s.call("open_order", s.opening(line), 200)
	before := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "checkouts", "order_payments", "debts", "card_ledger", "card_count_ledger", "orders", "order_items", "audit_log")
	r := s.HTTPWithToken(t, s.Owner, "checkout_orders", checkoutInput(checkoutOrder(a, cardPayment(card, "100")), checkoutOrder(b, payment("debt", "100"))))
	after := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "checkouts", "order_payments", "debts", "card_ledger", "card_count_ledger", "orders", "order_items", "audit_log")
	if r.Status != 409 || r.Body["detail"] != "未识别顾客不能挂账，请先补手机号" || before != after {
		t.Fatal(r)
	}
}

func TestMergeCustomerDebts(t *testing.T) {
	s := open(t)
	o := s.customerOrder(s.checkoutLine())
	s.paid(o, payment("debt", "100"))
	h := s.call("register_historical_debt", map[string]any{"customer_id": s.customer, "amount": "30", "incurred_on": "2020-01-01"}, 200)
	s.call("repay_debt", map[string]any{"debt_id": h["id"], "amount": "5", "method": "cash"}, 200)
	keep := s.call("create_customer", map[string]any{"phone": "13800000002"}, 200)["customer"].(map[string]any)
	s.call("merge_customers", map[string]any{"keep_id": keep["id"], "merged_id": s.customer}, 200)
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "list_customer_debts", map[string]any{"customer_id": keep["id"]})
		if r.Status != 200 || len(r.Body["items"].([]any)) != 2 || r.Body["totals"].(map[string]any)["outstanding"] != "125.00" {
			t.Fatal(r)
		}
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		var moved int
		if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM debts WHERE customer_id=$1", keep["id"]).Scan(&moved); err != nil || moved != 2 {
			t.Fatal(moved, err)
		}
		dbtest.Rollback(t, tx)
	}
}
