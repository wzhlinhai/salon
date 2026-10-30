package debts_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/debts"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

type fixture struct {
	*optest.Shop
	t        *testing.T
	customer uuid.UUID
}

func open(t *testing.T) *fixture {
	s := &fixture{Shop: optest.OpenShop(t), t: t}
	c := s.call("create_customer", map[string]any{"phone": "13800131234", "name": "欠款顾客"}, 200)
	s.customer = id(c["customer"].(map[string]any)["id"])
	return s
}
func id(v any) uuid.UUID { return uuid.MustParse(v.(string)) }
func (s *fixture) call(op string, in any, status int) map[string]any {
	s.t.Helper()
	return s.Call(s.t, s.Owner, op, in, status).Body
}
func (s *fixture) historical(amount string) map[string]any {
	s.t.Helper()
	return s.call("register_historical_debt", map[string]any{"customer_id": s.customer, "amount": amount, "incurred_on": "2020-01-02", "note": "迁移"}, 200)
}
func (s *fixture) snapshot() string {
	return dbtest.Snapshot(s.t, s.Pool, s.Res.TenantID, "debts", "debt_repayments", "audit_log")
}
func (s *fixture) refuse(c ops.Caller, op string, in any, status int, detail string) {
	s.t.Helper()
	before := s.snapshot()
	r := s.HTTP(s.t, &c, op, in)
	if r.Status != status || detail != "" && r.Body["detail"] != detail || s.snapshot() != before {
		s.t.Fatal(op, r)
	}
}
func repay(d map[string]any, amount string) map[string]any {
	return map[string]any{"debt_id": d["id"], "amount": amount, "method": "cash"}
}
func assertBalances(t *testing.T, d map[string]any, repaid, outstanding string) {
	t.Helper()
	if d["repaid"] != repaid || d["outstanding"] != outstanding {
		t.Fatal(d)
	}
}

func TestRepaymentRules(t *testing.T) {
	s := open(t)
	d := s.historical("100")
	c := s.MustCaller(s.Owner)
	if d["kind"] != "historical" || d["order_id"] != nil || d["business_date"] != "2020-01-02" || d["note"] != "迁移" || d["recorded_by"] != s.Res.OwnerID.String() {
		t.Fatal(d)
	}
	s.refuse(c, "repay_debt", repay(d, "100.01"), 409, "还款超过未还金额")
	s.refuse(c, "repay_debt", repay(d, "0"), 409, "还款金额须大于零")
	for _, method := range []string{"debt", "card", "count", "invalid"} {
		in := repay(d, "1")
		in["method"] = method
		s.refuse(c, "repay_debt", in, 422, "")
	}
	for _, amount := range []string{"-1", "0.001", "100000000"} {
		s.refuse(c, "repay_debt", repay(d, amount), 422, "")
	}
	d = s.call("repay_debt", repay(d, "30"), 200)
	assertBalances(t, d, "30.00", "70.00")
	s.refuse(c, "repay_debt", repay(d, "70.01"), 409, "还款超过未还金额")
	d = s.call("repay_debt", repay(d, "70"), 200)
	assertBalances(t, d, "100.00", "0.00")
	ps := d["repayments"].([]any)
	if len(ps) != 2 || ps[0].(map[string]any)["amount"] != "30.00" || ps[1].(map[string]any)["amount"] != "70.00" || ps[0].(map[string]any)["repaid_by"] != s.Res.OwnerID.String() {
		t.Fatal(ps)
	}
	s.refuse(c, "repay_debt", repay(d, "1"), 409, "这笔欠款已还清")
	for _, method := range []string{"unionpay", "wechat", "alipay"} {
		d = s.historical("1")
		in := repay(d, "1")
		in["method"] = method
		s.call("repay_debt", in, 200)
	}
}

