package payroll_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/payroll"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func reject(t *testing.T, r optest.Result, status int, detail string) {
	t.Helper()
	if r.Status != status || !strings.Contains(r.Detail(), detail) {
		t.Fatalf("got %d %s %v, want %d %s", r.Status, r.Detail(), r.Body, status, detail)
	}
}

func openingMonth(t *testing.T, s *optest.Shop) time.Time {
	t.Helper()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	q := dbgen.New(tx)
	store, err := q.GetStore(t.Context(), s.Res.StoreID)
	if err != nil {
		t.Fatal(err)
	}
	day, err := settings.BusinessDate(t.Context(), q, s.Res.StoreID, store.CreatedAt.Time)
	dbtest.Rollback(t, tx)
	if err != nil {
		t.Fatal(err)
	}
	return time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, time.UTC)
}
func month(m time.Time) string { return m.Format("2006-01") }
func salaryArgs(id uuid.UUID, m time.Time, mode, amount string) map[string]any {
	return map[string]any{"staff_id": id, "month": month(m), "mode": mode, "amount": amount}
}
func adjustmentArgs(id uuid.UUID, m time.Time, name, amount string) map[string]any {
	return map[string]any{"staff_id": id, "month": month(m), "name": name, "amount": amount, "note": "备注"}
}
func set(t *testing.T, s *optest.Shop, id uuid.UUID, m time.Time, mode, amount string) map[string]any {
	t.Helper()
	return s.Call(t, s.Owner, "set_base_salary", salaryArgs(id, m, mode, amount), 200).Body
}
func add(t *testing.T, s *optest.Shop, id uuid.UUID, m time.Time, name, amount string) map[string]any {
	t.Helper()
	return s.Call(t, s.Owner, "add_payroll_adjustment", adjustmentArgs(id, m, name, amount), 200).Body
}
func entries(t *testing.T, r optest.Result) []any {
	t.Helper()
	if r.Status != 200 {
		t.Fatal(r)
	}
	return r.Body["entries"].([]any)
}
func auditEntry(t *testing.T, s *optest.Shop, kind, action string, id uuid.UUID, before, after any) {
	t.Helper()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var old, newValue []byte
	err := tx.QueryRow(t.Context(), "SELECT before,after FROM audit_log WHERE target_kind=$1 AND action=$2 AND target_id=$3 ORDER BY at DESC,id DESC LIMIT 1", kind, action, id).Scan(&old, &newValue)
	dbtest.Rollback(t, tx)
	if err != nil {
		t.Fatal(err)
	}
	for i, raw := range [][]byte{old, newValue} {
		var got any
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
		}
		if !reflect.DeepEqual(got, []any{before, after}[i]) {
			t.Fatalf("audit %s %s: %v want %v", kind, action, got, []any{before, after}[i])
		}
	}
}

