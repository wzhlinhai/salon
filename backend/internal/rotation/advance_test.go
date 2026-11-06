package rotation_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func (s *fixture) openFor(staffID uuid.UUID, designated bool, itemIDs ...uuid.UUID) uuid.UUID {
	s.t.Helper()
	items := []any{}
	for _, itemID := range itemIDs {
		items = append(items, map[string]any{"item_id": itemID, "staff": []any{map[string]any{"slot": 1, "staff_id": staffID}}})
	}
	order := s.call("open_order", map[string]any{"gender": "female", "designated": designated, "items": items}, 200)
	return id(order["id"])
}

func (s *fixture) openForStaff(staffIDs []uuid.UUID, designated bool) uuid.UUID {
	s.t.Helper()
	items := []any{}
	for _, staffID := range staffIDs {
		items = append(items, map[string]any{"item_id": s.item, "staff": []any{map[string]any{"slot": 1, "staff_id": staffID}}})
	}
	order := s.call("open_order", map[string]any{"gender": "female", "designated": designated, "items": items}, 200)
	return id(order["id"])
}

func (s *fixture) pay(orderID uuid.UUID, amount string) {
	s.t.Helper()
	s.call("checkout_orders", map[string]any{"orders": []any{map[string]any{"order_id": orderID, "payments": []any{map[string]any{"method": "cash", "amount": amount}}}}}, 200)
}

func (s *fixture) assign(staffID uuid.UUID, designated bool, orderID uuid.UUID) {
	s.t.Helper()
	s.call("assign_turn", map[string]any{"queue_id": s.queue, "staff_id": staffID, "designated": designated, "order_id": orderID}, 200)
}

func (s *fixture) entry(staffID uuid.UUID) map[string]any {
	s.t.Helper()
	for _, entry := range entries(s.today()) {
		if entry["staff"].(map[string]any)["id"] == staffID.String() {
			return entry
		}
	}
	s.t.Fatalf("staff %s not in rotation", staffID)
	return nil
}

func TestDesignatedTakesTurn(t *testing.T) {
	t.Run("no turn", func(t *testing.T) {
		s := openRotation(t)
		for _, member := range s.members[:3] {
			s.join(member)
		}
		s.setSetting("rotation_designated_takes_turn", "no")
		order := s.openFor(s.members[0], true, s.item)
		s.assign(s.members[0], true, order)
		if got := s.entry(s.members[0]); got["status"] != "serving" || got["serving_designated"] != true {
			t.Fatal(got)
		}
		s.pay(order, "100")
		assertNames(t, s.names(s.today()), []string{"甲", "乙", "丙"})
		if got := s.entry(s.members[0]); got["status"] != "waiting" || got["position"] != float64(1) || got["serving_order_id"] != nil {
			t.Fatal(got)
		}
		if got := s.event("advance_designated_no_turn"); got["designated"] != true || got["order_id"] != order.String() {
			t.Fatal(got)
		}
	})
	t.Run("takes turn", func(t *testing.T) {
		s := openRotation(t)
		for _, member := range s.members[:3] {
			s.join(member)
		}
		order := s.openFor(s.members[0], true, s.item)
		s.assign(s.members[0], true, order)
		s.pay(order, "100")
		assertNames(t, s.names(s.today()), []string{"乙", "丙", "甲"})
		if got := s.event("advance_whole"); got["designated"] != true {
			t.Fatal(got)
		}
	})
}