func TestHistoricalRulesAndScope(t *testing.T) {
	s := open(t)
	c := s.MustCaller(s.Owner)
	in := map[string]any{"customer_id": s.customer, "amount": "10", "incurred_on": "2999-01-01"}
	s.refuse(c, "register_historical_debt", in, 409, "欠款日期不能晚于今天")
	in["incurred_on"] = "2020-01-02"
	in["amount"] = "0"
	s.refuse(c, "register_historical_debt", in, 409, "欠款金额须大于零")
	in["amount"] = "10"
	hidden := s.MustCaller(s.Owner)
	hidden.Permissions[perm.CustomerView] = ops.Grant{Scope: ops.ScopeSelf}
	s.refuse(hidden, "register_historical_debt", in, 404, "")
	d := s.historical("10")
	s.refuse(hidden, "repay_debt", repay(d, "1"), 404, "")
	c2 := s.call("create_customer", map[string]any{}, 200)["customer"].(map[string]any)
	in["customer_id"] = c2["id"]
	s.call("register_historical_debt", in, 200)
	s.call("merge_customers", map[string]any{"keep_id": s.customer, "merged_id": c2["id"]}, 200)
	s.refuse(c, "register_historical_debt", in, 409, "该顾客已合并到另一位顾客")
	for _, ch := range s.Channels() {
		r := ch.Call(t, c, "list_customer_debts", map[string]any{"customer_id": c2["id"]})
		if r.Status != 404 {
			t.Fatal(ch.Name, r)
		}
	}
}

func TestDebtPermissionsAndAI(t *testing.T) {
	s := open(t)
	d := s.historical("100")
	for _, tc := range []struct {
		op string
		in any
		p  ops.Permission
	}{
		{"repay_debt", repay(d, "1"), perm.PaymentCollect},
		{"register_historical_debt", map[string]any{"customer_id": s.customer, "amount": "1", "incurred_on": "2020-01-01"}, perm.DataImportExport},
		{"debt_summary", debts.DebtSummaryInput{}, perm.ReportView},
		{"list_customer_debts", debts.ListCustomerDebtsInput{CustomerID: s.customer}, perm.CustomerView},
	} {
		c := s.MustCaller(s.Owner)
		delete(c.Permissions, tc.p)
		for _, ch := range s.Channels() {
			before := s.snapshot()
			r := ch.Call(t, c, tc.op, tc.in)
			if r.Status != 403 || r.Detail() != "permission denied" || before != s.snapshot() {
				t.Fatal(ch.Name, tc.op, r)
			}
		}
		if tc.op == "repay_debt" || tc.op == "register_historical_debt" {
			before := s.snapshot()
			r := s.AI(t, s.MustCaller(s.Owner), tc.op, tc.in)
			if r.Status != 409 || r.Detail() != "operation requires a draft confirmed by a person" || before != s.snapshot() {
				t.Fatal(tc.op, r)
			}
		}
	}
}

func (s *fixture) otherStoreDebt() (uuid.UUID, uuid.UUID) {
	s.t.Helper()
	tx := dbtest.AppTx(s.t, s.Pool, s.Res.TenantID)
	store, err := dbgen.New(tx).CreateStore(context.Background(), dbgen.CreateStoreParams{TenantID: s.Res.TenantID, Name: "别店"})
	if err != nil {
		s.t.Fatal(err)
	}
	var debtID uuid.UUID
	if err := tx.QueryRow(context.Background(), "INSERT INTO debts(tenant_id,store_id,customer_id,amount,business_date,recorded_by) VALUES($1,$2,$3,99,'2020-01-01',$4) RETURNING id", s.Res.TenantID, store.ID, s.customer, s.Res.OwnerID).Scan(&debtID); err != nil {
		s.t.Fatal(err)
	}
	dbtest.Commit(s.t, tx)
	return store.ID, debtID
}

func TestRepayDebtStore(t *testing.T) {
	s := open(t)
	_, debtID := s.otherStoreDebt()
	before := s.snapshot()
	r := s.HTTPWithToken(t, s.Owner, "repay_debt", map[string]any{"debt_id": debtID, "amount": "1", "method": "cash"})
	after := s.snapshot()
	if r.Status != 404 {
		t.Error(r)
	}
	if before != after {
		t.Fatal("cross-store repayment changed debts, repayments or audit log")
	}
}

