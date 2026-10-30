package cards_test

import (
	"context"
	"errors"
	"fmt"
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

func upgrade(id, to any) map[string]any {
	return map[string]any{"card_id": id, "to_type_id": to, "principal": "0.00", "bonus": "0.00"}
}
func rule(from, to any, kind, threshold string) map[string]any {
	return map[string]any{"from_type_id": from, "to_type_id": to, "condition_kind": kind, "threshold": threshold}
}
func transfer(from, to any) map[string]any {
	return map[string]any{"from_card_id": from, "to_card_id": to, "principal": "10.00", "bonus": "0.25", "reason": " 核对转余额 "}
}
func assertSQL(t *testing.T, s *fixture, sql string, args ...any) {
	t.Helper()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var ok bool
	err := tx.QueryRow(context.Background(), sql, args...).Scan(&ok)
	if e := tx.Rollback(context.Background()); e != nil {
		t.Fatal(e)
	}
	if err != nil || !ok {
		t.Fatal(sql, ok, err)
	}
}
func upgradeSnapshot(t *testing.T, s *fixture) string {
	t.Helper()
	return snapshot(t, s) + dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "card_upgrade_rules", "card_upgrades", "card_transfers")
}
func TestManualUpgrade(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	s.call("adjust_card", map[string]any{"card_id": card["id"], "expiry": map[string]any{"expires_on": "2030-01-01"}, "reason": "原卡有效期"}, 200)
	target := s.call("create_card_type", typeInput("高级"), 200)
	in := upgrade(card["id"], target["id"])
	in["principal"], in["bonus"], in["sales_staff_ids"] = "25.50", "2.00", []any{s.Res.OwnerID}
	out := s.call("upgrade_card", in, 200)
	if out["card_type_id"] != target["id"] || out["principal"] != "1260.06" || out["bonus"] != "2.50" || out["expires_on"] != "2030-01-01" {
		t.Fatal(out)
	}
	assertSQL(t, s, `SELECT count(*)=1 AND bool_and(u.trigger='manual' AND u.rule_id IS NULL AND u.type_before=$2 AND u.type_after=$3 AND l.kind='upgrade' AND l.principal_delta=25.50 AND l.bonus_delta=2 AND (SELECT count(*) FROM card_ledger_sales WHERE ledger_id=l.id)=1) FROM card_upgrades u JOIN card_ledger l ON l.id=u.ledger_id WHERE u.card_id=$1`, card["id"], s.cardType, target["id"])
	s.call("upgrade_card", upgrade(card["id"], s.cardType), 200)
	for _, ch := range s.Channels() {
		caller := s.MustCaller(s.Owner)
		r := ch.Call(t, caller, "list_card_upgrades", map[string]any{"card_id": card["id"], "limit": 1})
		if r.Status != 200 || len(r.Body["entries"].([]any)) != 1 {
			t.Fatal(r)
		}
		entry := r.Body["entries"].([]any)[0].(map[string]any)
		if entry["type_after"].(map[string]any)["id"] != s.cardType || entry["trigger"] != "manual" || entry["actor"].(map[string]any)["name"] != "王老板" {
			t.Fatal(entry)
		}
		next := ch.Call(t, caller, "list_card_upgrades", map[string]any{"card_id": card["id"], "before_id": entry["id"], "limit": 1})
		if next.Status != 200 || len(next.Body["entries"].([]any)) != 1 || next.Body["entries"].([]any)[0].(map[string]any)["type_after"].(map[string]any)["id"] != target["id"] {
			t.Fatal(next)
		}
	}
	assertSQL(t, s, `SELECT count(*)=1 AND bool_and(l.principal_delta=0 AND l.bonus_delta=0 AND NOT EXISTS(SELECT 1 FROM card_ledger_sales WHERE ledger_id=l.id)) FROM card_ledger l WHERE l.card_id=$1 AND l.kind='upgrade' AND l.principal_delta=0`, card["id"])
}
func TestUpgradeRequiresSalesStaff(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	to := s.call("create_card_type", typeInput("高级"), 200)
	before := upgradeSnapshot(t, s)
	for _, key := range []string{"principal", "bonus"} {
		in := upgrade(card["id"], to["id"])
		in[key] = "0.01"
		if r := s.call("upgrade_card", in, 409); r["detail"] != "请选择一到十名不同的业绩归属人" {
			t.Fatal(r)
		}
	}
	if upgradeSnapshot(t, s) != before {
		t.Fatal("rejected upgrade changed rows")
	}
}
func TestUpgradeCurrentStoredValue(t *testing.T) {
	for _, kind := range []string{"count", "package"} {
		t.Run(kind, func(t *testing.T) {
			s := open(t)
			ct := s.countType(kind)
			in := s.opening()
			in["card_type_id"], in["principal"], in["bonus"] = ct["id"], "0.00", "0.00"
			card := s.call("open_card", in, 200)
			before := upgradeSnapshot(t, s)
			if r := s.call("upgrade_card", upgrade(card["id"], s.cardType), 409); r["detail"] != "仅储值卡支持升级" {
				t.Fatal(r)
			}
			if upgradeSnapshot(t, s) != before {
				t.Fatal("non-stored upgrade changed rows")
			}
		})
	}
}
func TestUpgradeTargetRules(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	if r := s.call("upgrade_card", upgrade(card["id"], s.cardType), 409); r["detail"] != "目标须为启用的其他储值卡类型" {
		t.Fatal(r)
	}
	s.call("upgrade_card", upgrade(card["id"], uuid.New()), 404)
	ct := s.countType("package")
	if r := s.call("upgrade_card", upgrade(card["id"], ct["id"]), 409); r["detail"] != "目标须为启用的其他储值卡类型" {
		t.Fatal(r)
	}
	to := s.call("create_card_type", typeInput("停用"), 200)
	input := typeInput("停用")
	input["id"], input["active"] = to["id"], false
	s.call("update_card_type", input, 200)
	if r := s.call("upgrade_card", upgrade(card["id"], to["id"]), 409); r["detail"] != "目标须为启用的其他储值卡类型" {
		t.Fatal(r)
	}
	assertSQL(t, s, `SELECT NOT EXISTS(SELECT 1 FROM card_upgrades) AND (SELECT count(*) FROM card_ledger)=1`)
}
func TestUpgradeRuleTypeRules(t *testing.T) {
	s := open(t)
	to := s.call("create_card_type", typeInput("高级"), 200)
	ct := s.countType("count")
	for _, tc := range []struct {
		in         map[string]any
		wantDetail string
	}{
		{rule(s.cardType, s.cardType, "single_recharge", "1.00"), "升级类型须不同，门槛须大于零"},
		{rule(s.cardType, to["id"], "single_recharge", "0.00"), "升级类型须不同，门槛须大于零"},
		{rule(ct["id"], to["id"], "single_recharge", "1.00"), "升级规则仅支持储值卡"},
		{rule(s.cardType, ct["id"], "single_recharge", "1.00"), "升级规则仅支持储值卡"},
	} {
		if r := s.call("create_card_upgrade_rule", tc.in, 409); r["detail"] != tc.wantDetail {
			t.Fatal(r)
		}
	}
	input := typeInput("高级")
	input["id"], input["active"] = to["id"], false
	s.call("update_card_type", input, 200)
	if r := s.call("create_card_upgrade_rule", rule(s.cardType, to["id"], "single_recharge", "1.00"), 409); r["detail"] != "目标卡类型已停用" {
		t.Fatal(r)
	}
	s.call("create_card_upgrade_rule", rule(uuid.New(), s.cardType, "single_recharge", "1.00"), 404)
}
func TestUpgradeRuleCycles(t *testing.T) {
	s := open(t)
	b := s.call("create_card_type", typeInput("乙"), 200)
	c := s.call("create_card_type", typeInput("丙"), 200)
	ab := s.call("create_card_upgrade_rule", rule(s.cardType, b["id"], "single_recharge", "100.00"), 200)
	if r := s.call("create_card_upgrade_rule", rule(b["id"], s.cardType, "single_recharge", "100.00"), 409); r["detail"] != "升级规则不能形成环路" {
		t.Fatal(r)
	}
	s.call("create_card_upgrade_rule", rule(b["id"], c["id"], "single_recharge", "100.00"), 200)
	if r := s.call("create_card_upgrade_rule", rule(c["id"], s.cardType, "single_recharge", "100.00"), 409); r["detail"] != "升级规则不能形成环路" {
		t.Fatal(r)
	}
	s.call("update_card_upgrade_rule", map[string]any{"id": ab["id"], "threshold": "200.00", "active": false}, 200)
	s.call("create_card_upgrade_rule", rule(c["id"], s.cardType, "single_recharge", "100.00"), 200)
	if r := s.call("update_card_upgrade_rule", map[string]any{"id": ab["id"], "threshold": "200.00", "active": true}, 409); r["detail"] != "升级规则不能形成环路" {
		t.Fatal(r)
	}
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "list_card_upgrade_rules", map[string]any{})
		if r.Status != 200 || len(r.Body["items"].([]any)) != 3 {
			t.Fatal(r)
		}
	}
}
func TestAutoUpgradeSingle(t *testing.T) {
	for _, amount := range []string{"99.99", "100.00"} {
		t.Run(amount, func(t *testing.T) {
			s := open(t)
			card := s.call("open_card", s.opening(), 200)
			to := s.call("create_card_type", typeInput("高级"), 200)
			r := s.call("create_card_upgrade_rule", rule(s.cardType, to["id"], "single_recharge", "100.00"), 200)
			in := s.recharge(card["id"])
			in["principal"] = amount
			out := s.call("recharge_card", in, 200)
			if amount == "99.99" {
				if out["card_type_id"] != s.cardType || out["upgraded_to"] != nil {
					t.Fatal(out)
				}
				return
			}
			if out["card_type_id"] != to["id"] || out["upgraded_to"].(map[string]any)["id"] != to["id"] {
				t.Fatal(out)
			}
			assertSQL(t, s, `SELECT count(*)=1 AND bool_and(u.trigger='auto' AND u.rule_id=$2 AND l.kind='recharge' AND l.principal_delta=100) FROM card_upgrades u JOIN card_ledger l ON l.id=u.ledger_id WHERE u.card_id=$1`, card["id"], r["id"])
		})
	}
}
func TestAutoUpgradeInactiveRule(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	to := s.call("create_card_type", typeInput("高级"), 200)
	r := s.call("create_card_upgrade_rule", rule(s.cardType, to["id"], "single_recharge", "0.50"), 200)
	s.call("update_card_upgrade_rule", map[string]any{"id": r["id"], "threshold": "0.50", "active": false}, 200)
	out := s.call("recharge_card", s.recharge(card["id"]), 200)
	if out["card_type_id"] != s.cardType || out["upgraded_to"] != nil {
		t.Fatal(out)
	}
	assertSQL(t, s, `SELECT NOT EXISTS(SELECT 1 FROM card_upgrades)`)
}
func TestAutoUpgradeInactiveTarget(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	to := s.call("create_card_type", typeInput("高级"), 200)
	s.call("create_card_upgrade_rule", rule(s.cardType, to["id"], "single_recharge", "0.50"), 200)
	in := typeInput("高级")
	in["id"], in["active"] = to["id"], false
	s.call("update_card_type", in, 200)
	out := s.call("recharge_card", s.recharge(card["id"]), 200)
	if out["card_type_id"] != s.cardType || out["principal"] != "1235.06" || out["upgraded_to"] != nil {
		t.Fatal(out)
	}
}
func TestAutoUpgradeTotal(t *testing.T) {
	s := open(t)
	opening := s.opening()
	opening["principal"], opening["bonus"] = "10.00", "0.00"
	card := s.call("open_card", opening, 200)
	middle := s.call("create_card_type", typeInput("中级"), 200)
	to := s.call("create_card_type", typeInput("高级"), 200)
	in := upgrade(card["id"], middle["id"])
	in["principal"], in["sales_staff_ids"] = "20.00", []any{s.Res.OwnerID}
	s.call("upgrade_card", in, 200)
	s.call("create_card_upgrade_rule", rule(middle["id"], to["id"], "total_recharge", "40.00"), 200)
	s.call("adjust_card", map[string]any{"card_id": card["id"], "principal": "1000.00", "reason": "更正不算充值"}, 200)
	other := s.call("open_card", s.opening(), 200)
	tr := transfer(other["id"], card["id"])
	tr["bonus"] = "0.00"
	s.call("transfer_card_balance", tr, 200)
	rc := s.recharge(card["id"])
	rc["principal"], rc["bonus"] = "5.00", "0.00"
	out := s.call("recharge_card", rc, 200)
	if out["card_type_id"] != middle["id"] || out["upgraded_to"] != nil {
		t.Fatal(out)
	}
	out = s.call("recharge_card", rc, 200)
	if out["card_type_id"] != to["id"] {
		t.Fatal(out)
	}
	assertSQL(t, s, `SELECT count(*)=2 FROM card_upgrades WHERE card_id=$1`, card["id"])
}
func TestAutoUpgradeDeterministic(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	a := s.call("create_card_type", typeInput("A"), 200)
	b := s.call("create_card_type", typeInput("B"), 200)
	c := s.call("create_card_type", typeInput("C"), 200)
	d := s.call("create_card_type", typeInput("D"), 200)
	for _, r := range []map[string]any{rule(s.cardType, c["id"], "single_recharge", "0.10"), rule(s.cardType, a["id"], "single_recharge", "0.50"), rule(s.cardType, b["id"], "single_recharge", "0.50"), rule(b["id"], d["id"], "single_recharge", "0.10")} {
		s.call("create_card_upgrade_rule", r, 200)
	}
	out := s.call("recharge_card", s.recharge(card["id"]), 200)
	if out["card_type_id"] != b["id"] {
		t.Fatal(out)
	}
	assertSQL(t, s, `SELECT count(*)=1 FROM card_upgrades WHERE card_id=$1`, card["id"])
}
func TestUpgradeTransferPermissionsAndAI(t *testing.T) {
	s := open(t)
	a := s.call("open_card", s.opening(), 200)
	b := s.call("open_card", s.opening(), 200)
	to := s.call("create_card_type", typeInput("高级"), 200)
	r := s.call("create_card_upgrade_rule", rule(s.cardType, to["id"], "single_recharge", "100.00"), 200)
	before := upgradeSnapshot(t, s)
	for _, tc := range []struct {
		op string
		in any
		p  ops.Permission
	}{
		{"upgrade_card", upgrade(a["id"], to["id"]), perm.CardSell},
		{"transfer_card_balance", transfer(a["id"], b["id"]), perm.CardAdjust},
		{"create_card_upgrade_rule", rule(s.cardType, to["id"], "total_recharge", "1.00"), perm.SettingsManage},
		{"update_card_upgrade_rule", map[string]any{"id": r["id"], "threshold": "2.00", "active": false}, perm.SettingsManage},
	} {
		t.Run(tc.op, func(t *testing.T) {
			caller := s.MustCaller(s.Owner)
			caller.Permissions = perm.All()
			delete(caller.Permissions, tc.p)
			if got := s.HTTP(t, &caller, tc.op, tc.in); got.Status != 403 || got.Detail() != "permission denied" {
				t.Fatal(got)
			}
			if got := s.AI(t, s.MustCaller(s.Owner), tc.op, tc.in); got.Status == 200 || got.Detail() != "operation requires a draft confirmed by a person" {
				t.Fatal(got)
			}
			if upgradeSnapshot(t, s) != before {
				t.Fatal("refused operation changed rows")
			}
		})
	}
	for _, ch := range s.Channels() {
		caller := s.MustCaller(s.Owner)
		delete(caller.Permissions, perm.CustomerView)
		r := ch.Call(t, caller, "list_card_upgrades", map[string]any{"card_id": a["id"]})
		if r.Status != 403 || r.Detail() != "permission denied" {
			t.Fatal(r)
		}
	}
}
func TestUpgradeSerializes(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	to := s.call("create_card_type", typeInput("高级"), 200)
	held := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, held, "SELECT id FROM cards WHERE id=$1 FOR NO KEY UPDATE", card["id"])
	done := make(chan optest.Result, 1)
	in := upgrade(card["id"], to["id"])
	in["principal"], in["sales_staff_ids"] = "1.00", []any{s.Res.OwnerID}
	go func() { done <- s.HTTPWithToken(t, s.Owner, "upgrade_card", in) }()
	dbtest.WaitForLockWait(t, s.Pool)
	dbtest.Exec(t, held, `INSERT INTO card_ledger (tenant_id,store_id,card_id,kind,principal_before,principal_delta,principal_after,bonus_before,bonus_delta,bonus_after,business_date,actor_id) VALUES ($1,$2,$3,'recharge',1234.56,10,1244.56,0.50,0,0.50,current_date,$4)`, s.Res.TenantID, s.Res.StoreID, card["id"], s.Res.OwnerID)
	dbtest.Commit(t, held)
	r := <-done
	if r.Status != 200 || r.Body["principal"] != "1245.56" {
		t.Fatal(r)
	}
}
func TestUpgradeRuleDatabaseProtections(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	to := s.call("create_card_type", typeInput("高级"), 200)
	r := s.call("create_card_upgrade_rule", rule(s.cardType, to["id"], "single_recharge", "1.00"), 200)
	for _, tc := range []struct {
		sql, code string
		args      []any
	}{
		{"UPDATE card_upgrades SET trigger='manual'", "42501", nil}, {"DELETE FROM card_upgrades", "42501", nil},
		{"UPDATE card_transfers SET reason='改'", "42501", nil}, {"DELETE FROM card_transfers", "42501", nil},
		{"DELETE FROM card_upgrade_rules", "42501", nil},
		{`INSERT INTO card_upgrade_rules (tenant_id,store_id,from_type_id,to_type_id,condition_kind,threshold) VALUES ($1,$2,$3,$3,'single_recharge',1)`, "23514", []any{s.Res.TenantID, s.Res.StoreID, s.cardType}},
		{`UPDATE card_upgrade_rules SET condition_kind='spending' WHERE id=$1`, "23514", []any{r["id"]}},
		{`UPDATE card_upgrade_rules SET threshold=0 WHERE id=$1`, "23514", []any{r["id"]}},
		{`INSERT INTO card_upgrades (tenant_id,store_id,card_id,type_before,type_after,trigger,business_date,actor_id) VALUES ($1,$2,$3,$4,$5,'invalid',current_date,$6)`, "23514", []any{s.Res.TenantID, s.Res.StoreID, card["id"], s.cardType, to["id"], s.Res.OwnerID}},
		{`INSERT INTO card_transfers (tenant_id,store_id,from_card_id,to_card_id,reason,from_ledger_id,to_ledger_id,business_date,actor_id) SELECT $1,$2,$3,$3,'同一卡',id,id,current_date,$4 FROM card_ledger WHERE card_id=$3 LIMIT 1`, "23514", []any{s.Res.TenantID, s.Res.StoreID, card["id"], s.Res.OwnerID}},
		{`INSERT INTO card_ledger (tenant_id,store_id,card_id,kind,principal_before,principal_delta,principal_after,bonus_before,bonus_delta,bonus_after,business_date,actor_id) VALUES ($1,$2,$3,'invalid',0,0,0,0,0,0,current_date,$4)`, "23514", []any{s.Res.TenantID, s.Res.StoreID, card["id"], s.Res.OwnerID}},
	} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), tc.sql, tc.args...)
		if e := tx.Rollback(context.Background()); e != nil {
			t.Fatal(e)
		}
		var pg *pgconn.PgError
		if !errors.As(err, &pg) || pg.Code != tc.code {
			t.Fatal(tc.sql, err)
		}
	}
}
func TestTransferBalance(t *testing.T) {
	s := open(t)
	a := s.call("open_card", s.opening(), 200)
	b := s.call("open_card", s.opening(), 200)
	out := s.call("transfer_card_balance", transfer(a["id"], b["id"]), 200)
	if out["from"].(map[string]any)["principal"] != "1224.56" || out["to"].(map[string]any)["bonus"] != "0.75" {
		t.Fatal(out)
	}
	for _, id := range []any{a["id"], b["id"]} {
		assertSQL(t, s, `WITH chain AS (SELECT *,lag(principal_after,1,0::numeric) OVER w p,lag(bonus_after,1,0::numeric) OVER w b FROM card_ledger WHERE card_id=$1 WINDOW w AS (ORDER BY at,id)) SELECT count(*)=2 AND bool_and(principal_before=p AND bonus_before=b) AND sum(principal_delta)=(array_agg(principal_after ORDER BY at DESC,id DESC))[1] AND sum(bonus_delta)=(array_agg(bonus_after ORDER BY at DESC,id DESC))[1] FROM chain`, id)
		for _, ch := range s.Channels() {
			r := ch.Call(t, s.MustCaller(s.Owner), "list_card_ledger", map[string]any{"card_id": id})
			if r.Status != 200 {
				t.Fatal(r)
			}
			entry := r.Body["entries"].([]any)[0].(map[string]any)
			want, other := "transfer_out", b["id"]
			if id == b["id"] {
				want, other = "transfer_in", a["id"]
			}
			if entry["kind"] != want || entry["reason"] != "核对转余额" || entry["other_card_id"] != other {
				t.Fatal(entry)
			}
		}
	}
	assertSQL(t, s, `SELECT count(*)=2 FROM audit_log WHERE action='transfer_card_balance'`)
}
func TestTransferInsufficientBalance(t *testing.T) {
	s := open(t)
	a := s.call("open_card", s.opening(), 200)
	b := s.call("open_card", s.opening(), 200)
	before := upgradeSnapshot(t, s)
	for _, key := range []string{"principal", "bonus"} {
		in := transfer(a["id"], b["id"])
		in[key] = "9999.00"
		r := s.call("transfer_card_balance", in, 409)
		if r["detail"] != "余额不足" {
			t.Fatal(r)
		}
	}
	if upgradeSnapshot(t, s) != before {
		t.Fatal("insufficient balance wrote rows")
	}
}
func TestTransferRules(t *testing.T) {
	s := open(t)
	a := s.call("open_card", s.opening(), 200)
	b := s.call("open_card", s.opening(), 200)
	if r := s.call("transfer_card_balance", transfer(a["id"], a["id"]), 409); r["detail"] != "不能向同一卡转余额" {
		t.Fatal(r)
	}
	in := transfer(a["id"], b["id"])
	in["principal"], in["bonus"] = "0.00", "0.00"
	if r := s.call("transfer_card_balance", in, 409); r["detail"] != "转余额合计须大于零" {
		t.Fatal(r)
	}
	in = transfer(a["id"], b["id"])
	in["reason"] = "   "
	if r := s.call("transfer_card_balance", in, 409); r["detail"] != "原因须为一到二百字" {
		t.Fatal(r)
	}
	in["reason"] = strings.Repeat("因", 201)
	s.call("transfer_card_balance", in, 422)
	ct := s.countType("count")
	opening := s.opening()
	opening["card_type_id"], opening["principal"], opening["bonus"] = ct["id"], "0.00", "0.00"
	c := s.call("open_card", opening, 200)
	if r := s.call("transfer_card_balance", transfer(a["id"], c["id"]), 409); r["detail"] != "次卡不能接收余额" {
		t.Fatal(r)
	}
	s.call("adjust_card", map[string]any{"card_id": b["id"], "principal": "99999999.99", "reason": "上限"}, 200)
	if r := s.call("transfer_card_balance", transfer(a["id"], b["id"]), 409); r["detail"] != "卡余额超过上限" {
		t.Fatal(r)
	}
	s.call("adjust_card", map[string]any{"card_id": b["id"], "principal": "0.00", "bonus": "99999999.99", "reason": "赠送上限"}, 200)
	if r := s.call("transfer_card_balance", transfer(a["id"], b["id"]), 409); r["detail"] != "卡余额超过上限" {
		t.Fatal(r)
	}
	assertSQL(t, s, `SELECT NOT EXISTS(SELECT 1 FROM card_transfers)`)
}
func TestTransferScope(t *testing.T) {
	s := open(t)
	a := s.call("open_card", s.opening(), 200)
	other := s.call("create_customer", map[string]any{"name": "另一位"}, 200)["customer"].(map[string]any)["id"]
	in := s.opening()
	in["customer_id"] = other
	b := s.call("open_card", in, 200)
	caller := s.MustCaller(s.Owner)
	caller.Permissions = perm.All()
	caller.Permissions[perm.CustomerView] = ops.Grant{Scope: ops.ScopeSelf}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "UPDATE customers SET responsible_staff_id=$2 WHERE id=$1", s.customer, caller.StaffID)
	dbtest.Commit(t, tx)
	before := upgradeSnapshot(t, s)
	for _, in := range []map[string]any{transfer(a["id"], b["id"]), transfer(b["id"], a["id"])} {
		if r := s.HTTP(t, &caller, "transfer_card_balance", in); r.Status != 404 {
			t.Fatal(r)
		}
	}
	if upgradeSnapshot(t, s) != before {
		t.Fatal("hidden customer transfer wrote rows")
	}
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "UPDATE customers SET responsible_staff_id=$2 WHERE id=$1", other, caller.StaffID)
	dbtest.Commit(t, tx)
	if r := s.HTTP(t, &caller, "transfer_card_balance", transfer(a["id"], b["id"])); r.Status != 200 {
		t.Fatal(r)
	}
}
func TestUpgradeScopeAndAudit(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	to := s.call("create_card_type", typeInput("高级"), 200)
	caller := s.MustCaller(s.Owner)
	caller.Permissions = perm.All()
	caller.Permissions[perm.CustomerView] = ops.Grant{Scope: ops.ScopeSelf}
	before := upgradeSnapshot(t, s)
	if r := s.HTTP(t, &caller, "upgrade_card", upgrade(card["id"], to["id"])); r.Status != 404 {
		t.Fatal(r)
	}
	if upgradeSnapshot(t, s) != before {
		t.Fatal("hidden card upgraded")
	}
	s.call("upgrade_card", upgrade(card["id"], to["id"]), 200)
	for _, ch := range s.Channels() {
		if r := ch.Call(t, caller, "list_card_upgrades", map[string]any{"card_id": card["id"]}); r.Status != 404 {
			t.Fatal(r)
		}
		r := ch.Call(t, caller, "list_audit_log", map[string]any{"target_id": card["id"]})
		if r.Status != 200 || len(r.Body["entries"].([]any)) != 0 {
			t.Fatal(r)
		}
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "UPDATE customers SET responsible_staff_id=$2 WHERE id=$1", s.customer, caller.StaffID)
	dbtest.Commit(t, tx)
	for _, ch := range s.Channels() {
		r := ch.Call(t, caller, "list_card_upgrades", map[string]any{"card_id": card["id"]})
		if r.Status != 200 || len(r.Body["entries"].([]any)) != 1 {
			t.Fatal(r)
		}
	}
}
func TestUpgradeTransferStore(t *testing.T) {
	s := open(t)
	a := s.call("open_card", s.opening(), 200)
	b := s.call("open_card", s.opening(), 200)
	staff, token := s.Hire("二店", "002", "second", s.Role("老板"))
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var store uuid.UUID
	if err := tx.QueryRow(context.Background(), "INSERT INTO stores (tenant_id,name) VALUES ($1,'二店') RETURNING id", s.Res.TenantID).Scan(&store); err != nil {
		t.Fatal(err)
	}
	dbtest.Exec(t, tx, "UPDATE staff SET store_id=$2 WHERE id=$1", staff, store)
	dbtest.Commit(t, tx)
	to := s.call("create_card_type", typeInput("高级"), 200)
	if r := s.HTTPWithToken(t, token, "upgrade_card", upgrade(a["id"], to["id"])); r.Status != 404 {
		t.Fatal(r)
	}
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(token), "list_card_upgrades", map[string]any{"card_id": a["id"]})
		if r.Status != 404 {
			t.Fatal(r)
		}
		r = ch.Call(t, s.MustCaller(token), "list_card_upgrade_rules", map[string]any{})
		if r.Status != 200 || len(r.Body["items"].([]any)) != 0 {
			t.Fatal(r)
		}
	}
	// 同租户外店的单张卡也不能作为转出或转入卡。
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "UPDATE cards SET store_id=$2 WHERE id=$1", b["id"], store)
	dbtest.Commit(t, tx)
	for _, in := range []map[string]any{transfer(a["id"], b["id"]), transfer(b["id"], a["id"])} {
		s.call("transfer_card_balance", in, 404)
	}
}
func waitCardWaiters(t *testing.T, s *fixture, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		err := s.Pool.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%cards%'`).Scan(&count)
		if err != nil {
			t.Fatal(err)
		}
		if count >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("card requests did not reach lock wait")
}
func resultWithin(t *testing.T, done <-chan optest.Result) {
	t.Helper()
	select {
	case r := <-done:
		if r.Status != 200 {
			t.Fatal(r)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("card operation timeout")
	}
}
func TestOppositeTransfersOrderedLocks(t *testing.T) {
	s := open(t)
	a := s.call("open_card", s.opening(), 200)
	b := s.call("open_card", s.opening(), 200)
	low, high := a["id"].(string), b["id"].(string)
	if low > high {
		low, high = high, low
	}
	held := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, held, "SELECT id FROM cards WHERE id=$1 FOR NO KEY UPDATE", low)
	done := make(chan optest.Result, 2)
	// 低→高先排队；突变的高→低会持高卡排在它后面，释放低卡后形成死锁。
	go func() { done <- s.HTTPWithToken(t, s.Owner, "transfer_card_balance", transfer(low, high)) }()
	waitCardWaiters(t, s, 1)
	go func() { done <- s.HTTPWithToken(t, s.Owner, "transfer_card_balance", transfer(high, low)) }()
	waitCardWaiters(t, s, 2)
	dbtest.Commit(t, held)
	resultWithin(t, done)
	resultWithin(t, done)
}
func TestMergeTransferOrderedLocks(t *testing.T) {
	s := open(t)
	a := s.call("open_card", s.opening(), 200)
	b := s.call("open_card", s.opening(), 200)
	low, high := a["id"].(string), b["id"].(string)
	if low > high {
		low, high = high, low
	}
	keep := s.call("create_customer", map[string]any{}, 200)["customer"].(map[string]any)["id"]
	// 改动索引列再改回，避免 HOT 保留低卡的旧索引位置，使堆扫描及顾客索引都先遇高卡。
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "UPDATE cards SET customer_id=$2 WHERE id=$1", low, keep)
	dbtest.Commit(t, tx)
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "UPDATE cards SET customer_id=$2 WHERE id=$1", low, s.customer)
	dbtest.Commit(t, tx)
	assertSQL(t, s, `SELECT (array_agg(id))[1]=$2 FROM cards WHERE customer_id=$1`, s.customer, high)
	held := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, held, "SELECT id FROM cards WHERE id=$1 FOR NO KEY UPDATE", low)
	done := make(chan optest.Result, 2)
	// 转余额先排低卡；无预锁的合并持高卡排在后面，释放低卡后形成死锁。
	go func() { done <- s.HTTPWithToken(t, s.Owner, "transfer_card_balance", transfer(low, high)) }()
	waitCardWaiters(t, s, 1)
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "merge_customers", map[string]any{"keep_id": keep, "merged_id": s.customer})
	}()
	waitCardWaiters(t, s, 2)
	dbtest.Commit(t, held)
	resultWithin(t, done)
	resultWithin(t, done)
	assertSQL(t, s, `SELECT count(*)=2 AND bool_and(customer_id=$1) FROM cards`, keep)
}
func TestUpgradeRuleAuditTarget(t *testing.T) {
	s := open(t)
	to := s.call("create_card_type", typeInput("高级"), 200)
	r := s.call("create_card_upgrade_rule", rule(s.cardType, to["id"], "single_recharge", "1.00"), 200)
	assertSQL(t, s, `SELECT count(*)=1 AND bool_and(target_kind='card_upgrade_rule' AND customer_id IS NULL) FROM audit_log WHERE target_id=$1`, r["id"])
}
func TestUpgradeReferenceLocks(t *testing.T) {
	for _, tc := range []struct {
		kind       string
		wantDetail string
	}{{"staff", "业绩归属人必须是本店在职员工"}, {"target", "目标须为启用的其他储值卡类型"}} {
		kind := tc.kind
		t.Run(kind, func(t *testing.T) {
			s := open(t)
			card := s.call("open_card", s.opening(), 200)
			to := s.call("create_card_type", typeInput("高级"), 200)
			in := upgrade(card["id"], to["id"])
			held := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			if kind == "target" {
				dbtest.Exec(t, held, "UPDATE card_types SET active=false WHERE id=$1", to["id"])
			} else {
				id, _ := s.Hire("员工", "002", "seller")
				in["principal"], in["sales_staff_ids"] = "1.00", []any{id}
				dbtest.Exec(t, held, "UPDATE staff SET active=false,deactivated_at=clock_timestamp() WHERE id=$1", id)
			}
			done := make(chan optest.Result, 1)
			go func() { done <- s.HTTPWithToken(t, s.Owner, "upgrade_card", in) }()
			dbtest.WaitForLockWait(t, s.Pool)
			dbtest.Commit(t, held)
			if r := <-done; r.Status != 409 || r.Detail() != tc.wantDetail {
				t.Fatal(r)
			}
		})
	}
}
func TestUpgradeRulesConcurrentCycle(t *testing.T) {
	s := open(t)
	to := s.call("create_card_type", typeInput("高级"), 200)
	held := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, held, "SELECT pg_advisory_xact_lock(hashtextextended($1::text,130031))", s.Res.StoreID.String())
	done := make(chan optest.Result, 2)
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "create_card_upgrade_rule", rule(s.cardType, to["id"], "single_recharge", "1.00"))
	}()
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "create_card_upgrade_rule", rule(to["id"], s.cardType, "single_recharge", "1.00"))
	}()
	dbtest.WaitForLockWait(t, s.Pool)
	dbtest.Commit(t, held)
	counts := map[int]int{}
	for range 2 {
		select {
		case r := <-done:
			counts[r.Status]++
			if r.Status == 409 && r.Detail() != "升级规则不能形成环路" {
				t.Fatal(r)
			}
		case <-time.After(8 * time.Second):
			t.Fatal("rule operation timeout")
		}
	}
	if fmt.Sprint(counts) != "map[200:1 409:1]" {
		t.Fatal(counts)
	}
}

