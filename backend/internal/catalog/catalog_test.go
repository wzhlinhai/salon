package catalog_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
)

type fixture struct {
	*optest.Shop
	t *testing.T
}

func open(t *testing.T) *fixture { return &fixture{optest.OpenShop(t), t} }
func (s *fixture) call(op string, in any, want int) map[string]any {
	s.t.Helper()
	r := s.HTTPWithToken(s.t, s.Owner, op, in)
	if r.Status != want {
		s.t.Fatalf("%s got %d %v, want %d", op, r.Status, r.Body, want)
	}
	return r.Body
}
func input(name string) map[string]any {
	return map[string]any{"name": name, "price": "1234.56", "discountable": false, "labor_fee_amount": "0.50", "labor_fee_percent": nil, "positions": []any{}, "level_prices": []any{}}
}
func (s *fixture) level(active bool) string {
	l := s.call("create_level", map[string]any{"name": "首席"}, 200)
	if !active {
		s.call("update_level", map[string]any{"id": l["id"], "name": "首席", "active": false}, 200)
	}
	return l["id"].(string)
}
func (s *fixture) position(active bool) string {
	p := s.call("create_position", map[string]any{"name": "大工"}, 200)
	if !active {
		s.call("update_position", map[string]any{"id": p["id"], "name": "大工", "active": false}, 200)
	}
	return p["id"].(string)
}
func update(in map[string]any, id any) map[string]any {
	out := map[string]any{}
	for k, v := range in {
		out[k] = v
	}
	out["id"] = id
	out["active"] = true
	return out
}

func TestRoundTripAndAudit(t *testing.T) {
	s := open(t)
	l := s.level(true)
	p := s.position(true)
	in := input("剪发")
	in["level_prices"] = []any{map[string]any{"level_id": l, "price": "0.50"}}
	in["positions"] = []any{map[string]any{"position_id": p, "required": true}, map[string]any{"position_id": p, "required": false}}
	created := s.call("create_item", in, 200)
	for _, key := range []string{"tenant_id", "store_id"} {
		if _, present := created[key]; present {
			t.Fatalf("exposed %s", key)
		}
	}
	if created["price"] != "1234.56" || created["labor_fee_amount"] != "0.50" || created["discountable"] != false || created["active"] != true || created["labor_fee_percent"] != nil {
		t.Fatal(created)
	}
	if created["positions"].([]any)[1].(map[string]any)["slot"] != float64(2) || created["level_prices"].([]any)[0].(map[string]any)["price"] != "0.50" {
		t.Fatal(created)
	}
	edited := update(in, created["id"])
	edited["name"] = "烫发"
	edited["labor_fee_amount"] = nil
	edited["labor_fee_percent"] = "12.50"
	edited["active"] = false
	after := s.call("update_item", edited, 200)
	if after["labor_fee_percent"] != "12.50" || after["labor_fee_amount"] != nil || after["active"] != false || after["name"] != "烫发" {
		t.Fatal(after)
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	rows, err := tx.Query(context.Background(), "SELECT action, target_kind, before, after FROM audit_log WHERE target_id=$1 ORDER BY at", created["id"])
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var action, kind string
		var beforeRaw, afterRaw []byte
		if err := rows.Scan(&action, &kind, &beforeRaw, &afterRaw); err != nil {
			t.Fatal(err)
		}
		if kind != "item" {
			t.Fatalf("action %s kind = %s, want item", action, kind)
		}
		var before, afterView map[string]any
		if err := json.Unmarshal(afterRaw, &afterView); err != nil {
			t.Fatal(err)
		}
		switch action {
		case "create_item":
			if string(beforeRaw) != "null" && len(beforeRaw) != 0 {
				t.Fatal(string(beforeRaw))
			}
			if !reflect.DeepEqual(afterView, created) {
				t.Fatal(afterView, created)
			}
		case "update_item":
			if err := json.Unmarshal(beforeRaw, &before); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, created) || !reflect.DeepEqual(afterView, after) {
				t.Fatal(before, afterView)
			}
		default:
			t.Fatal(action)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatal(count)
	}
}

