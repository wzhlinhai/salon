package orders_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/orders"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func (s *fixture) previewDraft(lines ...orders.OrderItemInput) map[string]any {
	s.t.Helper()
	if lines == nil {
		lines = []orders.OrderItemInput{s.line(1)}
	}
	in := s.opening(lines...)
	in.CustomerID = &s.customer
	return s.call("create_draft", map[string]any{"operation": "open_order", "params": in, "summary": "开单预览", "utterance": "开单"}, 200)
}

func (s *fixture) itemWithMemberPrice(ct map[string]any, member string) {
	s.t.Helper()
	in := s.itemInput("剪发", 3)
	in["id"], in["active"] = s.item, true
	in["member_prices"] = []any{map[string]any{"card_type_id": ct["id"], "price": member}}
	s.call("update_item", in, 200)
}

func TestDraftPreviewBothChannelsAndPrivacy(t *testing.T) {
	s := open(t)
	ct := s.paymentType("会员卡", nil)
	s.itemWithMemberPrice(ct, "80.00")
	s.paymentCard(ct, "500", "0")
	draft := s.previewDraft(s.line(1))
	var preview map[string]any
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "get_draft", map[string]any{"id": draft["id"]})
		if r.Status != 200 || r.Body["preview"] == nil || r.Body["preview_error"] != nil {
			t.Fatalf("%s: %v", ch.Name, r)
		}
		preview = r.Body["preview"].(map[string]any)
		raw, err := json.Marshal(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "13800000001") {
			t.Fatalf("%s leaked a phone: %s", ch.Name, raw)
		}
	}
	line := preview["lines"].([]any)[0].(map[string]any)
	staff := line["staff"].([]any)[0].(map[string]any)
	if line["item_name"] != "剪发" || line["price"] != "150.00" || line["price_source"] != "level" || staff["staff_name"] != "首席员工" {
		t.Fatal(line)
	}
	if _, ok := staff["price"]; ok {
		t.Fatal("staff preview still has a price", staff)
	}
	cards := preview["cards"].([]any)
	if len(cards) != 1 || cards[0].(map[string]any)["card_type_name"] != "会员卡" || cards[0].(map[string]any)["balance"] != "500.00" {
		t.Fatal(cards)
	}
	member := cards[0].(map[string]any)["member_prices"].([]any)[0].(map[string]any)
	if member["price"] != "80.00" || member["price_source"] != "member" || member["line"] != float64(0) || member["item_id"] != s.item.String() {
		t.Fatal(member)
	}
	other := s.MustCaller(s.Owner)
	other.StoreID = s.otherStore()
	for _, ch := range s.Channels() {
		if r := ch.Call(t, other, "get_draft", map[string]any{"id": draft["id"]}); r.Status != 404 || r.Body["preview"] != nil {
			t.Fatalf("%s cross-store = %v", ch.Name, r)
		}
	}
	_, token := s.Hire("无权限助理", "009", "preview-none")
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(token), "get_draft", map[string]any{"id": draft["id"]})
		if r.Status != 404 || r.Body["preview"] != nil {
			t.Fatalf("%s invisible = %v", ch.Name, r)
		}
	}
	noView := s.MustCaller(s.Owner)
	delete(noView.Permissions, "customer.view")
	for _, ch := range s.Channels() {
		r := ch.Call(t, noView, "get_draft", map[string]any{"id": draft["id"]})
		if r.Status != 200 || r.Body["preview"] == nil || len(r.Body["preview"].(map[string]any)["cards"].([]any)) != 0 {
			t.Fatalf("%s no customer.view = %v", ch.Name, r)
		}
	}
	role := s.call("create_role", map[string]any{"name": "本人顾客开单", "permissions": []any{map[string]any{"permission": "order.create"}, map[string]any{"permission": "customer.view", "scope": "self"}}}, 200)
	_, selfToken := s.Hire("限本人", "010", "preview-self", uuid.MustParse(role["id"].(string)))
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(selfToken), "get_draft", map[string]any{"id": draft["id"]})
		if r.Status != 200 || r.Body["preview"] == nil || len(r.Body["preview"].(map[string]any)["cards"].([]any)) != 0 {
			t.Fatalf("%s out-of-scope = %v", ch.Name, r)
		}
	}
}

func TestDraftPreviewRuleError(t *testing.T) {
	s := open(t)
	in := s.opening(orders.OrderItemInput{ItemID: s.item, Staff: []orders.OrderItemStaffInput{{Slot: 9, StaffID: s.employee}}})
	draft := s.call("create_draft", map[string]any{"operation": "open_order", "params": in, "summary": "错岗位", "utterance": "开单"}, 200)
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "get_draft", map[string]any{"id": draft["id"]})
		if r.Status != 200 || r.Body["preview"] != nil || r.Body["preview_error"] != "项目没有这个岗位" {
			t.Fatalf("%s: %v", ch.Name, r)
		}
	}
}

func TestDraftPreviewQueryCount(t *testing.T) {
	s := open(t)
	ct := s.paymentType("会员卡", nil)
	s.paymentCard(ct, "500", "0")
	one := s.previewDraft(s.line(1))
	many := s.previewDraft(s.line(1), s.line(2), s.line(3), s.line(1), s.line(2))
	counter := &paymentQueryCounter{}
	cfg := s.Pool.Config().Copy()
	cfg.ConnConfig.Tracer = counter
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	app := optest.New(t, pool)
	c := s.MustCaller(s.Owner)
	for _, ch := range app.Channels() {
		counter.all.Store(0)
		if r := ch.Call(t, c, "get_draft", map[string]any{"id": one["id"]}); r.Status != 200 {
			t.Fatal(r)
		}
		a := counter.all.Load()
		counter.all.Store(0)
		if r := ch.Call(t, c, "get_draft", map[string]any{"id": many["id"]}); r.Status != 200 {
			t.Fatal(r)
		}
		b := counter.all.Load()
		if a != b {
			t.Fatalf("%s query count grew with lines: %d != %d", ch.Name, a, b)
		}
	}
}

