package cards_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func (s *fixture) pointsRate(rate int) {
	s.call("change_setting", map[string]any{"key": "points_per_recharge_yuan", "value": rate, "effective_from": settings.Date(time.Now()).Format(time.DateOnly)}, 200)
}
func pointRows(t *testing.T, s *fixture, id any) int {
	t.Helper()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var n int
	err := tx.QueryRow(context.Background(), "SELECT count(*) FROM card_points_ledger WHERE card_id=$1", id).Scan(&n)
	if rollback := tx.Rollback(context.Background()); rollback != nil {
		t.Fatal(rollback)
	}
	if err != nil {
		t.Fatal(err)
	}
	return n
}
func TestPointsEarningRates(t *testing.T) {
	for _, tc := range []struct {
		rate             int
		principal, bonus string
		want             float64
		rows             int
	}{
		{0, "1234.56", "1234.56", 0, 0}, {1, "12.99", "999.99", 12, 1}, {10, "19.99", "999.99", 1, 1}, {10, "9.99", "999.99", 0, 0}, {100000, "999999.99", "0.00", 9, 1}, {1, "0.00", "1000.00", 0, 0},
	} {
		t.Run(tc.principal+"/"+tc.bonus+"/"+time.Duration(tc.rate).String(), func(t *testing.T) {
			s := open(t)
			s.pointsRate(tc.rate)
			in := s.opening()
			in["principal"], in["bonus"] = tc.principal, tc.bonus
			card := s.call("open_card", in, 200)
			if card["points"] != tc.want {
				t.Fatal(card)
			}
			in = s.recharge(card["id"])
			in["principal"], in["bonus"] = tc.principal, tc.bonus
			out := s.call("recharge_card", in, 200)
			if out["points"] != 2*tc.want || pointRows(t, s, card["id"]) != 2*tc.rows {
				t.Fatal(out)
			}
			for _, ch := range s.Channels() {
				r := ch.Call(t, s.MustCaller(s.Owner), "list_customer_cards", map[string]any{"customer_id": s.customer})
				if r.Status != 200 || r.Body["items"].([]any)[0].(map[string]any)["points"] != 2*tc.want {
					t.Fatal(ch.Name, r)
				}
			}
		})
	}
}

