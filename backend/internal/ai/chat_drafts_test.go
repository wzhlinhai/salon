package ai_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/ai"
	"github.com/wzhlinhai/salon/backend/internal/groupbuy"
	"github.com/wzhlinhai/salon/backend/internal/groupbuy/fakeprovider"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

type draftFixture struct {
	*optest.Shop
	t        *testing.T
	position uuid.UUID
	level    uuid.UUID
	stylist  uuid.UUID
	item     uuid.UUID
	customer uuid.UUID
	cardType uuid.UUID
	card     uuid.UUID
}

func newDraftFixture(t *testing.T) *draftFixture {
	t.Helper()
	s := &draftFixture{Shop: optest.OpenShop(t), t: t}
	s.SetNow(settings.Date(s.Now()).Add(12 * time.Hour))
	s.position = id(t, s.Call(t, s.Owner, "create_position", map[string]any{"name": "大工"}, 200).Body, "id")
	s.level = id(t, s.Call(t, s.Owner, "create_level", map[string]any{"name": "首席"}, 200).Body, "id")
	s.stylist, _ = s.Hire("王师傅", "002", "draft-stylist")
	s.Call(t, s.Owner, "update_staff", map[string]any{"staff_id": s.stylist, "name": "王师傅", "number": "002", "level_id": s.level, "position_id": nil}, 200)
	s.cardType = id(t, s.Call(t, s.Owner, "create_card_type", map[string]any{"name": "会员卡", "kind": "stored_value", "validity_kind": "none"}, 200).Body, "id")
	r := s.Call(t, s.Owner, "create_item", map[string]any{
		"name": "剪发", "price": "100.00", "discountable": true, "labor_fee_amount": nil, "labor_fee_percent": nil,
		"positions":     []any{map[string]any{"position_id": s.position, "required": true}},
		"level_prices":  []any{map[string]any{"level_id": s.level, "price": "150.00"}},
		"member_prices": []any{map[string]any{"card_type_id": s.cardType, "price": "80.00"}},
	}, 200)
	s.item = id(t, r.Body, "id")
	s.customer = id(t, s.Call(t, s.Owner, "create_customer", map[string]any{"name": "王姐", "phone": "13800000009"}, 200).Body["customer"].(map[string]any), "id")
	s.card = id(t, s.Call(t, s.Owner, "open_card", map[string]any{
		"customer_id": s.customer, "card_type_id": s.cardType, "principal": "500.00", "bonus": "0.00",
		"sales_staff_ids": []uuid.UUID{s.stylist}, "payments": []any{map[string]any{"method": "cash", "amount": "500.00"}},
	}, 200).Body, "id")
	return s
}

func (s *draftFixture) line(staff uuid.UUID) map[string]any {
	return map[string]any{"item_id": s.item, "staff": []any{map[string]any{"slot": 1, "staff_id": staff}}}
}

func (s *draftFixture) orderParams() map[string]any {
	return map[string]any{"customer_id": s.customer, "gender": "female", "designated": true, "items": []any{s.line(s.stylist)}}
}

func (s *draftFixture) orderArgs() map[string]any {
	out := s.orderParams()
	out["summary"], out["utterance"] = "给王姐剪发", "给王姐开一单剪发"
	return out
}

func (s *draftFixture) draftBody(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	drafts := body["drafts"].([]any)
	if len(drafts) != 1 {
		t.Fatalf("drafts = %v", drafts)
	}
	return drafts[0].(map[string]any)
}

func (s *draftFixture) openOrders() []any {
	s.t.Helper()
	return s.Call(s.t, s.Owner, "list_open_orders", map[string]any{}, 200).Body["items"].([]any)
}