func TestDebtListAndSummary(t *testing.T) {
	s := open(t)
	a := s.historical("10")
	b := s.historical("20")
	s.call("repay_debt", repay(a, "10"), 200)
	s.call("repay_debt", repay(b, "5"), 200)
	// 同一顾客在别店有欠款，也不能被本店列表或汇总带出。
	storeID, _ := s.otherStoreDebt()
	for _, ch := range s.Channels() {
		c := s.MustCaller(s.Owner)
		for _, only := range []bool{false, true} {
			r := ch.Call(t, c, "list_customer_debts", debts.ListCustomerDebtsInput{CustomerID: s.customer, OutstandingOnly: only})
			if r.Status != 200 {
				t.Fatal(r)
			}
			items := r.Body["items"].([]any)
			want := 2
			amount := "30.00"
			repaid := "15.00"
			if only {
				want = 1
				amount = "20.00"
				repaid = "5.00"
			}
			tot := r.Body["totals"].(map[string]any)
			if len(items) != want || items[0].(map[string]any)["id"] != b["id"] || tot["amount"] != amount || tot["repaid"] != repaid || tot["outstanding"] != "15.00" {
				t.Fatal(ch.Name, r)
			}
			for _, key := range []string{"customer", "phone", "name"} {
				if _, ok := items[0].(map[string]any)[key]; ok {
					t.Fatal(key, r)
				}
			}
		}
		r := ch.Call(t, c, "debt_summary", debts.DebtSummaryInput{})
		if r.Status != 200 || r.Body["amount"] != "30.00" || r.Body["repaid"] != "15.00" || r.Body["outstanding"] != "15.00" || r.Body["outstanding_count"] != float64(1) || r.Body["order"].(map[string]any)["amount"] != "0.00" || r.Body["historical"].(map[string]any)["amount"] != "30.00" {
			t.Fatal(ch.Name, r)
		}
		c.Permissions[perm.CustomerView] = ops.Grant{Scope: ops.ScopeSelf}
		r = ch.Call(t, c, "list_customer_debts", debts.ListCustomerDebtsInput{CustomerID: s.customer})
		if r.Status != 404 {
			t.Fatal(ch.Name, r)
		}
		// ReportView 汇总没有顾客级数据，不依赖顾客查看权限。
		delete(c.Permissions, perm.CustomerView)
		r = ch.Call(t, c, "debt_summary", debts.DebtSummaryInput{})
		if r.Status != 200 || r.Body["amount"] != "30.00" {
			t.Fatal(r)
		}
		c.Permissions[perm.CustomerView] = ops.Grant{}
		c.StoreID = storeID
		r = ch.Call(t, c, "list_customer_debts", debts.ListCustomerDebtsInput{CustomerID: s.customer})
		if r.Status != 404 {
			t.Fatal(r)
		}
	}
}

func TestDebtAudit(t *testing.T) {
	s := open(t)
	d := s.historical("10")
	s.call("repay_debt", repay(d, "3"), 200)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var count int
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM audit_log WHERE target_kind=$1 AND target_id=$2 AND customer_id=$3", "debt", d["id"], s.customer).Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}

	for _, ch := range s.Channels() {
		c := s.MustCaller(s.Owner)
		delete(c.Permissions, perm.CustomerFullPhone)
		r := ch.Call(t, c, "list_audit_log", map[string]any{"target_id": d["id"]})
		if r.Status != 200 {
			t.Fatal(r)
		}
		found := map[string]bool{}
		for _, raw := range r.Body["entries"].([]any) {
			e := raw.(map[string]any)
			action := e["action"].(string)
			found[action] = true
			if e["target_kind"] != "debt" || e["target_id"] != d["id"] {
				t.Fatal(e)
			}
			if action == "repay_debt" {
				assertBalances(t, e["before"].(map[string]any), "0.00", "10.00")
				assertBalances(t, e["after"].(map[string]any), "3.00", "7.00")
			}
			if action == "register_historical_debt" && (e["before"] != nil || e["after"].(map[string]any)["id"] != d["id"]) {
				t.Fatal(e)
			}
		}
		if !found["repay_debt"] || !found["register_historical_debt"] {
			t.Fatal(found)
		}
		c.Permissions[perm.CustomerView] = ops.Grant{Scope: ops.ScopeSelf}
		r = ch.Call(t, c, "list_audit_log", map[string]any{"target_id": d["id"]})
		if r.Status != 200 || len(r.Body["entries"].([]any)) != 0 {
			t.Fatal(r)
		}
	}
}

