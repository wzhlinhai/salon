package cards_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

type fixture struct {
	*optest.Shop
	t                  *testing.T
	customer, cardType string
}

func open(t *testing.T) *fixture {
	s := &fixture{Shop: optest.OpenShop(t), t: t}
	s.customer = s.call("create_customer", map[string]any{"name": "顾客"}, 200)["customer"].(map[string]any)["id"].(string)
	s.cardType = s.call("create_card_type", typeInput("储值"), 200)["id"].(string)
	return s
}
func typeInput(name string) map[string]any {
	return map[string]any{"name": name, "kind": "stored_value", "validity_kind": "none"}
}
func (s *fixture) call(op string, in any, status int) map[string]any {
	s.t.Helper()
	r := s.HTTPWithToken(s.t, s.Owner, op, in)
	if r.Status != status {
		s.t.Fatalf("%s got %d %v want %d", op, r.Status, r.Body, status)
	}
	return r.Body
}
func (s *fixture) opening() map[string]any {
	return map[string]any{"customer_id": s.customer, "card_type_id": s.cardType, "principal": "1234.56", "bonus": "0.50", "sales_staff_ids": []any{s.Res.OwnerID}}
}
func (s *fixture) recharge(id any) map[string]any {
	return map[string]any{"card_id": id, "principal": "0.50", "bonus": "1234.56", "sales_staff_ids": []any{s.Res.OwnerID}}
}
func exec(t *testing.T, tx pgx.Tx, sql string, args ...any) {
	t.Helper()
	if _, err := tx.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}