func TestPointsAdjustAndChain(t *testing.T) {
	s := open(t)
	s.pointsRate(10)
	card := s.call("open_card", s.opening(), 200)
	top := s.recharge(card["id"])
	top["principal"] = "25.99"
	if out := s.call("recharge_card", top, 200); out["points"] != float64(125) {
		t.Fatal(out)
	}
	for _, target := range []int{500, 2, 0} {
		out := s.call("adjust_card", map[string]any{"card_id": card["id"], "points": target, "reason": "核对积分"}, 200)
		if out["points"] != float64(target) {
			t.Fatal(out)
		}
	}
	if r := s.call("adjust_card", map[string]any{"card_id": card["id"], "points": 0, "reason": "无改动"}, 409); r["detail"] != "调整单没有任何改动" {
		t.Fatal(r)
	}
	for _, target := range []any{-1, 100000000, 1.5} {
		s.call("adjust_card", map[string]any{"card_id": card["id"], "points": target, "reason": "无效"}, 422)
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var valid bool
	err := tx.QueryRow(context.Background(), `WITH chain AS (SELECT *,lag(after,1,0) OVER (ORDER BY at,id) previous FROM card_points_ledger WHERE card_id=$1) SELECT count(*)=5 AND bool_and(before=previous AND after=before+delta) AND sum(delta)=(array_agg(after ORDER BY at DESC,id DESC))[1] AND bool_and(CASE WHEN kind='adjust' THEN adjustment_id IS NOT NULL ELSE card_ledger_id IS NOT NULL END) AND (SELECT bool_and(points_changed) FROM card_adjustments WHERE card_id=$1) FROM chain`, card["id"]).Scan(&valid)
	if rollback := tx.Rollback(context.Background()); rollback != nil {
		t.Fatal(rollback)
	}
	if err != nil || !valid {
		t.Fatal(valid, err)
	}
	for _, ch := range s.Channels() {
		caller := s.MustCaller(s.Owner)
		first := ch.Call(t, caller, "list_card_points", map[string]any{"card_id": card["id"], "limit": 2})
		if first.Status != 200 || len(first.Body["entries"].([]any)) != 2 {
			t.Fatal(first)
		}
		rows := first.Body["entries"].([]any)
		entry := rows[0].(map[string]any)
		if entry["kind"] != "adjust" || entry["before"] != float64(2) || entry["delta"] != float64(-2) || entry["after"] != float64(0) || entry["reason"] != "核对积分" || entry["actor"].(map[string]any)["name"] != "王老板" || entry["business_date"] != settings.Date(time.Now()).Format(time.DateOnly) {
			t.Fatal(entry)
		}
		second := ch.Call(t, caller, "list_card_points", map[string]any{"card_id": card["id"], "limit": 2, "before_id": rows[1].(map[string]any)["id"]})
		if second.Status != 200 || len(second.Body["entries"].([]any)) != 2 || second.Body["entries"].([]any)[0].(map[string]any)["after"] != float64(500) {
			t.Fatal(second)
		}
		last := ch.Call(t, caller, "list_card_points", map[string]any{"card_id": card["id"], "before_id": second.Body["entries"].([]any)[1].(map[string]any)["id"]})
		if last.Status != 200 || len(last.Body["entries"].([]any)) != 1 || last.Body["entries"].([]any)[0].(map[string]any)["kind"] != "open" {
			t.Fatal(last)
		}
	}
}

func TestPointsSalesOnly(t *testing.T) {
	s := open(t)
	s.pointsRate(10)
	card := s.call("open_card", s.opening(), 200)
	ct := s.call("create_card_type", typeInput("升级目标"), 200)
	out := s.call("upgrade_card", map[string]any{"card_id": card["id"], "to_type_id": ct["id"], "principal": "25.99", "bonus": "999.00", "sales_staff_ids": []any{s.Res.OwnerID}}, 200)
	if out["points"] != float64(125) {
		t.Fatal(out)
	}
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "list_card_points", map[string]any{"card_id": card["id"]})
		if r.Status != 200 {
			t.Fatal(ch.Name, r)
		}
		entry := r.Body["entries"].([]any)[0].(map[string]any)
		if entry["kind"] != "upgrade" || entry["delta"] != float64(2) {
			t.Fatal(entry)
		}
	}
	destination := s.call("open_card", s.opening(), 200)
	s.call("transfer_card_balance", map[string]any{"from_card_id": card["id"], "to_card_id": destination["id"], "principal": "100.00", "bonus": "10.00", "reason": "归集"}, 200)
	s.call("adjust_card", map[string]any{"card_id": card["id"], "principal": "5000.00", "bonus": "5000.00", "reason": "更正余额"}, 200)
	if pointRows(t, s, card["id"]) != 2 || pointRows(t, s, destination["id"]) != 1 {
		t.Fatal("transfer/adjust earned points")
	}
}

func TestPointsScope(t *testing.T) {
	s := open(t)
	s.pointsRate(1)
	card := s.call("open_card", s.opening(), 200)
	caller := s.MustCaller(s.Owner)
	caller.Permissions = perm.All()
	caller.Permissions[perm.CustomerView] = ops.Grant{Scope: ops.ScopeSelf}
	for _, own := range []bool{false, true} {
		if own {
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			dbtest.Exec(t, tx, "UPDATE customers SET responsible_staff_id=$2 WHERE id=$1", s.customer, caller.StaffID)
			dbtest.Commit(t, tx)
		}
		for _, ch := range s.Channels() {
			r := ch.Call(t, caller, "list_card_points", map[string]any{"card_id": card["id"]})
			want := 404
			if own {
				want = 200
			}
			if r.Status != want {
				t.Fatal(ch.Name, r)
			}
		}
	}
	delete(caller.Permissions, perm.CustomerView)
	for _, ch := range s.Channels() {
		if r := ch.Call(t, caller, "list_card_points", map[string]any{"card_id": card["id"]}); r.Status != 403 || r.Detail() != "permission denied" {
			t.Fatal(r)
		}
	}
}