func TestAutoSkipServingDesignated(t *testing.T) {
	t.Run("on", func(t *testing.T) {
		s := openRotation(t)
		s.join(s.members[0])
		s.join(s.members[1])
		order := s.openFor(s.members[0], true, s.item)
		s.assign(s.members[0], true, order)
		view := s.call("assign_turn", map[string]any{"queue_id": s.queue}, 200)
		if got := findStatus(view, "serving"); got == nil || got["staff"].(map[string]any)["id"] != s.members[1].String() {
			t.Fatal(view)
		}
		assertNames(t, s.names(s.today()), []string{"乙", "甲"})
		event := s.event("auto_skip")
		if event["reason"] != "点客服务中" || event["staff"].(map[string]any)["id"] != s.members[0].String() || event["from_position"] != float64(1) || event["to_position"] != float64(2) {
			t.Fatal(event)
		}
	})
	t.Run("off", func(t *testing.T) {
		s := openRotation(t)
		s.join(s.members[0])
		s.join(s.members[1])
		s.setSetting("rotation_skip_when_serving_designated", "no")
		order := s.openFor(s.members[0], true, s.item)
		s.assign(s.members[0], true, order)
		view := s.call("assign_turn", map[string]any{"queue_id": s.queue}, 200)
		if !servingStaff(view, s.members[1]) {
			t.Fatal(view)
		}
		assertNames(t, s.names(s.today()), []string{"甲", "乙"})
		for _, event := range s.events(settingsDate(s), settingsDate(s)) {
			if event["kind"] == "auto_skip" {
				t.Fatal("auto_skip recorded with setting off", event)
			}
		}
	})
}

func TestHalfTurnByPrice(t *testing.T) {
	t.Run("below", func(t *testing.T) {
		s := openRotation(t)
		for _, member := range s.members[:3] {
			s.join(member)
		}
		s.setSetting("rotation_half_rule", "by_price")
		s.setSetting("rotation_half_price_below", 150)
		first := s.openFor(s.members[0], false, s.item)
		s.assign(s.members[0], false, first)
		s.pay(first, "100")
		if got := s.entry(s.members[0]); got["status"] != "waiting" || got["position"] != float64(1) || got["half_pending"] != true {
			t.Fatal(got)
		}
		if got := s.event("advance_half"); got["from_position"] != float64(1) || got["to_position"] != float64(1) {
			t.Fatal(got)
		}
		second := s.openFor(s.members[0], false, s.item)
		s.assign(s.members[0], false, second)
		s.pay(second, "100")
		assertNames(t, s.names(s.today()), []string{"乙", "丙", "甲"})
		if got := s.entry(s.members[0]); got["half_pending"] != false || got["position"] != float64(3) {
			t.Fatal(got)
		}
	})
	t.Run("at threshold", func(t *testing.T) {
		s := openRotation(t)
		for _, member := range s.members[:3] {
			s.join(member)
		}
		s.setSetting("rotation_half_rule", "by_price")
		s.setSetting("rotation_half_price_below", 100)
		order := s.openFor(s.members[0], false, s.item)
		s.assign(s.members[0], false, order)
		s.pay(order, "100")
		assertNames(t, s.names(s.today()), []string{"乙", "丙", "甲"})
		if got := s.entry(s.members[0]); got["half_pending"] != false {
			t.Fatal(got)
		}
		if got := s.event("advance_whole"); got["staff"].(map[string]any)["id"] != s.members[0].String() {
			t.Fatal(got)
		}
	})
	t.Run("whole clears half mark", func(t *testing.T) {
		s := openRotation(t)
		for _, member := range s.members[:3] {
			s.join(member)
		}
		s.setSetting("rotation_half_rule", "by_price")
		s.setSetting("rotation_half_price_below", 150)
		first := s.openFor(s.members[0], false, s.item)
		s.assign(s.members[0], false, first)
		s.pay(first, "100")
		if got := s.entry(s.members[0]); got["half_pending"] != true {
			t.Fatal(got)
		}
		s.setSetting("rotation_half_price_below", 50)
		second := s.openFor(s.members[0], false, s.item)
		s.assign(s.members[0], false, second)
		s.pay(second, "100")
		assertNames(t, s.names(s.today()), []string{"乙", "丙", "甲"})
		if got := s.entry(s.members[0]); got["half_pending"] != false {
			t.Fatal(got)
		}
	})
}