func TestBaseSalaryHistory(t *testing.T) {
	s := optest.OpenShop(t)
	m := openingMonth(t, s).AddDate(0, 1, 0)
	first := set(t, s, s.Res.OwnerID, m, "base", "1000")
	auditEntry(t, s, "base_salary", "set_base_salary", s.Res.OwnerID, nil, first)
	for _, ch := range s.Channels() {
		for _, d := range []time.Time{m, m.AddDate(0, 3, 0)} {
			rows := entries(t, ch.Call(t, s.MustCaller(s.Owner), "list_base_salaries", map[string]any{"month": month(d)}))
			if len(rows) != 1 || !reflect.DeepEqual(rows[0].(map[string]any)["base_salary"], first) {
				t.Fatal(rows, first)
			}
		}
		rows := entries(t, ch.Call(t, s.MustCaller(s.Owner), "list_base_salaries", map[string]any{"month": month(m.AddDate(0, -1, 0))}))
		if rows[0].(map[string]any)["base_salary"] != nil {
			t.Fatal(rows)
		}
	}
	second := set(t, s, s.Res.OwnerID, m.AddDate(0, 1, 0), "guarantee", "2000")
	auditEntry(t, s, "base_salary", "set_base_salary", s.Res.OwnerID, first, second)
	third := set(t, s, s.Res.OwnerID, m.AddDate(0, 1, 0), "base", "3000")
	auditEntry(t, s, "base_salary", "set_base_salary", s.Res.OwnerID, second, third)
	// 回填更早生效的设置，保存时刻更新也不能覆盖较晚生效月。
	set(t, s, s.Res.OwnerID, m, "base", "1500")
	for _, ch := range s.Channels() {
		caller := s.MustCaller(s.Owner)
		early := entries(t, ch.Call(t, caller, "list_base_salaries", map[string]any{"month": month(m)}))
		if early[0].(map[string]any)["base_salary"].(map[string]any)["amount"] != "1500.00" {
			t.Fatal(early)
		}
		late := ch.Call(t, caller, "list_base_salaries", map[string]any{"month": month(m.AddDate(0, 1, 0))})
		rows := entries(t, late)
		if !reflect.DeepEqual(rows[0].(map[string]any)["base_salary"], third) {
			t.Fatal(rows, third)
		}
		a, _ := json.Marshal(first)
		b, _ := json.Marshal(late.Body)
		t.Logf("set_base_salary response: %s\nlist_base_salaries response: %s", a, b)
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var n int
	err := tx.QueryRow(t.Context(), "SELECT count(*) FROM staff_base_salaries").Scan(&n)
	dbtest.Rollback(t, tx)
	if err != nil || n != 4 {
		t.Fatal(n, err)
	}
}

func TestPayrollValidation(t *testing.T) {
	s := optest.OpenShop(t)
	m := openingMonth(t, s)
	for _, op := range []string{"set_base_salary", "add_payroll_adjustment", "list_base_salaries", "list_payroll_adjustments"} {
		args := salaryArgs(s.Res.OwnerID, m.AddDate(0, -1, 0), "base", "1")
		if strings.HasPrefix(op, "list_") {
			args = map[string]any{"month": month(m.AddDate(0, -1, 0))}
		}
		if op == "add_payroll_adjustment" {
			args = adjustmentArgs(s.Res.OwnerID, m.AddDate(0, -1, 0), "项目", "1")
		}
		reject(t, s.HTTPWithToken(t, s.Owner, op, args), 409, "结算月不能早于开店的月份（"+month(m)+"）")
		for _, bad := range []string{"2026-1", "2026-00", "2026-13", "2026-01-01", "garbage"} {
			args["month"] = bad
			reject(t, s.HTTPWithToken(t, s.Owner, op, args), 422, "validation failed")
		}
	}
	for _, limit := range []int{-1, 201} {
		reject(t, s.HTTPWithToken(t, s.Owner, "list_payroll_adjustments", map[string]any{"month": month(m), "limit": limit}), 422, "validation failed")
	}
	args := salaryArgs(s.Res.OwnerID, m, "unknown", "1")
	reject(t, s.HTTPWithToken(t, s.Owner, "set_base_salary", args), 422, "validation failed")
	args["mode"] = "base"
	args["amount"] = "-1"
	reject(t, s.HTTPWithToken(t, s.Owner, "set_base_salary", args), 422, "validation failed")
	for _, tc := range []struct {
		name, amount, note string
		status             int
		detail             string
	}{
		{"", "1", "", 422, "validation failed"},
		{"项目", "0", "", 409, "加减项金额不能为 0"},
		{" \t ", "1", "", 409, "加减项名称不能为空"},
		{strings.Repeat("名", 21), "1", "", 422, "validation failed"},
		{"项目", "1", strings.Repeat("注", 101), 422, "validation failed"},
	} {
		args := adjustmentArgs(s.Res.OwnerID, m, tc.name, tc.amount)
		args["note"] = tc.note
		reject(t, s.HTTPWithToken(t, s.Owner, "add_payroll_adjustment", args), tc.status, tc.detail)
	}
}

func TestPayrollReferencesAndStoreIsolation(t *testing.T) {
	s := optest.OpenShop(t)
	m := openingMonth(t, s)
	id, _ := s.Hire("已离职", "000", "left")
	s.Call(t, s.Owner, "deactivate_staff", map[string]any{"staff_id": id}, 200)
	set(t, s, id, m, "guarantee", "0")
	own := add(t, s, id, m, " 加项 ", "12.34")
	if own["name"] != "加项" {
		t.Fatal(own)
	}
	otherStore := uuid.New()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "INSERT INTO stores(id,tenant_id,name) VALUES($1,$2,'分店')", otherStore, s.Res.TenantID)
	dbtest.Commit(t, tx)
	other := s.MustCaller(s.Owner)
	other.StoreID = otherStore
	app := s.App
	// 同一租户另一门店的员工，避免 RLS 掩盖门店检查。
	r := app.HTTP(t, &other, "create_staff", map[string]any{"name": "分店员工", "number": "002", "role_ids": []uuid.UUID{}})
	if r.Status != 200 {
		t.Fatal(r)
	}
	foreignID := uuid.MustParse(r.Body["id"].(string))
	reject(t, s.HTTPWithToken(t, s.Owner, "set_base_salary", salaryArgs(foreignID, m, "base", "1")), 404, "not found")
	reject(t, s.HTTPWithToken(t, s.Owner, "add_payroll_adjustment", adjustmentArgs(foreignID, m, "项目", "1")), 404, "not found")
	for op, args := range map[string]any{"set_base_salary": salaryArgs(foreignID, m, "base", "9"), "add_payroll_adjustment": adjustmentArgs(foreignID, m, "外店", "9")} {
		r := app.HTTP(t, &other, op, args)
		if r.Status != 200 {
			t.Fatal(r)
		}
		if op == "add_payroll_adjustment" {
			for _, ch := range s.Channels() {
				page := entries(t, ch.Call(t, s.MustCaller(s.Owner), "list_payroll_adjustments", map[string]any{"month": month(m), "before_id": r.Body["id"]}))
				if len(page) != 0 {
					t.Fatal("foreign cursor returned rows", page)
				}
			}
			reject(t, s.HTTPWithToken(t, s.Owner, "delete_payroll_adjustment", map[string]any{"id": r.Body["id"]}), 404, "加减项不存在")
		}
	}
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	inForce, err := payroll.BaseSalariesInForce(t.Context(), dbgen.New(tx), s.Res.StoreID, m)
	dbtest.Rollback(t, tx)
	if err != nil || len(inForce) != 1 {
		t.Fatal("in-force helper crossed store", inForce, err)
	}
	for _, ch := range s.Channels() {
		rows := entries(t, ch.Call(t, s.MustCaller(s.Owner), "list_base_salaries", map[string]any{"month": month(m)}))
		if len(rows) != 2 || rows[0].(map[string]any)["staff"].(map[string]any)["id"] != id.String() || rows[0].(map[string]any)["active"] != false {
			t.Fatal(rows)
		}
		rows = entries(t, ch.Call(t, s.MustCaller(s.Owner), "list_payroll_adjustments", map[string]any{"month": month(m)}))
		if len(rows) != 1 || !reflect.DeepEqual(rows[0], own) {
			t.Fatal(rows)
		}
	}
}