func TestDraftPreviewAddItemUsesOrderSnapshot(t *testing.T) {
	s := open(t)
	ct := s.paymentType("会员卡", nil)
	s.itemWithMemberPrice(ct, "200.00")
	s.paymentCard(ct, "500", "0")
	order := s.customerOrder(s.line(1))
	s.paymentSetting("member_price_rule", "lowest")
	draft := s.call("create_draft", map[string]any{"operation": "add_order_item", "params": orders.AddOrderItemInput{OrderID: id(order["id"]), Item: s.line(1)}, "summary": "加项目", "utterance": "再加一个项目"}, 200)
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "get_draft", map[string]any{"id": draft["id"]})
		if r.Status != 200 || r.Body["preview"] == nil || r.Body["preview_error"] != nil {
			t.Fatalf("%s: %v", ch.Name, r)
		}
		preview := r.Body["preview"].(map[string]any)
		line := preview["lines"].([]any)[0].(map[string]any)
		if line["item_name"] != "剪发" || line["price"] != "150.00" || line["staff"].([]any)[0].(map[string]any)["slot"] != float64(1) {
			t.Fatal(line)
		}
		member := preview["cards"].([]any)[0].(map[string]any)["member_prices"].([]any)[0].(map[string]any)
		if member["price"] != "200.00" || member["price_source"] != "member" {
			t.Fatalf("%s snapshot member price = %v", ch.Name, member)
		}
	}
}

func TestDraftPreviewSlotOnePricing(t *testing.T) {
	s := open(t)
	ct := s.paymentType("会员卡", nil)
	levelB := s.call("create_level", map[string]any{"name": "总监"}, 200)
	employeeB, _ := s.Hire("总监员工", "011", "preview-level-b")
	s.call("update_staff", map[string]any{"staff_id": employeeB, "name": "总监员工", "number": "011", "level_id": id(levelB["id"]), "position_id": nil}, 200)
	in := s.itemInput("剪发", 3)
	in["id"], in["active"] = s.item, true
	in["level_prices"] = []any{
		map[string]any{"level_id": s.level, "price": "150.00"},
		map[string]any{"level_id": id(levelB["id"]), "price": "200.00"},
	}
	in["member_prices"] = []any{map[string]any{"card_type_id": ct["id"], "price": "80.00"}}
	s.call("update_item", in, 200)
	s.paymentCard(ct, "500", "0")
	onlySecond := s.previewDraft(orders.OrderItemInput{ItemID: s.item, Staff: []orders.OrderItemStaffInput{{Slot: 2, StaffID: employeeB}}})
	firstAndSecond := s.previewDraft(orders.OrderItemInput{ItemID: s.item, Staff: []orders.OrderItemStaffInput{{Slot: 1, StaffID: s.employee}, {Slot: 2, StaffID: employeeB}}})
	for _, tc := range []struct {
		name      string
		draft     map[string]any
		wantPrice string
		wantSrc   string
		wantLevel *uuid.UUID
	}{
		{"slot 1 empty", onlySecond, "100.00", "standard", nil},
		{"slot 1 level A", firstAndSecond, "150.00", "level", &s.level},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, ch := range s.Channels() {
				r := ch.Call(t, s.MustCaller(s.Owner), "get_draft", map[string]any{"id": tc.draft["id"]})
				if r.Status != 200 {
					t.Fatal(r)
				}
				preview := r.Body["preview"].(map[string]any)
				line := preview["lines"].([]any)[0].(map[string]any)
				if line["price"] != tc.wantPrice || line["price_source"] != tc.wantSrc {
					t.Fatalf("%s %s line = %v", ch.Name, tc.name, line)
				}
				if tc.wantLevel != nil && line["level_id"] != tc.wantLevel.String() {
					t.Fatalf("%s %s level = %v", ch.Name, tc.name, line)
				}
				member := preview["cards"].([]any)[0].(map[string]any)["member_prices"].([]any)[0].(map[string]any)
				if member["line"] != float64(0) || member["price"] != "80.00" {
					t.Fatalf("%s %s member = %v", ch.Name, tc.name, member)
				}
			}
		})
	}
}

func TestDraftPreviewAddItemRejectsPaidOrder(t *testing.T) {
	s := open(t)
	order := s.customerOrder(s.line(1, 2, 3))
	s.paid(order, payment("cash", "150.00"))
	draft := s.call("create_draft", map[string]any{"operation": "add_order_item", "params": orders.AddOrderItemInput{OrderID: id(order["id"]), Item: s.line(2)}, "summary": "加项目", "utterance": "再加一个项目"}, 200)
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "get_draft", map[string]any{"id": draft["id"]})
		if r.Status != 200 || r.Body["preview"] != nil || r.Body["preview_error"] != "单已结账" {
			t.Fatalf("%s: %v", ch.Name, r)
		}
	}
}

func TestDraftPreviewBackfillFuture(t *testing.T) {
	s := open(t)
	tomorrow := settings.Date(s.Now()).AddDate(0, 0, 1).Format(settings.DateLayout)
	in := s.opening(s.line(1))
	in.PaperDate = &tomorrow
	draft := s.call("create_draft", map[string]any{"operation": "open_order", "params": in, "summary": "补录", "utterance": "补录"}, 200)
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "get_draft", map[string]any{"id": draft["id"]})
		if r.Status != 200 || r.Body["preview"] != nil || r.Body["preview_error"] != "补录日期不能晚于今天" {
			t.Fatalf("%s: %v", ch.Name, r)
		}
	}
}
