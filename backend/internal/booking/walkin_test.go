package booking_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func (s *fixture) positionedItem(name string, duration int) uuid.UUID {
	return s.positionedItemSlots(name, duration, 1)
}

func (s *fixture) positionedItemSlots(name string, duration, slots int) uuid.UUID {
	s.t.Helper()
	position := id(s.call("create_position", map[string]any{"name": name + uuid.NewString()[:6]}, 200)["id"])
	positions := []any{}
	for range slots {
		positions = append(positions, map[string]any{"position_id": position, "required": true})
	}
	return id(s.call("create_item", map[string]any{
		"name": name, "price": "100", "discountable": true, "duration_minutes": duration, "bookable": true,
		"labor_fee_amount": nil, "labor_fee_percent": nil,
		"positions":    positions,
		"level_prices": []any{}, "member_prices": []any{},
	}, 200)["id"])
}

func (s *fixture) fixWalkinOrder(order map[string]any, at time.Time) {
	s.t.Helper()
	if _, err := s.Pool.Exec(context.Background(), "UPDATE orders SET opened_at=$1 WHERE id=$2", at, order["id"]); err != nil {
		s.t.Fatal(err)
	}
	if _, err := s.Pool.Exec(context.Background(), "UPDATE order_items SET added_at=$1 WHERE order_id=$2", at, order["id"]); err != nil {
		s.t.Fatal(err)
	}
}

func (s *fixture) walkinOrder(customer, staffID, itemID uuid.UUID) map[string]any {
	s.t.Helper()
	order := s.call("open_order", map[string]any{
		"customer_id": customer, "gender": "female", "designated": true,
		"items": []any{map[string]any{"item_id": itemID, "staff": []any{map[string]any{"slot": 1, "staff_id": staffID}}}},
	}, 200)
	s.fixWalkinOrder(order, s.Now())
	return order
}

func TestStaffAppointmentWalkinConflict(t *testing.T) {
	s := openBooking(t)
	item := s.positionedItem("主做烫发", 60)
	otherCustomer := id(s.call("create_customer", map[string]any{"name": "顾客乙", "phone": "13800000002"}, 200)["customer"].(map[string]any)["id"])
	s.SetNow(s.at(0, "14:00"))
	s.walkinOrder(s.customer, s.staff, item)

	const message = "该时段发型师在服务到店顾客，预计 15:00 结束"
	reject(t, s.appointment(map[string]any{"customer_id": otherCustomer, "staff_id": s.staff, "starts_at": s.at(0, "14:30"), "ends_at": s.at(0, "15:00")}, 0), http.StatusConflict, message)
	if r := s.appointment(map[string]any{"customer_id": otherCustomer, "staff_id": s.staff, "starts_at": s.at(0, "15:00"), "ends_at": s.at(0, "15:30")}, 0); r.Status != http.StatusOK {
		t.Fatalf("touching the walk-in end must be allowed: %v", r)
	}

	held := s.booked(map[string]any{"customer_id": otherCustomer, "staff_id": s.staff, "starts_at": s.at(0, "16:00"), "ends_at": s.at(0, "16:30")})
	s.rejectedUnchanged(t, id(held["id"]), s.fail("reschedule_appointment", map[string]any{"id": held["id"], "starts_at": s.at(0, "14:30"), "ends_at": s.at(0, "15:00")}), http.StatusConflict, message)
}

func TestStaffAppointmentWalkinSameCustomer(t *testing.T) {
	s := openBooking(t)
	item := s.positionedItem("主做烫发", 60)
	s.SetNow(s.at(0, "14:00"))
	s.walkinOrder(s.customer, s.staff, item)
	if r := s.appointment(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "14:30"), "ends_at": s.at(0, "15:00")}, 0); r.Status != http.StatusOK {
		t.Fatalf("same customer must be allowed: %v", r)
	}
}

func TestBlockMayOverlapWalkin(t *testing.T) {
	s := openBooking(t)
	item := s.positionedItem("主做烫发", 60)
	s.SetNow(s.at(0, "14:00"))
	s.walkinOrder(s.customer, s.staff, item)
	if r := s.block(s.staff, s.at(0, "14:30"), s.at(0, "15:00"), 0); r.Status != http.StatusOK {
		t.Fatalf("a block and a walk-in both mean busy: %v", r)
	}
}

func TestStaffAppointmentWalkinReportsLatestEnd(t *testing.T) {
	s := openBooking(t)
	long := s.positionedItem("长烫", 120)
	short := s.positionedItem("短项目", 30)
	third := id(s.call("create_customer", map[string]any{"name": "顾客丙", "phone": "13800000003"}, 200)["customer"].(map[string]any)["id"])
	s.SetNow(s.at(0, "14:00"))
	s.walkinOrder(s.customer, s.staff, long)
	s.SetNow(s.at(0, "14:30"))
	second := id(s.call("create_customer", map[string]any{"name": "顾客乙", "phone": "13800000002"}, 200)["customer"].(map[string]any)["id"])
	s.walkinOrder(second, s.staff, short)
	s.SetNow(s.at(0, "14:40"))
	reject(t, s.appointment(map[string]any{"customer_id": third, "staff_id": s.staff, "starts_at": s.at(0, "14:45"), "ends_at": s.at(0, "15:15")}, 0), http.StatusConflict, "该时段发型师在服务到店顾客，预计 16:00 结束")
}

func TestWalkinOnlyLowestSlotOccupies(t *testing.T) {
	s := openBooking(t)
	mainItem := s.positionedItem("主做", 60)
	dualItem := s.positionedItemSlots("双岗", 30, 2)
	s.SetNow(s.at(0, "14:00"))
	order := s.call("open_order", map[string]any{
		"customer_id": s.customer, "gender": "female", "designated": true,
		"items": []any{
			map[string]any{"item_id": mainItem, "staff": []any{map[string]any{"slot": 1, "staff_id": s.staff}}},
			map[string]any{"item_id": dualItem, "staff": []any{map[string]any{"slot": 1, "staff_id": s.other}, map[string]any{"slot": 2, "staff_id": s.staff}}},
		},
	}, 200)
	s.fixWalkinOrder(order, s.Now())
	day := settings.Date(s.Now()).Format(settings.DateLayout)
	for _, tc := range []struct {
		staff uuid.UUID
		end   time.Time
	}{
		{s.staff, s.at(0, "15:00")},
		{s.other, s.at(0, "14:30")},
	} {
		walkIns := s.call("list_appointments", map[string]any{"from": day, "to": day, "staff_id": tc.staff}, 200)["walk_ins"].([]any)
		if len(walkIns) != 1 {
			t.Fatalf("staff %s walk_ins = %v", tc.staff, walkIns)
		}
		got := walkIns[0].(map[string]any)
		if got["order_id"] != order["id"] || got["staff"].(map[string]any)["id"] != tc.staff.String() || got["starts_at"] != s.at(0, "14:00").Format("2006-01-02T15:04:05+08:00") || got["ends_at"] != tc.end.Format("2006-01-02T15:04:05+08:00") {
			t.Fatalf("staff %s walk-in = %v", tc.staff, got)
		}
	}
}
