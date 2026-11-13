package closing_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
)

func seal(t *testing.T, f *fixture, day string) map[string]any {
	t.Helper()
	return f.Call(t, f.Owner, "seal_day", map[string]any{"date": day}, 200).Body
}

func TestSealOrderRules(t *testing.T) {
	f := open(t)
	f.now("2026-10-05", 12)
	if out := seal(t, f, "2026-10-01"); out["sealed_through"] != "2026-10-01" {
		t.Fatal(out)
	}
	r := f.HTTP(t, new(f.MustCaller(f.Owner)), "seal_day", map[string]any{"date": "2026-10-03"})
	if r.Status != http.StatusConflict || r.Detail() != "只能按日期顺序封账，下一个封账日是 2026-10-02" {
		t.Fatal(r)
	}
	seal(t, f, "2026-10-02")

	f.now("2026-10-03", 12)
	order := f.order("2026-10-03", f.customer, "10.00")
	f.checkout(order, payment("cash", "10"))
	f.now("2026-10-05", 12)
	r = f.HTTP(t, new(f.MustCaller(f.Owner)), "seal_day", map[string]any{"date": "2026-10-03"})
	if r.Status != http.StatusConflict || r.Detail() != "请先日结 2026-10-03" {
		t.Fatal(r)
	}
	f.Call(t, f.Owner, "close_day", map[string]any{"date": "2026-10-03", "cash_counted": "10", "unionpay_actual": "0", "wechat_actual": "0", "alipay_actual": "0"}, 200)
	seal(t, f, "2026-10-03")

	f.now("2026-10-04", 12)
	open := f.order("2026-10-04", f.customer, "10.00")
	f.now("2026-10-05", 12)
	r = f.HTTP(t, new(f.MustCaller(f.Owner)), "seal_day", map[string]any{"date": "2026-10-04"})
	if r.Status != http.StatusConflict || !strings.HasPrefix(r.Detail(), "有未结账的挂单，请先处理：") || !strings.Contains(r.Detail(), open.String()) {
		t.Fatal(r)
	}
}

func TestSealRejectsTodayAndFuture(t *testing.T) {
	f := open(t)
	f.now("2026-10-02", 12)
	for _, day := range []string{"2026-10-02", "2026-10-03"} {
		r := f.HTTP(t, new(f.MustCaller(f.Owner)), "seal_day", map[string]any{"date": day})
		if r.Status != http.StatusConflict || r.Detail() != "只能封今天以前的营业日" {
			t.Fatal(r)
		}
	}
}

func TestUnsealRules(t *testing.T) {
	f := open(t)
	f.now("2026-10-03", 12)
	seal(t, f, "2026-10-01")
	r := f.HTTP(t, new(f.MustCaller(f.Owner)), "unseal_day", map[string]any{"reason": "   "})
	if r.Status != http.StatusConflict || r.Detail() != "请填写不超过200字的撤销封账原因" {
		t.Fatal(r)
	}
	out := f.Call(t, f.Owner, "unseal_day", map[string]any{"reason": "录错"}, 200).Body
	if out["sealed_through"] != nil {
		t.Fatal(out)
	}
	r = f.HTTP(t, new(f.MustCaller(f.Owner)), "unseal_day", map[string]any{"reason": "再撤"})
	if r.Status != http.StatusConflict || r.Detail() != "当前没有封账" {
		t.Fatal(r)
	}

	seal(t, f, "2026-10-01")
	seal(t, f, "2026-10-02")
	out = f.Call(t, f.Owner, "unseal_day", map[string]any{"reason": "撤销最后一天"}, 200).Body
	if out["sealed_through"] != "2026-10-01" {
		t.Fatal(out)
	}
	list := f.Call(t, f.Owner, "list_day_closes", map[string]any{"date_from": "2026-10-01", "date_to": "2026-10-02"}, 200).Body
	if list["sealed_through"] != "2026-10-01" {
		t.Fatal(list)
	}
	out = f.Call(t, f.Owner, "unseal_day", map[string]any{"reason": "撤销第一天"}, 200).Body
	if out["sealed_through"] != nil {
		t.Fatal(out)
	}
}