func commit(t *testing.T, tx pgx.Tx) {
	t.Helper()
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestLedgerChain(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	if card["principal"] != "1234.56" || card["bonus"] != "0.50" {
		t.Fatal(card)
	}
	for range 3 {
		s.call("recharge_card", s.recharge(card["id"]), 200)
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var valid bool
	err := tx.QueryRow(context.Background(), `WITH chain AS (SELECT *,lag(principal_after,1,0::numeric) OVER w p,lag(bonus_after,1,0::numeric) OVER w b FROM card_ledger WHERE card_id=$1 WINDOW w AS (ORDER BY at,id)) SELECT count(*)=4 AND bool_and(principal_before=p AND bonus_before=b AND principal_after=principal_before+principal_delta AND bonus_after=bonus_before+bonus_delta) AND sum(principal_delta)=(array_agg(principal_after ORDER BY at DESC,id DESC))[1] AND sum(bonus_delta)=(array_agg(bonus_after ORDER BY at DESC,id DESC))[1] FROM chain`, card["id"]).Scan(&valid)
	if err != nil || !valid {
		t.Fatal(valid, err)
	}
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "list_customer_cards", map[string]any{"customer_id": s.customer})
		if r.Status != 200 {
			t.Fatal(r)
		}
		item := r.Body["items"].([]any)[0].(map[string]any)
		if item["principal"] != "1236.06" || item["bonus"] != "3704.18" {
			t.Fatal(item)
		}
		r = ch.Call(t, s.MustCaller(s.Owner), "list_card_ledger", map[string]any{"card_id": card["id"], "limit": 2})
		if r.Status != 200 {
			t.Fatal(r)
		}
		rows := r.Body["entries"].([]any)
		if len(rows) != 2 {
			t.Fatal(r)
		}
		entry := rows[0].(map[string]any)
		if entry["principal_delta"] != "0.50" || entry["bonus_delta"] != "1234.56" || entry["actor"].(map[string]any)["name"] != "王老板" || len(entry["sales_staff"].([]any)) != 1 {
			t.Fatal(entry)
		}
		r = ch.Call(t, s.MustCaller(s.Owner), "list_card_ledger", map[string]any{"card_id": card["id"], "before_id": rows[1].(map[string]any)["id"], "limit": 2})
		if r.Status != 200 || len(r.Body["entries"].([]any)) != 2 {
			t.Fatal(r)
		}
		r = ch.Call(t, s.MustCaller(s.Owner), "list_card_types", map[string]any{})
		if r.Status != 200 || len(r.Body["items"].([]any)) != 1 {
			t.Fatal(r)
		}
	}
}
func TestInactiveCardType(t *testing.T) {
	s := open(t)
	in := typeInput("储值")
	in["id"] = s.cardType
	in["active"] = false
	s.call("update_card_type", in, 200)
	s.call("open_card", s.opening(), 409)
}
func TestSalesStaffRules(t *testing.T) {
	for _, mode := range []string{"inactive", "unknown", "empty", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			s := open(t)
			card := s.call("open_card", s.opening(), 200)
			id, _ := s.Hire("员工", "002", "staff")
			ids := []any{id}
			want := 409
			switch mode {
			case "inactive":
				tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
				exec(t, tx, "UPDATE staff SET active=false,deactivated_at=clock_timestamp() WHERE id=$1", id)
				commit(t, tx)
			case "unknown":
				ids = []any{uuid.New()}
			case "empty":
				ids = []any{}
				want = 422
			case "duplicate":
				ids = []any{id, id}
				want = 422
			}
			for _, op := range []string{"open_card", "recharge_card"} {
				in := s.opening()
				if op == "recharge_card" {
					in = s.recharge(card["id"])
				}
				in["sales_staff_ids"] = ids
				s.call(op, in, want)
			}
		})
	}
}
func TestMergedCustomer(t *testing.T) {
	s := open(t)
	keep := s.call("create_customer", map[string]any{}, 200)["customer"].(map[string]any)["id"]
	s.call("merge_customers", map[string]any{"keep_id": keep, "merged_id": s.customer}, 200)
	s.call("open_card", s.opening(), 409)
}
func TestZeroRecharge(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	in := s.recharge(card["id"])
	in["principal"] = "0.00"
	in["bonus"] = "0.00"
	s.call("recharge_card", in, 409)
}
func TestValidityRules(t *testing.T) {
	s := open(t)
	for _, in := range []map[string]any{
		{"validity_kind": "none", "validity_days": 1}, {"validity_kind": "fixed_date"}, {"validity_kind": "fixed_date", "validity_date": "bad"}, {"validity_kind": "days_after_open", "validity_days": 0}, {"validity_kind": "days_after_open", "validity_days": -1}, {"validity_kind": "days_after_open", "validity_days": 1, "validity_date": "2030-01-01"},
	} {
		in["name"] = "无效"
		in["kind"] = "stored_value"
		s.call("create_card_type", in, 409)
		in["id"] = s.cardType
		in["active"] = true
		s.call("update_card_type", in, 409)
	}
	in := typeInput("固定")
	in["validity_kind"] = "fixed_date"
	in["validity_date"] = "2030-05-06"
	ct := s.call("create_card_type", in, 200)
	opening := s.opening()
	opening["card_type_id"] = ct["id"]
	card := s.call("open_card", opening, 200)
	if card["expires_on"] != "2030-05-06" {
		t.Fatal(card)
	}
}
func TestPermissions(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	for _, tc := range []struct {
		op string
		in any
		p  ops.Permission
	}{{"open_card", s.opening(), perm.CardSell}, {"recharge_card", s.recharge(card["id"]), perm.CardSell}, {"create_card_type", typeInput("拒绝"), perm.SettingsManage}, {"update_card_type", map[string]any{"id": s.cardType, "name": "储值", "kind": "stored_value", "validity_kind": "none", "active": true}, perm.SettingsManage}, {"list_customer_cards", map[string]any{"customer_id": s.customer}, perm.CustomerView}, {"list_card_ledger", map[string]any{"card_id": card["id"]}, perm.CustomerView}} {
		caller := s.MustCaller(s.Owner)
		caller.Permissions = perm.All()
		delete(caller.Permissions, tc.p)
		r := s.HTTP(t, &caller, tc.op, tc.in)
		if r.Status != 403 {
			t.Fatalf("%s %v", tc.op, r)
		}
		if tc.p == perm.CustomerView {
			for _, ch := range s.Channels() {
				if r := ch.Call(t, caller, tc.op, tc.in); r.Status != 403 {
					t.Fatalf("%s %s %v", ch.Name, tc.op, r)
				}
			}
		}
	}
}
func snapshot(t *testing.T, s *fixture) string {
	t.Helper()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var raw []byte
	err := tx.QueryRow(context.Background(), `SELECT jsonb_build_array((SELECT jsonb_agg(t) FROM card_types t),(SELECT jsonb_agg(c) FROM cards c),(SELECT jsonb_agg(l) FROM card_ledger l),(SELECT jsonb_agg(s) FROM card_ledger_sales s),(SELECT jsonb_agg(a) FROM audit_log a))`).Scan(&raw)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func TestAIWritesRefused(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	before := snapshot(t, s)
	for _, tc := range []struct {
		op string
		in any
	}{{"open_card", s.opening()}, {"recharge_card", s.recharge(card["id"])}, {"create_card_type", typeInput("新卡")}, {"update_card_type", map[string]any{"id": s.cardType, "name": "改名", "kind": "stored_value", "validity_kind": "none", "active": false}}} {
		r := s.AI(t, s.MustCaller(s.Owner), tc.op, tc.in)
		if r.Status == 200 {
			t.Fatal(r)
		}
		if after := snapshot(t, s); after != before {
			t.Fatal("AI changed rows", after)
		}
	}
}
func TestScope(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	caller := s.MustCaller(s.Owner)
	caller.Permissions = perm.All()
	caller.Permissions[perm.CustomerView] = ops.Grant{Scope: ops.ScopeSelf}
	for _, own := range []bool{false, true} {
		if own {
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			exec(t, tx, "UPDATE customers SET responsible_staff_id=$2 WHERE id=$1", s.customer, caller.StaffID)
			commit(t, tx)
		}
		want := 404
		if own {
			want = 200
		}
		for _, tc := range []struct {
			op string
			in any
		}{{"open_card", s.opening()}, {"recharge_card", s.recharge(card["id"])}} {
			r := s.HTTP(t, &caller, tc.op, tc.in)
			if r.Status != want {
				t.Fatalf("%s own=%v %v", tc.op, own, r)
			}
		}
		for _, ch := range s.Channels() {
			for _, tc := range []struct {
				op string
				in any
			}{{"list_customer_cards", map[string]any{"customer_id": s.customer}}, {"list_card_ledger", map[string]any{"card_id": card["id"]}}} {
				r := ch.Call(t, caller, tc.op, tc.in)
				if r.Status != want {
					t.Fatalf("%s %s own=%v %v", ch.Name, tc.op, own, r)
				}
			}
			r := ch.Call(t, caller, "list_audit_log", map[string]any{"target_id": card["id"]})
			if r.Status != 200 {
				t.Fatal(r)
			}
			n := len(r.Body["entries"].([]any))
			if (!own && n != 0) || (own && n != 2) {
				t.Fatal(own, r)
			}
		}
	}
}
func TestIssuingStore(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	staff, token := s.Hire("二店员工", "002", "second", s.Role("老板"))
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var store uuid.UUID
	if err := tx.QueryRow(context.Background(), "INSERT INTO stores (tenant_id,name) VALUES ($1,'二店') RETURNING id", s.Res.TenantID).Scan(&store); err != nil {
		t.Fatal(err)
	}
	exec(t, tx, "UPDATE staff SET store_id=$2 WHERE id=$1", staff, store)
	commit(t, tx)
	in := s.recharge(card["id"])
	in["sales_staff_ids"] = []any{staff}
	// 外店请求连卡锁都不应取得；后续视图也有限定门店，不能让它遮住锁查询漏范围。
	held := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	exec(t, held, "SELECT id FROM cards WHERE id=$1 FOR NO KEY UPDATE", card["id"])
	done := make(chan optest.Result, 1)
	go func() { done <- s.HTTPWithToken(t, token, "recharge_card", in) }()
	select {
	case r := <-done:
		if r.Status != 404 {
			t.Fatal(r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cross-store recharge waited for the issuing store's card lock")
	}
	commit(t, held)
	for _, ch := range s.Channels() {
		caller := s.MustCaller(token)
		r := ch.Call(t, caller, "list_card_ledger", map[string]any{"card_id": card["id"]})
		if r.Status != 404 {
			t.Fatal(r)
		}
		r = ch.Call(t, caller, "list_customer_cards", map[string]any{"customer_id": s.customer})
		if r.Status != 200 || len(r.Body["items"].([]any)) != 0 {
			t.Fatal(r)
		}
	}
}
func TestRechargeSerializes(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	exec(t, tx, "SELECT id FROM cards WHERE id=$1 FOR NO KEY UPDATE", card["id"])
	done := make(chan optest.Result, 1)
	go func() { done <- s.HTTPWithToken(t, s.Owner, "recharge_card", s.recharge(card["id"])) }()
	dbtest.WaitForLockWait(t, s.Pool)
	exec(t, tx, `INSERT INTO card_ledger (tenant_id,store_id,card_id,kind,principal_before,principal_delta,principal_after,bonus_before,bonus_delta,bonus_after,business_date,actor_id) VALUES ($1,$2,$3,'recharge',1234.56,10,1244.56,0.50,10,10.50,current_date,$4)`, s.Res.TenantID, s.Res.StoreID, card["id"], s.Res.OwnerID)
	commit(t, tx)
	r := <-done
	if r.Status != 200 || r.Body["principal"] != "1245.06" || r.Body["bonus"] != "1245.06" {
		t.Fatal(r)
	}
}
func TestOpenWaitsForMerge(t *testing.T) {
	s := open(t)
	keep := s.call("create_customer", map[string]any{}, 200)["customer"].(map[string]any)["id"]
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	exec(t, tx, "UPDATE customers SET merged_into=$2 WHERE id=$1", s.customer, keep)
	done := make(chan optest.Result, 1)
	go func() { done <- s.HTTPWithToken(t, s.Owner, "open_card", s.opening()) }()
	dbtest.WaitForLockWait(t, s.Pool)
	commit(t, tx)
	r := <-done
	if r.Status != 409 {
		t.Fatal(r)
	}
	check := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var n int
	if err := check.QueryRow(context.Background(), "SELECT count(*) FROM cards WHERE customer_id=$1", s.customer).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}
func TestMergeMovesCards(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	before := s.call("list_card_ledger", map[string]any{"card_id": card["id"]}, 200)
	keep := s.call("create_customer", map[string]any{}, 200)["customer"].(map[string]any)["id"]
	s.call("merge_customers", map[string]any{"keep_id": keep, "merged_id": s.customer}, 200)
	after := s.call("list_customer_cards", map[string]any{"customer_id": keep}, 200)["items"].([]any)
	if len(after) != 1 || after[0].(map[string]any)["customer_id"] != keep {
		t.Fatal(after)
	}
	if !reflect.DeepEqual(before, s.call("list_card_ledger", map[string]any{"card_id": card["id"]}, 200)) {
		t.Fatal("ledger changed")
	}
}
func TestAuditTargets(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var kind string
	var customer *uuid.UUID
	if err := tx.QueryRow(context.Background(), "SELECT target_kind,customer_id FROM audit_log WHERE target_id=$1", card["id"]).Scan(&kind, &customer); err != nil || kind != "card" || customer == nil || customer.String() != s.customer {
		t.Fatal(kind, customer, err)
	}
	if err := tx.QueryRow(context.Background(), "SELECT target_kind,customer_id FROM audit_log WHERE target_id=$1", s.cardType).Scan(&kind, &customer); err != nil || kind != "card_type" || customer != nil {
		t.Fatal(kind, customer, err)
	}
}
func TestLedgerDatabaseProtections(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	for _, sql := range []string{"UPDATE card_ledger SET principal_after=principal_after WHERE card_id=$1", "DELETE FROM card_ledger WHERE card_id=$1"} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), sql, card["id"])
		if err == nil {
			t.Fatal("ledger mutation allowed", sql)
		}
		var pg interface{ SQLState() string }
		if !errors.As(err, &pg) || pg.SQLState() != "42501" {
			t.Fatal(err)
		}
	}
}