func TestHalfTurnByItem(t *testing.T) {
	s := openRotation(t)
	half := s.createItem("半轮项目", "100")
	normal := s.createItem("整轮项目", "100")
	s.setSetting("rotation_half_rule", "by_item")
	reject(t, s.HTTPWithToken(t, s.Owner, "set_half_turn_items", map[string]any{"item_ids": []uuid.UUID{uuid.New()}}), 409, "项目不存在")
	s.call("set_half_turn_items", map[string]any{"item_ids": []uuid.UUID{half}}, 200)
	for _, member := range s.members[:3] {
		s.join(member)
	}
	first := s.openFor(s.members[0], false, half)
	s.assign(s.members[0], false, first)
	s.pay(first, "100")
	if got := s.entry(s.members[0]); got["half_pending"] != true || got["position"] != float64(1) {
		t.Fatal(got)
	}
	mixed := s.openFor(s.members[0], false, half, normal)
	s.assign(s.members[0], false, mixed)
	s.pay(mixed, "200")
	assertNames(t, s.names(s.today()), []string{"乙", "丙", "甲"})
	if got := s.entry(s.members[0]); got["half_pending"] != false || got["position"] != float64(3) {
		t.Fatal(got)
	}
	// 另一名员工没有半轮标记，混合项目应直接记整轮，而不是第一次半轮只做标记。
	mixedAgain := s.openFor(s.members[1], false, half, normal)
	s.assign(s.members[1], false, mixedAgain)
	s.pay(mixedAgain, "200")
	assertNames(t, s.names(s.today()), []string{"丙", "甲", "乙"})
	if got := s.entry(s.members[1]); got["half_pending"] != false || got["position"] != float64(3) {
		t.Fatal(got)
	}
}

func TestRotationStats(t *testing.T) {
	s := openRotation(t)
	s.join(s.members[0])
	s.join(s.members[1])
	designated := s.openFor(s.members[0], true, s.item)
	s.assign(s.members[0], true, designated)
	s.pay(designated, "100")
	rotation := s.openFor(s.members[1], false, s.item)
	s.assign(s.members[1], false, rotation)
	s.pay(rotation, "100")
	s.join(s.members[2])
	unpaid := s.openFor(s.members[2], false, s.item)
	s.assign(s.members[2], false, unpaid)
	day := settingsDate(s).Format(settings.DateLayout)
	stats := s.call("get_rotation_stats", map[string]any{"from": day, "to": day}, 200)
	if stats["designated"] != float64(1) || stats["rotation"] != float64(1) || stats["total"] != float64(2) {
		t.Fatal(stats)
	}
	entries := []map[string]any{}
	for _, entry := range stats["entries"].([]any) {
		entries = append(entries, entry.(map[string]any))
	}
	if len(entries) != 2 {
		t.Fatal(entries)
	}
	for _, entry := range entries {
		staffID := entry["staff"].(map[string]any)["id"]
		switch staffID {
		case s.members[0].String():
			if entry["designated"] != float64(1) || entry["rotation"] != float64(0) || entry["total"] != float64(1) {
				t.Fatal(entry)
			}
		case s.members[1].String():
			if entry["designated"] != float64(0) || entry["rotation"] != float64(1) || entry["total"] != float64(1) {
				t.Fatal(entry)
			}
		default:
			t.Fatal(entry)
		}
	}
	owner := s.MustCaller(s.Owner)
	for _, ch := range s.Channels() {
		if r := ch.Call(t, owner, "get_rotation_stats", map[string]any{"from": day, "to": day}); r.Status != 200 || r.Body["total"] != float64(2) {
			t.Fatal(ch.Name, r)
		}
		if r := ch.Call(t, owner, "list_rotation_events", map[string]any{"from": day, "to": day}); r.Status != 200 || len(r.Body["entries"].([]any)) == 0 {
			t.Fatal(ch.Name, r)
		}
	}
}