func TestPointsStoreScope(t *testing.T) {
	s := open(t)
	s.pointsRate(1)
	card := s.call("open_card", s.opening(), 200)
	caller := s.MustCaller(s.Owner)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var other uuid.UUID
	if err := tx.QueryRow(context.Background(), "INSERT INTO stores (tenant_id,name) VALUES ($1,'外店') RETURNING id", s.Res.TenantID).Scan(&other); err != nil {
		t.Fatal(err)
	}
	otherCard := uuid.New()
	dbtest.Exec(t, tx, "INSERT INTO cards (tenant_id,store_id,id,customer_id,card_type_id,opened_at,created_by) SELECT tenant_id,$2,$3,customer_id,card_type_id,opened_at,created_by FROM cards WHERE id=$1", card["id"], other, otherCard)
	dbtest.Commit(t, tx)
	caller.StoreID = other
	for _, ch := range s.Channels() {
		if r := ch.Call(t, s.MustCaller(s.Owner), "list_card_points", map[string]any{"card_id": otherCard}); r.Status != 404 {
			t.Fatal(ch.Name, r)
		}
		if r := ch.Call(t, caller, "list_card_points", map[string]any{"card_id": card["id"]}); r.Status != 404 {
			t.Fatal(ch.Name, r)
		}
	}
}

func TestPointsRechargeWaitsForCardLock(t *testing.T) {
	s := open(t)
	s.pointsRate(1)
	card := s.call("open_card", s.opening(), 200)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "SELECT id FROM cards WHERE id=$1 FOR NO KEY UPDATE", card["id"])
	top := s.recharge(card["id"])
	top["principal"] = "10.00"
	done := make(chan optest.Result, 1)
	go func() { done <- s.HTTPWithToken(t, s.Owner, "recharge_card", top) }()
	dbtest.WaitForLockWait(t, s.Pool)
	dbtest.Exec(t, tx, `INSERT INTO card_points_ledger (tenant_id,store_id,card_id,kind,before,delta,after,business_date,actor_id) VALUES ($1,$2,$3,'adjust',1234,66,1300,current_date,$4)`, s.Res.TenantID, s.Res.StoreID, card["id"], s.Res.OwnerID)
	dbtest.Commit(t, tx)
	if r := <-done; r.Status != 200 || r.Body["points"] != float64(1310) {
		t.Fatal(r)
	}
	check := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var before int
	err := check.QueryRow(context.Background(), "SELECT before FROM card_points_ledger WHERE card_id=$1 ORDER BY at DESC,id DESC LIMIT 1", card["id"]).Scan(&before)
	if rollback := check.Rollback(context.Background()); rollback != nil {
		t.Fatal(rollback)
	}
	if err != nil || before != 1300 {
		t.Fatal(before, err)
	}
}

func TestPointsDatabaseProtections(t *testing.T) {
	s := open(t)
	s.pointsRate(1)
	card := s.call("open_card", s.opening(), 200)
	for _, sql := range []string{"UPDATE card_points_ledger SET after=after WHERE card_id=$1", "DELETE FROM card_points_ledger WHERE card_id=$1"} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), sql, card["id"])
		if rollback := tx.Rollback(context.Background()); rollback != nil {
			t.Fatal(rollback)
		}
		var pg *pgconn.PgError
		if !errors.As(err, &pg) || pg.Code != "42501" {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		kind                 string
		before, delta, after int
		constraint           string
	}{
		{"open", 0, 1, 2, "card_points_ledger_check"}, {"open", 0, -1, -1, "card_points_ledger_after_check"}, {"open", 0, 100000000, 100000000, "card_points_ledger_after_check"}, {"spend", 0, 1, 1, "card_points_ledger_kind_check"},
	} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), `INSERT INTO card_points_ledger (tenant_id,store_id,card_id,kind,before,delta,after,business_date,actor_id) VALUES ($1,$2,$3,$4,$5,$6,$7,current_date,$8)`, s.Res.TenantID, s.Res.StoreID, card["id"], tc.kind, tc.before, tc.delta, tc.after, s.Res.OwnerID)
		if rollback := tx.Rollback(context.Background()); rollback != nil {
			t.Fatal(rollback)
		}
		var pg *pgconn.PgError
		if !errors.As(err, &pg) || pg.Code != "23514" || pg.ConstraintName != tc.constraint {
			t.Fatal(tc, err)
		}
	}
}

