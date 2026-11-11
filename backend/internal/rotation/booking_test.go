package rotation_test

import (
	"context"
	"testing"
	"time"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
)

func TestAssignSkipsBookedStaff(t *testing.T) {
	t.Run("block", func(t *testing.T) {
		s := openRotation(t)
		s.join(s.members[0])
		s.join(s.members[1])
		s.call("create_block", map[string]any{"staff_id": s.members[0], "starts_at": s.Now(), "ends_at": s.Now().Add(time.Hour)}, 200)
		view := s.call("assign_turn", map[string]any{"queue_id": s.queue}, 200)
		if !servingStaff(view, s.members[1]) {
			t.Fatal(view)
		}
		event := s.event("auto_skip")
		if event["reason"] != "预约/私约占位中" || event["staff"].(map[string]any)["id"] != s.members[0].String() || event["from_position"] != float64(1) || event["to_position"] != float64(1) {
			t.Fatal(event)
		}
		if got := s.entry(s.members[0]); got["status"] != "waiting" || got["position"] != float64(1) {
			t.Fatal(got)
		}
		s.call("release_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[1]}, 200)
		s.Advance(time.Hour + time.Minute)
		view = s.call("assign_turn", map[string]any{"queue_id": s.queue}, 200)
		if !servingStaff(view, s.members[0]) {
			t.Fatal(view)
		}
		skips := 0
		for _, event := range s.events(settingsDate(s), settingsDate(s)) {
			if event["kind"] == "auto_skip" && event["reason"] == "预约/私约占位中" {
				skips++
			}
		}
		if skips != 1 {
			t.Fatalf("auto_skip events = %d, want only the skipped block", skips)
		}
	})
	t.Run("appointment", func(t *testing.T) {
		s := openRotation(t)
		s.join(s.members[0])
		s.join(s.members[1])
		customer := s.call("create_customer", map[string]any{"name": "预约顾客", "phone": "13800000001"}, 200)["customer"].(map[string]any)["id"]
		s.call("create_appointment", map[string]any{"customer_id": customer, "staff_id": s.members[0], "starts_at": s.Now(), "items": []any{map[string]any{"item_id": s.item}}}, 200)
		view := s.call("assign_turn", map[string]any{"queue_id": s.queue}, 200)
		if !servingStaff(view, s.members[1]) {
			t.Fatal(view)
		}
		if got := s.event("auto_skip"); got["reason"] != "预约/私约占位中" {
			t.Fatal(got)
		}
	})
}

func TestHalfTurnByAppointment(t *testing.T) {
	t.Run("linked completed appointment is half", func(t *testing.T) {
		s := openRotation(t)
		for _, member := range s.members[:3] {
			s.join(member)
		}
		s.setSetting("rotation_half_rule", "by_appointment")
		order := s.openFor(s.members[0], false, s.item)
		o := s.call("get_order", map[string]any{"id": order}, 200)
		appointment := s.call("create_appointment", map[string]any{"staff_id": s.members[0], "customer_id": o["customer_id"], "starts_at": s.Now(), "items": []any{map[string]any{"item_id": s.item}}}, 200)
		s.call("complete_appointment", map[string]any{"id": appointment["id"], "order_id": order}, 200)
		s.assign(s.members[0], false, order)
		s.pay(order, "100")
		if got := s.entry(s.members[0]); got["status"] != "waiting" || got["position"] != float64(1) || got["half_pending"] != true {
			t.Fatal(got)
		}
		if got := s.event("advance_half"); got["from_position"] != float64(1) || got["to_position"] != float64(1) {
			t.Fatal(got)
		}
	})
	t.Run("without appointment is whole", func(t *testing.T) {
		s := openRotation(t)
		for _, member := range s.members[:3] {
			s.join(member)
		}
		s.setSetting("rotation_half_rule", "by_appointment")
		order := s.openFor(s.members[0], false, s.item)
		s.assign(s.members[0], false, order)
		s.pay(order, "100")
		assertNames(t, s.names(s.today()), []string{"乙", "丙", "甲"})
		if got := s.entry(s.members[0]); got["half_pending"] != false || got["position"] != float64(3) {
			t.Fatal(got)
		}
		if got := s.event("advance_whole"); got["staff"].(map[string]any)["id"] != s.members[0].String() {
			t.Fatal(got)
		}
	})
}

func TestDesignatedAssignIgnoresBlock(t *testing.T) {
	s := openRotation(t)
	s.join(s.members[0])
	s.call("create_block", map[string]any{"staff_id": s.members[0], "starts_at": s.Now(), "ends_at": s.Now().Add(time.Hour)}, 200)
	view := s.call("assign_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[0]}, 200)
	if !servingStaff(view, s.members[0]) {
		t.Fatal(view)
	}
}

func TestFinishedBookingsDoNotOccupy(t *testing.T) {
	t.Run("cancelled block", func(t *testing.T) {
		s := openRotation(t)
		s.join(s.members[0])
		block := s.call("create_block", map[string]any{"staff_id": s.members[0], "starts_at": s.Now(), "ends_at": s.Now().Add(time.Hour)}, 200)
		s.call("cancel_block", map[string]any{"id": block["id"]}, 200)
		if view := s.call("assign_turn", map[string]any{"queue_id": s.queue}, 200); !servingStaff(view, s.members[0]) {
			t.Fatal(view)
		}
	})
	t.Run("cancelled appointment", func(t *testing.T) {
		s := openRotation(t)
		s.join(s.members[0])
		customer := s.call("create_customer", map[string]any{"name": "顾客", "phone": "13800000002"}, 200)["customer"].(map[string]any)["id"]
		appointment := s.call("create_appointment", map[string]any{"customer_id": customer, "staff_id": s.members[0], "starts_at": s.Now()}, 200)
		s.call("cancel_appointment", map[string]any{"id": appointment["id"], "reason": "顾客取消"}, 200)
		if view := s.call("assign_turn", map[string]any{"queue_id": s.queue}, 200); !servingStaff(view, s.members[0]) {
			t.Fatal(view)
		}
	})
	t.Run("completed appointment", func(t *testing.T) {
		s := openRotation(t)
		s.join(s.members[0])
		customer := s.call("create_customer", map[string]any{"name": "顾客", "phone": "13800000003"}, 200)["customer"].(map[string]any)["id"]
		appointment := s.call("create_appointment", map[string]any{"customer_id": customer, "staff_id": s.members[0], "starts_at": s.Now()}, 200)
		s.call("complete_appointment", map[string]any{"id": appointment["id"]}, 200)
		if view := s.call("assign_turn", map[string]any{"queue_id": s.queue}, 200); !servingStaff(view, s.members[0]) {
			t.Fatal(view)
		}
	})
}

func TestHalfTurnByAppointmentRequiresCompleted(t *testing.T) {
	for _, status := range []string{"pending", "confirmed"} {
		t.Run(status, func(t *testing.T) {
			s := openRotation(t)
			for _, member := range s.members[:3] {
				s.join(member)
			}
			s.setSetting("rotation_half_rule", "by_appointment")
			order := s.openFor(s.members[0], false, s.item)
			o := s.call("get_order", map[string]any{"id": order}, 200)
			appointment := s.call("create_appointment", map[string]any{"staff_id": s.members[0], "customer_id": o["customer_id"], "starts_at": s.Now(), "items": []any{map[string]any{"item_id": s.item}}}, 200)
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			if _, err := tx.Exec(context.Background(), "UPDATE appointments SET status=$1, order_id=$2 WHERE id=$3", status, order, appointment["id"]); err != nil {
				t.Fatal(err)
			}
			dbtest.Commit(t, tx)
			s.assign(s.members[0], false, order)
			s.pay(order, "100")
			assertNames(t, s.names(s.today()), []string{"乙", "丙", "甲"})
			if got := s.entry(s.members[0]); got["half_pending"] != false || got["position"] != float64(3) {
				t.Fatal(got)
			}
			if got := s.event("advance_whole"); got["staff"].(map[string]any)["id"] != s.members[0].String() {
				t.Fatal(got)
			}
		})
	}
}