func closedSnapshot(t *testing.T, f *fixture) string {
	t.Helper()
	return dbtest.Snapshot(t, f.Pool, f.Res.TenantID, "orders", "order_voids", "debts", "collections", "collection_events")
}

func TestSealedWritesRejectedAndAcceptedAfterUnseal(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	order := f.order("2026-10-01", f.customer, "100.00")
	f.checkout(order, payment("cash", "100"))
	collection := f.Call(t, f.Owner, "list_collections", map[string]any{"order_id": order}, 200).Body["items"].([]any)[0].(map[string]any)["id"]
	f.now("2026-10-02", 12)
	f.Call(t, f.Owner, "close_day", map[string]any{"date": "2026-10-01", "cash_counted": "100", "unionpay_actual": "0", "wechat_actual": "0", "alipay_actual": "0"}, 200)
	seal(t, f, "2026-10-01")
	before := closedSnapshot(t, f)

	for _, tc := range []struct {
		op string
		in any
	}{
		{"void_order", map[string]any{"order_id": order, "reason": "封账后撤单"}},
		{"register_historical_debt", map[string]any{"customer_id": f.customer, "amount": "10.00", "incurred_on": "2026-10-01"}},
		{"cancel_collection", map[string]any{"id": collection}},
	} {
		r := f.HTTP(t, new(f.MustCaller(f.Owner)), tc.op, tc.in)
		if r.Status != http.StatusConflict || r.Detail() != "该营业日已封账（2026-10-01）" {
			t.Fatal(tc.op, r)
		}
		if closedSnapshot(t, f) != before {
			t.Fatal(tc.op, "changed data")
		}
	}

	f.Call(t, f.Owner, "unseal_day", map[string]any{"reason": "测试撤销"}, 200)
	f.Call(t, f.Owner, "void_order", map[string]any{"order_id": order, "reason": "撤销封账后撤单"}, 200)
	f.Call(t, f.Owner, "register_historical_debt", map[string]any{"customer_id": f.customer, "amount": "10.00", "incurred_on": "2026-10-01"}, 200)
}

func TestUnsealPermission(t *testing.T) {
	f := open(t)
	f.now("2026-10-02", 12)
	role := f.Call(t, f.Owner, "create_role", map[string]any{"name": "封账员", "permissions": []any{map[string]any{"permission": "closing.seal"}}}, 200).Body
	roleID := uuid.MustParse(role["id"].(string))
	_, token := f.Hire("封账员", "002", "sealer", roleID)
	sealed := f.Call(t, token, "seal_day", map[string]any{"date": "2026-10-01"}, 200).Body
	if sealed["sealed_through"] != "2026-10-01" {
		t.Fatal(sealed)
	}
	r := f.HTTPWithToken(t, token, "unseal_day", map[string]any{"reason": "无权限"})
	if r.Status != http.StatusForbidden || r.Detail() != "permission denied" {
		t.Fatal(r)
	}
	f.Call(t, f.Owner, "unseal_day", map[string]any{"reason": "老板撤销"}, 200)
}

func TestOrderGuardIndependent(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	order := f.order("2026-10-01", f.customer, "10.00")
	item := f.Call(t, f.Owner, "get_order", map[string]any{"id": order}, 200).Body["items"].([]any)[0].(map[string]any)["id"]
	if _, err := f.Pool.Exec(t.Context(), `INSERT INTO store_sealings(tenant_id,store_id,sealed_through) VALUES($1,$2,$3) ON CONFLICT (tenant_id,store_id) DO UPDATE SET sealed_through=EXCLUDED.sealed_through`, f.Res.TenantID, f.Res.StoreID, "2026-10-01"); err != nil {
		t.Fatal(err)
	}
	before := dbtest.Snapshot(t, f.Pool, f.Res.TenantID, "order_items")
	r := f.HTTP(t, new(f.MustCaller(f.Owner)), "reprice_order_item", map[string]any{"order_id": order, "order_item_id": item, "price": "20.00", "reason": "封账后改价"})
	if r.Status != http.StatusConflict || r.Detail() != "该营业日已封账（2026-10-01）" {
		t.Fatal(r)
	}
	if dbtest.Snapshot(t, f.Pool, f.Res.TenantID, "order_items") != before {
		t.Fatal("guarded write changed data")
	}
}