func TestChatDraftOpenOrderPreviewAndConfirmation(t *testing.T) {
	s := newDraftFixture(t)
	args, err := json.Marshal(s.orderArgs())
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeModel{reply: func(call int, _ []ai.Message, _ []ops.Tool) (ai.Message, error) {
		if call == 1 {
			return toolCall("call_1", "open_order", string(args)), nil
		}
		return assistant("已生成开单草稿，请确认。"), nil
	}}
	s.SetAIModel(fake)
	r := chatCall(t, s.Shop, s.Owner, []map[string]any{user("给王姐开一单剪发")})
	if r.Status != http.StatusOK || len(s.openOrders()) != 0 {
		t.Fatalf("chat = %d %v", r.Status, r.Body)
	}
	draft := s.draftBody(t, r.Body)
	if draft["operation"] != "open_order" || draft["status"] != "pending" {
		t.Fatal(draft)
	}
	preview := draft["preview"].(map[string]any)
	line := preview["lines"].([]any)[0].(map[string]any)
	staff := line["staff"].([]any)[0].(map[string]any)
	if staff["staff_name"] != "王师傅" || line["price"] != "150.00" || line["price_source"] != "level" {
		t.Fatal(line)
	}
	cards := preview["cards"].([]any)
	if len(cards) != 1 {
		t.Fatal(cards)
	}
	card := cards[0].(map[string]any)
	if card["card_type_name"] != "会员卡" || card["balance"] != "500.00" {
		t.Fatal(card)
	}
	member := card["member_prices"].([]any)[0].(map[string]any)
	if member["price"] != "80.00" || member["price_source"] != "member" {
		t.Fatal(member)
	}
	done := s.Call(t, s.Owner, "confirm_draft", map[string]any{"id": draft["id"]}, 200).Body
	if done["status"] != "confirmed" || len(s.openOrders()) != 1 {
		t.Fatal(done)
	}
}

func TestChatDraftChangeOrderStaff(t *testing.T) {
	s := newDraftFixture(t)
	order := s.Call(t, s.Owner, "open_order", s.orderParams(), 200).Body
	itemID := order["items"].([]any)[0].(map[string]any)["id"]
	assistantID, _ := s.Hire("小李", "003", "draft-assistant")
	args, err := json.Marshal(map[string]any{"order_id": order["id"], "order_item_id": itemID, "staff": []any{map[string]any{"slot": 1, "staff_id": assistantID}}, "summary": "助理改成小李", "utterance": "刚才王姐那单，助理改成小李"})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeModel{reply: func(call int, messages []ai.Message, _ []ops.Tool) (ai.Message, error) {
		switch call {
		case 1:
			return toolCall("call_1", "list_open_orders", "{}"), nil
		case 2:
			if !strings.Contains(lastToolResult(messages), order["id"].(string)) {
				t.Fatalf("list_open_orders did not return the order: %s", lastToolResult(messages))
			}
			return toolCall("call_2", "set_order_item_staff", string(args)), nil
		default:
			return assistant("已起草补人修改，请确认。"), nil
		}
	}}
	s.SetAIModel(fake)
	r := chatCall(t, s.Shop, s.Owner, []map[string]any{user("刚才王姐那单，助理改成小李")})
	draft := s.draftBody(t, r.Body)
	if draft["operation"] != "set_order_item_staff" || len(s.openOrders()) != 1 {
		t.Fatal(draft)
	}
	before := s.Call(t, s.Owner, "get_order", map[string]any{"id": order["id"]}, 200).Body
	if before["items"].([]any)[0].(map[string]any)["positions"].([]any)[0].(map[string]any)["staff_id"] != s.stylist.String() {
		t.Fatal(before)
	}
	s.Call(t, s.Owner, "confirm_draft", map[string]any{"id": draft["id"]}, 200)
	after := s.Call(t, s.Owner, "get_order", map[string]any{"id": order["id"]}, 200).Body
	if after["items"].([]any)[0].(map[string]any)["positions"].([]any)[0].(map[string]any)["staff_id"] != assistantID.String() {
		t.Fatal(after)
	}
}