func TestRotationStatsDedupAcrossQueues(t *testing.T) {
	s := openRotation(t)
	second := s.call("create_rotation_queue", map[string]any{"name": "助理牌"}, 200)
	secondID := id(second["id"])
	s.call("set_queue_members", map[string]any{"queue_id": secondID, "staff_ids": []uuid.UUID{s.members[0]}}, 200)
	s.join(s.members[0])
	s.call("join_rotation", map[string]any{"queue_id": secondID, "staff_id": s.members[0]}, 200)
	order := s.openForStaff([]uuid.UUID{s.members[0]}, false)
	s.assign(s.members[0], false, order)
	s.call("assign_turn", map[string]any{"queue_id": secondID, "staff_id": s.members[0], "order_id": order}, 200)
	s.pay(order, "100")
	day := settingsDate(s).Format(settings.DateLayout)
	stats := s.call("get_rotation_stats", map[string]any{"from": day, "to": day}, 200)
	if stats["total"] != float64(1) || stats["rotation"] != float64(1) || stats["designated"] != float64(0) {
		t.Fatal(stats)
	}
	entries := stats["entries"].([]any)
	if len(entries) != 1 || entries[0].(map[string]any)["total"] != float64(1) {
		t.Fatal(entries)
	}
}

func TestCheckoutAdvancesEveryServingStaff(t *testing.T) {
	s := openRotation(t)
	s.join(s.members[0])
	s.join(s.members[1])
	order := s.openForStaff(s.members[:2], false)
	s.assign(s.members[0], false, order)
	s.assign(s.members[1], false, order)
	s.pay(order, "200")
	for _, staffID := range s.members[:2] {
		if got := s.entry(staffID); got["status"] != "waiting" || got["serving_order_id"] != nil {
			t.Fatal(staffID, got)
		}
	}
}

func TestCheckoutAdvancesServingBusinessDate(t *testing.T) {
	s := openRotation(t)
	s.join(s.members[0])
	day := settingsDate(s)
	order := s.openFor(s.members[0], false, s.item)
	s.assign(s.members[0], false, order)
	s.Advance(24 * time.Hour)
	s.pay(order, "100")
	view := s.rotate(day)
	got := entries(view)[0]
	if got["status"] != "waiting" || got["serving_order_id"] != nil {
		t.Fatal(got)
	}
	stats := s.call("get_rotation_stats", map[string]any{"from": day.Format(settings.DateLayout), "to": day.Format(settings.DateLayout)}, 200)
	if stats["total"] != float64(1) {
		t.Fatal(stats)
	}
}

func TestAdvanceUsesServingDesignated(t *testing.T) {
	t.Run("order designated but turn is rotation", func(t *testing.T) {
		s := openRotation(t)
		for _, member := range s.members[:3] {
			s.join(member)
		}
		s.setSetting("rotation_designated_takes_turn", "no")
		order := s.openFor(s.members[0], true, s.item)
		s.assign(s.members[0], false, order)
		s.pay(order, "100")
		assertNames(t, s.names(s.today()), []string{"乙", "丙", "甲"})
		if got := s.event("advance_whole"); got["designated"] != false {
			t.Fatal(got)
		}
		day := settingsDate(s).Format(settings.DateLayout)
		stats := s.call("get_rotation_stats", map[string]any{"from": day, "to": day}, 200)
		if stats["designated"] != float64(0) || stats["rotation"] != float64(1) {
			t.Fatal(stats)
		}
	})
	t.Run("order rotation but turn is designated", func(t *testing.T) {
		s := openRotation(t)
		for _, member := range s.members[:3] {
			s.join(member)
		}
		order := s.openFor(s.members[0], false, s.item)
		s.assign(s.members[0], true, order)
		s.pay(order, "100")
		assertNames(t, s.names(s.today()), []string{"乙", "丙", "甲"})
		if got := s.event("advance_whole"); got["designated"] != true {
			t.Fatal(got)
		}
		day := settingsDate(s).Format(settings.DateLayout)
		stats := s.call("get_rotation_stats", map[string]any{"from": day, "to": day}, 200)
		if stats["designated"] != float64(1) || stats["rotation"] != float64(0) {
			t.Fatal(stats)
		}
	})
}

