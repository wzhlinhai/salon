package cards_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

func adjustment(id any) map[string]any {
	return map[string]any{"card_id": id, "principal": "1000.00", "reason": " 对账更正 "}
}
func adjustmentCount(t *testing.T, s *fixture, id any, want int) {
	t.Helper()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var n int
	err := tx.QueryRow(context.Background(), "SELECT count(*) FROM card_adjustments WHERE card_id=$1", id).Scan(&n)
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err != nil || n != want {
		t.Fatal(n, err, "want", want)
	}
}
func TestAdjustmentBalance(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	for _, tc := range []struct{ p, b, dp, db string }{{"1000.00", "0.00", "-234.56", "-0.50"}, {"2000.00", "2.50", "1000.00", "2.50"}} {
		in := adjustment(card["id"])
		in["principal"], in["bonus"] = tc.p, tc.b
		out := s.call("adjust_card", in, 200)
		if out["principal"] != tc.p || out["bonus"] != tc.b {
			t.Fatal(out)
		}
		for _, ch := range s.Channels() {
			r := ch.Call(t, s.MustCaller(s.Owner), "list_card_ledger", map[string]any{"card_id": card["id"]})
			if r.Status != 200 {
				t.Fatal(r)
			}
			entry := r.Body["entries"].([]any)[0].(map[string]any)
			if entry["kind"] != "adjust" || entry["reason"] != "对账更正" || entry["principal_delta"] != tc.dp || entry["bonus_delta"] != tc.db || len(entry["sales_staff"].([]any)) != 0 {
				t.Fatal(entry)
			}
			r = ch.Call(t, s.MustCaller(s.Owner), "list_card_adjustments", map[string]any{"card_id": card["id"], "limit": 1})
			if r.Status != 200 || len(r.Body["entries"].([]any)) != 1 {
				t.Fatal(r)
			}
			a := r.Body["entries"].([]any)[0].(map[string]any)
			if a["reason"] != "对账更正" || a["balance"].(map[string]any)["principal_delta"] != tc.dp || a["actor"].(map[string]any)["name"] != "王老板" {
				t.Fatal(a)
			}
		}
	}
	adjustmentCount(t, s, card["id"], 2)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var valid bool
	err := tx.QueryRow(context.Background(), `WITH chain AS (SELECT *,lag(principal_after,1,0::numeric) OVER w p,lag(bonus_after,1,0::numeric) OVER w b FROM card_ledger WHERE card_id=$1 WINDOW w AS (ORDER BY at,id)) SELECT count(*)=3 AND bool_and(principal_before=p AND bonus_before=b) AND sum(principal_delta)=(array_agg(principal_after ORDER BY at DESC,id DESC))[1] AND sum(bonus_delta)=(array_agg(bonus_after ORDER BY at DESC,id DESC))[1] AND (SELECT count(*) FROM card_ledger_sales WHERE ledger_id IN (SELECT id FROM chain WHERE kind='adjust'))=0 FROM chain`, card["id"]).Scan(&valid)
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err != nil || !valid {
		t.Fatal(valid, err)
	}
	in := adjustment(card["id"])
	in["principal"] = "-1.00"
	s.call("adjust_card", in, 422)
	adjustmentCount(t, s, card["id"], 2)
}
func TestAdjustmentFields(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	ct := s.call("create_card_type", typeInput("另一类型"), 200)
	for i, tc := range []struct {
		in            map[string]any
		before, after any
	}{
		{map[string]any{"expiry": map[string]any{"expires_on": "2020-01-01"}}, nil, "2020-01-01"},
		{map[string]any{"expiry": map[string]any{"expires_on": "2030-01-01"}}, "2020-01-01", "2030-01-01"},
		{map[string]any{"expiry": map[string]any{"expires_on": nil}}, "2030-01-01", nil},
		{map[string]any{"card_type_id": ct["id"]}, nil, nil},
	} {
		tc.in["card_id"], tc.in["reason"] = card["id"], "更正"
		s.call("adjust_card", tc.in, 200)
		adjustmentCount(t, s, card["id"], i+1)
		for _, ch := range s.Channels() {
			r := ch.Call(t, s.MustCaller(s.Owner), "list_card_adjustments", map[string]any{"card_id": card["id"], "limit": 1})
			if r.Status != 200 {
				t.Fatal(r)
			}
			a := r.Body["entries"].([]any)[0].(map[string]any)
			if a["balance"] != nil {
				t.Fatal(a)
			}
			if i < 3 {
				if a["expiry_before"].(map[string]any)["expires_on"] != tc.before || a["expiry_after"].(map[string]any)["expires_on"] != tc.after {
					t.Fatal(a)
				}
			} else if a["card_type_before"].(map[string]any)["id"] != s.cardType || a["card_type_after"].(map[string]any)["id"] != ct["id"] || a["card_type_after"].(map[string]any)["name"] != "另一类型" {
				t.Fatal(a)
			}
		}
	}
	for _, ch := range s.Channels() {
		caller := s.MustCaller(s.Owner)
		first := ch.Call(t, caller, "list_card_adjustments", map[string]any{"card_id": card["id"], "limit": 2})
		if first.Status != 200 || len(first.Body["entries"].([]any)) != 2 {
			t.Fatal(first)
		}
		second := ch.Call(t, caller, "list_card_adjustments", map[string]any{"card_id": card["id"], "limit": 2, "before_id": first.Body["entries"].([]any)[1].(map[string]any)["id"]})
		if second.Status != 200 || len(second.Body["entries"].([]any)) != 2 || second.Body["entries"].([]any)[0].(map[string]any)["id"] == first.Body["entries"].([]any)[0].(map[string]any)["id"] {
			t.Fatal(second)
		}
	}
}
func TestAdjustmentCombined(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	ct := s.call("create_card_type", typeInput("新类型"), 200)
	in := adjustment(card["id"])
	in["expiry"] = map[string]any{"expires_on": "2020-01-01"}
	in["card_type_id"] = ct["id"]
	s.call("adjust_card", in, 200)
	adjustmentCount(t, s, card["id"], 1)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var valid bool
	err := tx.QueryRow(context.Background(), `SELECT count(*)=1 AND bool_and(a.expiry_changed AND a.expires_before IS NULL AND a.expires_after='2020-01-01' AND a.card_type_before=$2 AND a.card_type_after=$3 AND l.kind='adjust' AND l.principal_before=1234.56 AND l.principal_after=1000) FROM card_adjustments a JOIN card_ledger l ON l.id=a.ledger_id WHERE a.card_id=$1`, card["id"], s.cardType, ct["id"]).Scan(&valid)
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err != nil || !valid {
		t.Fatal(valid, err)
	}
}
func TestAdjustmentInactiveType(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	ct := s.call("create_card_type", typeInput("停用类型"), 200)
	in := typeInput("停用类型")
	in["id"], in["active"] = ct["id"], false
	s.call("update_card_type", in, 200)
	s.call("adjust_card", map[string]any{"card_id": card["id"], "card_type_id": ct["id"], "reason": "更正"}, 409)
	adjustmentCount(t, s, card["id"], 0)
}
func TestAdjustmentNoop(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	for _, in := range []map[string]any{{}, {"principal": "1234.56", "bonus": "0.50", "expiry": map[string]any{"expires_on": nil}, "card_type_id": s.cardType}} {
		in["card_id"], in["reason"] = card["id"], "无改动"
		r := s.call("adjust_card", in, 409)
		if r["detail"] != "调整单没有任何改动" {
			t.Fatal(r)
		}
	}
	adjustmentCount(t, s, card["id"], 0)
}
func TestAdjustmentReason(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	for _, reason := range []string{"", "   ", "\t\n"} {
		in := adjustment(card["id"])
		in["reason"] = reason
		want := 409
		if reason == "" {
			want = 422
		}
		s.call("adjust_card", in, want)
	}
	adjustmentCount(t, s, card["id"], 0)
}
func TestAdjustmentUnknownType(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	s.call("adjust_card", map[string]any{"card_id": card["id"], "card_type_id": uuid.New(), "reason": "更正"}, 404)
	adjustmentCount(t, s, card["id"], 0)
}
func TestAdjustmentPermissions(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	caller := s.MustCaller(s.Owner)
	caller.Permissions = perm.All()
	delete(caller.Permissions, perm.CardAdjust)
	if r := s.HTTP(t, &caller, "adjust_card", adjustment(card["id"])); r.Status != 403 {
		t.Fatal(r)
	}
	caller.Permissions = perm.All()
	delete(caller.Permissions, perm.CustomerView)
	for _, ch := range s.Channels() {
		if r := ch.Call(t, caller, "list_card_adjustments", map[string]any{"card_id": card["id"]}); r.Status != 403 {
			t.Fatal(r)
		}
	}
	adjustmentCount(t, s, card["id"], 0)
}
func TestAdjustmentAIRefused(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	before := snapshot(t, s)
	if r := s.AI(t, s.MustCaller(s.Owner), "adjust_card", adjustment(card["id"])); r.Status == 200 {
		t.Fatal(r)
	}
	if snapshot(t, s) != before {
		t.Fatal("AI changed rows")
	}
	adjustmentCount(t, s, card["id"], 0)
}
func TestAdjustmentScope(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	s.call("adjust_card", map[string]any{"card_id": card["id"], "principal": "1100.00", "reason": "初次更正"}, 200)
	other, _ := s.Hire("另一负责人", "002", "other")
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	exec(t, tx, "UPDATE customers SET responsible_staff_id=$2 WHERE id=$1", s.customer, other)
	commit(t, tx)
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
		if r := s.HTTP(t, &caller, "adjust_card", adjustment(card["id"])); r.Status != want {
			t.Fatal(r)
		}
		for _, ch := range s.Channels() {
			if r := ch.Call(t, caller, "list_card_adjustments", map[string]any{"card_id": card["id"]}); r.Status != want {
				t.Fatal(r)
			}
			r := ch.Call(t, caller, "list_audit_log", map[string]any{"target_id": card["id"]})
			if r.Status != 200 {
				t.Fatal(r)
			}
			n := 0
			for _, v := range r.Body["entries"].([]any) {
				if v.(map[string]any)["action"] == "adjust_card" {
					n++
				}
			}
			if (!own && n != 0) || (own && n != 2) {
				t.Fatal(r)
			}
		}
	}
}
func TestAdjustmentIssuingStore(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	staff, token := s.Hire("二店", "002", "second", s.Role("老板"))
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var store uuid.UUID
	if err := tx.QueryRow(context.Background(), "INSERT INTO stores (tenant_id,name) VALUES ($1,'二店') RETURNING id", s.Res.TenantID).Scan(&store); err != nil {
		t.Fatal(err)
	}
	exec(t, tx, "UPDATE staff SET store_id=$2 WHERE id=$1", staff, store)
	commit(t, tx)
	held := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	exec(t, held, "SELECT id FROM cards WHERE id=$1 FOR NO KEY UPDATE", card["id"])
	done := make(chan optest.Result, 1)
	go func() { done <- s.HTTPWithToken(t, token, "adjust_card", adjustment(card["id"])) }()
	select {
	case r := <-done:
		if r.Status != 404 {
			t.Fatal(r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cross-store adjustment waited for card lock")
	}
	commit(t, held)
	for _, ch := range s.Channels() {
		if r := ch.Call(t, s.MustCaller(token), "list_card_adjustments", map[string]any{"card_id": card["id"]}); r.Status != 404 {
			t.Fatal(r)
		}
	}
	adjustmentCount(t, s, card["id"], 0)
}
func TestAdjustmentSerializes(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	held := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	exec(t, held, "SELECT id FROM cards WHERE id=$1 FOR NO KEY UPDATE", card["id"])
	done := make(chan optest.Result, 1)
	go func() { done <- s.HTTPWithToken(t, s.Owner, "adjust_card", adjustment(card["id"])) }()
	dbtest.WaitForLockWait(t, s.Pool)
	exec(t, held, `INSERT INTO card_ledger (tenant_id,store_id,card_id,kind,principal_before,principal_delta,principal_after,bonus_before,bonus_delta,bonus_after,business_date,actor_id) VALUES ($1,$2,$3,'recharge',1234.56,10,1244.56,0.50,10,10.50,current_date,$4)`, s.Res.TenantID, s.Res.StoreID, card["id"], s.Res.OwnerID)
	commit(t, held)
	if r := <-done; r.Status != 200 {
		t.Fatal(r)
	}
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "list_card_adjustments", map[string]any{"card_id": card["id"]})
		if r.Status != 200 {
			t.Fatal(r)
		}
		b := r.Body["entries"].([]any)[0].(map[string]any)["balance"].(map[string]any)
		if b["principal_before"] != "1244.56" || b["principal_delta"] != "-244.56" || b["bonus_before"] != "10.50" {
			t.Fatal(b)
		}
	}
}
func TestAdjustmentWaitsForType(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	ct := s.call("create_card_type", typeInput("待停用"), 200)
	held := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	exec(t, held, "SELECT id FROM card_types WHERE id=$1 FOR NO KEY UPDATE", ct["id"])
	exec(t, held, "UPDATE card_types SET active=false WHERE id=$1", ct["id"])
	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "adjust_card", map[string]any{"card_id": card["id"], "card_type_id": ct["id"], "reason": "更正"})
	}()
	dbtest.WaitForLockWait(t, s.Pool)
	commit(t, held)
	if r := <-done; r.Status != 409 {
		t.Fatal(r)
	}
	adjustmentCount(t, s, card["id"], 0)
}
func TestAdjustmentDatabaseProtections(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	s.call("adjust_card", adjustment(card["id"]), 200)
	for _, tc := range []struct{ sql, code string }{
		{"UPDATE card_adjustments SET reason='改' WHERE card_id=$1", "42501"},
		{"DELETE FROM card_adjustments WHERE card_id=$1", "42501"},
		{`INSERT INTO card_adjustments (tenant_id,store_id,card_id,reason,expiry_changed,business_date,actor_id) VALUES ($1,$2,$3,'更正',false,current_date,$4)`, "23514"},
		{`INSERT INTO card_adjustments (tenant_id,store_id,card_id,reason,expiry_changed,business_date,actor_id) VALUES ($1,$2,$3,'   ',true,current_date,$4)`, "23514"},
	} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		sql := tc.sql
		if tc.code == "42501" {
			_, err := tx.Exec(context.Background(), sql, card["id"])
			var pg *pgconn.PgError
			if err := tx.Rollback(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !errors.As(err, &pg) || pg.Code != tc.code {
				t.Fatal(err)
			}
			continue
		}
		_, err := tx.Exec(context.Background(), sql, s.Res.TenantID, s.Res.StoreID, card["id"], s.Res.OwnerID)
		if e := tx.Rollback(context.Background()); e != nil {
			t.Fatal(e)
		}
		var pg *pgconn.PgError
		if !errors.As(err, &pg) || pg.Code != tc.code {
			t.Fatal(err)
		}
	}
}

func TestAdjustmentReasonLimits(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	reason := strings.Repeat("因", 200)
	in := adjustment(card["id"])
	in["reason"] = "  " + reason + "  "
	s.call("adjust_card", in, 200)
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "list_card_adjustments", map[string]any{"card_id": card["id"]})
		if r.Status != 200 || r.Body["entries"].([]any)[0].(map[string]any)["reason"] != reason {
			t.Fatal(r)
		}
	}
	in["principal"], in["reason"] = "900.00", reason+"因"
	s.call("adjust_card", in, 409)
	adjustmentCount(t, s, card["id"], 1)
}
