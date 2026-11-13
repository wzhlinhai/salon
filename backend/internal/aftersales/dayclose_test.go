package aftersales_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
)

func methodAmount(t *testing.T, lines []any, method string) string {
	t.Helper()
	for _, raw := range lines {
		row := raw.(map[string]any)
		if row["method"] == method {
			return row["amount"].(string)
		}
	}
	t.Fatalf("method %s not found in %v", method, lines)
	return ""
}

func TestAftersaleDayCloseFigures(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	cashOrder := f.order(1)
	f.checkout(cashOrder, payment("cash", "100"))
	ct := f.cardType("日结卡")
	card := f.card(ct, "100.00", "0.00", payment("cash", "100"))
	cardOrder := f.order(1)
	f.checkout(cardOrder, cardPayment("card", "100", id(card["id"]), nil))

	f.now("2026-10-02", 12)
	cashRefund := f.create(id(cashOrder["id"]), "partial_refund", "退现金", payment("cash", "50"))
	f.approve(id(cashRefund["id"]), nil)
	cardRefund := f.create(id(cardOrder["id"]), "partial_refund", "退卡本金", map[string]any{"method": "card_principal", "amount": "40", "card_id": card["id"]})
	f.approve(id(cardRefund["id"]), nil)

	preview := f.call("preview_day_close", map[string]any{"date": "2026-10-02"}, 200)
	if got := methodAmount(t, preview["on_site"].([]any), "cash"); got != "-50.00" {
		t.Fatal(preview["on_site"])
	}
	if got := methodAmount(t, preview["non_site"].([]any), "card_principal"); got != "-40.00" {
		t.Fatal(preview["non_site"])
	}
	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	var kind, delta, day string
	var aftersaleID uuid.UUID
	if err := tx.QueryRow(context.Background(), "SELECT kind,principal_delta::text,business_date::text,aftersale_id FROM card_ledger WHERE aftersale_id=$1", cardRefund["id"]).Scan(&kind, &delta, &day, &aftersaleID); err != nil {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	if kind != "aftersale_refund" || delta != "40.00" || day != "2026-10-02" || aftersaleID != id(cardRefund["id"]) {
		t.Fatal(kind, delta, day, aftersaleID)
	}
	for _, ch := range f.Channels() {
		r := ch.Call(t, f.MustCaller(f.Owner), "list_card_ledger", map[string]any{"card_id": card["id"]})
		if r.Status != 200 || r.Body["entries"].([]any)[0].(map[string]any)["aftersale_id"] != cardRefund["id"] {
			t.Fatal(ch.Name, r)
		}
	}

	f.now("2026-10-03", 12)
	caller := f.MustCaller(f.Owner)
	r := f.HTTP(t, &caller, "seal_day", map[string]any{"date": "2026-10-02"})
	if r.Status != 409 || r.Detail() != "请先日结 2026-10-02" {
		t.Fatal(r)
	}
	f.call("close_day", map[string]any{"date": "2026-10-02", "cash_counted": "0", "unionpay_actual": "0", "wechat_actual": "0", "alipay_actual": "0"}, 200)
	closed := f.call("get_day_close", map[string]any{"date": "2026-10-02"}, 200)
	system := closed["current"].(map[string]any)["system"].(map[string]any)
	if got := methodAmount(t, system["on_site"].([]any), "cash"); got != "-50.00" {
		t.Fatal(system["on_site"])
	}
	if got := methodAmount(t, system["non_site"].([]any), "card_principal"); got != "-40.00" {
		t.Fatal(system["non_site"])
	}
}

func TestAftersaleDayCloseBecomesStale(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	order := f.order(1)
	f.checkout(order, payment("cash", "100"))
	f.now("2026-10-02", 12)
	refund := f.create(id(order["id"]), "partial_refund", "退现金", payment("cash", "10"))
	preview := f.call("preview_day_close", map[string]any{"date": "2026-10-02"}, 200)
	system, err := json.Marshal(preview)
	if err != nil {
		t.Fatal(err)
	}
	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	dbtest.Exec(t, tx, `INSERT INTO day_closes(tenant_id,store_id,business_date,version,system,differences,cash_counted,unionpay_actual,wechat_actual,alipay_actual,created_by) VALUES($1,$2,'2026-10-02',1,$3,'[]',0,0,0,0,$4)`, f.Res.TenantID, f.Res.StoreID, system, f.Res.OwnerID)
	dbtest.Commit(t, tx)
	f.approve(id(refund["id"]), nil)
	f.now("2026-10-03", 12)
	before := f.snapshot()
	caller := f.MustCaller(f.Owner)
	r := f.HTTP(t, &caller, "seal_day", map[string]any{"date": "2026-10-02"})
	if r.Status != 409 || r.Detail() != "日结后当天账目有变化，请重新日结" || before != f.snapshot() {
		t.Fatal(r)
	}
}

func TestAftersaleOnSealedOrder(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	order := f.order(1)
	f.checkout(order, payment("cash", "100"))
	f.now("2026-10-02", 12)
	f.call("close_day", map[string]any{"date": "2026-10-01", "cash_counted": "100", "unionpay_actual": "0", "wechat_actual": "0", "alipay_actual": "0"}, 200)
	f.call("seal_day", map[string]any{"date": "2026-10-01"}, 200)
	f.now("2026-10-03", 12)
	refund := f.create(id(order["id"]), "partial_refund", "封账后退", payment("cash", "10"))
	out := f.approve(id(refund["id"]), nil)
	if out["status"] != "approved" || out["refunds"].([]any)[0].(map[string]any)["business_date"] != "2026-10-03" {
		t.Fatal(out)
	}
}

func TestAftersaleRefundOnlyDayRequiresClose(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	order := f.order(1)
	f.checkout(order, payment("cash", "100"))
	f.now("2026-10-02", 12)
	refund := f.create(id(order["id"]), "partial_refund", "只退现金", payment("cash", "10"))
	f.approve(id(refund["id"]), nil)
	f.now("2026-10-03", 12)
	caller := f.MustCaller(f.Owner)
	r := f.HTTP(t, &caller, "seal_day", map[string]any{"date": "2026-10-02"})
	if r.Status != 409 || r.Detail() != "请先日结 2026-10-02" {
		t.Fatal(r)
	}
}

func TestAftersaleApprovalSealedDay(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	order := f.order(1)
	f.checkout(order, payment("cash", "100"))
	f.now("2026-10-02", 12)
	refund := f.create(id(order["id"]), "partial_refund", "封账当天", payment("cash", "10"))
	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	dbtest.Exec(t, tx, `INSERT INTO store_sealings(tenant_id,store_id,sealed_through) VALUES($1,$2,'2026-10-02') ON CONFLICT (tenant_id,store_id) DO UPDATE SET sealed_through=EXCLUDED.sealed_through`, f.Res.TenantID, f.Res.StoreID)
	dbtest.Commit(t, tx)
	before := f.snapshot()
	caller := f.MustCaller(f.Owner)
	r := f.HTTP(t, &caller, "approve_aftersale", map[string]any{"id": refund["id"]})
	if r.Status != 409 || r.Detail() != "该营业日已封账（2026-10-02）" || before != f.snapshot() {
		t.Fatal(r)
	}
}