func (s *fixture) countType(kind string) map[string]any {
	s.t.Helper()
	return s.call("create_card_type", countType("次数类型", kind, countItem(s.item("次数项目"))), 200)
}

func TestUpgradeRuleRetainsStoredValueTypes(t *testing.T) {
	s := open(t)
	to := s.call("create_card_type", typeInput("高级"), 200)
	s.call("create_card_upgrade_rule", rule(s.cardType, to["id"], "single_recharge", "1.00"), 200)
	for _, id := range []any{s.cardType, to["id"]} {
		in := countType("不能换种类", "package", countItem(s.item(fmt.Sprint(id))))
		in["id"], in["active"] = id, true
		if r := s.call("update_card_type", in, 409); r["detail"] != "升级规则引用的卡类型不能改变种类" {
			t.Fatal(r)
		}
	}
}
func TestUpgradeRuleReferenceLocks(t *testing.T) {
	for _, target := range []bool{false, true} {
		t.Run(fmt.Sprint(target), func(t *testing.T) {
			s := open(t)
			to := s.call("create_card_type", typeInput("高级"), 200)
			held := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			id := any(s.cardType)
			if target {
				id = to["id"]
			}
			dbtest.Exec(t, held, "SELECT id FROM card_types WHERE id=$1 FOR NO KEY UPDATE", id)
			done := make(chan optest.Result, 1)
			go func() {
				done <- s.HTTPWithToken(t, s.Owner, "create_card_upgrade_rule", rule(s.cardType, to["id"], "single_recharge", "1.00"))
			}()
			dbtest.WaitForLockWait(t, s.Pool)
			if target {
				dbtest.Exec(t, held, "UPDATE card_types SET active=false WHERE id=$1", id)
			}
			dbtest.Commit(t, held)
			r := <-done
			want := 200
			if target {
				want = 409
			}
			if r.Status != want || want == 409 && r.Detail() != "目标卡类型已停用" {
				t.Fatal(r)
			}
		})
	}
}
func TestAutoUpgradeTargetLock(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	to := s.call("create_card_type", typeInput("高级"), 200)
	s.call("create_card_upgrade_rule", rule(s.cardType, to["id"], "single_recharge", "0.50"), 200)
	held := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, held, "UPDATE card_types SET active=false WHERE id=$1", to["id"])
	done := make(chan optest.Result, 1)
	go func() { done <- s.HTTPWithToken(t, s.Owner, "recharge_card", s.recharge(card["id"])) }()
	dbtest.WaitForLockWait(t, s.Pool)
	dbtest.Commit(t, held)
	r := <-done
	if r.Status != 200 || r.Body["card_type_id"] != s.cardType || r.Body["upgraded_to"] != nil {
		t.Fatal(r)
	}
}
func TestTransferAuditScope(t *testing.T) {
	s := open(t)
	a := s.call("open_card", s.opening(), 200)
	other := s.call("create_customer", map[string]any{"name": "另一位"}, 200)["customer"].(map[string]any)["id"]
	in := s.opening()
	in["customer_id"] = other
	b := s.call("open_card", in, 200)
	s.call("transfer_card_balance", transfer(a["id"], b["id"]), 200)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "UPDATE customers SET responsible_staff_id=$2 WHERE id=$1", s.customer, s.Res.OwnerID)
	dbtest.Commit(t, tx)
	caller := s.MustCaller(s.Owner)
	caller.Permissions = perm.All()
	caller.Permissions[perm.CustomerView] = ops.Grant{Scope: ops.ScopeSelf}
	for _, ch := range s.Channels() {
		for _, id := range []any{a["id"], b["id"]} {
			r := ch.Call(t, caller, "list_audit_log", map[string]any{"target_id": id})
			if r.Status != 200 {
				t.Fatal(r)
			}
			count := 0
			for _, e := range r.Body["entries"].([]any) {
				if e.(map[string]any)["action"] == "transfer_card_balance" {
					count++
				}
			}
			want := 0
			if id == a["id"] {
				want = 1
			}
			if count != want {
				t.Fatal(r)
			}
		}
	}
}
func TestUpgradeRuleStore(t *testing.T) {
	s := open(t)
	to := s.call("create_card_type", typeInput("高级"), 200)
	r := s.call("create_card_upgrade_rule", rule(s.cardType, to["id"], "single_recharge", "1.00"), 200)
	staff, token := s.Hire("二店", "002", "second", s.Role("老板"))
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var store uuid.UUID
	if err := tx.QueryRow(context.Background(), "INSERT INTO stores (tenant_id,name) VALUES ($1,'二店') RETURNING id", s.Res.TenantID).Scan(&store); err != nil {
		t.Fatal(err)
	}
	dbtest.Exec(t, tx, "UPDATE staff SET store_id=$2 WHERE id=$1", staff, store)
	dbtest.Commit(t, tx)
	for _, tc := range []struct {
		op string
		in any
	}{
		{"create_card_upgrade_rule", rule(s.cardType, to["id"], "total_recharge", "2.00")},
		{"update_card_upgrade_rule", map[string]any{"id": r["id"], "threshold": "2.00", "active": false}},
	} {
		if got := s.HTTPWithToken(t, token, tc.op, tc.in); got.Status != 404 {
			t.Fatal(got)
		}
	}
}

func TestTransferLocksAscending(t *testing.T) {
	s := open(t)
	a := s.call("open_card", s.opening(), 200)
	b := s.call("open_card", s.opening(), 200)
	low, high := a["id"].(string), b["id"].(string)
	if low > high {
		low, high = high, low
	}
	held := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, held, "SELECT id FROM cards WHERE id=$1 FOR NO KEY UPDATE", low)
	done := make(chan optest.Result, 1)
	go func() { done <- s.HTTPWithToken(t, s.Owner, "transfer_card_balance", transfer(high, low)) }()
	waitCardWaiters(t, s, 1)
	// 等低卡时不能已持有高卡，否则会与按升序锁卡的顾客合并发生反向等待。
	probe := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, lockErr := probe.Exec(context.Background(), "SELECT id FROM cards WHERE id=$1 FOR NO KEY UPDATE NOWAIT", high)
	dbtest.Rollback(t, probe)
	dbtest.Commit(t, held)
	optest.WaitResult(t, done, 200)
	if lockErr != nil {
		t.Fatal("transfer locked the higher UUID before the lower UUID", lockErr)
	}
}