func TestChatDraftGroupbuyOrder(t *testing.T) {
	s := newDraftFixture(t)
	s.Groupbuy.Register("douyin", fakeprovider.New("douyin", s.Now()))
	deal := s.Call(t, s.Owner, "create_groupbuy_deal", map[string]any{"platform": "douyin", "name": "剪发团购", "purchase_price": "99.00", "item_id": s.item}, 200).Body
	s.Call(t, s.Owner, "redeem_coupon", groupbuy.CouponRequest{Platform: "douyin", CouponCode: "chat-groupbuy", Key: "chat-groupbuy"}, 200)
	redemption := s.Call(t, s.Owner, "get_redemption", map[string]any{"key": "chat-groupbuy"}, 200).Body
	today := settings.Date(s.Now()).Format(settings.DateLayout)
	listArgs, err := json.Marshal(map[string]any{"from": today, "to": today, "unlinked_only": true})
	if err != nil {
		t.Fatal(err)
	}
	args, err := json.Marshal(map[string]any{"redemption_id": redemption["id"], "deal_id": deal["id"], "customer_id": s.customer, "gender": "female", "designated": true, "items": []any{s.line(s.stylist)}, "summary": "团购核销后开单", "utterance": "这单团购核销了，帮我开单"})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeModel{reply: func(call int, messages []ai.Message, _ []ops.Tool) (ai.Message, error) {
		switch call {
		case 1:
			return toolCall("call_1", "list_redemptions", string(listArgs)), nil
		case 2:
			if !strings.Contains(lastToolResult(messages), redemption["id"].(string)) {
				t.Fatalf("list_redemptions did not return the redemption: %s", lastToolResult(messages))
			}
			return toolCall("call_2", "open_groupbuy_order", string(args)), nil
		default:
			return assistant("已起草团购开单，请确认。"), nil
		}
	}}
	s.SetAIModel(fake)
	r := chatCall(t, s.Shop, s.Owner, []map[string]any{user("这单团购核销了，帮我开单")})
	draft := s.draftBody(t, r.Body)
	if draft["operation"] != "open_groupbuy_order" || len(s.openOrders()) != 0 || draft["preview"] == nil {
		t.Fatal(draft)
	}
	s.Call(t, s.Owner, "confirm_draft", map[string]any{"id": draft["id"]}, 200)
	orders := s.openOrders()
	if len(orders) != 1 {
		t.Fatal(orders)
	}
	linked := s.Call(t, s.Owner, "get_redemption", map[string]any{"key": "chat-groupbuy"}, 200).Body
	if linked["order_id"] != orders[0].(map[string]any)["id"] || linked["linked_at"] == nil {
		t.Fatal(linked)
	}
}

func TestChatDraftAdjustCardDualConfirm(t *testing.T) {
	s := newDraftFixture(t)
	today := settings.Date(s.Now()).Format(settings.DateLayout)
	s.Call(t, s.Owner, "change_setting", map[string]any{"key": "draft_dual_confirm", "value": "on", "effective_from": today}, 200)
	args, err := json.Marshal(map[string]any{"card_id": s.card, "principal": "600.00", "reason": "核对余额", "summary": "调整余额", "utterance": "把王姐卡余额改成六百"})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeModel{reply: func(call int, _ []ai.Message, _ []ops.Tool) (ai.Message, error) {
		if call == 1 {
			return toolCall("call_1", "adjust_card", string(args)), nil
		}
		return assistant("已起草调整单，请有权限的人确认。"), nil
	}}
	s.SetAIModel(fake)
	r := chatCall(t, s.Shop, s.Owner, []map[string]any{user("把王姐卡余额改成六百")})
	draft := s.draftBody(t, r.Body)
	if draft["operation"] != "adjust_card" {
		t.Fatal(draft)
	}
	owner := s.HTTPWithToken(t, s.Owner, "confirm_draft", map[string]any{"id": draft["id"]})
	if owner.Status != http.StatusConflict || !strings.Contains(owner.Detail(), "金额草稿必须由另一人确认") {
		t.Fatal(owner)
	}
	role := s.Call(t, s.Owner, "create_role", map[string]any{"name": "调卡员", "permissions": []any{map[string]any{"permission": "card.adjust"}, map[string]any{"permission": "customer.view"}}}, 200).Body
	_, token := s.Hire("店长甲", "004", "draft-manager", uuid.MustParse(role["id"].(string)))
	done := s.HTTPWithToken(t, token, "confirm_draft", map[string]any{"id": draft["id"]})
	if done.Status != http.StatusOK || done.Body["status"] != "confirmed" {
		t.Fatal(done)
	}
	card := s.Call(t, s.Owner, "list_customer_cards", map[string]any{"customer_id": s.customer}, 200).Body["items"].([]any)[0].(map[string]any)
	if card["principal"] != "600.00" {
		t.Fatal(card)
	}
}