func TestSealAndWriteAreSerialized(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	order := f.order("2026-10-01", f.customer, "100.00")
	f.checkout(order, payment("cash", "100"))
	f.now("2026-10-02", 12)
	f.Call(t, f.Owner, "close_day", map[string]any{"date": "2026-10-01", "cash_counted": "100", "unionpay_actual": "0", "wechat_actual": "0", "alipay_actual": "0"}, 200)

	held := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	dbtest.Exec(t, held, `SELECT pg_advisory_xact_lock_shared(hashtextextended($1::text || '/closing', 0))`, f.Res.StoreID.String())

	sealDone := make(chan optest.Result, 1)
	go func() {
		sealDone <- f.HTTP(t, new(f.MustCaller(f.Owner)), "seal_day", map[string]any{"date": "2026-10-01"})
	}()
	optest.AssertWaiting(t, f.Pool, sealDone, 1)
	voidDone := make(chan optest.Result, 1)
	go func() {
		voidDone <- f.HTTP(t, new(f.MustCaller(f.Owner)), "void_order", map[string]any{"order_id": order, "reason": "并发撤单"})
	}()
	optest.AssertWaiting(t, f.Pool, voidDone, 2)
	dbtest.Commit(t, held)

	optest.WaitResult(t, sealDone, http.StatusOK)
	r := optest.WaitResult(t, voidDone, http.StatusConflict)
	if r.Detail() != "该营业日已封账（2026-10-01）" {
		t.Fatal(r)
	}
	var status string
	if err := f.Pool.QueryRow(t.Context(), "SELECT status FROM orders WHERE id=$1", order).Scan(&status); err != nil || status != "paid" {
		t.Fatal(status, err)
	}
}

func TestUnsealStateMachine(t *testing.T) {
	f := open(t)
	f.now("2026-10-10", 12)
	seal(t, f, "2026-10-01")
	if out := f.Call(t, f.Owner, "unseal_day", map[string]any{"reason": "撤第一天"}, 200).Body; out["sealed_through"] != nil {
		t.Fatal(out)
	}
	seal(t, f, "2026-10-05")
	if out := f.Call(t, f.Owner, "unseal_day", map[string]any{"reason": "撤唯一一天"}, 200).Body; out["sealed_through"] != nil {
		t.Fatal(out)
	}
	seal(t, f, "2026-10-01")
	f.Call(t, f.Owner, "unseal_day", map[string]any{"reason": "撤掉"}, 200)
	seal(t, f, "2026-10-02")
	if out := f.Call(t, f.Owner, "unseal_day", map[string]any{"reason": "再撤掉"}, 200).Body; out["sealed_through"] != nil {
		t.Fatal(out)
	}
	seal(t, f, "2026-10-01")
	seal(t, f, "2026-10-02")
	if out := f.Call(t, f.Owner, "unseal_day", map[string]any{"reason": "撤销最后一天"}, 200).Body; out["sealed_through"] != "2026-10-01" {
		t.Fatal(out)
	}
	if out := f.Call(t, f.Owner, "unseal_day", map[string]any{"reason": "撤销第一天"}, 200).Body; out["sealed_through"] != nil {
		t.Fatal(out)
	}
}

