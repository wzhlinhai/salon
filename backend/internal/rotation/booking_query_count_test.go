package rotation_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/optest"
)

func TestAssignTurnOccupancyQueryCount(t *testing.T) {
	s := openRotation(t)
	for _, member := range s.members[:3] {
		s.join(member)
	}
	long := s.call("create_rotation_queue", map[string]any{"name": "五点牌"}, 200)
	longID := id(long["id"])
	s.call("set_queue_members", map[string]any{"queue_id": longID, "staff_ids": s.members}, 200)
	for _, member := range s.members {
		s.call("join_rotation", map[string]any{"queue_id": longID, "staff_id": member}, 200)
	}
	counter := &rotationQueryCounter{}
	config := s.Pool.Config().Copy()
	config.ConnConfig.Tracer = counter
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	app := optest.New(t, pool)
	app.SetNow(s.Now())
	caller := s.MustCaller(s.Owner)
	counts := []int64{}
	for _, queueID := range []uuid.UUID{s.queue, longID} {
		counter.all.Store(0)
		r := app.HTTP(t, &caller, "assign_turn", map[string]any{"queue_id": queueID})
		if r.Status != 200 {
			t.Fatal(queueID, r)
		}
		counts = append(counts, counter.all.Load())
	}
	if counts[0] != counts[1] {
		t.Fatalf("assign_turn query count grew with waiting staff: %v", counts)
	}
}
