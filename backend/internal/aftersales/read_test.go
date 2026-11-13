package aftersales_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/optest"
)

func TestAftersaleReads(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	firstOrder := f.order(1)
	f.checkout(firstOrder, payment("cash", "100"))
	first := f.create(id(firstOrder["id"]), "repair", "修补")
	secondOrder := f.paidOrder(payment("cash", "100"))
	second := f.create(id(secondOrder["id"]), "partial_refund", "部分退款", payment("cash", "10"))
	f.reject(id(second["id"]), nil)
	thirdOrder := f.paidOrder(payment("cash", "100"))
	third := f.create(id(thirdOrder["id"]), "partial_refund", "通过", payment("cash", "10"))
	f.approve(id(third["id"]), nil)

	caller := f.MustCaller(f.Owner)
	for _, ch := range f.Channels() {
		got := ch.Call(t, caller, "get_aftersale", map[string]any{"id": third["id"]})
		if got.Status != 200 || got.Body["id"] != third["id"] || got.Body["status"] != "approved" || len(got.Body["events"].([]any)) != 2 {
			t.Fatal(ch.Name, got)
		}
		list := ch.Call(t, caller, "list_aftersales", map[string]any{"date_from": "2026-10-01", "date_to": "2026-10-01"})
		if list.Status != 200 || len(list.Body["items"].([]any)) != 3 {
			t.Fatal(ch.Name, list)
		}
		raw, err := json.Marshal(list.Body)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "phone") || strings.Contains(string(raw), "customer") {
			t.Fatal(string(raw))
		}
		for _, tc := range []struct {
			args   map[string]any
			length int
		}{
			{map[string]any{"date_from": "2026-10-01", "date_to": "2026-10-01", "status": "approved"}, 1},
			{map[string]any{"date_from": "2026-10-01", "date_to": "2026-10-01", "status": "rejected"}, 1},
			{map[string]any{"date_from": "2026-10-01", "date_to": "2026-10-01", "status": "pending"}, 1},
			{map[string]any{"date_from": "2026-10-01", "date_to": "2026-10-01", "order_id": firstOrder["id"]}, 1},
		} {
			got := ch.Call(t, caller, "list_aftersales", tc.args)
			if got.Status != 200 || len(got.Body["items"].([]any)) != tc.length {
				t.Fatal(ch.Name, tc.args, got)
			}
		}
		for _, tc := range []struct{ from, to, detail string }{
			{"2026-02-01", "2026-01-01", "开始营业日不能晚于结束营业日"},
			{"2026-01-01", "2026-04-04", "查询范围最多 92 天"},
		} {
			got := ch.Call(t, caller, "list_aftersales", map[string]any{"date_from": tc.from, "date_to": tc.to})
			if got.Status != 409 || got.Detail() != tc.detail {
				t.Fatal(ch.Name, got)
			}
		}
	}
	denied := f.MustCaller(f.Owner)
	delete(denied.Permissions, "aftersales.manage")
	for _, ch := range f.Channels() {
		for _, tc := range []struct {
			op   string
			args any
		}{
			{"get_aftersale", map[string]any{"id": first["id"]}},
			{"list_aftersales", map[string]any{"date_from": "2026-10-01", "date_to": "2026-10-01"}},
		} {
			got := ch.Call(t, denied, tc.op, tc.args)
			if got.Status != 403 || got.Detail() != "permission denied" {
				t.Fatal(ch.Name, got)
			}
		}
	}
}

func TestAftersaleAI(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	order := f.order(1)
	f.checkout(order, payment("cash", "100"))
	owner := f.MustCaller(f.Owner)
	args := map[string]any{"order_id": order["id"], "type": "partial_refund", "reason": "AI 起草", "refund_lines": []any{payment("cash", "10")}}
	before := f.snapshot()
	optest.RequireDraftCreated(t, f.AI(t, owner, "create_aftersale", args), before == f.snapshot())

	pending := f.create(id(order["id"]), "repair", "待审批")
	before = f.snapshot()
	for _, tc := range []struct {
		op   string
		args any
	}{
		{"approve_aftersale", map[string]any{"id": pending["id"]}},
		{"reject_aftersale", map[string]any{"id": pending["id"]}},
	} {
		got := f.AI(t, owner, tc.op, tc.args)
		if got.Status != 404 || got.Detail() != "unknown operation" || before != f.snapshot() {
			t.Fatal(tc.op, got)
		}
	}
}

type aftersaleQueryCounter struct{ count atomic.Int64 }

func (c *aftersaleQueryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.count.Add(1)
	return ctx
}
func (*aftersaleQueryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestAftersaleListQueryCount(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	order := f.order(1)
	f.checkout(order, payment("cash", "100"))
	counter := &aftersaleQueryCounter{}
	config := f.Pool.Config().Copy()
	config.ConnConfig.Tracer = counter
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
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
	for _, n := range []int{1, 6} {
		for len(f.call("list_aftersales", map[string]any{"date_from": "2026-10-01", "date_to": "2026-10-01"}, 200)["items"].([]any)) < n {
			f.create(id(order["id"]), "repair", "修补")
		}
		for _, ch := range app.Channels() {
			counter.count.Store(0)
			r := ch.Call(t, caller, "list_aftersales", map[string]any{"date_from": "2026-10-01", "date_to": "2026-10-01"})
			if r.Status != 200 || len(r.Body["items"].([]any)) != n {
				t.Fatal(ch.Name, r)
			}
			got := counter.count.Load()
			if n == 1 {
				counts[ch.Name] = got
			} else if got != counts[ch.Name] {
				t.Fatalf("%s queries grew: %d -> %d", ch.Name, counts[ch.Name], got)
			}
		}
	}
}
