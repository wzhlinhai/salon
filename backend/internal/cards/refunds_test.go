package cards_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func (s *fixture) refundSnapshot() string {
	return dbtest.Snapshot(s.t, s.Pool, s.Res.TenantID, "card_refunds", "cards", "card_ledger", "card_count_ledger", "card_points_ledger", "card_upgrades", "card_transfers", "orders", "order_payments", "audit_log")
}
func (s *fixture) refundSetting(key string, value any) {
	s.call("change_setting", map[string]any{"key": key, "value": value, "effective_from": settings.Date(time.Now()).Format(time.DateOnly)}, 200)
}
func (s *fixture) refund(id any, want string) map[string]any {
	s.t.Helper()
	for _, ch := range s.Channels() {
		r := ch.Call(s.t, s.MustCaller(s.Owner), "preview_card_refund", map[string]any{"card_id": id})
		if r.Status != 200 || r.Body["amount"] != want {
			s.t.Fatal(ch.Name, r, want)
		}
	}
	out := s.call("refund_card", map[string]any{"card_id": id, "method": "wechat", "amount": want}, 200)
	if out["amount"] != want {
		s.t.Fatal(out)
	}
	card := out["card"].(map[string]any)
	if card["status"] != "refunded" || card["principal"] != "0.00" || card["bonus"] != "0.00" {
		s.t.Fatal(out)
	}
	tx := dbtest.AppTx(s.t, s.Pool, s.Res.TenantID)
	var valid bool
	err := tx.QueryRow(context.Background(), `SELECT r.amount=$2 AND r.method='wechat' AND r.principal=l.principal_before AND r.bonus=l.bonus_before AND l.principal_after=0 AND l.bonus_after=0 AND l.principal_delta=-r.principal AND l.bonus_delta=-r.bonus AND r.counts_amount=COALESCE((SELECT sum(cl.paid_before*ci.unit_price) FROM card_count_ledger cl JOIN card_items ci USING(card_id,item_id) WHERE cl.refund_id=r.id),0) AND (SELECT count(*) FROM card_count_ledger WHERE refund_id=r.id)=(SELECT count(*) FROM card_items WHERE card_id=r.card_id) AND NOT EXISTS(SELECT 1 FROM card_count_ledger WHERE refund_id=r.id AND (paid_after<>0 OR gift_after<>0)) FROM card_refunds r JOIN card_ledger l ON l.refund_id=r.id WHERE r.card_id=$1`, id, want).Scan(&valid)
	dbtest.Rollback(s.t, tx)
	if err != nil || !valid {
		s.t.Fatal(valid, err)
	}
	for _, ch := range s.Channels() {
		r := ch.Call(s.t, s.MustCaller(s.Owner), "list_card_ledger", map[string]any{"card_id": id})
		if r.Status != 200 {
			s.t.Fatal(r)
		}
		entry := r.Body["entries"].([]any)[0].(map[string]any)
		if entry["kind"] != "refund" || entry["refund_id"] != out["id"] || entry["refund_method"] != "wechat" || entry["refund_amount"] != want {
			s.t.Fatal(entry)
		}
	}
	return out
}

