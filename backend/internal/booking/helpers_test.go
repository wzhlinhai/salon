package booking_test

import (
	"context"
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
	position uuid.UUID
	item     uuid.UUID
	customer uuid.UUID
	staff    uuid.UUID
	other    uuid.UUID
}

func openBooking(t *testing.T) *fixture {
	t.Helper()
	s := &fixture{Shop: optest.OpenShop(t), t: t}
	s.SetNow(time.Date(2026, 6, 12, 12, 0, 0, 0, settings.Location))
	s.position = id(s.call("create_position", map[string]any{"name": "大工"}, 200)["id"])
	s.item = s.createItem("剪发", "100", 45, true)
	s.staff = s.hire("发型师甲", "002", "stylist-a", "发型师")
	s.other = s.hire("发型师乙", "003", "stylist-b", "发型师")
	s.customer = id(s.call("create_customer", map[string]any{"name": "顾客甲", "phone": "13800000001"}, 200)["customer"].(map[string]any)["id"])
	return s
}

func id(v any) uuid.UUID { return uuid.MustParse(v.(string)) }

func (s *fixture) call(op string, in any, want int) map[string]any {
	s.t.Helper()
	return s.Call(s.t, s.Owner, op, in, want).Body
}

func (s *fixture) hire(name, number, account, role string) uuid.UUID {
	s.t.Helper()
	staffID, _ := s.Hire(name, number, account, s.Role(role))
	return staffID
}

func (s *fixture) createItem(name, price string, duration int, bookable bool) uuid.UUID {
	s.t.Helper()
	item := s.call("create_item", map[string]any{
		"name": name, "price": price, "discountable": true, "duration_minutes": duration, "bookable": bookable,
		"labor_fee_amount": nil, "labor_fee_percent": nil, "positions": []any{}, "level_prices": []any{}, "member_prices": []any{},
	}, 200)
	return id(item["id"])
}

func (s *fixture) at(day int, clock string) time.Time {
	s.t.Helper()
	d, err := time.ParseInLocation(settings.DateLayout+" 15:04", settings.Date(s.Now()).AddDate(0, 0, day).Format(settings.DateLayout)+" "+clock, settings.Location)
	if err != nil {
		s.t.Fatal(err)
	}
	return d
}

func (s *fixture) appointment(input map[string]any, want int) optest.Result {
	s.t.Helper()
	if _, ok := input["customer_id"]; !ok {
		input["customer_id"] = s.customer
	}
	return s.Call(s.t, s.Owner, "create_appointment", input, want)
}

func (s *fixture) block(staffID uuid.UUID, start, end time.Time, want int) optest.Result {
	s.t.Helper()
	return s.Call(s.t, s.Owner, "create_block", map[string]any{"staff_id": staffID, "starts_at": start, "ends_at": end}, want)
}

func (s *fixture) fail(op string, input any) optest.Result {
	s.t.Helper()
	return s.Call(s.t, s.Owner, op, input, 0)
}

func (s *fixture) booked(input map[string]any) map[string]any {
	s.t.Helper()
	return s.appointment(input, 200).Body
}

func (s *fixture) bookedBlock(staffID uuid.UUID, start, end time.Time) map[string]any {
	s.t.Helper()
	return s.block(staffID, start, end, 200).Body
}

func (s *fixture) setSetting(key string, value any) {
	s.t.Helper()
	s.call("change_setting", map[string]any{"key": key, "value": value, "effective_from": settings.Date(s.Now()).Format(settings.DateLayout)}, 200)
}

func (s *fixture) snapshot() string {
	s.t.Helper()
	return dbtest.Snapshot(s.t, s.Pool, s.Res.TenantID, "appointments", "appointment_items", "appointment_events")
}

func (s *fixture) events(appointmentID uuid.UUID) []map[string]any {
	s.t.Helper()
	view := s.call("get_appointment", map[string]any{"id": appointmentID}, 200)
	out := []map[string]any{}
	for _, event := range view["events"].([]any) {
		out = append(out, event.(map[string]any))
	}
	return out
}

func (s *fixture) eventKinds(appointmentID uuid.UUID) []string {
	s.t.Helper()
	out := []string{}
	for _, event := range s.events(appointmentID) {
		out = append(out, event["kind"].(string))
	}
	return out
}

func (s *fixture) insertPending(staffID, customerID uuid.UUID, start, end time.Time) uuid.UUID {
	s.t.Helper()
	tx := dbtest.AppTx(s.t, s.Pool, s.Res.TenantID)
	var appointmentID uuid.UUID
	if err := tx.QueryRow(context.Background(), `INSERT INTO appointments (tenant_id,store_id,staff_id,customer_id,kind,starts_at,ends_at,status,created_by) VALUES ($1,$2,$3,$4,'appointment',$5,$6,'pending',$7) RETURNING id`, s.Res.TenantID, s.Res.StoreID, staffID, customerID, start, end, s.Res.OwnerID).Scan(&appointmentID); err != nil {
		s.t.Fatal(err)
	}
	dbtest.Commit(s.t, tx)
	return appointmentID
}

func reject(t *testing.T, r optest.Result, status int, detail string) {
	t.Helper()
	if r.Status != status || !strings.Contains(r.Detail(), detail) {
		t.Fatalf("got %d %q (%v), want %d containing %q", r.Status, r.Detail(), r.Body, status, detail)
	}
}

type appointmentSnapshot struct {
	status string
	events []map[string]any
}

func (s *fixture) appointmentState(id uuid.UUID) appointmentSnapshot {
	s.t.Helper()
	view := s.call("get_appointment", map[string]any{"id": id}, 200)
	return appointmentSnapshot{status: view["status"].(string), events: s.events(id)}
}

func (s *fixture) appointmentCount(day string) int {
	s.t.Helper()
	return len(s.call("list_appointments", map[string]any{"from": day, "to": day}, 200)["entries"].([]any))
}

func (s *fixture) rejectedUnchanged(t *testing.T, id uuid.UUID, r optest.Result, status int, detail string) {
	t.Helper()
	before := s.appointmentState(id)
	reject(t, r, status, detail)
	after := s.appointmentState(id)
	if before.status != after.status || !reflect.DeepEqual(before.events, after.events) {
		t.Fatalf("rejected call changed appointment: before=%+v after=%+v", before, after)
	}
}
