package cards_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

func (s *fixture) item(name string) string {
	s.t.Helper()
	return s.call("create_item", map[string]any{"name": name, "price": "30.00", "discountable": true, "labor_fee_amount": nil, "labor_fee_percent": nil, "positions": []any{}, "level_prices": []any{}}, 200)["id"].(string)
}
func countItem(id string) map[string]any {
	return map[string]any{"item_id": id, "paid_count": 10, "gift_count": 3, "unit_price": "12.34"}
}
func countType(name, kind string, items ...map[string]any) map[string]any {
	return map[string]any{"name": name, "kind": kind, "validity_kind": "none", "items": items}
}
func (s *fixture) countCard(kind string) (map[string]any, string, map[string]any) {
	s.t.Helper()
	item := s.item("剪发")
	in := countType("次数卡", kind, countItem(item))
	ct := s.call("create_card_type", in, 200)
	opening := s.opening()
	opening["card_type_id"] = ct["id"]
	if kind == "count" {
		opening["principal"] = "0.00"
		opening["bonus"] = "0.00"
	}
	return s.call("open_card", opening, 200), item, in
}

func TestCardTypeItemRules(t *testing.T) {
	s := open(t)
	item := s.item("剪发")
	for i, tc := range []struct {
		kind   string
		items  []map[string]any
		status int
	}{
		{"stored_value", nil, 200}, {"stored_value", []map[string]any{countItem(item)}, 409},
		{"count", nil, 409}, {"package", nil, 409}, {"count", []map[string]any{countItem(item)}, 200}, {"package", []map[string]any{countItem(item)}, 200},
		{"count", []map[string]any{countItem(item), countItem(item)}, 409}, {"count", []map[string]any{countItem(uuid.NewString())}, 409},
	} {
		s.call("create_card_type", countType(fmt.Sprint("种类", i), tc.kind, tc.items...), tc.status)
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "UPDATE items SET active=false WHERE id=$1", item)
	dbtest.Commit(t, tx)
	s.call("create_card_type", countType("停用项目", "count", countItem(item)), 409)
	for _, id := range []string{item, uuid.NewString()} {
		in := countType("编辑无效项目", "count", countItem(id))
		in["id"], in["active"] = s.cardType, true
		s.call("update_card_type", in, 409)
	}
}
func TestCardTypeItemLimits(t *testing.T) {
	s := open(t)
	item := s.item("剪发")
	for _, field := range []string{"paid_count", "gift_count"} {
		x := countItem(item)
		x[field] = -1
		s.call("create_card_type", countType("负次数", "count", x), 422)
	}
	for _, field := range []string{"paid_count", "gift_count"} {
		x := countItem(item)
		x[field] = 10000
		in := countType("超过次数上限", "count", x)
		s.call("create_card_type", in, 422)
		in["id"], in["active"] = s.cardType, true
		s.call("update_card_type", in, 422)
	}
	x := countItem(item)
	x["paid_count"] = 0
	s.call("create_card_type", countType("零购买次数", "count", x), 422)
	x = countItem(item)
	x["unit_price"] = "-0.01"
	s.call("create_card_type", countType("负单次金额", "count", x), 422)
	tooMany := make([]map[string]any, 21)
	for i := range tooMany {
		tooMany[i] = countItem(uuid.NewString())
	}
	s.call("create_card_type", countType("超出项目上限", "count", tooMany...), 422)
}
func TestCardTypeItemDiff(t *testing.T) {
	s := open(t)
	a, b, c := s.item("剪"), s.item("烫"), s.item("染")
	in := countType("定义", "count", countItem(a), countItem(b))
	ct := s.call("create_card_type", in, 200)
	xmin := func() string {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		var x string
		if err := tx.QueryRow(context.Background(), "SELECT xmin::text FROM card_type_items WHERE card_type_id=$1 AND item_id=$2", ct["id"], a).Scan(&x); err != nil {
			t.Fatal(err)
		}
		dbtest.Rollback(t, tx)
		return x
	}
	before := xmin()
	in["id"], in["active"] = ct["id"], true
	in["name"] = "改名保留项目"
	s.call("update_card_type", in, 200)
	if xmin() != before {
		t.Fatal("unchanged active row rewritten")
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "UPDATE items SET active=false WHERE id=$1", a)
	dbtest.Commit(t, tx)
	in["id"], in["active"] = ct["id"], true
	in["items"] = []any{countItem(a), countItem(c)}
	out := s.call("update_card_type", in, 200)
	if xmin() != before || len(out["items"].([]any)) != 2 {
		t.Fatal(out, "unchanged row rewritten")
	}
	changed := countItem(a)
	changed["gift_count"] = 4
	in["items"] = []any{changed, countItem(c)}
	s.call("update_card_type", in, 409)
}
func TestCardTypeKindChange(t *testing.T) {
	s := open(t)
	item := s.item("剪")
	in := countType("换种类", "count", countItem(item))
	in["id"], in["active"] = s.cardType, true
	s.call("update_card_type", in, 200)
	in["kind"] = "package"
	s.call("update_card_type", in, 200)
	opening := s.opening()
	s.call("open_card", opening, 200)
	in["kind"] = "count"
	s.call("update_card_type", in, 409)
}
func TestCountOpeningSnapshot(t *testing.T) {
	for _, kind := range []string{"count", "package"} {
		t.Run(kind, func(t *testing.T) {
			s := open(t)
			card, item, in := s.countCard(kind)
			if card["kind"] != kind || card["counts_price"] != "123.40" {
				t.Fatal(card)
			}
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			var valid bool
			err := tx.QueryRow(context.Background(), `SELECT (SELECT count(*)=1 FROM card_items WHERE card_id=$1 AND item_id=$2 AND unit_price=12.34 AND paid_count_opened=10 AND gift_count_opened=3) AND (SELECT count(*)=1 FROM card_count_ledger WHERE card_id=$1 AND kind='open' AND paid_before=0 AND paid_delta=10 AND paid_after=10 AND gift_before=0 AND gift_delta=3 AND gift_after=3) AND (SELECT count(*)=1 FROM card_ledger WHERE card_id=$1 AND kind='open')`, card["id"], item).Scan(&valid)
			dbtest.Rollback(t, tx)
			if err != nil || !valid {
				t.Fatal(valid, err)
			}
			x := countItem(item)
			x["paid_count"] = 20
			x["unit_price"] = "50.00"
			in["items"] = []any{x}
			in["id"], in["active"] = card["card_type_id"], true
			s.call("update_card_type", in, 200)
			for _, ch := range s.Channels() {
				r := ch.Call(t, s.MustCaller(s.Owner), "list_customer_cards", map[string]any{"customer_id": s.customer})
				if r.Status != 200 {
					t.Fatal(r)
				}
				after := r.Body["items"].([]any)[0].(map[string]any)
				remaining := after["items"].([]any)[0].(map[string]any)
				if after["counts_price"] != "123.40" || remaining["paid_remaining"] != float64(10) || remaining["gift_remaining"] != float64(3) || remaining["unit_price"] != "12.34" {
					t.Fatal(after)
				}
			}
		})
	}
}
func TestCountZeroPrincipal(t *testing.T) {
	s := open(t)
	card, _, _ := s.countCard("count")
	in := s.opening()
	in["card_type_id"] = card["card_type_id"]
	in["bonus"] = "0.00"
	s.call("open_card", in, 409)
	in["principal"] = "0.00"
	in["bonus"] = "1.00"
	s.call("open_card", in, 409)
}
func TestCountRecharge(t *testing.T) {
	s := open(t)
	card, _, _ := s.countCard("count")
	for _, zero := range []bool{false, true} {
		in := s.recharge(card["id"])
		if zero {
			in["principal"], in["bonus"] = "0.00", "0.00"
		}
		out := s.call("recharge_card", in, 409)
		if out["detail"] != "次卡不能充值" {
			t.Fatal(out)
		}
	}
}
func target(id string, p, g int) map[string]any {
	return map[string]any{"item_id": id, "paid": p, "gift": g}
}
func TestAdjustCounts(t *testing.T) {
	s := open(t)
	a, b := s.item("剪发"), s.item("烫发")
	second := countItem(b)
	second["paid_count"], second["gift_count"] = 20, 8
	ct := s.call("create_card_type", countType("双项目套餐", "package", countItem(a), second), 200)
	opening := s.opening()
	opening["card_type_id"] = ct["id"]
	card := s.call("open_card", opening, 200)
	assertRemaining := func(card map[string]any, paidA, giftA, paidB, giftB int) {
		t.Helper()
		rows := card["items"].([]any)
		if len(rows) != 2 {
			t.Fatal(card)
		}
		want := map[string][2]int{a: {paidA, giftA}, b: {paidB, giftB}}
		for _, row := range rows {
			x := row.(map[string]any)
			id := x["item_id"].(string)
			counts, ok := want[id]
			if !ok || x["paid_remaining"] != float64(counts[0]) || x["gift_remaining"] != float64(counts[1]) {
				t.Fatalf("remaining counts for %s: %v want %v", id, x, counts)
			}
			delete(want, id)
		}
		if len(want) != 0 {
			t.Fatal("missing remaining counts", want)
		}
	}
	assertRemaining(card, 10, 3, 20, 8)
	for _, step := range []struct {
		id                                     string
		paid, gift, paidA, giftA, paidB, giftB int
	}{
		{a, 5, 1, 5, 1, 20, 8}, {b, 15, 4, 5, 1, 15, 4}, {a, 12, 6, 12, 6, 15, 4}, {b, 22, 9, 12, 6, 22, 9},
	} {
		out := s.call("adjust_card", map[string]any{"card_id": card["id"], "counts": []any{target(step.id, step.paid, step.gift)}, "reason": "核对次数"}, 200)
		assertRemaining(out, step.paidA, step.giftA, step.paidB, step.giftB)
	}
	adjustmentCount(t, s, card["id"], 4)
	for _, want := range []struct {
		id         string
		paid, gift int
	}{{a, 12, 6}, {b, 22, 9}} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		var paidValid, giftValid bool
		err := tx.QueryRow(context.Background(), `WITH chain AS (
   SELECT *,lag(paid_after,1,0) OVER w p,lag(gift_after,1,0) OVER w g
   FROM card_count_ledger WHERE card_id=$1 AND item_id=$2 WINDOW w AS (ORDER BY at,id)
  ) SELECT count(*)=3 AND bool_and(paid_before=p) AND sum(paid_delta)=(array_agg(paid_after ORDER BY at DESC,id DESC))[1] AND (array_agg(paid_after ORDER BY at DESC,id DESC))[1]=$3,
   count(*)=3 AND bool_and(gift_before=g) AND sum(gift_delta)=(array_agg(gift_after ORDER BY at DESC,id DESC))[1] AND (array_agg(gift_after ORDER BY at DESC,id DESC))[1]=$4 FROM chain`, card["id"], want.id, want.paid, want.gift).Scan(&paidValid, &giftValid)
		dbtest.Rollback(t, tx)
		if err != nil || !paidValid || !giftValid {
			t.Fatal(want, paidValid, giftValid, err)
		}
	}
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "list_customer_cards", map[string]any{"customer_id": s.customer})
		if r.Status != 200 || len(r.Body["items"].([]any)) != 1 {
			t.Fatal(r)
		}
		assertRemaining(r.Body["items"].([]any)[0].(map[string]any), 12, 6, 22, 9)
		for _, want := range []struct {
			id                     string
			paidBefore, giftBefore int
		}{{a, 5, 1}, {b, 15, 4}} {
			var before any
			seen := map[string]bool{}
			for page := 0; page < 3; page++ {
				in := map[string]any{"card_id": card["id"], "item_id": want.id, "limit": 2}
				if before != nil {
					in["before_id"] = before
				}
				r := ch.Call(t, s.MustCaller(s.Owner), "list_card_counts", in)
				if r.Status != 200 {
					t.Fatal(r)
				}
				rows := r.Body["entries"].([]any)
				expected := []int{2, 1, 0}[page]
				if len(rows) != expected {
					t.Fatal(ch.Name, want.id, page, r)
				}
				for _, row := range rows {
					x := row.(map[string]any)
					if x["item"].(map[string]any)["id"] != want.id || seen[x["id"].(string)] {
						t.Fatal("mixed or duplicate item across pages", r)
					}
					seen[x["id"].(string)] = true
					before = x["id"]
				}
				if page == 0 {
					x := rows[0].(map[string]any)
					if x["reason"] != "核对次数" || x["paid_before"] != float64(want.paidBefore) || x["gift_before"] != float64(want.giftBefore) {
						t.Fatal(x)
					}
				}
				if page == 1 && rows[0].(map[string]any)["kind"] != "open" {
					t.Fatal(r)
				}
			}
			if len(seen) != 3 {
				t.Fatal(seen)
			}
		}
		r = ch.Call(t, s.MustCaller(s.Owner), "list_card_adjustments", map[string]any{"card_id": card["id"]})
		if r.Status != 200 || len(r.Body["entries"].([]any)) != 4 || len(r.Body["entries"].([]any)[0].(map[string]any)["counts"].([]any)) != 1 {
			t.Fatal(r)
		}
	}
	s.call("adjust_card", map[string]any{"card_id": card["id"], "counts": []any{target(a, 11, 5)}, "principal": "10.00", "expiry": map[string]any{"expires_on": "2030-01-01"}, "reason": "合并更正"}, 200)
	adjustmentCount(t, s, card["id"], 5)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var valid bool
	err := tx.QueryRow(context.Background(), `SELECT count(*)=1 FROM card_adjustments a JOIN card_ledger l ON l.id=a.ledger_id JOIN card_count_ledger c ON c.adjustment_id=a.id WHERE a.card_id=$1 AND a.reason='合并更正' AND a.expiry_changed AND a.counts_changed`, card["id"]).Scan(&valid)
	dbtest.Rollback(t, tx)
	if err != nil || !valid {
		t.Fatal(valid, err)
	}
}
func TestAdjustCountLimits(t *testing.T) {
	s := open(t)
	card, item, _ := s.countCard("count")
	for _, x := range []map[string]any{target(item, 10000, 3), target(item, 10, 10000)} {
		s.call("adjust_card", map[string]any{"card_id": card["id"], "counts": []any{x}, "reason": "超过次数上限"}, 422)
	}
	adjustmentCount(t, s, card["id"], 0)
}
func TestCountsNoop(t *testing.T) {
	s := open(t)
	card, item, _ := s.countCard("count")
	s.call("adjust_card", map[string]any{"card_id": card["id"], "counts": []any{target(item, 10, 3)}, "reason": "相同"}, 409)
	s.call("adjust_card", map[string]any{"card_id": card["id"], "counts": []any{target(item, 10, 2)}, "reason": "只改次数"}, 200)
	adjustmentCount(t, s, card["id"], 1)
}
func TestCountUnknownItem(t *testing.T) {
	s := open(t)
	card, _, _ := s.countCard("count")
	s.call("adjust_card", map[string]any{"card_id": card["id"], "counts": []any{target(uuid.NewString(), 1, 0)}, "reason": "未知项目"}, 409)
}
func TestListCountsScope(t *testing.T) {
	s := open(t)
	card, _, _ := s.countCard("count")
	caller := s.MustCaller(s.Owner)
	caller.Permissions = perm.All()
	caller.Permissions[perm.CustomerView] = ops.Grant{Scope: ops.ScopeSelf}
	for _, ch := range s.Channels() {
		if r := ch.Call(t, caller, "list_card_counts", map[string]any{"card_id": card["id"]}); r.Status != 404 {
			t.Fatal(r)
		}
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "UPDATE customers SET responsible_staff_id=$2 WHERE id=$1", s.customer, caller.StaffID)
	dbtest.Commit(t, tx)
	for _, ch := range s.Channels() {
		if r := ch.Call(t, caller, "list_card_counts", map[string]any{"card_id": card["id"]}); r.Status != 200 {
			t.Fatal(r)
		}
	}
}
func TestListCountsStore(t *testing.T) {
	s := open(t)
	card, _, _ := s.countCard("count")
	staff, token := s.Hire("二店", "002", "second", s.Role("老板"))
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var store uuid.UUID
	if err := tx.QueryRow(context.Background(), "INSERT INTO stores (tenant_id,name) VALUES ($1,'二店') RETURNING id", s.Res.TenantID).Scan(&store); err != nil {
		t.Fatal(err)
	}
	dbtest.Exec(t, tx, "UPDATE staff SET store_id=$2 WHERE id=$1", staff, store)
	otherCard := uuid.New()
	dbtest.Exec(t, tx, "INSERT INTO cards (tenant_id,store_id,id,customer_id,card_type_id,opened_at,created_by) SELECT tenant_id,$2,$3,customer_id,card_type_id,opened_at,created_by FROM cards WHERE id=$1", card["id"], store, otherCard)
	dbtest.Commit(t, tx)
	for _, ch := range s.Channels() {
		if r := ch.Call(t, s.MustCaller(s.Owner), "list_card_counts", map[string]any{"card_id": otherCard}); r.Status != 404 {
			t.Fatal(ch.Name, r)
		}
		if r := ch.Call(t, s.MustCaller(token), "list_card_counts", map[string]any{"card_id": card["id"]}); r.Status != 404 {
			t.Fatal(r)
		}
	}
}
func TestCountWritesPermissionAndAI(t *testing.T) {
	s := open(t)
	card, item, in := s.countCard("package")
	in["id"], in["active"] = card["card_type_id"], true
	cases := []struct {
		op         string
		in         any
		permission ops.Permission
	}{{"create_card_type", countType("AI新卡", "count", countItem(item)), perm.SettingsManage}, {"update_card_type", in, perm.SettingsManage}, {"open_card", s.opening(), perm.CardSell}, {"recharge_card", s.recharge(card["id"]), perm.CardSell}, {"adjust_card", map[string]any{"card_id": card["id"], "counts": []any{target(item, 2, 1)}, "reason": "AI更正"}, perm.CardAdjust}}
	before := countSnapshot(t, s)
	for _, tc := range cases {
		caller := s.MustCaller(s.Owner)
		caller.Permissions = perm.All()
		delete(caller.Permissions, tc.permission)
		if r := s.HTTP(t, &caller, tc.op, tc.in); r.Status != 403 {
			t.Fatal(r)
		}
		if r := s.AI(t, s.MustCaller(s.Owner), tc.op, tc.in); r.Status == 200 {
			t.Fatal(r)
		}
	}
	if countSnapshot(t, s) != before {
		t.Fatal("refused writes changed data")
	}
	caller := s.MustCaller(s.Owner)
	caller.Permissions = perm.All()
	delete(caller.Permissions, perm.CustomerView)
	for _, ch := range s.Channels() {
		if r := ch.Call(t, caller, "list_card_counts", map[string]any{"card_id": card["id"]}); r.Status != 403 {
			t.Fatal(r)
		}
	}
}
func countSnapshot(t *testing.T, s *fixture) string {
	t.Helper()
	return dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "card_types", "cards", "card_type_items", "card_items", "card_count_ledger", "card_adjustments", "card_ledger")
}
func TestCountsSerialize(t *testing.T) {
	s := open(t)
	card, item, _ := s.countCard("count")
	held := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, held, "SELECT id FROM cards WHERE id=$1 FOR NO KEY UPDATE", card["id"])
	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "adjust_card", map[string]any{"card_id": card["id"], "counts": []any{target(item, 7, 2)}, "reason": "并发"})
	}()
	dbtest.WaitForLockWait(t, s.Pool)
	dbtest.Exec(t, held, `INSERT INTO card_count_ledger (tenant_id,store_id,card_id,item_id,kind,paid_before,paid_delta,paid_after,gift_before,gift_delta,gift_after,business_date,actor_id) VALUES ($1,$2,$3,$4,'adjust',10,-2,8,3,-2,1,current_date,$5)`, s.Res.TenantID, s.Res.StoreID, card["id"], item, s.Res.OwnerID)
	dbtest.Commit(t, held)
	if r := <-done; r.Status != 200 {
		t.Fatal(r)
	}
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "list_card_counts", map[string]any{"card_id": card["id"], "limit": 1})
		if r.Status != 200 {
			t.Fatal(r)
		}
		x := r.Body["entries"].([]any)[0].(map[string]any)
		if x["paid_before"] != float64(8) || x["gift_before"] != float64(1) {
			t.Fatal(x)
		}
	}
}
func TestOpenCountsWaitsForItem(t *testing.T) {
	s := open(t)
	item := s.item("剪")
	ct := s.call("create_card_type", countType("次卡", "count", countItem(item)), 200)
	in := s.opening()
	in["card_type_id"], in["principal"], in["bonus"] = ct["id"], "0.00", "0.00"
	held := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, held, "SELECT id FROM items WHERE id=$1 FOR NO KEY UPDATE", item)
	done := make(chan optest.Result, 1)
	go func() { done <- s.HTTPWithToken(t, s.Owner, "open_card", in) }()
	dbtest.WaitForLockWait(t, s.Pool)
	dbtest.Exec(t, held, "UPDATE items SET active=false WHERE id=$1", item)
	dbtest.Commit(t, held)
	if r := <-done; r.Status != 409 {
		t.Fatal(r)
	}
}
func TestCountsDatabaseProtections(t *testing.T) {
	s := open(t)
	card, item, _ := s.countCard("count")
	for _, table := range []string{"card_items", "card_count_ledger"} {
		for _, verb := range []string{"UPDATE", "DELETE"} {
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			sql := fmt.Sprintf("DELETE FROM %s WHERE card_id=$1", table)
			if verb == "UPDATE" {
				sql = fmt.Sprintf("UPDATE %s SET store_id=store_id WHERE card_id=$1", table)
			}
			_, err := tx.Exec(context.Background(), sql, card["id"])
			dbtest.Rollback(t, tx)
			if err == nil || !strings.Contains(err.Error(), "42501") {
				t.Fatal(table, verb, err)
			}
		}
	}
	for _, values := range []string{"0,1,0,0,0,0", "0,0,0,0,1,0", "0,-1,-1,0,0,0", "0,0,0,0,-1,-1", "0,10000,10000,0,0,0", "0,0,0,0,10000,10000"} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), `INSERT INTO card_count_ledger (tenant_id,store_id,card_id,item_id,kind,paid_before,paid_delta,paid_after,gift_before,gift_delta,gift_after,business_date,actor_id) VALUES ($1,$2,$3,$4,'open',`+values+`,current_date,$5)`, s.Res.TenantID, s.Res.StoreID, card["id"], item, s.Res.OwnerID)
		dbtest.Rollback(t, tx)
		if err == nil || !strings.Contains(err.Error(), "23514") {
			t.Fatal(values, err)
		}
	}
}

