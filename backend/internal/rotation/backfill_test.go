package rotation_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func (s *fixture) backfillOrder() uuid.UUID {
	s.t.Helper()
	paper := settings.Date(s.Now()).AddDate(0, 0, -1).Format(settings.DateLayout)
	if _, err := s.Pool.Exec(context.Background(), "UPDATE stores SET created_at=$1 WHERE id=$2", settings.Date(s.Now()).AddDate(0, -1, 0), s.Res.StoreID); err != nil {
		s.t.Fatal(err)
	}
	order := s.call("open_order", map[string]any{
		"gender": "female", "designated": true, "paper_date": paper,
		"items": []any{map[string]any{"item_id": s.item, "staff": []any{map[string]any{"slot": 1, "staff_id": s.members[0]}}}},
	}, 200)
	return id(order["id"])
}

func TestAssignBackfillRejected(t *testing.T) {
	s := openRotation(t)
	for _, member := range s.members[:2] {
		s.join(member)
	}
	orderID := s.backfillOrder()
	r := s.HTTPWithToken(t, s.Owner, "assign_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[0], "designated": true, "order_id": orderID})
	if r.Status != 409 || r.Detail() != "补录单不能接轮牌" {
		t.Fatal(r)
	}
}

func TestBackfillCheckoutDoesNotAdvanceRotation(t *testing.T) {
	s := openRotation(t)
	for _, member := range s.members[:2] {
		s.join(member)
	}
	orderID := s.backfillOrder()
	// assign_turn 已拒绝补录单；用 SQL 构造“历史已关联”的状态，守住结账时的跳过。
	s.assign(s.members[0], true, s.openFor(s.members[0], true, s.item))
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "UPDATE rotation_turns SET serving_order_id=$1 WHERE queue_id=$2 AND staff_id=$3 AND business_date=$4", orderID, s.queue, s.members[0], settings.Date(s.Now()))
	dbtest.Commit(t, tx)
	if got := s.entry(s.members[0]); got["status"] != "serving" || got["serving_order_id"] != orderID.String() {
		t.Fatal(got)
	}
	s.pay(orderID, "100")
	if got := s.entry(s.members[0]); got["status"] != "serving" || got["serving_order_id"] != orderID.String() {
		t.Fatalf("补录单结账推进了轮牌: %v", got)
	}
}
