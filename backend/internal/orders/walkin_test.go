package orders_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/orders"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func (s *fixture) walkinItem(duration int) uuid.UUID {
	s.t.Helper()
	in := s.itemInput("烫发", 1)
	in["duration_minutes"] = duration
	in["bookable"] = true
	return id(s.call("create_item", in, 200)["id"])
}

func (s *fixture) walkinOrder(customer, staffID, itemID uuid.UUID) map[string]any {
	s.t.Helper()
	in := s.opening(orders.OrderItemInput{ItemID: itemID, Staff: []orders.OrderItemStaffInput{{Slot: 1, StaffID: staffID}}})
	in.CustomerID = &customer
	order := s.call("open_order", in, 200)
	s.fixOrderTimes(order, s.Now())
	return order
}

func (s *fixture) fixOrderTimes(order map[string]any, at time.Time) {
	s.t.Helper()
	if _, err := s.Pool.Exec(context.Background(), "UPDATE orders SET opened_at=$1 WHERE id=$2", at, order["id"]); err != nil {
		s.t.Fatal(err)
	}
	if _, err := s.Pool.Exec(context.Background(), "UPDATE order_items SET added_at=$1 WHERE order_id=$2", at, order["id"]); err != nil {
		s.t.Fatal(err)
	}
}

