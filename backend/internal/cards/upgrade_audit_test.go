package cards_test

import (
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

func TestUpgradeRuleDeactivateInactiveTarget(t *testing.T) {
	s := open(t)
	target := s.call("create_card_type", typeInput("目标"), 200)
	r := s.call("create_card_upgrade_rule", rule(s.cardType, target["id"], "single_recharge", "10.00"), 200)
	in := typeInput("目标")
	in["id"], in["active"] = target["id"], false
	s.call("update_card_type", in, 200)
	out := s.call("update_card_upgrade_rule", map[string]any{"id": r["id"], "threshold": "10.00", "active": false}, 200)
	if out["active"] != false {
		t.Fatal(out)
	}
	s.call("update_card_upgrade_rule", map[string]any{"id": r["id"], "threshold": "10.00", "active": true}, 409)
	assertSQL(t, s, `SELECT NOT active FROM card_upgrade_rules WHERE id=$1`, r["id"])
}

func TestManualUpgradeAudit(t *testing.T) { checkCardUpgradeAudit(t, false) }
func TestAutoUpgradeAudit(t *testing.T)   { checkCardUpgradeAudit(t, true) }

func checkCardUpgradeAudit(t *testing.T, automatic bool) {
	t.Helper()
	s := open(t)
	own := s.call("open_card", s.opening(), 200)
	other := s.call("create_customer", map[string]any{"name": "另一位顾客"}, 200)["customer"].(map[string]any)["id"]
	opening := s.opening()
	opening["customer_id"] = other
	hidden := s.call("open_card", opening, 200)
	target := s.call("create_card_type", typeInput("高级"), 200)
	action := "upgrade_card"
	if automatic {
		action = "auto_upgrade_card"
		s.call("create_card_upgrade_rule", rule(s.cardType, target["id"], "single_recharge", "0.50"), 200)
	}
	for _, card := range []map[string]any{own, hidden} {
		if automatic {
			s.call("recharge_card", s.recharge(card["id"]), 200)
		} else {
			s.call("upgrade_card", upgrade(card["id"], target["id"]), 200)
		}
		assertSQL(t, s, `SELECT count(*)=1 AND bool_and(target_kind='card' AND target_id=$2 AND customer_id=$3 AND before->>'card_type_id'=$4 AND after->>'card_type_id'=$5) FROM audit_log WHERE action=$1 AND target_id=$2`, action, card["id"], card["customer_id"], s.cardType, target["id"])
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	exec(t, tx, "UPDATE customers SET responsible_staff_id=$2 WHERE id=$1", s.customer, s.Res.OwnerID)
	commit(t, tx)
	owner := s.MustCaller(s.Owner)
	self := s.MustCaller(s.Owner)
	self.Permissions = perm.All()
	self.Permissions[perm.CustomerView] = ops.Grant{Scope: ops.ScopeSelf}
	for _, ch := range s.Channels() {
		for _, card := range []map[string]any{own, hidden} {
			// 全店调用者正向断言两份卡日志的操作、目标和升级前后类型。
			r := ch.Call(t, owner, "list_audit_log", map[string]any{"target_id": card["id"]})
			if r.Status != 200 {
				t.Fatal(ch.Name, r)
			}
			found := 0
			for _, raw := range r.Body["entries"].([]any) {
				e := raw.(map[string]any)
				if e["action"] != action {
					continue
				}
				found++
				if e["target_kind"] != "card" || e["target_id"] != card["id"] || e["before"].(map[string]any)["card_type_id"] != s.cardType || e["after"].(map[string]any)["card_type_id"] != target["id"] {
					t.Fatal(ch.Name, e)
				}
			}
			if found != 1 {
				t.Fatalf("%s %s audit count=%d, want 1", ch.Name, action, found)
			}
			// 本人范围只看自己负责的顾客，不能让全隐藏的日志伪装成通过。
			r = ch.Call(t, self, "list_audit_log", map[string]any{"target_id": card["id"]})
			if r.Status != 200 {
				t.Fatal(ch.Name, r)
			}
			found = 0
			for _, raw := range r.Body["entries"].([]any) {
				if raw.(map[string]any)["action"] == action {
					found++
				}
			}
			want := 0
			if card["id"] == own["id"] {
				want = 1
			}
			if found != want {
				t.Fatalf("%s %s self audit count=%d, want %d", ch.Name, action, found, want)
			}
		}
	}
}

func TestUpdateCardUpgradeRuleAudit(t *testing.T) {
	s := open(t)
	target := s.call("create_card_type", typeInput("高级"), 200)
	r := s.call("create_card_upgrade_rule", rule(s.cardType, target["id"], "single_recharge", "10.00"), 200)
	s.call("update_card_upgrade_rule", map[string]any{"id": r["id"], "threshold": "20.00", "active": false}, 200)
	assertSQL(t, s, `SELECT count(*)=1 AND bool_and(target_kind='card_upgrade_rule' AND target_id=$1 AND customer_id IS NULL AND before->>'from_type_id'=$2 AND after->>'from_type_id'=$2 AND before->>'to_type_id'=$3 AND after->>'to_type_id'=$3 AND before->>'threshold'='10.00' AND after->>'threshold'='20.00' AND before->>'active'='true' AND after->>'active'='false') FROM audit_log WHERE action='update_card_upgrade_rule' AND target_id=$1`, r["id"], s.cardType, target["id"])
	caller := s.MustCaller(s.Owner)
	caller.Permissions = perm.All()
	caller.Permissions[perm.CustomerView] = ops.Grant{Scope: ops.ScopeSelf}
	for _, ch := range s.Channels() {
		result := ch.Call(t, caller, "list_audit_log", map[string]any{"target_id": r["id"]})
		if result.Status != 200 {
			t.Fatal(result)
		}
		found := 0
		for _, raw := range result.Body["entries"].([]any) {
			e := raw.(map[string]any)
			if e["action"] != "update_card_upgrade_rule" {
				continue
			}
			found++
			if e["target_kind"] != "card_upgrade_rule" || e["target_id"] != r["id"] {
				t.Fatal(e)
			}
			before, after := e["before"].(map[string]any), e["after"].(map[string]any)
			if before["from_type_id"] != s.cardType || after["from_type_id"] != s.cardType || before["to_type_id"] != target["id"] || after["to_type_id"] != target["id"] || before["threshold"] != "10.00" || after["threshold"] != "20.00" || before["active"] != true || after["active"] != false {
				t.Fatal(e)
			}
		}
		if found != 1 {
			t.Fatalf("%s update_card_upgrade_rule audit count=%d, want 1", ch.Name, found)
		}
	}
}