func TestDuplicateAndLaborFeeRules(t *testing.T) {
	s := open(t)
	in := input("剪发")
	first := s.call("create_item", in, 200)
	s.call("create_item", in, 409)
	second := s.call("create_item", input("烫发"), 200)
	s.call("update_item", update(in, second["id"]), 409)
	in["name"] = "双手工费"
	in["labor_fee_percent"] = "10.00"
	// 必须是业务规则的 409；数据库 CHECK 失败会变成 500，不能冒充处理函数保护。
	s.call("create_item", in, 409)
	s.call("update_item", update(in, first["id"]), 409)
}

func TestInactiveLevel(t *testing.T) {
	s := open(t)
	l := s.level(false)
	in := input("剪发")
	item := s.call("create_item", in, 200)
	in["name"] = "新增剪发"
	in["level_prices"] = []any{map[string]any{"level_id": l, "price": "88.00"}}
	s.call("create_item", in, 409)
	s.call("update_item", update(in, item["id"]), 409)
}
func TestInactivePosition(t *testing.T) {
	s := open(t)
	p := s.position(false)
	in := input("剪发")
	item := s.call("create_item", in, 200)
	in["name"] = "新增剪发"
	in["positions"] = []any{map[string]any{"position_id": p, "required": true}}
	s.call("create_item", in, 409)
	s.call("update_item", update(in, item["id"]), 409)
}