func TestOrderBookingConflicts(t *testing.T) {
	s := open(t)
	s.SetNow(time.Date(2026, 6, 12, 14, 0, 0, 0, settings.Location))
	item := s.walkinItem(120)
	other := id(s.call("create_customer", map[string]any{"name": "顾客乙", "phone": "13800000002"}, 200)["customer"].(map[string]any)["id"])
	linkedOther := id(s.call("create_customer", map[string]any{"name": "顾客丙", "phone": "13800000003"}, 200)["customer"].(map[string]any)["id"])
	at := func(clock string) time.Time {
		parsed, err := time.ParseInLocation(settings.DateLayout+" 15:04", "2026-06-12 "+clock, settings.Location)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	cancelled := id(s.call("create_appointment", map[string]any{"customer_id": other, "staff_id": s.employee, "starts_at": at("14:00"), "ends_at": at("14:10")}, 200)["id"])
	s.call("cancel_appointment", map[string]any{"id": cancelled, "reason": "顾客取消"}, 200)
	completed := id(s.call("create_appointment", map[string]any{"customer_id": other, "staff_id": s.employee, "starts_at": at("14:00"), "ends_at": at("14:10")}, 200)["id"])
	s.call("complete_appointment", map[string]any{"id": completed}, 200)
	noShow := id(s.call("create_appointment", map[string]any{"customer_id": other, "staff_id": s.employee, "starts_at": at("14:00"), "ends_at": at("14:10")}, 200)["id"])
	s.SetNow(at("14:01"))
	s.call("mark_no_show", map[string]any{"id": noShow}, 200)
	s.SetNow(at("14:00"))
	cancelledBlock := id(s.call("create_block", map[string]any{"staff_id": s.employee, "starts_at": at("14:00"), "ends_at": at("14:10")}, 200)["id"])
	s.call("cancel_block", map[string]any{"id": cancelledBlock}, 200)
	otherAppointment := id(s.call("create_appointment", map[string]any{
		"customer_id": other, "staff_id": s.employee, "starts_at": at("15:00"), "ends_at": at("15:30"),
	}, 200)["id"])
	ownAppointment := id(s.call("create_appointment", map[string]any{
		"customer_id": s.customer, "staff_id": s.employee, "starts_at": at("14:30"), "ends_at": at("15:00"),
	}, 200)["id"])
	block := id(s.call("create_block", map[string]any{"staff_id": s.employee, "starts_at": at("15:30"), "ends_at": at("16:00")}, 200)["id"])
	linked := id(s.call("create_appointment", map[string]any{
		"customer_id": linkedOther, "staff_id": s.employee, "starts_at": at("14:00"), "ends_at": at("14:30"),
	}, 200)["id"])

	order := s.walkinOrder(s.customer, s.employee, item)
	if _, err := s.Pool.Exec(context.Background(), "UPDATE appointments SET order_id=$1 WHERE id=$2", order["id"], linked); err != nil {
		t.Fatal(err)
	}
	for _, ch := range s.Channels() {
		got := ch.Call(t, s.MustCaller(s.Owner), "get_order", orders.GetOrderInput{ID: id(order["id"])})
		if got.Status != 200 {
			t.Fatal(ch.Name, got)
		}
		conflicts := got.Body["booking_conflicts"].([]any)
		if len(conflicts) != 2 {
			t.Fatalf("%s conflicts = %v", ch.Name, conflicts)
		}
		byID := map[string]map[string]any{}
		for _, raw := range conflicts {
			row := raw.(map[string]any)
			byID[row["appointment_id"].(string)] = row
		}
		appointment := byID[otherAppointment.String()]
		if appointment == nil || appointment["kind"] != "appointment" || appointment["staff"].(map[string]any)["id"] != s.employee.String() || appointment["starts_at"] != at("15:00").Format(time.RFC3339) || appointment["ends_at"] != at("15:30").Format(time.RFC3339) {
			t.Fatalf("%s appointment conflict = %v", ch.Name, appointment)
		}
		privateBlock := byID[block.String()]
		if privateBlock == nil || privateBlock["kind"] != "block" || privateBlock["staff"].(map[string]any)["id"] != s.employee.String() || privateBlock["starts_at"] != at("15:30").Format(time.RFC3339) || privateBlock["ends_at"] != at("16:00").Format(time.RFC3339) {
			t.Fatalf("%s block conflict = %v", ch.Name, privateBlock)
		}
		for _, id := range []uuid.UUID{ownAppointment, linked, cancelled, completed, noShow, cancelledBlock} {
			if byID[id.String()] != nil {
				t.Fatalf("%s excluded record %s must not be a conflict: %v", ch.Name, id, byID)
			}
		}
		list := ch.Call(t, s.MustCaller(s.Owner), "list_open_orders", orders.ListOpenOrdersInput{})
		if list.Status != 200 || len(list.Body["items"].([]any)) != 1 {
			t.Fatal(ch.Name, list)
		}
		if len(list.Body["items"].([]any)[0].(map[string]any)["booking_conflicts"].([]any)) != 2 {
			t.Fatalf("%s list conflicts = %v", ch.Name, list.Body)
		}
	}

	s.call("checkout_orders", map[string]any{"orders": []any{map[string]any{
		"order_id": order["id"], "payments": []any{map[string]any{"method": "cash", "amount": order["receivable"]}},
	}}}, 200)
	for _, ch := range s.Channels() {
		got := ch.Call(t, s.MustCaller(s.Owner), "get_order", orders.GetOrderInput{ID: id(order["id"])})
		if got.Status != 200 || len(got.Body["booking_conflicts"].([]any)) != 0 {
			t.Fatalf("%s after checkout = %v", ch.Name, got)
		}
	}
}

func TestOrderBookingConflictsQueryCount(t *testing.T) {
	s := open(t)
	s.SetNow(time.Date(2026, 6, 12, 14, 0, 0, 0, settings.Location))
	item := s.walkinItem(120)
	other := id(s.call("create_customer", map[string]any{"name": "顾客乙", "phone": "13800000002"}, 200)["customer"].(map[string]any)["id"])
	start := time.Date(2026, 6, 12, 15, 0, 0, 0, settings.Location)
	s.call("create_appointment", map[string]any{"customer_id": other, "staff_id": s.employee, "starts_at": start, "ends_at": start.Add(30 * time.Minute)}, 200)
	s.walkinOrder(s.customer, s.employee, item)

	counter := &paymentQueryCounter{}
	config := s.Pool.Config().Copy()
	config.ConnConfig.Tracer = counter
	pool, err := db.NewPoolWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	app := optest.New(t, pool)
	app.SetNow(s.Now())
	owner := s.MustCaller(s.Owner)
	count := func(ch optest.Channel, want int) int64 {
		counter.all.Store(0)
		r := ch.Call(t, owner, "list_open_orders", orders.ListOpenOrdersInput{})
		if r.Status != 200 || len(r.Body["items"].([]any)) != want {
			t.Fatalf("%s open orders = %v", ch.Name, r)
		}
		for _, raw := range r.Body["items"].([]any) {
			if len(raw.(map[string]any)["booking_conflicts"].([]any)) != 1 {
				t.Fatalf("%s conflicts = %v", ch.Name, raw)
			}
		}
		return counter.all.Load()
	}
	one := map[string]int64{}
	for _, ch := range app.Channels() {
		one[ch.Name] = count(ch, 1)
		if one[ch.Name] == 0 {
			t.Fatalf("%s baseline query count is zero", ch.Name)
		}
	}
	for range 4 {
		s.walkinOrder(s.customer, s.employee, item)
	}
	for _, ch := range app.Channels() {
		if many := count(ch, 5); one[ch.Name] != many {
			t.Fatalf("%s query count grew with open orders: %d vs %d", ch.Name, one[ch.Name], many)
		}
	}
}

func TestOrderBookingConflictsFollowChanges(t *testing.T) {
	s := open(t)
	s.SetNow(time.Date(2026, 6, 12, 14, 0, 0, 0, settings.Location))
	item := s.walkinItem(120)
	other := id(s.call("create_customer", map[string]any{"name": "顾客乙", "phone": "13800000002"}, 200)["customer"].(map[string]any)["id"])
	start := time.Date(2026, 6, 12, 15, 0, 0, 0, settings.Location)
	appointment := id(s.call("create_appointment", map[string]any{"customer_id": other, "staff_id": s.employee, "starts_at": start, "ends_at": start.Add(30 * time.Minute)}, 200)["id"])
	in := s.opening()
	in.CustomerID = &s.customer
	order := s.call("open_order", in, 200)
	s.fixOrderTimes(order, s.Now())
	line := orders.OrderItemInput{ItemID: item, Staff: []orders.OrderItemStaffInput{{Slot: 1, StaffID: s.employee}}}
	if conflicts := order["booking_conflicts"].([]any); len(conflicts) != 0 {
		t.Fatalf("empty order conflicts = %v", conflicts)
	}
	added := s.call("add_order_item", orders.AddOrderItemInput{OrderID: id(order["id"]), Item: line}, 200)
	s.fixOrderTimes(added, s.Now())
	added = s.call("get_order", orders.GetOrderInput{ID: id(order["id"])}, 200)
	if conflicts := added["booking_conflicts"].([]any); len(conflicts) != 1 || conflicts[0].(map[string]any)["appointment_id"] != appointment.String() {
		t.Fatalf("add_order_item conflicts = %v", conflicts)
	}
	orderItemID := added["items"].([]any)[0].(map[string]any)["id"]
	removed := s.call("remove_order_item", map[string]any{"order_id": order["id"], "order_item_id": orderItemID}, 200)
	if conflicts := removed["booking_conflicts"].([]any); len(conflicts) != 0 {
		t.Fatalf("remove_order_item conflicts = %v", conflicts)
	}
	added = s.call("add_order_item", orders.AddOrderItemInput{OrderID: id(order["id"]), Item: line}, 200)
	s.fixOrderTimes(added, s.Now())
	added = s.call("get_order", orders.GetOrderInput{ID: id(order["id"])}, 200)
	if conflicts := added["booking_conflicts"].([]any); len(conflicts) != 1 {
		t.Fatalf("re-added conflicts = %v", conflicts)
	}
	checkout := s.call("checkout_orders", map[string]any{
		"orders": []any{map[string]any{
			"order_id": order["id"],
			"payments": []any{map[string]any{"method": "cash", "amount": added["receivable"]}},
		}},
	}, 200)
	paid := checkout["items"].([]any)[0].(map[string]any)
	if paid["status"] != "paid" || len(paid["booking_conflicts"].([]any)) != 0 {
		t.Fatalf("checkout conflicts = %v", paid)
	}
	voided := s.call("void_order", voidInput(paid), 200)
	if voided["status"] != "voided" || len(voided["booking_conflicts"].([]any)) != 0 {
		t.Fatalf("void conflicts = %v", voided)
	}
}

func TestMergeMovesWalkinOccupancy(t *testing.T) {
	s := open(t)
	item := s.walkinItem(120)
	other := id(s.call("create_customer", map[string]any{"name": "顾客乙", "phone": "13800000002"}, 200)["customer"].(map[string]any)["id"])
	start := time.Date(2026, 6, 12, 14, 30, 0, 0, settings.Location)
	s.SetNow(time.Date(2026, 6, 12, 14, 0, 0, 0, settings.Location))
	appointment := id(s.call("create_appointment", map[string]any{"customer_id": other, "staff_id": s.employee, "starts_at": start, "ends_at": start.Add(30 * time.Minute)}, 200)["id"])
	s.SetNow(time.Date(2026, 6, 12, 13, 0, 0, 0, settings.Location))
	source := s.walkinOrder(s.customer, s.employee, item)
	source = s.call("get_order", orders.GetOrderInput{ID: id(source["id"])}, 200)
	if conflicts := source["booking_conflicts"].([]any); len(conflicts) != 1 || conflicts[0].(map[string]any)["appointment_id"] != appointment.String() {
		t.Fatalf("source conflicts = %v", conflicts)
	}
	s.SetNow(time.Date(2026, 6, 12, 14, 0, 0, 0, settings.Location))
	in := s.opening()
	in.CustomerID = &s.customer
	target := s.call("open_order", in, 200)
	s.fixOrderTimes(target, s.Now())
	if conflicts := target["booking_conflicts"].([]any); len(conflicts) != 0 {
		t.Fatalf("target before merge conflicts = %v", conflicts)
	}
	merged := s.call("merge_orders", mergeInput(target, source), 200)
	if conflicts := merged["booking_conflicts"].([]any); len(conflicts) != 1 || conflicts[0].(map[string]any)["appointment_id"] != appointment.String() {
		t.Fatalf("merged conflicts = %v", conflicts)
	}
	if got := s.call("get_order", orders.GetOrderInput{ID: id(source["id"])}, 200); got["status"] != "merged" || len(got["booking_conflicts"].([]any)) != 0 {
		t.Fatalf("source after merge = %v", got)
	}
	day := settings.Date(s.Now()).Format(settings.DateLayout)
	walkIns := s.call("list_appointments", map[string]any{"from": day, "to": day}, 200)["walk_ins"].([]any)
	if len(walkIns) != 1 {
		t.Fatalf("walk_ins after merge = %v", walkIns)
	}
	row := walkIns[0].(map[string]any)
	if row["order_id"] != target["id"] || row["starts_at"] != time.Date(2026, 6, 12, 14, 0, 0, 0, settings.Location).Format(time.RFC3339) || row["ends_at"] != time.Date(2026, 6, 12, 16, 0, 0, 0, settings.Location).Format(time.RFC3339) {
		t.Fatalf("merged walk-in = %v", row)
	}
}