func TestRefundAmounts(t *testing.T) {
	for _, tc := range []struct {
		name, principal, bonus string
		fee                    int
		amount                 string
	}{{"forfeit", "1234.56", "0.50", 0, "1234.56"}, {"half-fen", "0.05", "1.00", 10, "0.04"}, {"all-fee", "100", "20", 100, "0.00"}, {"zero", "0", "0", 0, "0.00"}} {
		t.Run(tc.name, func(t *testing.T) {
			s := open(t)
			s.refundSetting("card_refund_fee_percent", tc.fee)
			in := s.opening()
			in["principal"], in["bonus"] = tc.principal, tc.bonus
			card := s.call("open_card", in, 200)
			s.call("adjust_card", map[string]any{"card_id": card["id"], "expiry": map[string]any{"expires_on": "2000-01-01"}, "reason": "过期卡"}, 200)
			out := s.refund(card["id"], tc.amount)
			if out["card"].(map[string]any)["points"] != card["points"] {
				t.Fatal(out)
			}
		})
	}
}
func TestRefundCounts(t *testing.T) {
	for _, kind := range []string{"count", "package"} {
		t.Run(kind, func(t *testing.T) {
			s := open(t)
			a, b := s.item("剪"), s.item("烫")
			ct := s.call("create_card_type", countType("套餐", kind, countItem(a), countItem(b)), 200)
			in := s.opening()
			in["card_type_id"] = ct["id"]
			want := "1246.90"
			if kind == "count" {
				in["principal"], in["bonus"] = "0", "0"
				want = "12.34"
			}
			card := s.call("open_card", in, 200)
			s.call("adjust_card", map[string]any{"card_id": card["id"], "reason": "剩余次数", "counts": []any{map[string]any{"item_id": a, "paid": 1, "gift": 7}, map[string]any{"item_id": b, "paid": 0, "gift": 0}}}, 200)
			out := s.refund(card["id"], want)
			if out["counts_amount"] != "12.34" || len(out["items"].([]any)) != 2 {
				t.Fatal(out)
			}
			rows := s.call("list_card_counts", map[string]any{"card_id": card["id"]}, 200)["entries"].([]any)
			if rows[0].(map[string]any)["kind"] != "refund" || rows[1].(map[string]any)["kind"] != "refund" {
				t.Fatal(rows)
			}
		})
	}
}
func TestRefundQuoteChanged(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	quote := s.call("preview_card_refund", map[string]any{"card_id": card["id"]}, 200)
	s.call("recharge_card", s.recharge(card["id"]), 200)
	before := s.refundSnapshot()
	r := s.call("refund_card", map[string]any{"card_id": card["id"], "method": "cash", "amount": quote["amount"]}, 409)
	if r["detail"] != "退款金额已变化，请重新确认" || before != s.refundSnapshot() {
		t.Fatal(r)
	}
	s.refund(card["id"], "1235.06")
}
func TestRefundAccess(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	in := map[string]any{"card_id": card["id"], "method": "cash", "amount": "1234.56"}
	before := s.refundSnapshot()
	for _, variant := range []struct {
		name   string
		status int
	}{{"permission", 403}, {"scope", 404}, {"store", 404}, {"tenant", 404}} {
		caller := s.MustCaller(s.Owner)
		switch variant.name {
		case "permission":
			caller.Permissions = perm.All()
			delete(caller.Permissions, perm.CardAdjust)
		case "scope":
			caller.Permissions = perm.All()
			caller.Permissions[perm.CustomerView] = ops.Grant{Scope: ops.ScopeSelf}
		case "store":
			caller.StoreID = uuid.New()
		case "tenant":
			caller.TenantID = uuid.New()
		}
		for _, op := range []string{"preview_card_refund", "refund_card"} {
			args := any(in)
			if op == "preview_card_refund" {
				args = map[string]any{"card_id": card["id"]}
			}
			if r := s.HTTP(t, &caller, op, args); r.Status != variant.status || variant.status == 403 && r.Detail() != "permission denied" {
				t.Fatal(variant.name, op, r)
			}
		}
	}
	if r := s.AI(t, s.MustCaller(s.Owner), "refund_card", in); r.Status != 409 || r.Detail() != "operation requires a draft confirmed by a person" {
		t.Fatal(r)
	}
	if before != s.refundSnapshot() {
		t.Fatal("denied refund changed data")
	}
}
func TestRefundAudit(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	s.refund(card["id"], "1234.56")
	for _, ch := range s.Channels() {
		caller := s.MustCaller(s.Owner)
		r := ch.Call(t, caller, "list_audit_log", map[string]any{"target_id": card["id"]})
		if r.Status != 200 {
			t.Fatal(r)
		}
		a := r.Body["entries"].([]any)[0].(map[string]any)
		if a["action"] != "refund_card" || a["before"].(map[string]any)["principal"] != "1234.56" || a["after"].(map[string]any)["status"] != "refunded" {
			t.Fatal(a)
		}
		caller.Permissions = perm.All()
		caller.Permissions[perm.CustomerView] = ops.Grant{Scope: ops.ScopeSelf}
		r = ch.Call(t, caller, "list_audit_log", map[string]any{"target_id": card["id"]})
		if r.Status != 200 || len(r.Body["entries"].([]any)) != 0 {
			t.Fatal(r)
		}
	}
}
func TestRefundChangesRefused(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	other := s.call("open_card", s.opening(), 200)
	ct := s.call("create_card_type", typeInput("升级目标"), 200)
	s.call("create_card_upgrade_rule", rule(s.cardType, ct["id"], "single_recharge", "0.01"), 200)
	s.refund(card["id"], "1234.56")
	for _, tc := range []struct {
		op string
		in any
	}{
		{"refund_card", map[string]any{"card_id": card["id"], "method": "cash", "amount": "0"}},
		{"preview_card_refund", map[string]any{"card_id": card["id"]}},
		{"recharge_card", s.recharge(card["id"])},
		{"adjust_card", adjustment(card["id"])},
		{"upgrade_card", map[string]any{"card_id": card["id"], "to_type_id": ct["id"], "principal": "0", "bonus": "0"}},
		{"transfer_card_balance", map[string]any{"from_card_id": card["id"], "to_card_id": other["id"], "principal": "1", "bonus": "0", "reason": "转出"}},
		{"transfer_card_balance", map[string]any{"from_card_id": other["id"], "to_card_id": card["id"], "principal": "1", "bonus": "0", "reason": "转入"}},
	} {
		before := s.refundSnapshot()
		r := s.call(tc.op, tc.in, 409)
		if r["detail"] != "卡已退" || before != s.refundSnapshot() {
			t.Fatal(tc.op, r)
		}
	}
}
func TestRefundSerializes(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	held := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, held, "SELECT id FROM cards WHERE id=$1 FOR NO KEY UPDATE", card["id"])
	done := make(chan optest.Result, 2)
	in := map[string]any{"card_id": card["id"], "method": "cash", "amount": "1234.56"}
	go func() { done <- s.HTTPWithToken(t, s.Owner, "refund_card", in) }()
	dbtest.WaitForLockWait(t, s.Pool)
	go func() { done <- s.HTTPWithToken(t, s.Owner, "refund_card", in) }()
	optest.AssertWaiting(t, done)
	dbtest.Commit(t, held)
	var a optest.Result
	select {
	case a = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("refund timed out")
	}
	want := 200
	if a.Status == 200 {
		want = 409
	}
	b := optest.WaitResult(t, done, want)
	if a.Status != 200 && a.Status != 409 || a.Status == b.Status {
		t.Fatal(a, b)
	}
	for _, r := range []optest.Result{a, b} {
		if r.Status == 409 && r.Detail() != "卡已退" {
			t.Fatal(r)
		}
	}
}