func TestAssignValidatesOrder(t *testing.T) {
	s := openRotation(t)
	s.join(s.members[0])
	reject(t, s.HTTPWithToken(t, s.Owner, "assign_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[0], "order_id": uuid.New()}), 404, "not found")
	order := s.openFor(s.members[0], false, s.item)
	s.pay(order, "100")
	before := s.snapshot()
	reject(t, s.HTTPWithToken(t, s.Owner, "assign_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[0], "order_id": order}), 409, "只能对未结账未撤单的单接客")
	if s.snapshot() != before {
		t.Fatal("rejected assignment changed data")
	}
}

func TestAdvanceRequiresServingOrder(t *testing.T) {
	s := openRotation(t)
	s.join(s.members[0])
	s.call("assign_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[0]}, 200)
	order := s.openFor(s.members[0], false, s.item)
	s.pay(order, "100")
	got := s.entry(s.members[0])
	if got["status"] != "serving" || got["serving_order_id"] != nil {
		t.Fatal(got)
	}
	day := settingsDate(s).Format(settings.DateLayout)
	stats := s.call("get_rotation_stats", map[string]any{"from": day, "to": day}, 200)
	if stats["total"] != float64(0) {
		t.Fatal(stats)
	}
}

func TestCheckoutWithoutServingStaffIsNoop(t *testing.T) {
	s := openRotation(t)
	s.join(s.members[0])
	order := s.openFor(s.members[0], false, s.item)
	s.pay(order, "100")
	if got := s.entry(s.members[0]); got["status"] != "waiting" || got["position"] != float64(1) {
		t.Fatal(got)
	}
	day := settingsDate(s).Format(settings.DateLayout)
	stats := s.call("get_rotation_stats", map[string]any{"from": day, "to": day}, 200)
	if stats["total"] != float64(0) {
		t.Fatal(stats)
	}
}

func TestCheckoutAdvancesEveryQueue(t *testing.T) {
	s := openRotation(t)
	second := s.call("create_rotation_queue", map[string]any{"name": "助理牌"}, 200)
	secondID := id(second["id"])
	s.call("set_queue_members", map[string]any{"queue_id": secondID, "staff_ids": []uuid.UUID{s.members[0], s.members[1]}}, 200)
	s.join(s.members[0])
	s.call("join_rotation", map[string]any{"queue_id": secondID, "staff_id": s.members[0]}, 200)
	s.call("join_rotation", map[string]any{"queue_id": secondID, "staff_id": s.members[1]}, 200)
	order := s.openFor(s.members[0], false, s.item)
	s.assign(s.members[0], false, order)
	s.call("assign_turn", map[string]any{"queue_id": secondID, "staff_id": s.members[0], "order_id": order}, 200)
	s.pay(order, "100")
	for i, queueID := range []uuid.UUID{s.queue, secondID} {
		view := s.call("get_rotation", map[string]any{"queue_id": queueID}, 200)
		wantPosition := float64(1 + i)
		for _, entry := range entries(view) {
			if entry["staff"].(map[string]any)["id"] != s.members[0].String() {
				continue
			}
			if entry["status"] != "waiting" || entry["position"] != wantPosition {
				t.Fatal(view)
			}
		}
		events := s.call("list_rotation_events", map[string]any{"queue_id": queueID, "from": settingsDate(s).Format(settings.DateLayout), "to": settingsDate(s).Format(settings.DateLayout)}, 200)
		found := false
		for _, raw := range events["entries"].([]any) {
			if raw.(map[string]any)["kind"] == "advance_whole" {
				found = true
			}
		}
		if !found {
			t.Fatal("queue was not advanced", queueID, events)
		}
	}
}

func TestCheckoutDoesNotAdvanceAnotherOrder(t *testing.T) {
	s := openRotation(t)
	s.join(s.members[0])
	s.join(s.members[1])
	first := s.openFor(s.members[0], false, s.item)
	s.assign(s.members[0], false, first)
	second := s.openFor(s.members[0], false, s.item)
	s.pay(second, "100")
	if got := s.entry(s.members[0]); got["status"] != "serving" || got["serving_order_id"] != first.String() || got["position"] != float64(1) {
		t.Fatal(got)
	}
	day := settingsDate(s).Format(settings.DateLayout)
	stats := s.call("get_rotation_stats", map[string]any{"from": day, "to": day}, 200)
	if stats["total"] != float64(0) {
		t.Fatal(stats)
	}
}
