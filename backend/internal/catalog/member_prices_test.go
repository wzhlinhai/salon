package catalog_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/catalog"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func (s *fixture) cardType(name string) string {
	return s.call("create_card_type", map[string]any{"name": name, "kind": "stored_value", "validity_kind": "none"}, 200)["id"].(string)
}
func (s *fixture) memberRule(value string, days int) {
	s.call("change_setting", map[string]any{"key": "member_price_rule", "value": value, "effective_from": settings.Date(time.Now()).AddDate(0, 0, days).Format(time.DateOnly)}, 200)
}

func TestMemberQuoteCombinations(t *testing.T) {
	s := open(t)
	level := s.level(true)
	staff, _ := s.Hire("有级别员工", "002", "memberquote")
	s.call("update_staff", map[string]any{"staff_id": staff, "name": "有级别员工", "number": "002", "level_id": level, "position_id": nil}, 200)
	ct := s.cardType("会员")
	noMember := s.cardType("无会员价")
	for _, rule := range []string{"member_first", "lowest"} {
		s.memberRule(rule, 0)
		for _, tc := range []struct{ name, level, member, card, price, source string }{
			{"无卡", "50.00", "80.00", "", "50.00", "level"},
			{"无会员价", "50.00", "80.00", noMember, "50.00", "level"},
			{"无级别价", "", "80.00", ct, "80.00", "member"},
			{"会员价较低", "90.00", "80.00", ct, "80.00", "member"},
			{"会员价较高", "50.00", "80.00", ct, map[bool]string{true: "50.00", false: "80.00"}[rule == "lowest"], map[bool]string{true: "level", false: "member"}[rule == "lowest"]},
			{"同价", "80.00", "80.00", ct, "80.00", "member"},
			{"标准价", "", "", "", "1234.56", "standard"},
		} {
			t.Run(rule+tc.name, func(t *testing.T) {
				in := input(rule + tc.name)
				if tc.level != "" {
					in["level_prices"] = []any{map[string]any{"level_id": level, "price": tc.level}}
				}
				if tc.member != "" {
					in["member_prices"] = []any{map[string]any{"card_type_id": ct, "price": tc.member}}
				}
				item := s.call("create_item", in, 200)
				args := map[string]any{"item_id": item["id"], "staff_id": staff}
				var cardID *uuid.UUID
				if tc.card != "" {
					id := uuid.MustParse(tc.card)
					cardID = &id
					args["card_type_id"] = id
				}
				for _, ch := range s.Channels() {
					r := ch.Call(t, s.MustCaller(s.Owner), "quote_item_price", args)
					if r.Status != 200 || r.Body["price"] != tc.price || r.Body["source"] != tc.source {
						t.Fatal(ch.Name, r)
					}
					if tc.source == "member" && (r.Body["card_type_id"] != ct || r.Body["level_id"] != nil) {
						t.Fatal(r)
					}
				}
				err := db.InTenant(context.Background(), s.Pool, s.Res.TenantID, func(q *dbgen.Queries) error {
					rules, err := settings.Effective(context.Background(), q, s.Res.StoreID, settings.Date(time.Now()))
					if err != nil {
						return err
					}
					result, err := catalog.Quote(context.Background(), q, s.Res.StoreID, uuid.MustParse(item["id"].(string)), &staff, cardID, rules)
					if err != nil {
						return err
					}
					if result.Price.String() != tc.price || result.Source != tc.source {
						t.Fatal(result)
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestMemberQuoteEffectiveSetting(t *testing.T) {
	s := open(t)
	ct := s.cardType("会员")
	in := input("会员项目")
	in["price"] = "50.00"
	in["member_prices"] = []any{map[string]any{"card_type_id": ct, "price": "80.00"}}
	item := s.call("create_item", in, 200)
	args := map[string]any{"item_id": item["id"], "staff_id": s.Res.OwnerID, "card_type_id": ct}
	s.memberRule("lowest", 1)
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "quote_item_price", args)
		if r.Status != 200 || r.Body["price"] != "80.00" {
			t.Fatal("future setting applied", r)
		}
	}
	s.memberRule("lowest", 0)
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "quote_item_price", args)
		if r.Status != 200 || r.Body["price"] != "50.00" {
			t.Fatal("effective setting ignored", r)
		}
	}
	off := map[string]any{"id": ct, "name": "会员", "kind": "stored_value", "validity_kind": "none", "active": false}
	s.call("update_card_type", off, 200)
	s.memberRule("member_first", 0)
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "quote_item_price", args)
		if r.Status != 200 || r.Body["price"] != "80.00" {
			t.Fatal("inactive type must still price", r)
		}
		args["card_type_id"] = uuid.New()
		if r := ch.Call(t, s.MustCaller(s.Owner), "quote_item_price", args); r.Status != 404 {
			t.Fatal(r)
		}
		args["card_type_id"] = ct
	}
}

func memberVersions(t *testing.T, s *fixture, id any) []string {
	t.Helper()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	rows, err := tx.Query(context.Background(), "SELECT card_type_id::text||':'||price::text||':'||xmin::text FROM item_member_prices WHERE item_id=$1 ORDER BY card_type_id", id)
	if err != nil {
		t.Fatal(err)
	}
	result := []string{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		result = append(result, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestMemberPriceDiff(t *testing.T) {
	s := open(t)
	a, b := s.cardType("甲"), s.cardType("乙")
	in := input("剪发会员价")
	in["member_prices"] = []any{map[string]any{"card_type_id": a, "price": "30.00"}, map[string]any{"card_type_id": b, "price": "40.00"}}
	item := s.call("create_item", in, 200)
	before := memberVersions(t, s, item["id"])
	s.call("update_card_type", map[string]any{"id": a, "name": "甲", "kind": "stored_value", "validity_kind": "none", "active": false}, 200)
	s.call("update_item", update(in, item["id"]), 200)
	if after := memberVersions(t, s, item["id"]); !reflect.DeepEqual(before, after) {
		t.Fatal("unchanged rows rewritten", before, after)
	}
	in["member_prices"] = []any{map[string]any{"card_type_id": a, "price": "31.00"}}
	changed := s.call("update_item", update(in, item["id"]), 200)
	if len(changed["member_prices"].([]any)) != 1 || changed["member_prices"].([]any)[0].(map[string]any)["price"] != "31.00" {
		t.Fatal(changed)
	}
	in["member_prices"] = []any{}
	s.call("update_item", update(in, item["id"]), 200)
	if len(memberVersions(t, s, item["id"])) != 0 {
		t.Fatal("row not removed")
	}
}

func TestMemberPriceReferences(t *testing.T) {
	s := open(t)
	ct := s.cardType("停用")
	s.call("update_card_type", map[string]any{"id": ct, "name": "停用", "kind": "stored_value", "validity_kind": "none", "active": false}, 200)
	item := s.call("create_item", input("现有"), 200)
	for _, tc := range []struct {
		id     string
		status int
	}{{ct, 409}, {uuid.NewString(), 404}} {
		in := input("新引用")
		in["member_prices"] = []any{map[string]any{"card_type_id": tc.id, "price": "10.00"}}
		s.call("create_item", in, tc.status)
		s.call("update_item", update(in, item["id"]), tc.status)
	}
	in := input("重复")
	ct = s.cardType("启用")
	in["member_prices"] = []any{map[string]any{"card_type_id": ct, "price": "1.00"}, map[string]any{"card_type_id": ct, "price": "2.00"}}
	s.call("create_item", in, 409)
	s.call("update_item", update(in, item["id"]), 409)
	rows := []any{}
	for range 21 {
		rows = append(rows, map[string]any{"card_type_id": ct, "price": "1.00"})
	}
	in["member_prices"] = rows
	s.call("create_item", in, 422)
	s.call("update_item", update(in, item["id"]), 422)
}

func TestMemberPriceShareLock(t *testing.T) {
	for _, op := range []string{"create_item", "update_item"} {
		t.Run(op, func(t *testing.T) {
			s := open(t)
			ct := s.cardType("引用锁")
			in := input("新项目")
			item := s.call("create_item", input("已有项目"), 200)
			in["member_prices"] = []any{map[string]any{"card_type_id": ct, "price": "10.00"}}
			if op == "update_item" {
				in = update(in, item["id"])
			}
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			if _, err := tx.Exec(context.Background(), "UPDATE card_types SET active=false WHERE id=$1", ct); err != nil {
				t.Fatal(err)
			}
			done := make(chan optest.Result, 1)
			go func() { done <- s.HTTPWithToken(t, s.Owner, op, in) }()
			dbtest.WaitForLockWait(t, s.Pool)
			if err := tx.Commit(context.Background()); err != nil {
				t.Fatal(err)
			}
			if r := <-done; r.Status != 409 {
				t.Fatal(r)
			}
		})
	}
}

func TestMemberPriceWritePermissionsAndAI(t *testing.T) {
	s := open(t)
	ct := s.cardType("权限卡")
	in := input("会员价格权限")
	in["member_prices"] = []any{map[string]any{"card_type_id": ct, "price": "10.00"}}
	item := s.call("create_item", in, 200)
	_, token := s.Hire("普通员工", "002", "memberdenied")
	for _, tc := range []struct {
		op   string
		args map[string]any
	}{{"create_item", in}, {"update_item", update(in, item["id"])}} {
		before := s.call("list_items", map[string]any{}, 200)
		if r := s.HTTPWithToken(t, token, tc.op, tc.args); r.Status != 403 {
			t.Fatal(r)
		}
		if r := s.AI(t, s.MustCaller(s.Owner), tc.op, tc.args); r.Status != 409 {
			t.Fatal(r)
		}
		if after := s.call("list_items", map[string]any{}, 200); !reflect.DeepEqual(before, after) {
			t.Fatal("denied write changed member prices")
		}
	}
}

func TestMemberPriceStoreScope(t *testing.T) {
	s := open(t)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var store, ct uuid.UUID
	if err := tx.QueryRow(context.Background(), "INSERT INTO stores (tenant_id,name) VALUES ($1,'二店') RETURNING id", s.Res.TenantID).Scan(&store); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(context.Background(), "INSERT INTO card_types (tenant_id,store_id,name,kind,validity_kind) VALUES ($1,$2,'外店类型','stored_value','none') RETURNING id", s.Res.TenantID, store).Scan(&ct); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	item := s.call("create_item", input("门店项目"), 200)
	in := input("外店引用")
	in["member_prices"] = []any{map[string]any{"card_type_id": ct, "price": "10.00"}}
	s.call("create_item", in, 404)
	s.call("update_item", update(in, item["id"]), 404)
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "quote_item_price", map[string]any{"item_id": item["id"], "staff_id": s.Res.OwnerID, "card_type_id": ct})
		if r.Status != 404 {
			t.Fatal(ch.Name, r)
		}
	}
}

func TestQuoteUsesSnapshotRules(t *testing.T) {
	s := open(t)
	ct := uuid.MustParse(s.cardType("快照会员"))
	in := input("快照取价")
	in["price"] = "50.00"
	in["member_prices"] = []any{map[string]any{"card_type_id": ct, "price": "80.00"}}
	item := s.call("create_item", in, 200)
	s.memberRule("member_first", 0)
	ctx := context.Background()
	var snapshot uuid.UUID
	if err := db.InTenant(ctx, s.Pool, s.Res.TenantID, func(q *dbgen.Queries) error {
		var err error
		snapshot, err = settings.TakeSnapshot(ctx, q, s.Res.TenantID, s.Res.StoreID, time.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	s.memberRule("lowest", 0)
	if err := db.InTenant(ctx, s.Pool, s.Res.TenantID, func(q *dbgen.Queries) error {
		rules, err := settings.LoadSnapshot(ctx, q, s.Res.StoreID, snapshot)
		if err != nil {
			return err
		}
		price, err := catalog.Quote(ctx, q, s.Res.StoreID, uuid.MustParse(item["id"].(string)), &s.Res.OwnerID, &ct, rules)
		if err != nil {
			return err
		}
		if price.Price.String() != "80.00" || price.Source != "member" {
			t.Fatalf("snapshot Quote = %+v, want member 80.00", price)
		}
		day, err := settings.BusinessDate(ctx, q, s.Res.StoreID, time.Now())
		if err != nil {
			return err
		}
		current, err := settings.Effective(ctx, q, s.Res.StoreID, day)
		if err != nil {
			return err
		}
		price, err = catalog.Quote(ctx, q, s.Res.StoreID, uuid.MustParse(item["id"].(string)), &s.Res.OwnerID, &ct, current)
		if err != nil {
			return err
		}
		if price.Price.String() != "50.00" || price.Source != "standard" {
			t.Fatalf("current Quote = %+v, want standard 50.00", price)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestQuoteNilStaff(t *testing.T) {
	s := open(t)
	level := s.level(true)
	s.call("update_staff", map[string]any{"staff_id": s.Res.OwnerID, "name": "王老板", "number": "001", "level_id": level, "position_id": nil}, 200)
	ct := uuid.MustParse(s.cardType("无员工会员"))
	in := input("无员工报价")
	in["price"] = "100.00"
	in["level_prices"] = []any{map[string]any{"level_id": level, "price": "150.00"}}
	in["member_prices"] = []any{map[string]any{"card_type_id": ct, "price": "80.00"}}
	item := s.call("create_item", in, 200)
	if err := db.InTenant(context.Background(), s.Pool, s.Res.TenantID, func(q *dbgen.Queries) error {
		rules, err := settings.Effective(context.Background(), q, s.Res.StoreID, settings.Date(time.Now()))
		if err != nil {
			return err
		}
		for _, card := range []*uuid.UUID{nil, &ct} {
			quote, err := catalog.Quote(context.Background(), q, s.Res.StoreID, uuid.MustParse(item["id"].(string)), nil, card, rules)
			if err != nil {
				return err
			}
			price, source := "100.00", "standard"
			if card != nil {
				price, source = "80.00", "member"
			}
			if quote.Price.String() != price || quote.Source != source || quote.LevelID != nil {
				t.Fatal(quote)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