func TestRefundPreviewAccessChannels(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	before := s.refundSnapshot()
	for _, ch := range s.Channels() {
		for _, scope := range []bool{false, true} {
			caller := s.MustCaller(s.Owner)
			caller.Permissions = perm.All()
			want := 403
			if scope {
				caller.Permissions[perm.CustomerView] = ops.Grant{Scope: ops.ScopeSelf}
				want = 404
			} else {
				delete(caller.Permissions, perm.CardAdjust)
			}
			r := ch.Call(t, caller, "preview_card_refund", map[string]any{"card_id": card["id"]})
			if r.Status != want || want == 403 && r.Detail() != "permission denied" {
				t.Fatal(ch.Name, r)
			}
		}
	}
	if before != s.refundSnapshot() {
		t.Fatal("preview wrote data")
	}
}

func TestRefundHiddenRefunded(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	s.refund(card["id"], "1234.56")
	caller := s.MustCaller(s.Owner)
	caller.Permissions = perm.All()
	caller.Permissions[perm.CustomerView] = ops.Grant{Scope: ops.ScopeSelf}
	before := s.refundSnapshot()
	if r := s.HTTP(t, &caller, "refund_card", map[string]any{"card_id": card["id"], "method": "cash", "amount": "0"}); r.Status != 404 {
		t.Fatal(r)
	}
	for _, ch := range s.Channels() {
		if r := ch.Call(t, caller, "preview_card_refund", map[string]any{"card_id": card["id"]}); r.Status != 404 {
			t.Fatal(ch.Name, r)
		}
		if r := ch.Call(t, caller, "list_card_ledger", map[string]any{"card_id": card["id"]}); r.Status != 404 {
			t.Fatal(ch.Name, r)
		}
	}
	if before != s.refundSnapshot() {
		t.Fatal("hidden refunded card changed")
	}
}
