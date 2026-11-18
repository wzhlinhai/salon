package miniprogram_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func (s *fixture) createPositionedItem(name string, duration, slots int, kind string) uuid.UUID {
	s.t.Helper()
	position := call(s.t, s.Shop, "create_position", map[string]any{"name": name + uuid.NewString()[:6]}, 200)
	positions := []any{}
	for range slots {
		positions = append(positions, map[string]any{"position_id": position["id"], "required": true})
	}
	in := map[string]any{
		"name": name + uuid.NewString()[:6], "price": "10.00", "discountable": true, "duration_minutes": duration,
		"bookable": true, "labor_fee_amount": nil, "labor_fee_percent": nil,
		"positions": positions, "level_prices": []any{}, "member_prices": []any{},
	}
	if kind != "" {
		in["kind"] = kind
	}
	return uuid.MustParse(call(s.t, s.Shop, "create_item", in, 200)["id"].(string))
}

func (s *fixture) fixWalkinTimes(order map[string]any, at time.Time) {
	s.t.Helper()
	if _, err := s.Pool.Exec(context.Background(), "UPDATE orders SET opened_at=$1 WHERE id=$2", at, order["id"]); err != nil {
		s.t.Fatal(err)
	}
	if _, err := s.Pool.Exec(context.Background(), "UPDATE order_items SET added_at=$1 WHERE order_id=$2", at, order["id"]); err != nil {
		s.t.Fatal(err)
	}
}

func (s *fixture) markBackfill(order map[string]any) {
	s.t.Helper()
	if _, err := s.Pool.Exec(context.Background(), "UPDATE orders SET backfill=true WHERE id=$1", order["id"]); err != nil {
		s.t.Fatal(err)
	}
}

func (s *fixture) markBusinessDate(order map[string]any, day string) {
	s.t.Helper()
	if _, err := s.Pool.Exec(context.Background(), "UPDATE orders SET business_date=$1 WHERE id=$2", day, order["id"]); err != nil {
		s.t.Fatal(err)
	}
}

func (s *fixture) openWalkin(customer, staffID, itemID uuid.UUID, slots ...int16) map[string]any {
	s.t.Helper()
	staff := []any{}
	for _, slot := range slots {
		staff = append(staff, map[string]any{"slot": slot, "staff_id": staffID})
	}
	order := call(s.t, s.Shop, "open_order", map[string]any{
		"customer_id": customer, "gender": "female", "designated": true,
		"items": []any{map[string]any{"item_id": itemID, "staff": staff}},
	}, 200)
	s.fixWalkinTimes(order, s.Now())
	return order
}

func (s *fixture) checkoutWalkin(order map[string]any) {
	s.t.Helper()
	call(s.t, s.Shop, "checkout_orders", map[string]any{
		"orders": []any{map[string]any{"order_id": order["id"], "payments": []any{map[string]any{"method": "cash", "amount": "10.00"}}}},
	}, 200)
}

func (s *fixture) hasSlotFor(token string, staffID uuid.UUID, clock string) bool {
	s.t.Helper()
	return hasSlot(slotsOf(s.t, s.selfSlots(token, staffID, s.day(0), []uuid.UUID{s.item})), clock)
}

func TestSelfBookingWalkinOccupancy(t *testing.T) {
	s := openSelf(t)
	item := s.createPositionedItem("主做烫发", 120, 1, "")
	s.SetNow(s.at(0, "14:00"))
	s.openWalkin(s.customerA, s.stylist, item, 1)

	slots := slotsOf(t, s.selfSlots(s.tokenB, s.stylist, s.day(0), []uuid.UUID{s.item}))
	for _, clock := range []string{"14:00", "14:30", "15:00", "15:30"} {
		if hasSlot(slots, clock) {
			t.Fatalf("slot %s overlaps the walk-in: %v", clock, slots)
		}
	}
	if !hasSlot(slots, "16:00") {
		t.Fatalf("slot at walk-in end is missing: %v", slots)
	}
	// fixture 已有顾客乙 15:00–15:30 的预约；用 14:30 单独验证挂单占用，时段列表仍覆盖 15:00。
	requireDetail(t, s.selfBook(s.tokenB, s.stylist, s.at(0, "14:30"), []uuid.UUID{s.item}), http.StatusConflict, "该时段已有预约或占位")
}