func TestPayrollAdjustments(t *testing.T) {
	s := optest.OpenShop(t)
	m := openingMonth(t, s)
	id, _ := s.Hire("员工", "002", "worker")
	first := add(t, s, s.Res.OwnerID, m, "奖励", "100")
	auditEntry(t, s, "payroll_adjustment", "add_payroll_adjustment", uuid.MustParse(first["id"].(string)), nil, first)
	second := add(t, s, id, m, "减项", "-20")
	third := add(t, s, s.Res.OwnerID, m, "加项", "5")
	add(t, s, id, m.AddDate(0, 1, 0), "次月", "1")
	// 同一创建时刻仍以 ID 倒序稳定分页。
	if _, err := s.Pool.Exec(t.Context(), "UPDATE payroll_adjustments SET created_at='2099-01-01' WHERE month=$1", m); err != nil {
		t.Fatal(err)
	}
	for _, ch := range s.Channels() {
		c := s.MustCaller(s.Owner)
		rows := entries(t, ch.Call(t, c, "list_payroll_adjustments", map[string]any{"month": month(m), "limit": 2}))
		if len(rows) != 2 || rows[0].(map[string]any)["id"] != third["id"] || rows[1].(map[string]any)["id"] != second["id"] {
			t.Fatal(rows)
		}
		tail := entries(t, ch.Call(t, c, "list_payroll_adjustments", map[string]any{"month": month(m), "before_id": second["id"]}))
		if len(tail) != 1 || tail[0].(map[string]any)["id"] != first["id"] {
			t.Fatal(tail)
		}
		filtered := entries(t, ch.Call(t, c, "list_payroll_adjustments", map[string]any{"month": month(m), "staff_id": id}))
		if len(filtered) != 1 || filtered[0].(map[string]any)["amount"] != "-20.00" {
			t.Fatal(filtered)
		}
		reject(t, ch.Call(t, c, "list_payroll_adjustments", map[string]any{"month": month(m.AddDate(0, -1, 0))}), 409, "结算月不能早于开店的月份")
	}
	// 使用未改创建时间的次月项验证删除前日志完整视图。
	deleted := add(t, s, id, m, "删除项", "-1")
	removed := s.Call(t, s.Owner, "delete_payroll_adjustment", map[string]any{"id": deleted["id"]}, 200)
	if removed.Body["ok"] != true {
		t.Fatal(removed)
	}
	auditEntry(t, s, "payroll_adjustment", "delete_payroll_adjustment", uuid.MustParse(deleted["id"].(string)), deleted, nil)
	for _, missing := range []any{deleted["id"], uuid.New()} {
		reject(t, s.HTTPWithToken(t, s.Owner, "delete_payroll_adjustment", map[string]any{"id": missing}), 404, "加减项不存在")
	}
}

