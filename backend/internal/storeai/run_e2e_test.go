package storeai_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/ai"
	"github.com/wzhlinhai/salon/backend/internal/groupbuy"
	"github.com/wzhlinhai/salon/backend/internal/groupbuy/fakeprovider"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/storeai"
)

type promptingModel struct {
	mu       sync.Mutex
	calls    int
	toolSeen []string
}

type promptEvent struct {
	Kind    string `json:"kind"`
	Subject *struct {
		Kind string    `json:"kind"`
		ID   uuid.UUID `json:"id"`
	} `json:"subject"`
	Payload json.RawMessage `json:"payload"`
}

func (m *promptingModel) Chat(_ context.Context, messages []ai.Message, tools []ops.Tool) (ai.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	names := make([]string, 0, len(tools))
	hasRemind := false
	for _, tool := range tools {
		names = append(names, tool.Name)
		if tool.Name == "remind_me" {
			hasRemind = true
		}
	}
	m.toolSeen = append(m.toolSeen, names...)
	events := promptEvents(messages)
	last := messages[len(messages)-1]
	if last.Role == ai.RoleTool {
		if !hasRemind && strings.Contains(last.Content, `"items"`) {
			if subjectID, redeemedBy, ok := redemptionTarget(events, last.Content); ok {
				raw, _ := json.Marshal(map[string]any{
					"to_staff_id": redeemedBy, "kind": "inform",
					"subject_kind": storeai.SubjectCouponRedemption, "subject_id": subjectID,
				})
				return ai.Message{Role: ai.RoleAssistant, ToolCalls: []ai.ToolCall{{ID: "create_request", Name: "create_request", Arguments: string(raw)}}}, nil
			}
		}
		return finalTurn("安静"), nil
	}
	if hasRemind {
		for _, event := range events {
			if event.Subject != nil && event.Subject.Kind == storeai.SubjectCouponRedemption {
				raw, _ := json.Marshal(map[string]any{
					"tier": "now", "subject_kind": storeai.SubjectCouponRedemption, "subject_id": event.Subject.ID,
					"body": "这张券还没关联单据",
				})
				return ai.Message{Role: ai.RoleAssistant, ToolCalls: []ai.ToolCall{{ID: "remind_me", Name: "remind_me", Arguments: string(raw)}}}, nil
			}
		}
		return finalTurn("安静"), nil
	}
	for _, event := range events {
		if event.Subject == nil {
			continue
		}
		switch event.Kind {
		case storeai.EventCouponRedeemed, storeai.EventCouponUnlinked:
			raw, _ := json.Marshal(map[string]any{
				"subject_kind": event.Subject.Kind, "subject_id": event.Subject.ID, "minutes": 30,
			})
			return ai.Message{Role: ai.RoleAssistant, ToolCalls: []ai.ToolCall{{ID: "schedule_follow_up", Name: "schedule_follow_up", Arguments: string(raw)}}}, nil
		case storeai.EventFollowUpDue:
			raw, _ := json.Marshal(map[string]any{"from": "2026-11-27", "to": "2026-11-27", "unlinked_only": true})
			return ai.Message{Role: ai.RoleAssistant, ToolCalls: []ai.ToolCall{{ID: "list_redemptions", Name: "list_redemptions", Arguments: string(raw)}}}, nil
		case storeai.EventCouponLinked, storeai.EventCouponRevoked:
			raw, _ := json.Marshal(map[string]any{"status": "closed"})
			return ai.Message{Role: ai.RoleAssistant, ToolCalls: []ai.ToolCall{{ID: "finish", Name: "finish", Arguments: string(raw)}}}, nil
		}
	}
	return finalTurn("安静"), nil
}

func promptEvents(messages []ai.Message) []promptEvent {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != ai.RoleUser {
			continue
		}
		var events []promptEvent
		if json.Unmarshal([]byte(messages[i].Content), &events) == nil {
			return events
		}
	}
	return nil
}

func redemptionTarget(events []promptEvent, list string) (uuid.UUID, uuid.UUID, bool) {
	for _, event := range events {
		if event.Subject == nil {
			continue
		}
		var result struct {
			Items []struct {
				ID         uuid.UUID `json:"id"`
				RedeemedBy uuid.UUID `json:"redeemed_by"`
			} `json:"items"`
		}
		if json.Unmarshal([]byte(list), &result) != nil {
			return uuid.Nil, uuid.Nil, false
		}
		for _, item := range result.Items {
			if item.ID == event.Subject.ID {
				return item.ID, item.RedeemedBy, true
			}
		}
	}
	return uuid.Nil, uuid.Nil, false
}