func TestSelfBookingWalkinOwnCustomer(t *testing.T) {
	s := openSelf(t)
	item := s.createPositionedItem("主做烫发", 120, 1, "")
	s.SetNow(s.at(0, "14:00"))
	s.openWalkin(s.customerA, s.stylist, item, 1)
	if created := s.selfBook(s.tokenA, s.stylist, s.at(0, "14:30"), []uuid.UUID{s.item}); created.Status != http.StatusOK {
		t.Fatalf("own walk-in must not block the order customer: %v", created)
	}
}

func TestSelfBookingWalkinReleaseAndEdits(t *testing.T) {
	t.Run("checkout releases the slot", func(t *testing.T) {
		s := openSelf(t)
		item := s.createPositionedItem("主做烫发", 120, 1, "")
		s.SetNow(s.at(0, "14:00"))
		order := s.openWalkin(s.customerA, s.stylist, item, 1)
		if s.hasSlotFor(s.tokenB, s.stylist, "14:30") {
			t.Fatal("walk-in must occupy the slot")
		}
		s.checkoutWalkin(order)
		if !s.hasSlotFor(s.tokenB, s.stylist, "14:30") {
			t.Fatal("checkout must release the slot")
		}
	})
	t.Run("removing the item releases the slot", func(t *testing.T) {
		s := openSelf(t)
		item := s.createPositionedItem("主做烫发", 120, 1, "")
		s.SetNow(s.at(0, "14:00"))
		order := s.openWalkin(s.customerA, s.stylist, item, 1)
		line := order["items"].([]any)[0].(map[string]any)
		call(t, s.Shop, "remove_order_item", map[string]any{"order_id": order["id"], "order_item_id": line["id"]}, 200)
		if !s.hasSlotFor(s.tokenB, s.stylist, "14:30") {
			t.Fatal("removed item must release the slot")
		}
	})
	t.Run("changing staff moves the occupancy", func(t *testing.T) {
		s := openSelf(t)
		item := s.createPositionedItem("主做烫发", 120, 1, "")
		stylist2, _ := s.Hire("发型师乙", "003", "walkin-stylist-2", s.Role("发型师"))
		call(t, s.Shop, "update_staff", map[string]any{"staff_id": stylist2, "name": "发型师乙", "number": "003", "self_bookable": true}, 200)
		s.SetNow(s.at(0, "14:00"))
		order := s.openWalkin(s.customerA, s.stylist, item, 1)
		line := order["items"].([]any)[0].(map[string]any)
		call(t, s.Shop, "set_order_item_staff", map[string]any{
			"order_id": order["id"], "order_item_id": line["id"],
			"staff": []any{map[string]any{"slot": 1, "staff_id": stylist2}},
		}, 200)
		if !s.hasSlotFor(s.tokenB, s.stylist, "14:30") {
			t.Fatal("old stylist must be free after the change")
		}
		slots := slotsOf(t, s.selfSlots(s.tokenB, stylist2, s.day(0), []uuid.UUID{s.item}))
		if hasSlot(slots, "14:30") {
			t.Fatalf("new stylist must be occupied: %v", slots)
		}
	})
}