func TestPointsWritesPermissionsAndAI(t *testing.T) {
	s := open(t)
	s.pointsRate(1)
	card := s.call("open_card", s.opening(), 200)
	caller := s.MustCaller(s.Owner)
	for _, tc := range []struct {
		op string
		in map[string]any
		p  ops.Permission
	}{
		{"open_card", s.opening(), perm.CardSell}, {"recharge_card", s.recharge(card["id"]), perm.CardSell}, {"adjust_card", map[string]any{"card_id": card["id"], "points": 10, "reason": "更正"}, perm.CardAdjust},
	} {
		before := snapshot(t, s)
		denied := caller
		denied.Permissions = perm.All()
		delete(denied.Permissions, tc.p)
		if r := s.HTTP(t, &denied, tc.op, tc.in); r.Status != 403 || r.Detail() != "permission denied" {
			t.Fatal(r)
		}
		if r := s.AI(t, caller, tc.op, tc.in); r.Status != 409 || r.Detail() != "operation requires a draft confirmed by a person" {
			t.Fatal(r)
		}
		if after := snapshot(t, s); after != before {
			t.Fatal("denied write changed rows")
		}
	}
}

func TestPointsLimitAndRollback(t *testing.T) {
	s := open(t)
	s.pointsRate(1)
	card := s.call("open_card", s.opening(), 200)
	s.call("adjust_card", map[string]any{"card_id": card["id"], "points": 99999999, "reason": "积分上限"}, 200)
	before := snapshot(t, s)
	in := s.recharge(card["id"])
	in["principal"] = "1.00"
	if r := s.call("recharge_card", in, 409); r["detail"] != "积分超过上限" {
		t.Fatal(r)
	}
	if after := snapshot(t, s); after != before {
		t.Fatal("overflow left ledger rows")
	}
}

func TestPointsFutureRate(t *testing.T) {
	s := open(t)
	s.pointsRate(10)
	s.call("change_setting", map[string]any{"key": "points_per_recharge_yuan", "value": 1, "effective_from": settings.Date(time.Now()).AddDate(0, 0, 1).Format(time.DateOnly)}, 200)
	card := s.call("open_card", s.opening(), 200)
	if card["points"] != float64(123) {
		t.Fatal("future rate affected opening", card)
	}
	top := s.recharge(card["id"])
	top["principal"] = "25.99"
	if out := s.call("recharge_card", top, 200); out["points"] != float64(125) {
		t.Fatal("future rate affected recharge", out)
	}
}

func TestCardTypeEditLocksItemsBeforeType(t *testing.T) {
	s := open(t)
	item := s.call("create_item", map[string]any{"name": "锁序项目", "price": "10.00", "discountable": true, "labor_fee_amount": nil, "labor_fee_percent": nil, "positions": []any{}, "level_prices": []any{}}, 200)
	in := map[string]any{"name": "套餐锁序", "kind": "package", "validity_kind": "none", "items": []any{map[string]any{"item_id": item["id"], "paid_count": 1, "gift_count": 0, "unit_price": "10.00"}}}
	ct := s.call("create_card_type", in, 200)
	in["id"], in["active"] = ct["id"], true
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "SELECT id FROM items WHERE id=$1 FOR NO KEY UPDATE", item["id"])
	done := make(chan optest.Result, 1)
	go func() { done <- s.HTTPWithToken(t, s.Owner, "update_card_type", in) }()
	dbtest.WaitForLockWait(t, s.Pool)
	// 编辑正在等项目时不能已持有类型写锁，否则与会员价的项目 → 类型路径形成环。
	dbtest.Exec(t, tx, "SET LOCAL lock_timeout='1s'")
	dbtest.Exec(t, tx, "SELECT id FROM card_types WHERE id=$1 FOR SHARE", ct["id"])
	dbtest.Commit(t, tx)
	if r := <-done; r.Status != 200 {
		t.Fatal(r)
	}
}