func TestPayrollPermissionsAndAI(t *testing.T) {
	s := optest.OpenShop(t)
	m := openingMonth(t, s)
	adjustment := add(t, s, s.Res.OwnerID, m, "已有", "1")
	set(t, s, s.Res.OwnerID, m, "base", "1")
	inputs := map[string]any{
		"set_base_salary":           salaryArgs(s.Res.OwnerID, m, "base", "9"),
		"add_payroll_adjustment":    adjustmentArgs(s.Res.OwnerID, m, "加项", "9"),
		"delete_payroll_adjustment": map[string]any{"id": adjustment["id"]},
		"list_base_salaries":        map[string]any{"month": month(m)},
		"list_payroll_adjustments":  map[string]any{"month": month(m)},
	}
	caller := s.MustCaller(s.Owner)
	before := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "staff_base_salaries", "payroll_adjustments", "audit_log")
	for _, op := range []string{"set_base_salary", "add_payroll_adjustment", "delete_payroll_adjustment"} {
		r := s.AI(t, caller, op, inputs[op])
		if !errors.Is(r.Err, ops.ErrDraftRequired) {
			t.Fatal(r)
		}
	}
	if got := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "staff_base_salaries", "payroll_adjustments", "audit_log"); got != before {
		t.Fatal("AI changed data")
	}
	for _, ch := range s.Channels() {
		rows := entries(t, ch.Call(t, caller, "list_audit_log", map[string]any{}))
		seen := map[string]bool{}
		for _, row := range rows {
			seen[row.(map[string]any)["target_kind"].(string)] = true
		}
		if !seen["base_salary"] || !seen["payroll_adjustment"] {
			t.Fatal("authorized payroll audit missing", rows)
		}
	}
	caller.Permissions = map[ops.Permission]ops.Grant{}
	for _, ch := range s.Channels() {
		for op, args := range inputs {
			reject(t, ch.Call(t, caller, op, args), 403, "permission denied")
		}
		caller.Permissions[perm.AuditLogView] = ops.Grant{}
		rows := entries(t, ch.Call(t, caller, "list_audit_log", map[string]any{}))
		for _, row := range rows {
			kind := row.(map[string]any)["target_kind"]
			if kind == "base_salary" || kind == "payroll_adjustment" {
				t.Fatal("payroll audit leaked", rows)
			}
		}
		delete(caller.Permissions, perm.AuditLogView)
	}
}

func TestPayrollLocks(t *testing.T) {
	s := optest.OpenShop(t)
	m := openingMonth(t, s)
	adjustment := add(t, s, s.Res.OwnerID, m, "已有", "1")
	for _, tc := range []struct {
		op   string
		args any
	}{
		{"set_base_salary", salaryArgs(s.Res.OwnerID, m, "base", "1")},
		{"add_payroll_adjustment", adjustmentArgs(s.Res.OwnerID, m, "新增", "1")},
		{"delete_payroll_adjustment", map[string]any{"id": adjustment["id"]}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			if err := dbgen.New(tx).LockPayroll(context.Background(), s.Res.StoreID); err != nil {
				t.Fatal(err)
			}
			done := make(chan optest.Result, 1)
			go func() { done <- s.HTTPWithToken(t, s.Owner, tc.op, tc.args) }()
			optest.AssertWaiting(t, done)
			dbtest.Rollback(t, tx)
			optest.WaitResult(t, done, 200)
		})
	}
}