func TestSelfBookingWalkinNonOccupyingLines(t *testing.T) {
	t.Run("goods line occupies nothing", func(t *testing.T) {
		s := openSelf(t)
		item := s.createPositionedItem("卖品", 120, 1, "goods")
		s.SetNow(s.at(0, "14:00"))
		s.openWalkin(s.customerA, s.stylist, item, 1)
		if !s.hasSlotFor(s.tokenB, s.stylist, "14:30") {
			t.Fatal("goods must not occupy the stylist")
		}
	})
	t.Run("assistant slot occupies nothing", func(t *testing.T) {
		s := openSelf(t)
		item := s.createPositionedItem("洗吹", 120, 2, "")
		s.SetNow(s.at(0, "14:00"))
		s.openWalkin(s.customerA, s.stylist, item, 2)
		if !s.hasSlotFor(s.tokenB, s.stylist, "14:30") {
			t.Fatal("assistant slot must not occupy the stylist")
		}
	})
	t.Run("previous business date occupies nothing", func(t *testing.T) {
		s := openSelf(t)
		item := s.createPositionedItem("主做烫发", 120, 1, "")
		s.SetNow(s.at(0, "14:00"))
		order := s.openWalkin(s.customerA, s.stylist, item, 1)
		s.markBusinessDate(order, s.day(-1))
		if !s.hasSlotFor(s.tokenB, s.stylist, "14:30") {
			t.Fatal("previous business date must not occupy today")
		}
	})
	t.Run("backfill order occupies nothing", func(t *testing.T) {
		s := openSelf(t)
		item := s.createPositionedItem("主做烫发", 120, 1, "")
		s.SetNow(s.at(0, "14:00"))
		order := s.openWalkin(s.customerA, s.stylist, item, 1)
		s.markBackfill(order)
		if !s.hasSlotFor(s.tokenB, s.stylist, "14:30") {
			t.Fatal("backfill order must not occupy the stylist")
		}
	})
	t.Run("occupancy ends when the duration ends", func(t *testing.T) {
		s := openSelf(t)
		item := s.createPositionedItem("主做剪发", 60, 1, "")
		s.SetNow(s.at(0, "14:00"))
		s.openWalkin(s.customerA, s.stylist, item, 1)
		s.SetNow(s.at(0, "15:30"))
		if !s.hasSlotFor(s.tokenB, s.stylist, "15:30") {
			t.Fatal("slot after the walk-in end must be free")
		}
	})
}

func TestSelfBookingWalkinCrossesBusinessDay(t *testing.T) {
	s := openSelf(t)
	s.setSetting(settings.KeyBusinessHours, map[string]any{"open": "00:00", "close": "23:59"})
	item := s.createPositionedItem("跨日长烫", 600, 1, "")
	s.SetNow(s.at(0, "23:00"))
	s.openWalkin(s.customerA, s.stylist, item, 1)

	slots := slotsOf(t, s.selfSlots(s.tokenB, s.stylist, s.day(1), []uuid.UUID{s.item}))
	for _, clock := range []string{"00:00", "00:30", "08:30"} {
		if hasSlot(slots, clock) {
			t.Fatalf("tomorrow slot %s overlaps the crossing walk-in: %v", clock, slots)
		}
	}
	if !hasSlot(slots, "09:00") {
		t.Fatalf("slot at crossing walk-in end is missing: %v", slots)
	}
	requireDetail(t, s.selfBook(s.tokenB, s.stylist, s.at(1, "00:30"), []uuid.UUID{s.item}), http.StatusConflict, "该时段已有预约或占位")
	requireDetail(t, s.HTTPWithToken(t, s.Owner, "create_appointment", map[string]any{
		"customer_id": s.customerB, "staff_id": s.stylist, "starts_at": s.at(1, "00:30"), "ends_at": s.at(1, "01:00"),
	}), http.StatusConflict, "该时段发型师在服务到店顾客，预计 09:00 结束")
}

func TestSelfBookingWalkinUsesDefaultMinutes(t *testing.T) {
	s := openSelf(t)
	s.setSetting(settings.KeyBookingDefaultMinutes, 45)
	item := s.createPositionedItem("没设时长", 0, 1, "")
	s.SetNow(s.at(0, "14:00"))
	order := s.openWalkin(s.customerA, s.stylist, item, 1)
	walkIns := call(t, s.Shop, "list_appointments", map[string]any{"from": s.day(0), "to": s.day(0)}, 200)["walk_ins"].([]any)
	if len(walkIns) != 1 {
		t.Fatalf("walk_ins = %v", walkIns)
	}
	got := walkIns[0].(map[string]any)
	if got["order_id"] != order["id"] || got["starts_at"] != s.at(0, "14:00").Format("2006-01-02T15:04:05+08:00") || got["ends_at"] != s.at(0, "14:45").Format("2006-01-02T15:04:05+08:00") {
		t.Fatalf("default-duration walk-in = %v", got)
	}
}