func TestLedgerPrincipalArithmeticConstraint(t *testing.T) {
	checkLedgerArithmetic(t, 2, 1, "card_ledger_check")
}

func TestLedgerBonusArithmeticConstraint(t *testing.T) {
	checkLedgerArithmetic(t, 1, 2, "card_ledger_check1")
}

func checkLedgerArithmetic(t *testing.T, principalAfter, bonusAfter int, constraint string) {
	t.Helper()
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, err := tx.Exec(context.Background(), `INSERT INTO card_ledger (tenant_id,store_id,card_id,kind,principal_before,principal_delta,principal_after,bonus_before,bonus_delta,bonus_after,business_date,actor_id) VALUES ($1,$2,$3,'recharge',0,1,$5,0,1,$6,current_date,$4)`, s.Res.TenantID, s.Res.StoreID, card["id"], s.Res.OwnerID, principalAfter, bonusAfter)
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "23514" || pg.ConstraintName != constraint {
		t.Fatalf("want CHECK %s (23514), got %v", constraint, err)
	}
}

func TestCardTypeLocksBeforeAudit(t *testing.T) {
	s := open(t)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	exec(t, tx, "UPDATE card_types SET name='锁内名称' WHERE id=$1", s.cardType)
	in := typeInput("改后")
	in["id"] = s.cardType
	in["active"] = true
	done := make(chan optest.Result, 1)
	go func() { done <- s.HTTPWithToken(t, s.Owner, "update_card_type", in) }()
	dbtest.WaitForLockWait(t, s.Pool)
	commit(t, tx)
	if r := <-done; r.Status != 200 {
		t.Fatal(r)
	}
	check := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var name string
	if err := check.QueryRow(context.Background(), "SELECT before->>'name' FROM audit_log WHERE target_id=$1 AND action='update_card_type'", s.cardType).Scan(&name); err != nil || name != "锁内名称" {
		t.Fatal(name, err)
	}
}
func TestOpenReferenceShareLocks(t *testing.T) {
	for _, kind := range []string{"staff", "card_type"} {
		t.Run(kind, func(t *testing.T) {
			s := open(t)
			in := s.opening()
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			if kind == "staff" {
				id, _ := s.Hire("员工", "002", "reference")
				in["sales_staff_ids"] = []any{id}
				exec(t, tx, "UPDATE staff SET active=false,deactivated_at=clock_timestamp() WHERE id=$1", id)
			} else {
				exec(t, tx, "UPDATE card_types SET active=false WHERE id=$1", s.cardType)
			}
			done := make(chan optest.Result, 1)
			go func() { done <- s.HTTPWithToken(t, s.Owner, "open_card", in) }()
			dbtest.WaitForLockWait(t, s.Pool)
			commit(t, tx)
			if r := <-done; r.Status != 409 {
				t.Fatal(r)
			}
		})
	}
}
func TestCardTypeNamesAndMissingRows(t *testing.T) {
	s := open(t)
	s.call("create_card_type", typeInput("储值"), 409)
	in := typeInput("储值")
	other := s.call("create_card_type", typeInput("第二种"), 200)
	in["id"] = other["id"]
	in["active"] = true
	s.call("update_card_type", in, 409)
	in["name"] = " "
	s.call("update_card_type", in, 409)
	in["name"] = "不存在"
	in["id"] = uuid.New()
	s.call("update_card_type", in, 404)
}
func TestWriteReturnsRequireCustomerView(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	caller := s.MustCaller(s.Owner)
	caller.Permissions = perm.All()
	delete(caller.Permissions, perm.CustomerView)
	for _, tc := range []struct {
		op string
		in any
	}{{"open_card", s.opening()}, {"recharge_card", s.recharge(card["id"])}} {
		r := s.HTTP(t, &caller, tc.op, tc.in)
		if r.Status != 404 {
			t.Fatal(tc.op, r)
		}
	}
}
func TestBalanceLimit(t *testing.T) {
	for _, field := range []string{"principal", "bonus"} {
		t.Run(field, func(t *testing.T) {
			s := open(t)
			in := s.opening()
			in[field] = "99999999.99"
			card := s.call("open_card", in, 200)
			before := snapshot(t, s)
			r := s.call("recharge_card", s.recharge(card["id"]), 409)
			if r["detail"] != "卡余额超过上限" {
				t.Fatal(r)
			}
			if after := snapshot(t, s); after != before {
				t.Fatal("rejected recharge changed rows", after)
			}
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			var count int
			if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM card_ledger WHERE card_id=$1", card["id"]).Scan(&count); err != nil || count != 1 {
				t.Fatal(count, err)
			}
		})
	}
}
