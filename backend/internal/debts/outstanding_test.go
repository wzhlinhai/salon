package debts_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/debts"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func (s *fixture) createItem(name, price string) uuid.UUID {
	s.t.Helper()
	return id(s.call("create_item", map[string]any{"name": name, "price": price, "discountable": true, "labor_fee_amount": nil, "labor_fee_percent": nil, "positions": []any{}, "level_prices": []any{}}, 200)["id"])
}

func (s *fixture) order(customer uuid.UUID, itemIDs ...uuid.UUID) map[string]any {
	s.t.Helper()
	items := make([]any, len(itemIDs))
	for i, itemID := range itemIDs {
		items[i] = map[string]any{"item_id": itemID, "staff": []any{}}
	}
	return s.call("open_order", map[string]any{"customer_id": customer, "gender": "female", "designated": true, "items": items}, 200)
}

func payment(method, amount string) map[string]any {
	return map[string]any{"method": method, "amount": amount}
}

func (s *fixture) checkout(o map[string]any, payments ...map[string]any) map[string]any {
	s.t.Helper()
	body := s.call("checkout_orders", map[string]any{"orders": []any{map[string]any{"order_id": o["id"], "payments": payments}}}, 200)
	return body["items"].([]any)[0].(map[string]any)
}

func (s *fixture) historicalFor(customer uuid.UUID, amount string) map[string]any {
	s.t.Helper()
	return s.call("register_historical_debt", map[string]any{"customer_id": customer, "amount": amount, "incurred_on": "2020-01-02", "note": "迁移"}, 200)
}

func (s *fixture) orderDebt(name, phone string, item uuid.UUID) {
	s.t.Helper()
	customer := id(s.call("create_customer", map[string]any{"name": name, "phone": phone}, 200)["customer"].(map[string]any)["id"])
	s.checkout(s.order(customer, item), payment("debt", "10.00"))
}

func TestOutstandingDebtsOrderAndHistorical(t *testing.T) {
	s := open(t)
	s.SetNow(time.Date(2026, 11, 25, 12, 0, 0, 0, settings.Location))
	cut := s.createItem("剪发", "70.00")
	shampoo := s.createItem("洗发水", "30.00")
	o := s.order(s.customer, cut)
	s.call("add_order_item", map[string]any{"order_id": o["id"], "item": map[string]any{"item_id": shampoo, "staff": []any{}}}, 200)
	s.checkout(o, payment("debt", "100"))
	s.historical("40")
	c := s.MustCaller(s.Owner)
	for _, ch := range s.Channels() {
		r := ch.Call(t, c, "list_outstanding_debts", debts.OutstandingDebtsInput{})
		if r.Status != 200 {
			t.Fatal(ch.Name, r)
		}
		items := r.Body["items"].([]any)
		if len(items) != 2 {
			t.Fatalf("%s: items = %d", ch.Name, len(items))
		}
		historical := items[0].(map[string]any)
		if historical["kind"] != "historical" || historical["business_date"] != "2020-01-02" || historical["order_id"] != nil || len(historical["item_names"].([]any)) != 0 {
			t.Fatal(ch.Name, historical)
		}
		order := items[1].(map[string]any)
		names := order["item_names"].([]any)
		if order["kind"] != "order" || order["order_id"] != o["id"] || order["business_date"] != "2026-11-25" || len(names) != 2 || names[0] != "剪发" || names[1] != "洗发水" {
			t.Fatal(ch.Name, order)
		}
		customer := order["customer"].(map[string]any)
		recordedBy := order["recorded_by"].(map[string]any)
		if customer["id"] != s.customer.String() || customer["name"] != "欠款顾客" || recordedBy["id"] != s.Res.OwnerID.String() || recordedBy["name"] != "王老板" {
			t.Fatal(ch.Name, order)
		}
		if order["amount"] != "100.00" || order["repaid"] != "0.00" || order["outstanding"] != "100.00" {
			t.Fatal(ch.Name, order)
		}
	}
}

func TestOutstandingDebtsItemNamesSkipRemoved(t *testing.T) {
	s := open(t)
	keep := s.createItem("剪发", "70.00")
	remove := s.createItem("洗发水", "30.00")
	o := s.order(s.customer, keep)
	added := s.call("add_order_item", map[string]any{"order_id": o["id"], "item": map[string]any{"item_id": remove, "staff": []any{}}}, 200)
	var removedID uuid.UUID
	for _, raw := range added["items"].([]any) {
		line := raw.(map[string]any)
		if line["item_name"] == "洗发水" {
			removedID = id(line["id"])
		}
	}
	if removedID == uuid.Nil {
		t.Fatal(added)
	}
	s.call("remove_order_item", map[string]any{"order_id": o["id"], "order_item_id": removedID}, 200)
	s.checkout(o, payment("debt", "70"))
	c := s.MustCaller(s.Owner)
	for _, ch := range s.Channels() {
		r := ch.Call(t, c, "list_outstanding_debts", debts.OutstandingDebtsInput{})
		if r.Status != 200 {
			t.Fatal(ch.Name, r)
		}
		items := r.Body["items"].([]any)
		if len(items) != 1 {
			t.Fatal(ch.Name, items)
		}
		names := items[0].(map[string]any)["item_names"].([]any)
		if len(names) != 1 || names[0] != "剪发" {
			t.Fatal(ch.Name, names)
		}
	}
}

