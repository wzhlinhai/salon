package commission_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
)

func reference(t *testing.T, s *optest.Shop, op string, args map[string]any) uuid.UUID {
	t.Helper()
	r := s.HTTPWithToken(t, s.Owner, op, args)
	if r.Status != 200 {
		t.Fatal(r)
	}
	return uuid.MustParse(r.Body["id"].(string))
}

func TestCommissionReferences(t *testing.T) {
	s := optest.OpenShop(t)
	day := today(t, s)
	staffID, _ := s.Hire("员工", "002", "worker")
	positionID := reference(t, s, "create_position", map[string]any{"name": "岗位"})
	itemID := reference(t, s, "create_item", map[string]any{"name": "项目", "kind": "service", "price": "100", "discountable": true, "labor_fee_amount": nil, "labor_fee_percent": nil, "positions": []any{}, "level_prices": []any{}, "member_prices": []any{}})
	for _, tc := range []struct {
		op   string
		args map[string]any
	}{
		{"deactivate_staff", map[string]any{"staff_id": staffID}},
		{"update_position", map[string]any{"id": positionID, "name": "岗位", "active": false}},
		{"update_item", map[string]any{"id": itemID, "name": "项目", "kind": "service", "price": "100", "discountable": true, "labor_fee_amount": nil, "labor_fee_percent": nil, "active": false, "positions": []any{}, "level_prices": []any{}, "member_prices": []any{}}},
	} {
		r := s.HTTPWithToken(t, s.Owner, tc.op, tc.args)
		if r.Status != 200 {
			t.Fatal(tc.op, r)
		}
	}
	sc := emptyScheme()
	r := rule("item")
	r["by_position"] = true
	r["by_designated"] = true
	r["by_member"] = true
	r["cells"] = []any{map[string]any{"position_id": positionID, "designated": false, "member": true, "percent": "0"}, map[string]any{"position_id": positionID, "designated": true, "member": false, "fixed_amount": "0"}}
	sc["rules"] = []any{r}
	sc["overrides"] = []any{
		map[string]any{"source": "item", "staff_id": staffID, "percent": "10"},
		map[string]any{"source": "item", "item_id": itemID, "fixed_amount": "2"},
		map[string]any{"source": "item", "staff_id": staffID, "item_id": itemID, "percent": "100"},
	}
	sc["excluded_staff"] = []any{staffID}
	saved := save(t, s, day, sc)
	save(t, s, day, sc)
	for _, ch := range s.Channels() {
		got := ch.Call(t, s.MustCaller(s.Owner), "get_commission_scheme", map[string]any{"version": 1})
		if got.Status != 200 {
			t.Fatal(got)
		}
		scheme := got.Body["scheme"].(map[string]any)
		cell := scheme["rules"].([]any)[0].(map[string]any)["cells"].([]any)[0].(map[string]any)
		if cell["position"].(map[string]any)["name"] != "岗位" {
			t.Fatal(got)
		}
		overrides := scheme["overrides"].([]any)
		if overrides[0].(map[string]any)["staff"].(map[string]any)["name"] != "员工" || overrides[1].(map[string]any)["item"].(map[string]any)["name"] != "项目" {
			t.Fatal(got)
		}
		if scheme["excluded_staff"].([]any)[0].(map[string]any)["staff"].(map[string]any)["name"] != "员工" {
			t.Fatal(got)
		}
	}
	input, _ := json.Marshal(map[string]any{"effective_from": day.Format(time.DateOnly), "scheme": sc})
	output, _ := json.Marshal(saved.Body)
	t.Logf("saved scheme input=%s\nreturned view=%s", input, output)
	// 同租户第二家店，避免仅凭 RLS 的存在检查掩盖门店检查。
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var otherStore, otherStaff, otherPosition, otherItem uuid.UUID
	for _, entry := range []struct {
		sql  string
		dest *uuid.UUID
		args []any
	}{
		{"INSERT INTO stores(tenant_id,name) VALUES($1,'外店') RETURNING id", &otherStore, []any{s.Res.TenantID}},
	} {
		if err := tx.QueryRow(t.Context(), entry.sql, entry.args...).Scan(entry.dest); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.QueryRow(t.Context(), "INSERT INTO staff(tenant_id,store_id,name,number) VALUES($1,$2,'外店员工','999') RETURNING id", s.Res.TenantID, otherStore).Scan(&otherStaff); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(t.Context(), "INSERT INTO positions(tenant_id,store_id,name) VALUES($1,$2,'外店岗位') RETURNING id", s.Res.TenantID, otherStore).Scan(&otherPosition); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(t.Context(), "INSERT INTO items(tenant_id,store_id,name,kind,price) VALUES($1,$2,'外店项目','service',1) RETURNING id", s.Res.TenantID, otherStore).Scan(&otherItem); err != nil {
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)
	for _, tc := range []struct {
		kind, detail string
		id           uuid.UUID
	}{
		{"staff", "提成方案的员工必须属于本店", otherStaff},
		{"excluded", "提成方案的员工必须属于本店", otherStaff},
		{"position", "提成方案的岗位必须属于本店", otherPosition},
		{"item", "提成方案的项目必须属于本店", otherItem},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			bad := emptyScheme()
			rr := rule("item")
			bad["rules"] = []any{rr}
			switch tc.kind {
			case "staff":
				bad["overrides"] = []any{map[string]any{"source": "item", "staff_id": tc.id, "percent": "1"}}
			case "excluded":
				bad["excluded_staff"] = []any{tc.id}
			case "position":
				rr["by_position"] = true
				rr["cells"] = []any{map[string]any{"position_id": tc.id, "percent": "1"}}
			case "item":
				bad["overrides"] = []any{map[string]any{"source": "item", "item_id": tc.id, "percent": "1"}}
			}
			reject(t, s.HTTPWithToken(t, s.Owner, "save_commission_scheme", map[string]any{"effective_from": day.Format(time.DateOnly), "scheme": bad}), 409, tc.detail)
		})
	}
}