func TestOpeningMonthUsesBusinessDate(t *testing.T) {
	s := optest.OpenShop(t)
	if _, err := s.Pool.Exec(t.Context(), "UPDATE stores SET created_at='2026-02-01 01:00:00+08' WHERE id=$1", s.Res.StoreID); err != nil {
		t.Fatal(err)
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, `INSERT INTO setting_changes(tenant_id,store_id,key,value,is_default,old_value,old_is_default,effective_from,changed_by) VALUES($1,$2,'business_day_end','"02:00"',false,'"00:00"',true,'2026-01-01',$3)`, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID)
	dbtest.Commit(t, tx)
	for _, ch := range s.Channels() {
		rows := entries(t, ch.Call(t, s.MustCaller(s.Owner), "list_base_salaries", map[string]any{"month": "2026-01"}))
		if len(rows) != 1 {
			t.Fatal(rows)
		}
		reject(t, ch.Call(t, s.MustCaller(s.Owner), "list_base_salaries", map[string]any{"month": "2025-12"}), 409, "结算月不能早于开店的月份（2026-01）")
	}
}

func TestBaseSalarySavedAtOrder(t *testing.T) {
	s := optest.OpenShop(t)
	m := openingMonth(t, s)
	set(t, s, s.Res.OwnerID, m, "base", "10")
	set(t, s, s.Res.OwnerID, m, "guarantee", "20")
	if _, err := s.Pool.Exec(t.Context(), "UPDATE staff_base_salaries SET saved_at='2099-01-01' WHERE amount=10"); err != nil {
		t.Fatal(err)
	}
	for _, ch := range s.Channels() {
		rows := entries(t, ch.Call(t, s.MustCaller(s.Owner), "list_base_salaries", map[string]any{"month": month(m)}))
		if rows[0].(map[string]any)["base_salary"].(map[string]any)["amount"] != "10.00" {
			t.Fatal(rows)
		}
	}
	if _, err := s.Pool.Exec(t.Context(), "UPDATE staff_base_salaries SET saved_at='2099-01-01'"); err != nil {
		t.Fatal(err)
	}
	for _, ch := range s.Channels() {
		rows := entries(t, ch.Call(t, s.MustCaller(s.Owner), "list_base_salaries", map[string]any{"month": month(m)}))
		if rows[0].(map[string]any)["base_salary"].(map[string]any)["amount"] != "20.00" {
			t.Fatal(rows)
		}
	}
}

func TestPayrollAdjustmentCreatedAtOrder(t *testing.T) {
	s := optest.OpenShop(t)
	m := openingMonth(t, s)
	first := add(t, s, s.Res.OwnerID, m, "先建后排序", "1")
	second := add(t, s, s.Res.OwnerID, m, "后建", "2")
	if _, err := s.Pool.Exec(t.Context(), "UPDATE payroll_adjustments SET created_at='2099-01-01' WHERE id=$1", first["id"]); err != nil {
		t.Fatal(err)
	}
	for _, ch := range s.Channels() {
		rows := entries(t, ch.Call(t, s.MustCaller(s.Owner), "list_payroll_adjustments", map[string]any{"month": month(m)}))
		if len(rows) != 2 || rows[0].(map[string]any)["id"] != first["id"] || rows[1].(map[string]any)["id"] != second["id"] {
			t.Fatal(rows)
		}
	}
}

func TestPayrollManageDoesNotRequireStaffAuthority(t *testing.T) {
	s := optest.OpenShop(t)
	m := openingMonth(t, s)
	_, token := s.Hire("结算员", "002", "payroll")
	caller := s.MustCaller(token)
	caller.Permissions = map[ops.Permission]ops.Grant{perm.PayrollManage: {}}
	salary := s.HTTP(t, &caller, "set_base_salary", salaryArgs(s.Res.OwnerID, m, "base", "0"))
	if salary.Status != 200 || salary.Body["saved_by"].(map[string]any)["id"] != caller.StaffID.String() {
		t.Fatal(salary)
	}
	adjustment := s.HTTP(t, &caller, "add_payroll_adjustment", adjustmentArgs(s.Res.OwnerID, m, "结算", "1"))
	if adjustment.Status != 200 || adjustment.Body["created_by"].(map[string]any)["id"] != caller.StaffID.String() {
		t.Fatal(adjustment)
	}
	r := s.HTTP(t, &caller, "delete_payroll_adjustment", map[string]any{"id": adjustment.Body["id"]})
	if r.Status != 200 {
		t.Fatal(r)
	}
}
