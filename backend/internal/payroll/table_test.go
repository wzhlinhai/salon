package payroll_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/payroll"
	"github.com/wzhlinhai/salon/backend/internal/payrollmonth"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func pastShop(t *testing.T) (*optest.Shop, time.Time) {
	t.Helper()
	s := optest.OpenShop(t)
	day := settings.Date(time.Now())
	m := time.Date(day.Year(), day.Month()-4, 1, 0, 0, 0, 0, time.UTC)
	for _, table := range []string{"stores", "staff"} {
		if _, err := s.Pool.Exec(t.Context(), "UPDATE "+table+" SET created_at=$1", m.Add(12*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	return s, m
}
func historicalStaff(t *testing.T, s *optest.Shop, id uuid.UUID, created time.Time, departed *time.Time) {
	t.Helper()
	if _, err := s.Pool.Exec(t.Context(), "UPDATE staff SET created_at=$1,active=$2,deactivated_at=$3 WHERE id=$4", created, departed == nil, departed, id); err != nil {
		t.Fatal(err)
	}
}
func payrollView(t *testing.T, s *optest.Shop, m time.Time) map[string]any {
	t.Helper()
	return s.Call(t, s.Owner, "get_payroll", map[string]any{"month": month(m)}, 200).Body
}
func payrollLines(t *testing.T, body map[string]any) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, v := range body["lines"].([]any) {
		row := v.(map[string]any)
		out[row["staff"].(map[string]any)["id"].(string)] = row
	}
	return out
}
func assertMoney(t *testing.T, row map[string]any, want map[string]string) {
	t.Helper()
	for k, v := range want {
		if row[k] != v {
			t.Fatalf("%s = %v want %s; %v", k, row[k], v, row)
		}
	}
}
func schemeAt(t *testing.T, s *optest.Shop, m time.Time, version int, percent string) {
	t.Helper()
	rules := []any{}
	for _, source := range []string{"item", "consume", "labor", "sale"} {
		rules = append(rules, map[string]any{"source": source, "base": "performance", "cells": []any{map[string]any{"percent": percent}}})
	}
	raw, err := json.Marshal(map[string]any{"rules": rules, "overrides": []any{}, "excluded_staff": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, `INSERT INTO commission_scheme_versions(tenant_id,store_id,version,effective_from,scheme,saved_by,saved_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, s.Res.TenantID, s.Res.StoreID, version, m, raw, s.Res.OwnerID, m.Add(-time.Hour))
	dbtest.Commit(t, tx)
}

// payPerformance 通过真实结账建立引用，再准备有四类来源和跨月冲回的历史业绩。
func payPerformance(t *testing.T, s *optest.Shop, id uuid.UUID, m time.Time, reversal bool) {
	t.Helper()
	position := s.Call(t, s.Owner, "create_position", map[string]any{"name": "岗位" + uuid.NewString()[:8]}, 200).Body["id"]
	item := s.Call(t, s.Owner, "create_item", map[string]any{"name": "项目" + uuid.NewString()[:8], "kind": "service", "price": "100", "discountable": true, "labor_fee_amount": nil, "labor_fee_percent": nil, "positions": []any{map[string]any{"position_id": position, "required": false, "share_percent": 100}}, "level_prices": []any{}, "member_prices": []any{}}, 200).Body["id"]
	order := s.Call(t, s.Owner, "open_order", map[string]any{"gender": "female", "designated": false, "items": []any{map[string]any{"item_id": item, "staff": []any{map[string]any{"slot": 1, "staff_id": id}}}}}, 200).Body
	s.Call(t, s.Owner, "checkout_orders", map[string]any{"orders": []any{map[string]any{"order_id": order["id"], "payments": []any{map[string]any{"method": "cash", "amount": "100"}}}}}, 200)
	if reversal {
		s.Call(t, s.Owner, "void_order", map[string]any{"order_id": order["id"], "reason": "冲回"}, 200)
	}
	// 移动原笔和冲回到不同月份，确保目标月净提成为负。
	originalMonth := m
	if reversal {
		originalMonth = m.AddDate(0, -1, 0)
	}
	if _, err := s.Pool.Exec(t.Context(), `UPDATE performance_entries SET business_date=CASE WHEN reverses_id IS NULL THEN $1::date ELSE $2::date END,created_at=$3 WHERE order_id=$4`, originalMonth.AddDate(0, 0, 5), m.AddDate(0, 0, 6), m.Add(24*time.Hour), order["id"]); err != nil {
		t.Fatal(err)
	}
	if reversal {
		return
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	for _, source := range []struct{ kind, method string }{{"consume", "count_paid"}, {"labor", "labor"}} {
		dbtest.Exec(t, tx, `INSERT INTO performance_entries(tenant_id,store_id,kind,business_date,order_id,order_item_id,checkout_id,settings_snapshot_id,slot,position_id,staff_id,method,paid_amount,ratio,amount,created_at,member) SELECT tenant_id,store_id,$1,business_date,order_id,order_item_id,checkout_id,settings_snapshot_id,slot,position_id,staff_id,$2,paid_amount,100,paid_amount,created_at,member FROM performance_entries WHERE order_id=$3 AND kind='item'`, source.kind, source.method, order["id"])
	}
	dbtest.Commit(t, tx)
	customer := s.Call(t, s.Owner, "create_customer", map[string]any{"name": "顾客", "gender": "female"}, 200).Body["customer"].(map[string]any)["id"]
	packet := s.Call(t, s.Owner, "issue_red_packet", map[string]any{"customer_id": customer, "kind": "sold", "face_value": "100", "price": "100", "expires_on": settings.Date(time.Now()).AddDate(1, 0, 0).Format(time.DateOnly), "sales_staff_ids": []uuid.UUID{id}, "payments": []any{map[string]any{"method": "cash", "amount": "100"}}}, 200).Body
	if _, err := s.Pool.Exec(t.Context(), "UPDATE performance_entries SET business_date=$1,created_at=$2 WHERE red_packet_id=$3", m.AddDate(0, 0, 7), m.Add(24*time.Hour), packet["id"]); err != nil {
		t.Fatal(err)
	}
}

func TestPayrollLiveFormulas(t *testing.T) {
	s, first := pastShop(t)
	m := first.AddDate(0, 1, 0)
	guarantee, _ := s.Hire("保底", "002", "guarantee")
	high, _ := s.Hire("超保底", "003", "high")
	negative, _ := s.Hire("负提成", "004", "negative")
	for _, id := range []uuid.UUID{guarantee, high, negative} {
		historicalStaff(t, s, id, first, nil)
	}
	schemeAt(t, s, first, 1, "10")
	set(t, s, s.Res.OwnerID, m, "base", "1000")
	set(t, s, guarantee, m, "guarantee", "100")
	set(t, s, high, m, "guarantee", "20")
	payPerformance(t, s, s.Res.OwnerID, m, false)
	payPerformance(t, s, guarantee, m, false)
	payPerformance(t, s, high, m, false)
	payPerformance(t, s, negative, m, true)
	add(t, s, s.Res.OwnerID, m, "奖励", "5")
	add(t, s, s.Res.OwnerID, m, "扣款", "-2")
	add(t, s, guarantee, m, "扣款", "-4")
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "get_payroll", map[string]any{"month": month(m)})
		if r.Status != 200 {
			t.Fatal(r)
		}
		lines := payrollLines(t, r.Body)
		if len(lines) != 4 {
			t.Fatal(lines)
		}
		assertMoney(t, lines[s.Res.OwnerID.String()], map[string]string{"applied_amount": "1000.00", "base_pay": "1000.00", "item_commission": "10.00", "consume_commission": "10.00", "labor_commission": "10.00", "sale_commission": "10.00", "commission_total": "40.00", "guarantee_top_up": "0.00", "adjustments_total": "3.00", "total": "1043.00"})
		assertMoney(t, lines[guarantee.String()], map[string]string{"base_pay": "0.00", "commission_total": "40.00", "guarantee_top_up": "60.00", "adjustments_total": "-4.00", "total": "96.00"})
		assertMoney(t, lines[high.String()], map[string]string{"guarantee_top_up": "0.00", "total": "40.00"})
		assertMoney(t, lines[negative.String()], map[string]string{"mode": "base", "applied_amount": "0.00", "item_commission": "-10.00", "total": "-10.00"})
		assertMoney(t, r.Body["totals"].(map[string]any), map[string]string{"applied_amount": "1120.00", "base_pay": "1000.00", "item_commission": "20.00", "consume_commission": "30.00", "labor_commission": "30.00", "sale_commission": "30.00", "commission_total": "110.00", "guarantee_top_up": "60.00", "adjustments_total": "-1.00", "total": "1169.00"})
		raw, _ := json.Marshal(r.Body)
		t.Logf("get_payroll: %s", raw)
	}
	before := payrollView(t, s, m)
	add(t, s, negative, m, "更正", "1")
	if reflect.DeepEqual(before, payrollView(t, s, m)) {
		t.Fatal("open month did not recompute adjustment")
	}
	payPerformance(t, s, negative, m, false)
	if got := payrollLines(t, payrollView(t, s, m))[negative.String()]["commission_total"]; got != "30.00" {
		t.Fatal(got)
	}
}

func TestPayrollEmployment(t *testing.T) {
	s, first := pastShop(t)
	m := first.AddDate(0, 1, 0)
	ids := []uuid.UUID{}
	for i := range 6 {
		id, _ := s.Hire(fmt.Sprint("员工", i), fmt.Sprint(i+2), fmt.Sprint("employment", i))
		ids = append(ids, id)
		historicalStaff(t, s, id, first, nil)
		set(t, s, id, m, "base", "100")
	}
	// 0: 早已离职；1: 当月离职；2: 当月入职；3: 未来入职；4: 离职后仅加减项；5: 默认零底薪。
	prev := m.Add(-12 * time.Hour)
	during := m.AddDate(0, 0, 15)
	historicalStaff(t, s, ids[0], first, &prev)
	historicalStaff(t, s, ids[1], first, &during)
	historicalStaff(t, s, ids[2], during, nil)
	historicalStaff(t, s, ids[3], m.AddDate(0, 1, 0), nil)
	historicalStaff(t, s, ids[4], first, &prev)
	set(t, s, ids[5], m, "base", "0")
	add(t, s, ids[4], m, "离职更正", "-1")
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "get_payroll", map[string]any{"month": month(m)})
		if r.Status != 200 {
			t.Fatal(r)
		}
		lines := payrollLines(t, r.Body)
		if len(lines) != 3 || lines[ids[0].String()] != nil || lines[ids[3].String()] != nil || lines[ids[5].String()] != nil {
			t.Fatal(lines)
		}
		for _, id := range ids[1:3] {
			assertMoney(t, lines[id.String()], map[string]string{"applied_amount": "100.00", "total": "100.00"})
		}
		assertMoney(t, lines[ids[4].String()], map[string]string{"applied_amount": "0.00", "base_pay": "0.00", "total": "-1.00"})
	}
	// 午夜后两点前离职属于前一个营业月；入职也用营业月。
	s.SetNow(first.AddDate(0, 0, -2).Add(12 * time.Hour))
	s.Call(t, s.Owner, "change_setting", map[string]any{"key": "business_day_end", "value": "02:00", "effective_from": first.Format(time.DateOnly)}, 200)
	s.SetNow(time.Time{})
	midnight := time.Date(m.Year(), m.Month(), 1, 1, 0, 0, 0, settings.Location)
	historicalStaff(t, s, ids[1], first, &midnight)
	historicalStaff(t, s, ids[3], midnight, nil)
	lines := payrollLines(t, payrollView(t, s, m))
	if lines[ids[1].String()] != nil || lines[ids[3].String()] == nil {
		t.Fatal(lines)
	}
	// 非在职员工采用保底时，底薪部分和补差都只能按零金额计算。
	set(t, s, ids[4], m, "guarantee", "500")
	assertMoney(t, payrollLines(t, payrollView(t, s, m))[ids[4].String()], map[string]string{"applied_amount": "0.00", "guarantee_top_up": "0.00", "total": "-1.00"})
}

func TestPayrollLockRules(t *testing.T) {
	s, m := pastShop(t)
	current := settings.Date(time.Now()).Format("2006-01")
	reject(t, s.HTTPWithToken(t, s.Owner, "lock_payroll_month", map[string]any{"month": current}), 409, current+" 还没结束，不能锁定")
	reject(t, s.HTTPWithToken(t, s.Owner, "lock_payroll_month", map[string]any{"month": month(m.AddDate(0, 2, 0))}), 409, "请先锁定 "+month(m.AddDate(0, 1, 0)))
	reject(t, s.HTTPWithToken(t, s.Owner, "lock_payroll_month", map[string]any{"month": month(m.AddDate(0, -1, 0))}), 409, "结算月不能早于开店的月份（"+month(m)+"）")
	set(t, s, s.Res.OwnerID, m, "base", "20")
	adjustment := add(t, s, s.Res.OwnerID, m, "锁定保留", "1")
	locked := s.Call(t, s.Owner, "lock_payroll_month", map[string]any{"month": month(m)}, 200).Body
	if locked["locked"] != true || locked["locked_at"] == nil || locked["locked_by"].(map[string]any)["id"] != s.Res.OwnerID.String() {
		t.Fatal(locked)
	}
	if !reflect.DeepEqual(locked, payrollView(t, s, m)) {
		t.Fatal("lock response differs")
	}
	reject(t, s.HTTPWithToken(t, s.Owner, "lock_payroll_month", map[string]any{"month": month(m)}), 409, month(m)+" 已经锁定")
	before := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "staff_base_salaries", "payroll_adjustments", "commission_settlements", "settled_commissions", "payroll_lines", "audit_log")
	for _, tc := range []struct {
		op     string
		args   any
		detail string
	}{
		{"set_base_salary", salaryArgs(s.Res.OwnerID, m, "base", "5"), "设置底薪"},
		{"add_payroll_adjustment", adjustmentArgs(s.Res.OwnerID, m, "新增", "2"), "添加加减项"},
		{"delete_payroll_adjustment", map[string]any{"id": adjustment["id"]}, "删除加减项"},
	} {
		reject(t, s.HTTPWithToken(t, s.Owner, tc.op, tc.args), 409, month(m)+" 已经锁定，不能再"+tc.detail)
	}
	if got := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "staff_base_salaries", "payroll_adjustments", "commission_settlements", "settled_commissions", "payroll_lines", "audit_log"); got != before {
		t.Fatal("rejection wrote data")
	}
	set(t, s, s.Res.OwnerID, m.AddDate(0, 1, 0), "base", "30")
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "list_payroll_months", map[string]any{})
		rows := entries(t, r)
		if len(rows) != 5 {
			t.Fatal(rows)
		}
		for i, v := range rows {
			row := v.(map[string]any)
			if row["month"] != month(m.AddDate(0, 4-i, 0)) || row["locked"] != (i == 4) || row["lockable"] != (i == 3) {
				t.Fatal(rows)
			}
			if i == 4 && (row["locked_at"] != locked["locked_at"] || !reflect.DeepEqual(row["locked_by"], locked["locked_by"])) {
				t.Fatal(row)
			}
		}
		raw, _ := json.Marshal(r.Body)
		t.Logf("list_payroll_months: %s", raw)
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	settlement, err := payrollmonth.GetSettlement(t.Context(), dbgen.New(tx), s.Res.StoreID, payrollmonth.Of(m))
	dbtest.Rollback(t, tx)
	if err != nil {
		t.Fatal(err)
	}
	auditEntry(t, s, "payroll_month", "lock_payroll_month", settlement.ID, nil, locked)
	caller := s.MustCaller(s.Owner)
	for _, ch := range s.Channels() {
		rows := entries(t, ch.Call(t, caller, "list_audit_log", map[string]any{"target_id": settlement.ID}))
		if len(rows) != 1 {
			t.Fatal(rows)
		}
	}
	caller.Permissions = map[ops.Permission]ops.Grant{perm.AuditLogView: {}}
	for _, ch := range s.Channels() {
		if rows := entries(t, ch.Call(t, caller, "list_audit_log", map[string]any{"target_id": settlement.ID})); len(rows) != 0 {
			t.Fatal("audit leaked", rows)
		}
	}
}

func TestPayrollLockAtomicity(t *testing.T) {
	s, m := pastShop(t)
	set(t, s, s.Res.OwnerID, m, "base", "1")
	schemeAt(t, s, m, 1, "10")
	payPerformance(t, s, s.Res.OwnerID, m, false)
	if _, err := s.Pool.Exec(t.Context(), "ALTER TABLE payroll_lines ADD CONSTRAINT forced_failure CHECK (total < 0)"); err != nil {
		t.Fatal(err)
	}
	r := s.HTTPWithToken(t, s.Owner, "lock_payroll_month", map[string]any{"month": month(m)})
	if r.Status != 500 {
		t.Fatal(r)
	} // rejection-detail: 人工数据库错误，只检查服务器错误及完整回滚
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	counts := []int{}
	for _, table := range []string{"commission_settlements", "settled_commissions", "payroll_lines"} {
		var n int
		err := tx.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&n)
		if err != nil {
			dbtest.Rollback(t, tx)
			t.Fatal(err)
		}
		counts = append(counts, n)
	}
	dbtest.Rollback(t, tx)
	if !reflect.DeepEqual(counts, []int{0, 0, 0}) {
		t.Fatal(counts)
	}
	if _, err := s.Pool.Exec(t.Context(), "ALTER TABLE payroll_lines DROP CONSTRAINT forced_failure"); err != nil {
		t.Fatal(err)
	}
	s.Call(t, s.Owner, "lock_payroll_month", map[string]any{"month": month(m)}, 200)
}

func TestPayrollLockPermissionAI(t *testing.T) {
	s, m := pastShop(t)
	set(t, s, s.Res.OwnerID, m, "base", "1")
	args := map[string]any{"month": month(m)}
	caller := s.MustCaller(s.Owner)
	before := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "commission_settlements", "settled_commissions", "payroll_lines", "audit_log")
	r := s.AI(t, caller, "lock_payroll_month", args)
	optest.RequireDraftCreated(t, r, dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "commission_settlements", "settled_commissions", "payroll_lines", "audit_log") == before)
	caller.Permissions = map[ops.Permission]ops.Grant{}
	for _, ch := range s.Channels() {
		for _, op := range []string{"get_payroll", "list_payroll_months", "lock_payroll_month"} {
			input := any(args)
			if op == "list_payroll_months" {
				input = map[string]any{}
			}
			reject(t, ch.Call(t, caller, op, input), 403, "permission denied")
		}
	}
	if got := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "commission_settlements", "settled_commissions", "payroll_lines", "audit_log"); got != before {
		t.Fatal("denied call wrote data")
	}
}

func TestPayrollTableQueryCount(t *testing.T) {
	s, m := pastShop(t)
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
	caller := s.MustCaller(s.Owner)
	for _, n := range []int{1, 5} {
		if n == 1 {
			set(t, s, s.Res.OwnerID, m, "base", "1")
		} else {
			for i := range 4 {
				id, _ := s.Hire(fmt.Sprint(i), fmt.Sprint(i+2), fmt.Sprint("count", i))
				historicalStaff(t, s, id, m, nil)
				set(t, s, id, m, "base", "1")
			}
		}
		for _, ch := range app.Channels() {
			counter.count.Store(0)
			r := ch.Call(t, caller, "get_payroll", map[string]any{"month": month(m)})
			count := counter.count.Load()
			if r.Status != 200 || len(r.Body["lines"].([]any)) != n {
				t.Fatal(r)
			}
			if n == 1 {
				counts[ch.Name] = count
			} else if count != counts[ch.Name] {
				t.Fatal(count, counts)
			}
		}
		s.Call(t, s.Owner, "lock_payroll_month", map[string]any{"month": month(m)}, 200)
		for _, ch := range app.Channels() {
			for _, op := range []string{"get_payroll", "list_payroll_months"} {
				args := map[string]any{}
				if op == "get_payroll" {
					args["month"] = month(m)
				}
				counter.count.Store(0)
				r := ch.Call(t, caller, op, args)
				count := counter.count.Load()
				if r.Status != 200 {
					t.Fatal(r)
				}
				key := ch.Name + op + "locked"
				if n == 1 {
					counts[key] = count
				} else if count != counts[key] {
					t.Fatal(key, count, counts[key])
				}
			}
		}
		m = m.AddDate(0, 1, 0)

	}
}

func commissionRange(m time.Time, months int) map[string]any {
	return map[string]any{"date_from": m.Format(time.DateOnly), "date_to": m.AddDate(0, months, -1).Format(time.DateOnly), "limit": 200}
}
func TestPayrollStoredCommissions(t *testing.T) {
	s, m := pastShop(t)
	next := m.AddDate(0, 1, 0)
	other, token := s.Hire("员工", "002", "stored")
	historicalStaff(t, s, other, m, nil)
	schemeAt(t, s, m.AddDate(0, -1, 0), 1, "10")
	set(t, s, s.Res.OwnerID, m, "guarantee", "50")
	payPerformance(t, s, s.Res.OwnerID, m, false)
	payPerformance(t, s, other, m, false)
	payPerformance(t, s, other, m, true)
	payPerformance(t, s, s.Res.OwnerID, next, false)
	// 同营业日按写入时刻排序，刻意让它与 UUID 的先后相反。
	if _, err := s.Pool.Exec(t.Context(), "UPDATE performance_entries SET created_at=$1 WHERE staff_id=$2 AND business_date BETWEEN $3 AND $4", m.Add(48*time.Hour), s.Res.OwnerID, m, m.AddDate(0, 1, -1)); err != nil {
		t.Fatal(err)
	}
	before := s.Call(t, s.Owner, "list_commissions", commissionRange(m, 1), 200).Body
	openBefore := s.Call(t, s.Owner, "list_commissions", commissionRange(next, 1), 200).Body
	live := payrollView(t, s, m)
	locked := s.Call(t, s.Owner, "lock_payroll_month", map[string]any{"month": month(m)}, 200).Body
	if !reflect.DeepEqual(locked["lines"], live["lines"]) || !reflect.DeepEqual(locked["totals"], live["totals"]) {
		t.Fatal(locked, live)
	}
	// 存储的物理顺序不等于输出排序，确保读取必须使用统一比较器。
	if _, err := s.Pool.Exec(t.Context(), "CLUSTER settled_commissions USING settled_commissions_lookup"); err != nil {
		t.Fatal(err)
	}

	if got := s.Call(t, s.Owner, "list_commissions", commissionRange(m, 1), 200).Body; !reflect.DeepEqual(got, before) {
		t.Fatal("snapshot differs", got, before)
	}
	// 新版本的保存时刻早于历史业绩：同一份方案确实会改变未锁定月。
	schemeAt(t, s, m, 2, "80")
	openAfter := s.Call(t, s.Owner, "list_commissions", commissionRange(next, 1), 200).Body
	if reflect.DeepEqual(openBefore, openAfter) || openAfter["total"] != "320.00" {
		t.Fatal("live version replacement ineffective", openBefore, openAfter)
	}
	payPerformance(t, s, other, m, false)
	if got := payrollView(t, s, m); !reflect.DeepEqual(got, locked) {
		t.Fatal("locked payroll changed", got, locked)
	}
	if got := s.Call(t, s.Owner, "list_commissions", commissionRange(m, 1), 200).Body; !reflect.DeepEqual(got, before) {
		t.Fatal("locked commissions changed", got, before)
	}
	// 名称在读取时解析；金额、解释中的版本和计算保持不变。
	if _, err := s.Pool.Exec(t.Context(), "UPDATE staff SET name='新名字' WHERE id=$1", other); err != nil {
		t.Fatal(err)
	}
	for _, v := range before["entries"].([]any) {
		row := v.(map[string]any)
		ref := row["staff"].(map[string]any)
		if ref["id"] == other.String() {
			ref["name"] = "新名字"
		}
	}
	for _, v := range locked["lines"].([]any) {
		row := v.(map[string]any)
		ref := row["staff"].(map[string]any)
		if ref["id"] == other.String() {
			ref["name"] = "新名字"
		}
	}
	caller := s.MustCaller(token)
	caller.Permissions = map[ops.Permission]ops.Grant{perm.PerformanceView: {Scope: ops.ScopeSelf}}
	for _, ch := range s.Channels() {
		got := ch.Call(t, s.MustCaller(s.Owner), "get_payroll", map[string]any{"month": month(m)})
		if got.Status != 200 || !reflect.DeepEqual(got.Body, locked) {
			t.Fatal(got, locked)
		}
		got = ch.Call(t, s.MustCaller(s.Owner), "list_commissions", commissionRange(m, 1))
		if got.Status != 200 || !reflect.DeepEqual(got.Body, before) {
			t.Fatal(got, before)
		}
		self := ch.Call(t, caller, "list_commissions", commissionRange(m, 1))
		if self.Status != 200 || self.Body["total"] != "30.00" || len(self.Body["entries"].([]any)) != 5 {
			t.Fatal(self)
		}
		for _, v := range self.Body["entries"].([]any) {
			if v.(map[string]any)["staff"].(map[string]any)["id"] != other.String() {
				t.Fatal(self)
			}
		}
		args := commissionRange(m, 1)
		args["staff_id"] = s.Res.OwnerID
		if got := ch.Call(t, caller, "list_commissions", args); got.Status != 200 || len(got.Body["entries"].([]any)) != 0 || got.Body["total"] != "0.00" {
			t.Fatal(got)
		}
		args = commissionRange(m, 2)
		all := ch.Call(t, s.MustCaller(s.Owner), "list_commissions", args)
		expected := append(append([]any{}, openAfter["entries"].([]any)...), before["entries"].([]any)...)
		if all.Status != 200 || all.Body["total"] != "390.00" || !reflect.DeepEqual(all.Body["entries"], expected) {
			t.Fatal(all, expected)
		}
		args["limit"] = 3
		page := ch.Call(t, s.MustCaller(s.Owner), "list_commissions", args)
		if page.Status != 200 || !reflect.DeepEqual(page.Body["entries"], expected[:3]) {
			t.Fatal(page)
		}
		args["before_id"] = expected[2].(map[string]any)["id"]
		page = ch.Call(t, s.MustCaller(s.Owner), "list_commissions", args)
		if page.Status != 200 || !reflect.DeepEqual(page.Body["entries"], expected[3:6]) || page.Body["total"] != "390.00" {
			t.Fatal(page)
		}
	}
}

func TestPayrollLockQueue(t *testing.T) {
	s, m := pastShop(t)
	set(t, s, s.Res.OwnerID, m, "base", "1")
	// 锁月本身也必须等待同一把咨询锁。
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	if err := dbgen.New(tx).LockPayroll(t.Context(), s.Res.StoreID); err != nil {
		dbtest.Rollback(t, tx)
		t.Fatal(err)
	}
	done := make(chan optest.Result, 1)
	go func() { done <- s.HTTPWithToken(t, s.Owner, "lock_payroll_month", map[string]any{"month": month(m)}) }()
	optest.AssertWaiting(t, s.Pool, done, 1)
	dbtest.Rollback(t, tx)
	optest.WaitResult(t, done, 200)
	// 用事务中的真实锁月处理器持锁，三个写操作各自排队，提交后才能检查锁月状态。
	for _, op := range []string{"set_base_salary", "add_payroll_adjustment", "delete_payroll_adjustment"} {
		m = m.AddDate(0, 1, 0)
		adjustment := add(t, s, s.Res.OwnerID, m, "待锁", "1")
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := payroll.LockPayrollMonthForTest(t.Context(), ops.Env{Now: time.Now(), Caller: s.MustCaller(s.Owner), Q: dbgen.New(tx)}, &payroll.MonthInput{Month: month(m)})
		if err != nil {
			dbtest.Rollback(t, tx)
			t.Fatal(err)
		}
		args := any(salaryArgs(s.Res.OwnerID, m, "base", "2"))
		detail := "设置底薪"
		if op == "add_payroll_adjustment" {
			args = adjustmentArgs(s.Res.OwnerID, m, "排队", "2")
			detail = "添加加减项"
		}
		if op == "delete_payroll_adjustment" {
			args = map[string]any{"id": adjustment["id"]}
			detail = "删除加减项"
		}
		done := make(chan optest.Result, 1)
		go func() { done <- s.HTTPWithToken(t, s.Owner, op, args) }()
		optest.AssertWaiting(t, s.Pool, done, 1)
		dbtest.Commit(t, tx)
		result := optest.WaitResult(t, done, 409)
		reject(t, result, 409, month(m)+" 已经锁定，不能再"+detail)
	}
}

func TestPayrollSettlementWithoutMatchingCommissions(t *testing.T) {
	s, m := pastShop(t)
	other, token := s.Hire("员工", "002", "emptysettlement")
	historicalStaff(t, s, other, m, nil)
	schemeAt(t, s, m, 1, "10")
	// 空结算月在后来出现业绩时也必须保持为空。
	s.Call(t, s.Owner, "lock_payroll_month", map[string]any{"month": month(m)}, 200)
	payPerformance(t, s, s.Res.OwnerID, m, false)
	payPerformance(t, s, other, m, false)
	// 下一月有老板的已结算明细，但没有该员工的已结算明细。
	next := m.AddDate(0, 1, 0)
	payPerformance(t, s, s.Res.OwnerID, next, false)
	s.Call(t, s.Owner, "lock_payroll_month", map[string]any{"month": month(next)}, 200)
	payPerformance(t, s, other, next, false)
	self := s.MustCaller(token)
	self.Permissions = map[ops.Permission]ops.Grant{perm.PerformanceView: {Scope: ops.ScopeSelf}}
	owner := s.MustCaller(s.Owner)
	for _, ch := range s.Channels() {
		for _, month := range []time.Time{m, next} {
			result := ch.Call(t, self, "list_commissions", commissionRange(month, 1))
			if result.Status != 200 || result.Body["total"] != "0.00" || len(result.Body["entries"].([]any)) != 0 {
				t.Fatal(ch.Name, result)
			}
		}
		empty := ch.Call(t, owner, "list_commissions", commissionRange(m, 1))
		if empty.Status != 200 || empty.Body["total"] != "0.00" || len(empty.Body["entries"].([]any)) != 0 {
			t.Fatal(ch.Name, empty)
		}
		stored := ch.Call(t, owner, "list_commissions", commissionRange(next, 1))
		if stored.Status != 200 || stored.Body["total"] != "40.00" || len(stored.Body["entries"].([]any)) != 4 {
			t.Fatal(ch.Name, stored)
		}
	}
}
