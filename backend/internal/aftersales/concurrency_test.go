package aftersales_test

import (
	"testing"
	"time"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
)

func collect(t *testing.T, done <-chan optest.Result, n int) []optest.Result {
	t.Helper()
	out := []optest.Result{}
	for len(out) < n {
		select {
		case r := <-done:
			out = append(out, r)
		case <-time.After(10 * time.Second):
			t.Fatal("concurrent request timed out")
		}
	}
	return out
}

func TestAftersaleConcurrentApprovals(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	order := f.order(1)
	f.checkout(order, payment("cash", "100"))
	a := f.create(id(order["id"]), "partial_refund", "第一笔", payment("cash", "60"))
	b := f.create(id(order["id"]), "partial_refund", "第二笔", payment("cash", "60"))

	held := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	dbtest.Exec(t, held, "SELECT id FROM orders WHERE id=$1 FOR NO KEY UPDATE", order["id"])
	done := make(chan optest.Result, 2)
	for _, id := range []any{a["id"], b["id"]} {
		in := map[string]any{"id": id}
		go func() { done <- f.HTTPWithToken(t, f.Owner, "approve_aftersale", in) }()
	}
	optest.AssertWaiting(t, f.Pool, done, 2)
	dbtest.Commit(t, held)
	results := collect(t, done, 2)
	ok, rejected := 0, 0
	for _, r := range results {
		switch r.Status {
		case 200:
			ok++
		case 409:
			rejected++
			if r.Detail() != "退款合计不能超过可退金额 40.00" {
				t.Fatal(r)
			}
		default:
			t.Fatal(r)
		}
	}
	if ok != 1 || rejected != 1 {
		t.Fatal(results)
	}
}

func TestAftersaleApproveVoidRace(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	order := f.order(1)
	f.checkout(order, payment("cash", "100"))
	refund := f.create(id(order["id"]), "full_refund", "全额退", payment("cash", "100"))

	held := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	dbtest.Exec(t, held, "SELECT id FROM orders WHERE id=$1 FOR NO KEY UPDATE", order["id"])
	done := make(chan optest.Result, 2)
	go func() {
		done <- f.HTTPWithToken(t, f.Owner, "approve_aftersale", map[string]any{"id": refund["id"]})
	}()
	go func() {
		done <- f.HTTPWithToken(t, f.Owner, "void_order", map[string]any{"order_id": order["id"], "reason": "录入错误"})
	}()
	optest.AssertWaiting(t, f.Pool, done, 2)
	dbtest.Commit(t, held)
	results := collect(t, done, 2)
	approved, voided := false, false
	for _, r := range results {
		switch r.Status {
		case 200:
			switch r.Body["status"] {
			case "approved":
				approved = true
			case "voided":
				voided = true
			default:
				t.Fatal(r)
			}
		case 409:
			if r.Detail() != "有已生效的退款售后单，不能撤单" && r.Detail() != "原单已撤，售后单不能生效" {
				t.Fatal(r)
			}
		default:
			t.Fatal(r)
		}
	}
	if approved == voided {
		t.Fatal("expected exactly one of approve and void to win", results)
	}
}