func TestSealRejectsChangedDay(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	order := f.order("2026-10-01", f.customer, "10.00")
	f.checkout(order, payment("cash", "10"))
	card := f.openCard("100.00", "0.00", payment("cash", "100"))
	f.now("2026-10-02", 12)
	f.Call(t, f.Owner, "close_day", map[string]any{"date": "2026-10-01", "cash_counted": "110", "unionpay_actual": "0", "wechat_actual": "0", "alipay_actual": "0"}, 200)
	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	var ledger uuid.UUID
	if err := tx.QueryRow(context.Background(), `SELECT id FROM card_ledger WHERE card_id=$1 ORDER BY id LIMIT 1`, card["id"]).Scan(&ledger); err != nil {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	if _, err := f.Pool.Exec(t.Context(), `INSERT INTO sale_payments(tenant_id,store_id,card_ledger_id,method,amount,business_date,created_by) VALUES($1,$2,$3,'cash',1,'2026-10-01',$4)`, f.Res.TenantID, f.Res.StoreID, ledger, f.Res.OwnerID); err != nil {
		t.Fatal(err)
	}
	r := f.HTTP(t, new(f.MustCaller(f.Owner)), "seal_day", map[string]any{"date": "2026-10-01"})
	if r.Status != http.StatusConflict || r.Detail() != "日结后当天账目有变化，请重新日结" {
		t.Fatal(r)
	}
	var n int
	if err := f.Pool.QueryRow(t.Context(), "SELECT count(*) FROM store_sealings WHERE store_id=$1 AND sealed_through IS NOT NULL", f.Res.StoreID).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	f.Call(t, f.Owner, "close_day", map[string]any{"date": "2026-10-01", "cash_counted": "110", "unionpay_actual": "0", "wechat_actual": "0", "alipay_actual": "0"}, 200)
	seal(t, f, "2026-10-01")
}

func TestCollectionOnlyDayRequiresClose(t *testing.T) {
	f := open(t)
	f.now("2026-09-30", 12)
	order := f.order("2026-09-30", f.customer, "10.00")
	f.checkout(order, payment("cash", "10"))
	if _, err := f.Pool.Exec(t.Context(), `INSERT INTO collections(tenant_id,store_id,order_id,channel,amount,status,created_by,created_at,updated_at) VALUES($1,$2,$3,'cash',1,'succeeded',$4,'2026-10-01 12:00:00+08','2026-10-01 12:00:00+08')`, f.Res.TenantID, f.Res.StoreID, order, f.Res.OwnerID); err != nil {
		t.Fatal(err)
	}
	f.now("2026-10-02", 12)
	r := f.HTTP(t, new(f.MustCaller(f.Owner)), "seal_day", map[string]any{"date": "2026-10-01"})
	if r.Status != http.StatusConflict || r.Detail() != "请先日结 2026-10-01" {
		t.Fatal(r)
	}
	f.Call(t, f.Owner, "close_day", map[string]any{"date": "2026-10-01", "cash_counted": "0", "unionpay_actual": "0", "wechat_actual": "0", "alipay_actual": "0"}, 200)
	seal(t, f, "2026-10-01")
}

func TestFirstSealCoversEarlierDaysWithoutClose(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	a := f.order("2026-10-01", f.customer, "10.00")
	f.checkout(a, payment("cash", "10"))
	f.now("2026-10-02", 12)
	b := f.order("2026-10-02", f.customer, "20.00")
	f.checkout(b, payment("cash", "20"))
	f.now("2026-10-03", 12)
	f.Call(t, f.Owner, "close_day", map[string]any{"date": "2026-10-02", "cash_counted": "20", "unionpay_actual": "0", "wechat_actual": "0", "alipay_actual": "0"}, 200)
	if out := seal(t, f, "2026-10-02"); out["sealed_through"] != "2026-10-02" {
		t.Fatal(out)
	}
}

func TestGuardedOrderWritesRejected(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	first := f.order("2026-10-01", f.customer, "10.00")
	firstItem := f.Call(t, f.Owner, "get_order", map[string]any{"id": first}, 200).Body["items"].([]any)[0].(map[string]any)["id"]
	second := f.order("2026-10-01", f.customer, "10.00")
	if _, err := f.Pool.Exec(t.Context(), `INSERT INTO store_sealings(tenant_id,store_id,sealed_through) VALUES($1,$2,$3) ON CONFLICT (tenant_id,store_id) DO UPDATE SET sealed_through=EXCLUDED.sealed_through`, f.Res.TenantID, f.Res.StoreID, "2026-10-01"); err != nil {
		t.Fatal(err)
	}
	before := dbtest.Snapshot(t, f.Pool, f.Res.TenantID, "orders", "order_items", "order_item_positions")
	for _, tc := range []struct {
		op string
		in map[string]any
	}{
		{"add_order_item", map[string]any{"order_id": first, "item": map[string]any{"item_id": f.item, "staff": []any{}}}},
		{"remove_order_item", map[string]any{"order_id": first, "order_item_id": firstItem}},
		{"set_order_item_staff", map[string]any{"order_id": first, "order_item_id": firstItem, "staff": []any{}}},
		{"reprice_order_item", map[string]any{"order_id": first, "order_item_id": firstItem, "price": "20.00", "reason": "封账后改价"}},
		{"merge_orders", map[string]any{"order_id": first, "merged_order_ids": []any{second}}},
		{"checkout_orders", map[string]any{"orders": []any{map[string]any{"order_id": first, "payments": []any{payment("cash", "10.00")}}}}},
	} {
		r := f.HTTP(t, new(f.MustCaller(f.Owner)), tc.op, tc.in)
		if r.Status != http.StatusConflict || r.Detail() != "该营业日已封账（2026-10-01）" {
			t.Fatal(tc.op, r)
		}
		if dbtest.Snapshot(t, f.Pool, f.Res.TenantID, "orders", "order_items", "order_item_positions") != before {
			t.Fatal(tc.op, "changed data")
		}
	}
	if _, err := f.Pool.Exec(t.Context(), "DELETE FROM store_sealings WHERE store_id=$1", f.Res.StoreID); err != nil {
		t.Fatal(err)
	}
	f.Call(t, f.Owner, "reprice_order_item", map[string]any{"order_id": first, "order_item_id": firstItem, "price": "20.00", "reason": "解封后改价"}, 200)
}

func TestRefundCollectionRejectedAfterSeal(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	order := f.order("2026-10-01", f.customer, "10.00")
	f.checkout(order, payment("cash", "10"))
	collection := f.Call(t, f.Owner, "list_collections", map[string]any{"order_id": order}, 200).Body["items"].([]any)[0].(map[string]any)["id"]
	f.now("2026-10-02", 12)
	f.Call(t, f.Owner, "close_day", map[string]any{"date": "2026-10-01", "cash_counted": "10", "unionpay_actual": "0", "wechat_actual": "0", "alipay_actual": "0"}, 200)
	seal(t, f, "2026-10-01")
	r := f.HTTP(t, new(f.MustCaller(f.Owner)), "refund_collection", map[string]any{"id": collection, "reason": "封账后退款"})
	if r.Status != http.StatusConflict || r.Detail() != "该营业日已封账（2026-10-01）" {
		t.Fatal(r)
	}
	f.Call(t, f.Owner, "unseal_day", map[string]any{"reason": "测试退款"}, 200)
	f.Call(t, f.Owner, "refund_collection", map[string]any{"id": collection, "reason": "解封后退款"}, 200)
}

func TestSealedDraftConfirmRejected(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	order := f.order("2026-10-01", f.customer, "10.00")
	f.checkout(order, payment("cash", "10"))
	f.now("2026-10-02", 12)
	f.Call(t, f.Owner, "close_day", map[string]any{"date": "2026-10-01", "cash_counted": "10", "unionpay_actual": "0", "wechat_actual": "0", "alipay_actual": "0"}, 200)
	seal(t, f, "2026-10-01")
	caller := f.MustCaller(f.Owner)
	draft := f.AI(t, caller, "void_order", map[string]any{"order_id": order, "reason": "封账后撤单"}).Body["id"]
	r := f.HTTP(t, &caller, "confirm_draft", map[string]any{"id": draft})
	if r.Status != http.StatusConflict || r.Detail() != "该营业日已封账（2026-10-01）" {
		t.Fatal(r)
	}
}

func TestBackfillEntryDayNeedsNoClose(t *testing.T) {
	f := open(t)
	f.now("2026-11-10", 12)
	r := f.Call(t, f.Owner, "open_order", map[string]any{
		"gender": "female", "designated": false, "paper_date": "2026-11-09",
		"items": []any{map[string]any{"item_id": f.item, "staff": []any{}}},
	}, 200)
	orderID := uuid.MustParse(r.Body["id"].(string))
	f.checkout(orderID, payment("cash", "100"))
	f.now("2026-11-11", 12)
	seal(t, f, "2026-11-10")
}