type queryCounter struct{ n atomic.Int64 }

func (c *queryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.n.Add(1)
	return ctx
}
func (*queryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestCommissionQueryCount(t *testing.T) {
	s := optest.OpenShop(t)
	day := today(t, s)
	for _, n := range []int{1, 5} {
		sc := emptyScheme()
		r := rule("item")
		r["by_position"] = true
		cells, overrides, excluded := []any{}, []any{}, []any{}
		for i := range n {
			name := fmt.Sprintf("%d-%d", n, i)
			sid, _ := s.Hire(name, name, "staff"+name)
			pid := reference(t, s, "create_position", map[string]any{"name": name})
			iid := reference(t, s, "create_item", map[string]any{"name": name, "kind": "service", "price": "1", "discountable": true, "labor_fee_amount": nil, "labor_fee_percent": nil, "positions": []any{}, "level_prices": []any{}, "member_prices": []any{}})
			cells = append(cells, map[string]any{"position_id": pid, "percent": "10"})
			overrides = append(overrides, map[string]any{"source": "item", "staff_id": sid, "item_id": iid, "percent": "20"})
			excluded = append(excluded, sid)
		}
		r["cells"] = cells
		sc["rules"] = []any{r}
		sc["overrides"] = overrides
		sc["excluded_staff"] = excluded
		save(t, s, day, sc)
	}
	counter := &queryCounter{}
	cfg := s.Pool.Config().Copy()
	cfg.ConnConfig.Tracer = counter
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(t.Context()); err != nil {
		t.Fatal(err)
	}
	app := optest.New(t, pool)
	caller := s.MustCaller(s.Owner)
	for _, ch := range app.Channels() {
		var counts []int64
		for version := 1; version <= 2; version++ {
			counter.n.Store(0)
			got := ch.Call(t, caller, "get_commission_scheme", map[string]any{"version": version})
			if got.Status != 200 || len(got.Body["scheme"].(map[string]any)["excluded_staff"].([]any)) != []int{1, 5}[version-1] {
				t.Fatal(got)
			}
			counts = append(counts, counter.n.Load())
		}
		if counts[0] != counts[1] {
			t.Fatal(ch.Name, counts)
		}
		counts = nil
		for _, limit := range []int{1, 2} {
			counter.n.Store(0)
			got := ch.Call(t, caller, "list_commission_scheme_versions", map[string]any{"limit": limit})
			if got.Status != 200 || len(got.Body["versions"].([]any)) != limit {
				t.Fatal(got)
			}
			counts = append(counts, counter.n.Load())
		}
		if counts[0] != counts[1] {
			t.Fatal(ch.Name, counts)
		}
	}
}