func versions(t *testing.T, s *fixture, id any) []string {
	t.Helper()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	rows, err := tx.Query(context.Background(), "SELECT 'level:'||level_id::text||':'||xmin::text FROM item_level_prices WHERE item_id=$1 UNION ALL SELECT 'slot:'||slot::text||':'||xmin::text FROM item_positions WHERE item_id=$1 ORDER BY 1", id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	return out
}
func TestUnchangedChildrenKeepXmin(t *testing.T) {
	s := open(t)
	l := s.level(true)
	p := s.position(true)
	in := input("剪发")
	in["level_prices"] = []any{map[string]any{"level_id": l, "price": "88.00"}}
	in["positions"] = []any{map[string]any{"position_id": p, "required": true}}
	item := s.call("create_item", in, 200)
	before := versions(t, s, item["id"])
	s.call("update_level", map[string]any{"id": l, "name": "首席", "active": false}, 200)
	s.call("update_position", map[string]any{"id": p, "name": "大工", "active": false}, 200)
	edited := update(in, item["id"])
	edited["price"] = "0.50"
	s.call("update_item", edited, 200)
	if after := versions(t, s, item["id"]); !reflect.DeepEqual(before, after) {
		t.Fatalf("child rows rewritten: before=%v after=%v", before, after)
	}
	edited["level_prices"] = []any{map[string]any{"level_id": l, "price": "99.00"}}
	edited["positions"] = []any{map[string]any{"position_id": p, "required": false}}
	after := s.call("update_item", edited, 200)
	if after["level_prices"].([]any)[0].(map[string]any)["price"] != "99.00" || after["positions"].([]any)[0].(map[string]any)["required"] != false {
		t.Fatal(after)
	}
	edited["level_prices"] = []any{}
	edited["positions"] = []any{}
	after = s.call("update_item", edited, 200)
	if len(after["level_prices"].([]any)) != 0 || len(after["positions"].([]any)) != 0 {
		t.Fatal(after)
	}
}

func TestPermissionsAndAIDraft(t *testing.T) {
	s := open(t)
	_, token := s.Hire("发型师", "002", "stylist", s.Role("发型师"))
	caller := s.MustCaller(token)
	in := input("剪发")
	item := s.call("create_item", in, 200)
	edited := update(in, item["id"])
	for _, tc := range []struct {
		op   string
		args any
	}{{"create_item", input("新项目")}, {"update_item", edited}} {
		r := s.HTTPWithToken(t, token, tc.op, tc.args)
		if r.Status != 403 {
			t.Fatalf("%s: %v", tc.op, r)
		}
		before := s.call("list_items", map[string]any{}, 200)
		r = s.AI(t, s.MustCaller(s.Owner), tc.op, tc.args)
		if r.Status == 200 {
			t.Fatal(r)
		}
		if after := s.call("list_items", map[string]any{}, 200); !reflect.DeepEqual(before, after) {
			t.Fatal("AI changed data", before, after)
		}
	}
	for _, ch := range s.Channels() {
		for _, tc := range []struct {
			op   string
			args any
		}{{"list_items", map[string]any{}}, {"quote_item_price", map[string]any{"item_id": item["id"], "staff_id": caller.StaffID}}} {
			if r := ch.Call(t, caller, tc.op, tc.args); r.Status != 200 {
				t.Fatal(ch.Name, tc.op, r)
			}
		}
	}
}

func TestReadChannels(t *testing.T) {
	s := open(t)
	l := s.level(true)
	staffID, token := s.Hire("发型师", "002", "stylist", s.Role("发型师"))
	caller := s.MustCaller(token)
	s.call("update_staff", map[string]any{"staff_id": staffID, "name": "发型师", "number": "002", "level_id": l, "position_id": nil}, 200)
	in := input("剪发")
	standard := s.call("create_item", in, 200)
	in["name"] = "烫发"
	in["level_prices"] = []any{map[string]any{"level_id": l, "price": "0.50"}}
	special := s.call("create_item", in, 200)
	in["name"] = "停用"
	inactive := s.call("create_item", in, 200)
	off := update(in, inactive["id"])
	off["active"] = false
	s.call("update_item", off, 200)
	other := optest.OpenShop(t)
	foreign := other.HTTPWithToken(t, other.Owner, "create_item", input("外店"))
	if foreign.Status != 200 {
		t.Fatal(foreign)
	}
	for _, ch := range s.Channels() {
		t.Run(ch.Name, func(t *testing.T) {
			for _, tc := range []struct {
				name        string
				item, staff any
				status      int
				price       string
				level       any
			}{
				{"标准价", standard["id"], staffID, 200, "1234.56", nil},
				{"级别价", special["id"], staffID, 200, "0.50", l},
				{"无级别", special["id"], s.Res.OwnerID, 200, "1234.56", nil},
				{"停用", inactive["id"], staffID, 409, "", nil},
				{"跨租户", foreign.Body["id"], staffID, 404, "", nil},
				{"外店员工", special["id"], other.Res.OwnerID, 404, "", nil},
			} {
				t.Run(tc.name, func(t *testing.T) {
					r := ch.Call(t, caller, "quote_item_price", map[string]any{"item_id": tc.item, "staff_id": tc.staff})
					if r.Status != tc.status {
						t.Fatalf("%v", r)
					}
					if tc.status == 200 {
						level, present := r.Body["level_id"]
						if r.Body["price"] != tc.price || (tc.level == nil && present) || (tc.level != nil && (!present || level != tc.level)) {
							t.Fatal(r.Body)
						}
					}
				})
			}
			r := ch.Call(t, caller, "list_items", map[string]any{})
			if r.Status != 200 {
				t.Fatal(r)
			}
			items := r.Body["items"].([]any)
			if len(items) != 3 || items[2].(map[string]any)["id"] != inactive["id"] {
				t.Fatal(r)
			}
		})
	}
}

func TestUnknownReferencesAndDuplicates(t *testing.T) {
	s := open(t)
	in := input("剪发")
	item := s.call("create_item", in, 200)
	for _, key := range []string{"level_prices", "positions"} {
		draft := input("未知引用")
		if key == "level_prices" {
			draft[key] = []any{map[string]any{"level_id": uuid.NewString(), "price": "1.00"}}
		} else {
			draft[key] = []any{map[string]any{"position_id": uuid.NewString(), "required": true}}
		}
		s.call("create_item", draft, 404)
		s.call("update_item", update(draft, item["id"]), 404)
	}
	l := s.level(true)
	in["level_prices"] = []any{map[string]any{"level_id": l, "price": "1.00"}, map[string]any{"level_id": l, "price": "2.00"}}
	s.call("create_item", in, 409)
	s.call("update_item", update(in, item["id"]), 409)
	s.call("update_item", update(input("不存在"), uuid.New()), 404)
}

func TestLocksBeforeReadingState(t *testing.T) {
	s := open(t)
	in := input("剪发")
	item := s.call("create_item", in, 200)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	if _, err := tx.Exec(context.Background(), "UPDATE items SET name='锁内改名' WHERE id=$1", item["id"]); err != nil {
		t.Fatal(err)
	}
	done := make(chan optest.Result, 1)
	go func() { done <- s.HTTPWithToken(t, s.Owner, "update_item", update(in, item["id"])) }()
	dbtest.WaitForLockWait(t, s.Pool)
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	r := <-done
	if r.Status != 200 {
		t.Fatal(r)
	}
	auditTx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var beforeName string
	if err := auditTx.QueryRow(context.Background(), "SELECT before->>'name' FROM audit_log WHERE target_id=$1 AND action='update_item'", item["id"]).Scan(&beforeName); err != nil {
		t.Fatal(err)
	}
	if beforeName != "锁内改名" {
		t.Fatalf("before snapshot read without lock: %s", beforeName)
	}
}

func TestReferenceShareLocks(t *testing.T) {
	for _, kind := range []string{"level", "position"} {
		t.Run(kind, func(t *testing.T) {
			s := open(t)
			in := input("剪发")
			var id string
			if kind == "level" {
				id = s.level(true)
				in["level_prices"] = []any{map[string]any{"level_id": id, "price": "88.00"}}
			} else {
				id = s.position(true)
				in["positions"] = []any{map[string]any{"position_id": id, "required": true}}
			}
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			table := "levels"
			if kind == "position" {
				table = "positions"
			}
			if _, err := tx.Exec(context.Background(), "UPDATE "+table+" SET active=false WHERE id=$1", id); err != nil {
				t.Fatal(err)
			}
			done := make(chan optest.Result, 1)
			go func() { done <- s.HTTPWithToken(t, s.Owner, "create_item", in) }()
			dbtest.WaitForLockWait(t, s.Pool)
			if err := tx.Commit(context.Background()); err != nil {
				t.Fatal(err)
			}
			if r := <-done; r.Status != 409 {
				t.Fatalf("referenced concurrently deactivated %s: %v", kind, r)
			}
		})
	}
}

func TestAmountOverflow(t *testing.T) {
	s := open(t)
	levelID := s.level(true)
	in := input("金额上限")
	in["price"] = "99999999.99"
	in["labor_fee_amount"] = "99999999.99"
	in["level_prices"] = []any{map[string]any{"level_id": levelID, "price": "99999999.99"}}
	item := s.call("create_item", in, 200)
	for _, key := range []string{"price", "labor_fee_amount"} {
		if item[key] != "99999999.99" {
			t.Fatal(item)
		}
	}
	if item["level_prices"].([]any)[0].(map[string]any)["price"] != "99999999.99" {
		t.Fatal(item)
	}
	for _, key := range []string{"price", "level_prices", "labor_fee_amount"} {
		t.Run(key, func(t *testing.T) {
			draft := input("金额溢出" + key)
			if key == "level_prices" {
				draft[key] = []any{map[string]any{"level_id": levelID, "price": "100000000.00"}}
			} else {
				draft[key] = "100000000.00"
			}
			for _, op := range []string{"create_item", "update_item"} {
				args := draft
				if op == "update_item" {
					args = update(draft, item["id"])
				}
				result := s.HTTPWithToken(t, s.Owner, op, args)
				if result.Status != 422 {
					t.Fatalf("%s overflow %s: got %d %v, want 422", op, key, result.Status, result.Body)
				}
			}
		})
	}
	list := s.call("list_items", map[string]any{}, 200)["items"].([]any)
	if len(list) != 1 || !reflect.DeepEqual(list[0], item) {
		t.Fatal(list)
	}
}

func TestItemChildrenLimit(t *testing.T) {
	s := open(t)
	positionID := s.position(true)
	levels := []any{}
	positions := []any{}
	for i := range 20 {
		level := s.call("create_level", map[string]any{"name": fmt.Sprintf("级别%d", i)}, 200)
		levels = append(levels, map[string]any{"level_id": level["id"], "price": "1.00"})
		positions = append(positions, map[string]any{"position_id": positionID, "required": false})
	}
	in := input("二十行")
	in["level_prices"] = levels
	in["positions"] = positions
	item := s.call("create_item", in, 200)
	s.call("update_item", update(in, item["id"]), 200)
	for _, key := range []string{"positions", "level_prices"} {
		draft := input("二十一行" + key)
		draft["positions"] = positions
		draft["level_prices"] = levels
		rows := draft[key].([]any)
		draft[key] = append(append([]any{}, rows...), rows[0])
		s.call("create_item", draft, 422)
		s.call("update_item", update(draft, item["id"]), 422)
	}
}

func TestChildQueriesSelectItemIDs(t *testing.T) {
	s := open(t)
	levelID := s.level(true)
	positionID := s.position(true)
	in := input("项目一")
	in["level_prices"] = []any{map[string]any{"level_id": levelID, "price": "88.00"}}
	in["positions"] = []any{map[string]any{"position_id": positionID, "required": true}}
	first := s.call("create_item", in, 200)
	in["name"] = "项目二"
	second := s.call("create_item", in, 200)
	firstID := uuid.MustParse(first["id"].(string))
	secondID := uuid.MustParse(second["id"].(string))
	for _, ids := range [][]uuid.UUID{{firstID}, {firstID, secondID}, {}} {
		err := db.InTenant(context.Background(), s.Pool, s.Res.TenantID, func(q *dbgen.Queries) error {
			prices, err := q.ListItemLevelPrices(context.Background(), dbgen.ListItemLevelPricesParams{StoreID: s.Res.StoreID, ItemIds: ids})
			if err != nil {
				return err
			}
			positions, err := q.ListItemPositions(context.Background(), dbgen.ListItemPositionsParams{StoreID: s.Res.StoreID, ItemIds: ids})
			if err != nil {
				return err
			}
			if len(prices) != len(ids) || len(positions) != len(ids) {
				t.Fatalf("ids=%v prices=%v positions=%v", ids, prices, positions)
			}
			allowed := map[uuid.UUID]bool{}
			for _, id := range ids {
				allowed[id] = true
			}
			for _, price := range prices {
				if !allowed[price.ItemID] {
					t.Fatal(price)
				}
			}
			for _, position := range positions {
				if !allowed[position.ItemID] {
					t.Fatal(position)
				}
			}
			prices, err = q.ListItemLevelPrices(context.Background(), dbgen.ListItemLevelPricesParams{StoreID: uuid.New(), ItemIds: ids})
			if err != nil {
				return err
			}
			positions, err = q.ListItemPositions(context.Background(), dbgen.ListItemPositionsParams{StoreID: uuid.New(), ItemIds: ids})
			if err != nil {
				return err
			}
			if len(prices) != 0 || len(positions) != 0 {
				t.Fatal("child queries ignored store scope")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	items := s.call("list_items", map[string]any{}, 200)["items"].([]any)
	if len(items) != 2 {
		t.Fatal(items)
	}
	for _, item := range items {
		view := item.(map[string]any)
		if len(view["level_prices"].([]any)) != 1 || len(view["positions"].([]any)) != 1 {
			t.Fatal(view)
		}
	}
}