func TestOutstandingDebtsExcludesRepaidAndVoided(t *testing.T) {
	s := open(t)
	s.SetNow(time.Date(2026, 11, 25, 12, 0, 0, 0, settings.Location))
	repaid := s.historical("50")
	s.call("repay_debt", repay(repaid, "50"), 200)
	o := s.order(s.customer, s.createItem("染色", "80.00"))
	s.checkout(o, payment("debt", "80"))
	s.call("void_order", map[string]any{"order_id": o["id"], "reason": "录错"}, 200)
	partial := s.historical("100")
	s.call("repay_debt", repay(partial, "30"), 200)
	c := s.MustCaller(s.Owner)
	for _, ch := range s.Channels() {
		r := ch.Call(t, c, "list_outstanding_debts", debts.OutstandingDebtsInput{})
		if r.Status != 200 {
			t.Fatal(ch.Name, r)
		}
		items := r.Body["items"].([]any)
		if len(items) != 1 {
			t.Fatal(ch.Name, items)
		}
		d := items[0].(map[string]any)
		if d["id"] != partial["id"] || d["kind"] != "historical" {
			t.Fatal(ch.Name, d)
		}
		assertBalances(t, d, "30.00", "70.00")
	}
}

func TestOutstandingDebtsStore(t *testing.T) {
	s := open(t)
	s.historical("10")
	_, otherDebt := s.otherStoreDebt()
	c := s.MustCaller(s.Owner)
	for _, ch := range s.Channels() {
		r := ch.Call(t, c, "list_outstanding_debts", debts.OutstandingDebtsInput{})
		if r.Status != 200 {
			t.Fatal(ch.Name, r)
		}
		items := r.Body["items"].([]any)
		if len(items) != 1 {
			t.Fatal(ch.Name, items)
		}
		if items[0].(map[string]any)["id"] == otherDebt.String() {
			t.Fatal(ch.Name, "other store debt listed")
		}
	}
}

func TestOutstandingDebtsCustomerScope(t *testing.T) {
	s := open(t)
	other, _ := s.Hire("别的员工", "009", "other-staff")
	otherCustomer := id(s.call("create_customer", map[string]any{"name": "别人的顾客"}, 200)["customer"].(map[string]any)["id"])
	s.call("assign_customer_responsible", map[string]any{"customer_ids": []uuid.UUID{s.customer}, "staff_id": s.Res.OwnerID}, 200)
	s.call("assign_customer_responsible", map[string]any{"customer_ids": []uuid.UUID{otherCustomer}, "staff_id": other}, 200)
	s.historical("10")
	s.historicalFor(otherCustomer, "20")
	c := s.MustCaller(s.Owner)
	c.Permissions[perm.CustomerView] = ops.Grant{Scope: ops.ScopeSelf}
	for _, ch := range s.Channels() {
		r := ch.Call(t, c, "list_outstanding_debts", debts.OutstandingDebtsInput{})
		if r.Status != 200 {
			t.Fatal(ch.Name, r)
		}
		items := r.Body["items"].([]any)
		if len(items) != 1 || items[0].(map[string]any)["customer"].(map[string]any)["id"] != s.customer.String() {
			t.Fatal(ch.Name, items)
		}
	}
}

func TestOutstandingDebtsPermissions(t *testing.T) {
	s := open(t)
	for _, p := range []ops.Permission{perm.ReportView, perm.CustomerView} {
		c := s.MustCaller(s.Owner)
		delete(c.Permissions, p)
		for _, ch := range s.Channels() {
			r := ch.Call(t, c, "list_outstanding_debts", debts.OutstandingDebtsInput{})
			if r.Status != 403 || r.Detail() != "permission denied" {
				t.Fatal(ch.Name, p, r)
			}
		}
	}
}

func TestOutstandingDebtsEmptyList(t *testing.T) {
	s := open(t)
	body, err := json.Marshal(debts.OutstandingDebtsInput{})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, ops.PathPrefix+"list_outstanding_debts", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.Owner)
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Fatal(rec.Code, rec.Body.String())
	}
}

func TestOutstandingDebtsIDTieBreak(t *testing.T) {
	s := open(t)
	first := s.historical("10")
	second := s.historical("20")
	want := []string{first["id"].(string), second["id"].(string)}
	slices.Sort(want)
	c := s.MustCaller(s.Owner)
	r := s.HTTP(t, &c, "list_outstanding_debts", debts.OutstandingDebtsInput{})
	if r.Status != 200 {
		t.Fatal(r)
	}
	items := r.Body["items"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["business_date"] != items[1].(map[string]any)["business_date"] {
		t.Fatal(items)
	}
	for i, raw := range items {
		if raw.(map[string]any)["id"] != want[i] {
			t.Fatal(i, raw, want)
		}
	}
}

type outstandingQueryCounter struct{ all atomic.Int64 }

func (c *outstandingQueryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.all.Add(1)
	return ctx
}

func (*outstandingQueryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestOutstandingDebtsQueryCount(t *testing.T) {
	s := open(t)
	item := s.createItem("剪发", "10.00")
	s.orderDebt("顾客1", "13900000001", item)
	counter := &outstandingQueryCounter{}
	config := s.Pool.Config().Copy()
	config.ConnConfig.Tracer = counter
	pool, err := db.NewPoolWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	app := optest.New(t, pool)
	c := s.MustCaller(s.Owner)
	counts := []int64{}
	for _, want := range []int{1, 10} {
		counter.all.Store(0)
		for _, ch := range app.Channels() {
			r := ch.Call(t, c, "list_outstanding_debts", debts.OutstandingDebtsInput{})
			if r.Status != 200 || len(r.Body["items"].([]any)) != want {
				t.Fatal(ch.Name, r)
			}
		}
		counts = append(counts, counter.all.Load())
		if want == 1 {
			for i := 2; i <= 10; i++ {
				s.orderDebt(fmt.Sprintf("顾客%d", i), fmt.Sprintf("139%08d", i), item)
			}
		}
	}
	if counts[0] != counts[1] {
		t.Fatal("SQL query count grew with outstanding debts", counts)
	}
}