func TestChatUpdateOwnDraftThenConfirm(t *testing.T) {
	s := newDraftFixture(t)
	fake := &fakeModel{reply: func(call int, messages []ai.Message, _ []ops.Tool) (ai.Message, error) {
		switch call {
		case 1:
			args, _ := json.Marshal(s.orderArgs())
			return toolCall("call_1", "open_order", string(args)), nil
		case 2:
			var draft map[string]any
			if err := json.Unmarshal([]byte(lastToolResult(messages)), &draft); err != nil {
				t.Fatalf("draft tool result: %v", err)
			}
			params := draft["params"].(map[string]any)
			params["gender"] = "male"
			args, _ := json.Marshal(map[string]any{"id": draft["id"], "params": params})
			return toolCall("call_2", "update_draft_params", string(args)), nil
		default:
			return assistant("已修改草稿，请确认。"), nil
		}
	}}
	s.SetAIModel(fake)
	r := chatCall(t, s.Shop, s.Owner, []map[string]any{user("改成男客")})
	draft := s.draftBody(t, r.Body)
	if draft["params"].(map[string]any)["gender"] != "male" {
		t.Fatal(draft)
	}
	done := s.Call(t, s.Owner, "confirm_draft", map[string]any{"id": draft["id"]}, 200).Body
	if done["status"] != "confirmed_modified" {
		t.Fatal(done)
	}
}

func TestChatUpdateMasterDraftRejected(t *testing.T) {
	s := newDraftFixture(t)
	existing := s.Call(t, s.Owner, "create_draft", map[string]any{"operation": "open_order", "params": s.orderParams(), "summary": "已有草稿", "utterance": "之前的口述"}, 200).Body
	args, err := json.Marshal(map[string]any{"id": existing["id"], "params": s.orderParams()})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeModel{reply: func(call int, messages []ai.Message, _ []ops.Tool) (ai.Message, error) {
		if call == 1 {
			return toolCall("call_1", "update_draft_params", string(args)), nil
		}
		return assistant(lastToolResult(messages)), nil
	}}
	s.SetAIModel(fake)
	r := chatCall(t, s.Shop, s.Owner, []map[string]any{user("改一下刚才那张草稿")})
	if r.Status != http.StatusOK {
		t.Fatal(r)
	}
	source := r.Body["sources"].([]any)[0].(map[string]any)
	if source["ok"] != false || !strings.Contains(source["error"].(string), "只能修改本次对话起草的草稿") {
		t.Fatal(source)
	}
	got := s.Call(t, s.Owner, "get_draft", map[string]any{"id": existing["id"]}, 200).Body
	if got["status"] != "pending" || len(got["events"].([]any)) != 1 {
		t.Fatal(got)
	}
}

func TestChatConfirmRequiresTargetPermission(t *testing.T) {
	s := newDraftFixture(t)
	draft := s.Call(t, s.Owner, "create_draft", map[string]any{"operation": "open_order", "params": s.orderParams(), "summary": "开单", "utterance": "开单"}, 200).Body
	_, token := s.Hire("助理", "005", "draft-no-permission")
	r := s.HTTPWithToken(t, token, "confirm_draft", map[string]any{"id": draft["id"]})
	if r.Status != http.StatusForbidden || !strings.Contains(r.Detail(), "permission denied") {
		t.Fatal(r)
	}
	if got := s.Call(t, s.Owner, "get_draft", map[string]any{"id": draft["id"]}, 200).Body; got["status"] != "pending" {
		t.Fatal(got)
	}
}

