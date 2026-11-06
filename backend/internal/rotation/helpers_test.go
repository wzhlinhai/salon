package rotation_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

type fixture struct {
	*optest.Shop
	t        *testing.T
	queue    uuid.UUID
	position uuid.UUID
	item     uuid.UUID
	members  []uuid.UUID
}

func openRotation(t *testing.T) *fixture {
	t.Helper()
	s := &fixture{Shop: optest.OpenShop(t), t: t}
	s.SetNow(time.Date(2026, 6, 12, 12, 0, 0, 0, settings.Location))
	s.position = id(s.call("create_position", map[string]any{"name": "大工"}, 200)["id"])
	s.item = s.createItem("剪发", "100")
	s.members = []uuid.UUID{}
	for i, name := range []string{"甲", "乙", "丙", "丁", "戊"} {
		staffID, _ := s.Hire(name, "10"+string(rune('0'+i)), "staff"+string(rune('a'+i)))
		s.members = append(s.members, staffID)
	}
	q := s.call("create_rotation_queue", map[string]any{"name": "剪发牌"}, 200)
	s.queue = id(q["id"])
	s.setMembers(s.members[:3])
	return s
}

func id(v any) uuid.UUID { return uuid.MustParse(v.(string)) }

func (s *fixture) call(op string, in any, want int) map[string]any {
	s.t.Helper()
	return s.Call(s.t, s.Owner, op, in, want).Body
}

func (s *fixture) setMembers(ids []uuid.UUID) {
	s.t.Helper()
	s.call("set_queue_members", map[string]any{"queue_id": s.queue, "staff_ids": ids}, 200)
}

func (s *fixture) createItem(name, price string) uuid.UUID {
	s.t.Helper()
	item := s.call("create_item", map[string]any{
		"name": name, "price": price, "discountable": true,
		"labor_fee_amount": nil, "labor_fee_percent": nil, "level_prices": []any{},
		"positions": []any{map[string]any{"position_id": s.position, "required": true}},
	}, 200)
	return id(item["id"])
}

func (s *fixture) rotate(date time.Time) map[string]any {
	s.t.Helper()
	return s.call("get_rotation", map[string]any{"queue_id": s.queue, "date": date.Format(settings.DateLayout)}, 200)
}

func (s *fixture) today() map[string]any {
	s.t.Helper()
	return s.rotate(settings.Date(s.Now()))
}

func (s *fixture) names(view map[string]any) []string {
	s.t.Helper()
	out := []string{}
	for _, entry := range entries(view) {
		out = append(out, entry["staff"].(map[string]any)["name"].(string))
	}
	return out
}

func (s *fixture) statuses(view map[string]any) []string {
	s.t.Helper()
	out := []string{}
	for _, entry := range entries(view) {
		out = append(out, entry["status"].(string))
	}
	return out
}

func entries(view map[string]any) []map[string]any {
	out := []map[string]any{}
	for _, entry := range view["entries"].([]any) {
		out = append(out, entry.(map[string]any))
	}
	return out
}

func (s *fixture) join(staffID uuid.UUID) map[string]any {
	s.t.Helper()
	return s.call("join_rotation", map[string]any{"queue_id": s.queue, "staff_id": staffID}, 200)
}

func (s *fixture) events(from, to time.Time) []map[string]any {
	s.t.Helper()
	view := s.call("list_rotation_events", map[string]any{"queue_id": s.queue, "from": from.Format(settings.DateLayout), "to": to.Format(settings.DateLayout)}, 200)
	out := []map[string]any{}
	for _, entry := range view["entries"].([]any) {
		out = append(out, entry.(map[string]any))
	}
	return out
}

func (s *fixture) event(kind string) map[string]any {
	s.t.Helper()
	day := settings.Date(s.Now())
	for _, event := range s.events(day, day) {
		if event["kind"] == kind {
			return event
		}
	}
	s.t.Fatalf("no %s event: %v", kind, s.events(day, day))
	return nil
}

func (s *fixture) snapshot() string {
	s.t.Helper()
	return dbtest.Snapshot(s.t, s.Pool, s.Res.TenantID, "rotation_queues", "rotation_members", "rotation_turns", "rotation_events", "rotation_half_items")
}

func reject(t *testing.T, r optest.Result, status int, detail string) {
	t.Helper()
	if r.Status != status || !strings.Contains(r.Detail(), detail) {
		t.Fatalf("got %d %q (%v), want %d %q", r.Status, r.Detail(), r.Body, status, detail)
	}
}

func settingsDate(s *fixture) time.Time { return settings.Date(s.Now()) }

func servingStaff(view map[string]any, staffID uuid.UUID) bool {
	for _, entry := range entries(view) {
		if entry["status"] == "serving" && entry["staff"].(map[string]any)["id"] == staffID.String() {
			return true
		}
	}
	return false
}

func findStatus(view map[string]any, status string) map[string]any {
	for _, entry := range entries(view) {
		if entry["status"] == status {
			return entry
		}
	}
	return nil
}

func assertNames(t *testing.T, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
}