func TestRepaymentConcurrency(t *testing.T) {
	s := open(t)
	d := s.historical("100")
	c := s.MustCaller(s.Owner)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	env := ops.Env{Caller: c, Q: dbgen.New(tx)}
	var input debts.RepayDebtInput
	input.DebtID = id(d["id"])
	input.Method = "cash"
	if err := input.Amount.Scan("60"); err != nil {
		t.Fatal(err)
	}
	first, err := debts.RepayDebtAtForTest(context.Background(), env, &input, time.Now())
	if err != nil || first.Repaid.String() != "60.00" {
		t.Fatal(first, err)
	}
	done := make(chan optest.Result, 1)
	go func() { done <- s.HTTP(t, &c, "repay_debt", repay(d, "60")) }()
	optest.AssertWaiting(t, done)
	dbtest.Commit(t, tx)
	r := optest.WaitResult(t, done, 409)
	if r.Body["detail"] != "还款超过未还金额" {
		t.Fatal(r)
	}
	got := s.call("list_customer_debts", debts.ListCustomerDebtsInput{CustomerID: s.customer}, 200)["items"].([]any)[0].(map[string]any)
	assertBalances(t, got, "60.00", "40.00")
}

func TestDebtBusinessDate(t *testing.T) {
	s := open(t)
	d := s.historical("100")
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, `INSERT INTO setting_changes(tenant_id,store_id,key,value,is_default,old_value,old_is_default,effective_from,changed_by) VALUES($1,$2,'business_day_end','"02:00"',false,'"00:00"',true,'2020-01-01',$3)`, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID)
	dbtest.Commit(t, tx)
	now := time.Date(2026, 5, 10, 1, 30, 0, 0, settings.Location)
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	env := ops.Env{Caller: s.MustCaller(s.Owner), Q: dbgen.New(tx)}
	in := debts.RepayDebtInput{DebtID: id(d["id"]), Method: "cash"}
	if err := in.Amount.Scan("1"); err != nil {
		t.Fatal(err)
	}
	out, err := debts.RepayDebtAtForTest(context.Background(), env, &in, now)
	if err != nil || out.Repayments[0].BusinessDate != "2026-05-09" {
		t.Fatal(out, err)
	}
	hist := debts.RegisterHistoricalDebtInput{CustomerID: s.customer, Amount: in.Amount, IncurredOn: "2026-05-10"}
	_, err = debts.RegisterHistoricalDebtAtForTest(context.Background(), env, &hist, now)
	if err == nil || !strings.Contains(err.Error(), "欠款日期不能晚于今天") {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
}

func TestHistoricalReferenceLock(t *testing.T) {
	s := open(t)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "SELECT id FROM customers WHERE id=$1 FOR NO KEY UPDATE", s.customer)
	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "register_historical_debt", map[string]any{"customer_id": s.customer, "amount": "10", "incurred_on": "2020-01-01"})
	}()
	optest.AssertWaiting(t, done)
	dbtest.Rollback(t, tx)
	optest.WaitResult(t, done, 200)
}

func TestDebtLargeTotals(t *testing.T) {
	s := open(t)
	for range 2 {
		s.historical("99999999.99")
	}
	for _, ch := range s.Channels() {
		c := s.MustCaller(s.Owner)
		listed := ch.Call(t, c, "list_customer_debts", debts.ListCustomerDebtsInput{CustomerID: s.customer})
		if listed.Status != 200 || listed.Body["totals"].(map[string]any)["amount"] != "199999999.98" {
			t.Fatal(listed)
		}
		summary := ch.Call(t, c, "debt_summary", debts.DebtSummaryInput{})
		if summary.Status != 200 || summary.Body["amount"] != "199999999.98" {
			t.Fatal(summary)
		}
	}
}