func TestCouponAgentEndToEnd(t *testing.T) {
	shop := open(t)
	now := at("2026-11-27", 10, 0)
	shop.SetNow(now)
	shop.Groupbuy.Register("douyin", fakeprovider.New("douyin", now))
	redeemed := shop.Call(t, shop.Owner, "redeem_coupon", groupbuy.CouponRequest{Platform: "douyin", CouponCode: "e2e", Key: "e2e"}, 200)
	redemptionID := uuid.MustParse(redeemed.Body["id"].(string))
	model := &promptingModel{}
	runner := newRunner(shop, model)

	runOnce(t, shop, now, runner.Run)
	if timers := allTimers(t, shop, storeInbox(t, shop).ID); len(timers) != 1 || timers[0].Status != "scheduled" {
		t.Fatalf("timers after redeem = %+v", timers)
	}

	now = now.Add(30 * time.Minute)
	shop.SetNow(now)
	runOnce(t, shop, now, runner.Run)
	if requests := allEvents(t, shop, staffInbox(t, shop, shop.Res.OwnerID).ID); len(requests) == 0 || requests[len(requests)-1].Kind != storeai.EventRequest {
		t.Fatalf("owner inbox = %+v", requests)
	}

	runOnce(t, shop, now, runner.Run)
	rows := outboxRows(t, shop, shop.Res.OwnerID)
	if len(rows) != 1 || rows[0].SubjectID != redemptionID || rows[0].Status != "queued" {
		t.Fatalf("outbox = %+v", rows)
	}
	runOnce(t, shop, now, runner.Run)

	list := shop.Call(t, shop.Owner, "list_my_reminders", map[string]any{}, 200)
	if items := list.Body["items"].([]any); len(items) != 1 {
		t.Fatalf("reminders before link = %v", list.Body)
	}

	item := shop.Call(t, shop.Owner, "create_item", map[string]any{
		"name": "履约项目", "price": "398.00", "discountable": true,
		"labor_fee_amount": nil, "labor_fee_percent": nil, "positions": []any{}, "level_prices": []any{},
	}, 200)
	itemID := uuid.MustParse(item.Body["id"].(string))
	deal := shop.Call(t, shop.Owner, "create_groupbuy_deal", map[string]any{
		"platform": "douyin", "name": "e2e 券", "purchase_price": "99.00", "item_id": itemID,
	}, 200)
	order := shop.Call(t, shop.Owner, "open_order", map[string]any{
		"gender": "female", "designated": false,
		"items": []any{map[string]any{"item_id": itemID, "staff": []any{}}},
	}, 200)
	orderID := uuid.MustParse(order.Body["id"].(string))
	shop.Call(t, shop.Owner, "link_coupon_order", map[string]any{
		"redemption_id": redemptionID, "deal_id": deal.Body["id"], "order_id": orderID,
	}, 200)

	list = shop.Call(t, shop.Owner, "list_my_reminders", map[string]any{}, 200)
	if items := list.Body["items"].([]any); len(items) != 0 {
		t.Fatalf("reminders after link = %v", list.Body)
	}
}

func TestStoreToolsRejectLinkedCoupon(t *testing.T) {
	shop := open(t)
	now := at("2026-11-27", 10, 0)
	shop.SetNow(now)
	shop.Groupbuy.Register("douyin", fakeprovider.New("douyin", now))
	redeemed := shop.Call(t, shop.Owner, "redeem_coupon", groupbuy.CouponRequest{Platform: "douyin", CouponCode: "linked-tools", Key: "linked-tools"}, 200)
	redemptionID := uuid.MustParse(redeemed.Body["id"].(string))
	item := shop.Call(t, shop.Owner, "create_item", map[string]any{
		"name": "关联项目", "price": "398.00", "discountable": true,
		"labor_fee_amount": nil, "labor_fee_percent": nil, "positions": []any{}, "level_prices": []any{},
	}, 200)
	itemID := uuid.MustParse(item.Body["id"].(string))
	deal := shop.Call(t, shop.Owner, "create_groupbuy_deal", map[string]any{
		"platform": "douyin", "name": "关联券", "purchase_price": "99.00", "item_id": itemID,
	}, 200)
	order := shop.Call(t, shop.Owner, "open_order", map[string]any{
		"gender": "female", "designated": false,
		"items": []any{map[string]any{"item_id": itemID, "staff": []any{}}},
	}, 200)
	shop.Call(t, shop.Owner, "link_coupon_order", map[string]any{
		"redemption_id": redemptionID, "deal_id": deal.Body["id"], "order_id": order.Body["id"],
	}, 200)
	model := &scriptedModel{turns: []ai.Message{
		toolTurn("schedule_follow_up", map[string]any{"subject_kind": storeai.SubjectCouponRedemption, "subject_id": redemptionID, "minutes": 30}),
		toolTurn("create_request", map[string]any{"to_staff_id": shop.Res.OwnerID, "kind": "inform", "subject_kind": storeai.SubjectCouponRedemption, "subject_id": redemptionID}),
		finalTurn("结束"),
	}}
	runOnce(t, shop, now, newRunner(shop, model).Run)
	final := model.messages[len(model.messages)-1]
	found := 0
	for _, message := range final {
		if message.Role == ai.RoleTool && strings.Contains(message.Content, "这张券已关联或已撤销") {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("tool messages = %+v", final)
	}
}