func TestChatDraftCallCap(t *testing.T) {
	s := newDraftFixture(t)
	args, err := json.Marshal(s.orderArgs())
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeModel{reply: func(call int, _ []ai.Message, _ []ops.Tool) (ai.Message, error) {
		if call == 1 {
			calls := make([]ai.ToolCall, 6)
			for i := range calls {
				calls[i] = ai.ToolCall{ID: fmt.Sprintf("call_%d", i+1), Name: "open_order", Arguments: string(args)}
			}
			return ai.Message{Role: ai.RoleAssistant, ToolCalls: calls}, nil
		}
		return assistant("最多五张草稿。"), nil
	}}
	s.SetAIModel(fake)
	r := chatCall(t, s.Shop, s.Owner, []map[string]any{user("开六单")})
	if r.Status != http.StatusOK || len(r.Body["drafts"].([]any)) != 5 {
		t.Fatal(r)
	}
	sources := r.Body["sources"].([]any)
	if len(sources) != 6 || !strings.Contains(sources[5].(map[string]any)["error"].(string), "一次对话最多起草 5 张草稿") {
		t.Fatal(sources)
	}
}

func TestChatUpdateDraftInvalidID(t *testing.T) {
	s := newDraftFixture(t)
	existing := s.Call(t, s.Owner, "create_draft", map[string]any{"operation": "open_order", "params": s.orderParams(), "summary": "已有草稿", "utterance": "之前的口述"}, 200).Body
	foreign, err := json.Marshal(map[string]any{"id": existing["id"], "params": s.orderParams()})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args string
	}{
		{"missing id", `{}`},
		{"malformed id", `{"id":"not-a-uuid"}`},
		{"foreign id", string(foreign)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeModel{reply: func(call int, messages []ai.Message, _ []ops.Tool) (ai.Message, error) {
				if call == 1 {
					return toolCall("call_1", "update_draft_params", tc.args), nil
				}
				return assistant(lastToolResult(messages)), nil
			}}
			s.SetAIModel(fake)
			r := chatCall(t, s.Shop, s.Owner, []map[string]any{user("改一下草稿")})
			if r.Status != http.StatusOK {
				t.Fatal(r)
			}
			source := r.Body["sources"].([]any)[0].(map[string]any)
			if source["ok"] != false || !strings.Contains(source["error"].(string), "只能修改本次对话起草的草稿") {
				t.Fatalf("%s source = %v", tc.name, source)
			}
		})
	}
	got := s.Call(t, s.Owner, "get_draft", map[string]any{"id": existing["id"]}, 200).Body
	if got["status"] != "pending" || len(got["events"].([]any)) != 1 {
		t.Fatal(got)
	}
}

func TestChatDraftCapCountsFailures(t *testing.T) {
	s := newDraftFixture(t)
	fake := &fakeModel{reply: func(call int, _ []ai.Message, _ []ops.Tool) (ai.Message, error) {
		if call == 1 {
			calls := make([]ai.ToolCall, 6)
			for i := range calls {
				calls[i] = ai.ToolCall{ID: fmt.Sprintf("call_%d", i+1), Name: "open_order", Arguments: `{}`}
			}
			return ai.Message{Role: ai.RoleAssistant, ToolCalls: calls}, nil
		}
		return assistant("最多五张草稿。"), nil
	}}
	s.SetAIModel(fake)
	r := chatCall(t, s.Shop, s.Owner, []map[string]any{user("连续起草六次")})
	if r.Status != http.StatusOK || len(r.Body["drafts"].([]any)) != 0 {
		t.Fatal(r)
	}
	sources := r.Body["sources"].([]any)
	if len(sources) != 6 {
		t.Fatal(sources)
	}
	for i := 0; i < 5; i++ {
		source := sources[i].(map[string]any)
		if source["ok"] != false || strings.Contains(source["error"].(string), "一次对话最多起草 5 张草稿") {
			t.Fatalf("source %d = %v", i, source)
		}
	}
	if !strings.Contains(sources[5].(map[string]any)["error"].(string), "一次对话最多起草 5 张草稿") {
		t.Fatal(sources[5])
	}
}
