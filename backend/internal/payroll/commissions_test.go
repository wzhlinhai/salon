package payroll_test

import (
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

func TestPayrollCommissions(t *testing.T) {
	s, m := pastShop(t)
	id, _ := s.Hire("明细员工", "002", "detail")
	historicalStaff(t, s, id, m, nil)
	schemeAt(t, s, m.AddDate(0, -1, 0), 1, "10")
	payPerformance(t, s, id, m, false)
	payPerformance(t, s, id, m, true)
	payPerformance(t, s, s.Res.OwnerID, m, false)
	otherStore := uuid.New()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "INSERT INTO stores(id,tenant_id,name) VALUES($1,$2,'分店')", otherStore, s.Res.TenantID)
	dbtest.Commit(t, tx)
	other := s.MustCaller(s.Owner)
	other.StoreID = otherStore
	foreign := s.HTTP(t, &other, "create_staff", map[string]any{"name": "分店员工", "number": "003", "role_ids": []uuid.UUID{}})
	if foreign.Status != 200 {
		t.Fatal(foreign)
	}
	foreignID := uuid.MustParse(foreign.Body["id"].(string))
	for _, locked := range []bool{false, true} {
		if locked {
			s.Call(t, s.Owner, "lock_payroll_month", map[string]any{"month": month(m)}, 200)
		}
		for _, ch := range s.Channels() {
			caller := s.MustCaller(s.Owner)
			caller.Permissions = map[ops.Permission]ops.Grant{perm.PayrollManage: {}, perm.PerformanceView: {Scope: ops.ScopeSelf}}
			args := map[string]any{"month": month(m), "staff_id": id}
			r := ch.Call(t, caller, "list_payroll_commissions", args)
			rows := entries(t, r)
			if len(rows) != 5 {
				t.Fatal(rows)
			}
			reversal := false
			for _, row := range rows {
				c := row.(map[string]any)
				if c["staff"].(map[string]any)["id"] != id.String() {
					t.Fatal(c)
				}
				reversal = reversal || c["reversal"] == true
			}
			if !reversal {
				t.Fatal("缺少冲回", rows)
			}
			p := ch.Call(t, caller, "get_payroll", map[string]any{"month": month(m)})
			if p.Status != 200 {
				t.Fatal(p)
			}
			line := payrollLines(t, p.Body)[id.String()]
			if r.Body["total"] != line["commission_total"] {
				t.Fatal(r, line)
			}
			for i, v := range r.Body["totals"].([]any) {
				total := v.(map[string]any)
				source := []string{"item", "consume", "labor", "sale"}[i]
				if total["source"] != source || total["amount"] != line[source+"_commission"] {
					t.Fatal(total, line)
				}
			}
			expectedArgs := commissionRange(m, 1)
			expectedArgs["staff_id"] = id
			expectedArgs["limit"] = 1
			expected := ch.Call(t, s.MustCaller(s.Owner), "list_commissions", expectedArgs)
			args["limit"] = 1
			first := ch.Call(t, caller, "list_payroll_commissions", args)
			if !reflect.DeepEqual(first.Body, expected.Body) {
				t.Fatal(first, expected)
			}
			args["before_id"] = entries(t, first)[0].(map[string]any)["id"]
			next := entries(t, ch.Call(t, caller, "list_payroll_commissions", args))
			if len(next) != 1 || !reflect.DeepEqual(next[0], rows[1]) {
				t.Fatal(next, rows)
			}
			args = map[string]any{"month": month(m), "staff_id": id, "source": "sale"}
			sale := ch.Call(t, caller, "list_payroll_commissions", args)
			if len(entries(t, sale)) != 1 || sale.Body["total"] != "10.00" {
				t.Fatal(sale)
			}
			for _, foreign := range []uuid.UUID{uuid.New(), foreignID} {
				empty := ch.Call(t, caller, "list_payroll_commissions", map[string]any{"month": month(m), "staff_id": foreign})
				if len(entries(t, empty)) != 0 || empty.Body["total"] != "0.00" {
					t.Fatal(empty)
				}
			}
			caller.Permissions = map[ops.Permission]ops.Grant{}
			reject(t, ch.Call(t, caller, "list_payroll_commissions", args), 403, "permission denied")
			caller = s.MustCaller(s.Owner)
			reject(t, ch.Call(t, caller, "list_payroll_commissions", map[string]any{"month": month(m.AddDate(0, -1, 0)), "staff_id": id}), 409, "结算月不能早于开店的月份（"+month(m)+"）")
			detail := "validation failed"
			if ch.Name == "ai" {
				detail = "invalid arguments: expected string to match pattern ^[0-9]{4}-(0[1-9]|1[0-2])$ (month: 2026-13)"
			}
			reject(t, ch.Call(t, caller, "list_payroll_commissions", map[string]any{"month": "2026-13", "staff_id": id}), 422, detail)
		}
	}
}

func TestPayrollCommissionsQueryCount(t *testing.T) {
	s, m := pastShop(t)
	schemeAt(t, s, m.AddDate(0, -1, 0), 1, "10")
	payPerformance(t, s, s.Res.OwnerID, m, true)
	counter := &queryCounter{}
	config := s.Pool.Config().Copy()
	config.ConnConfig.Tracer = counter
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(t.Context()); err != nil {
		t.Fatal(err)
	}
	app := optest.New(t, pool)
	counts := map[string]int64{}
	for _, n := range []int{1, 5} {
		if n > 1 {
			payPerformance(t, s, s.Res.OwnerID, m, false)
		}
		for _, ch := range app.Channels() {
			counter.count.Store(0)
			r := ch.Call(t, s.MustCaller(s.Owner), "list_payroll_commissions", map[string]any{"month": month(m), "staff_id": s.Res.OwnerID})
			count := counter.count.Load()
			if len(entries(t, r)) != n {
				t.Fatal(r)
			}
			if n == 1 {
				counts[ch.Name] = count
			} else if count != counts[ch.Name] {
				t.Fatal("查询次数增长", counts, count)
			}
		}
	}
}