func TestCountDatabaseLimits(t *testing.T) {
	s := open(t)
	card, item, _ := s.countCard("count")
	second := s.item("烫发")
	for _, column := range []string{"paid_count", "gift_count"} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), "UPDATE card_type_items SET "+column+"=10000 WHERE card_type_id=$1 AND item_id=$2", card["card_type_id"], item)
		dbtest.Rollback(t, tx)
		if err == nil || !strings.Contains(err.Error(), "23514") {
			t.Fatal(column, err)
		}
	}
	for _, counts := range [][2]int{{10000, 0}, {1, 10000}} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), `INSERT INTO card_items (tenant_id,store_id,card_id,item_id,unit_price,paid_count_opened,gift_count_opened) VALUES ($1,$2,$3,$4,1,$5,$6)`, s.Res.TenantID, s.Res.StoreID, card["id"], second, counts[0], counts[1])
		dbtest.Rollback(t, tx)
		if err == nil || !strings.Contains(err.Error(), "23514") {
			t.Fatal(counts, err)
		}
	}
	in := countItem(item)
	in["paid_count"], in["gift_count"] = 9999, 9999
	ct := s.call("create_card_type", countType("上限次卡", "count", in), 200)
	opening := s.opening()
	opening["card_type_id"], opening["principal"], opening["bonus"] = ct["id"], "0.00", "0.00"
	opened := s.call("open_card", opening, 200)
	counts := opened["items"].([]any)[0].(map[string]any)
	if counts["paid_remaining"] != float64(9999) || counts["gift_remaining"] != float64(9999) {
		t.Fatal(opened)
	}
	out := s.call("adjust_card", map[string]any{"card_id": card["id"], "counts": []any{target(item, 9999, 9999)}, "reason": "上限可用"}, 200)
	counts = out["items"].([]any)[0].(map[string]any)
	if counts["paid_remaining"] != float64(9999) || counts["gift_remaining"] != float64(9999) {
		t.Fatal(out)
	}
}
