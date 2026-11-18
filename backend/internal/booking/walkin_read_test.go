package booking_test

import (
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func TestListAppointmentsWalkIns(t *testing.T) {
	s := openBooking(t)
	item := s.positionedItem("主做烫发", 60)
	s.SetNow(s.at(0, "14:00"))
	order := s.walkinOrder(s.customer, s.staff, item)
	otherOrder := s.walkinOrder(s.customer, s.other, item)
	day := settings.Date(s.Now()).Format(settings.DateLayout)
	next := settings.Date(s.Now()).AddDate(0, 0, 1).Format(settings.DateLayout)
	owner := s.MustCaller(s.Owner)
	for _, ch := range s.Channels() {
		list := ch.Call(t, owner, "list_appointments", map[string]any{"from": day, "to": day})
		if list.Status != 200 {
			t.Fatal(ch.Name, list)
		}
		walkIns := list.Body["walk_ins"].([]any)
		if len(walkIns) != 2 {
			t.Fatalf("%s walk_ins = %v", ch.Name, walkIns)
		}
		byOrder := map[string]map[string]any{}
		for _, raw := range walkIns {
			row := raw.(map[string]any)
			byOrder[row["order_id"].(string)] = row
			for _, key := range []string{"customer", "customer_id", "order"} {
				if _, ok := row[key]; ok {
					t.Fatalf("%s walk-in leaked customer data: %v", ch.Name, row)
				}
			}
		}
		own := byOrder[order["id"].(string)]
		if own == nil || own["staff"].(map[string]any)["id"] != s.staff.String() || own["starts_at"] != s.at(0, "14:00").Format("2006-01-02T15:04:05+08:00") || own["ends_at"] != s.at(0, "15:00").Format("2006-01-02T15:04:05+08:00") {
			t.Fatalf("%s own walk-in = %v", ch.Name, own)
		}
		if byOrder[otherOrder["id"].(string)] == nil {
			t.Fatalf("%s other walk-in missing: %v", ch.Name, byOrder)
		}

		filtered := ch.Call(t, owner, "list_appointments", map[string]any{"from": day, "to": day, "staff_id": s.staff})
		if filtered.Status != 200 || len(filtered.Body["walk_ins"].([]any)) != 1 || filtered.Body["walk_ins"].([]any)[0].(map[string]any)["staff"].(map[string]any)["id"] != s.staff.String() {
			t.Fatalf("%s staff filter = %v", ch.Name, filtered)
		}
		excluded := ch.Call(t, owner, "list_appointments", map[string]any{"from": next, "to": next})
		if excluded.Status != 200 || len(excluded.Body["walk_ins"].([]any)) != 0 {
			t.Fatalf("%s range exclusion = %v", ch.Name, excluded)
		}
	}

	stylist := s.MustCaller(s.MustLogin("stylist-a", "password-stylist-a"))
	for _, ch := range s.Channels() {
		r := ch.Call(t, stylist, "list_appointments", map[string]any{"from": day, "to": day})
		if r.Status != 200 {
			t.Fatal(ch.Name, r)
		}
		scoped := r.Body["walk_ins"].([]any)
		if len(scoped) != 1 || scoped[0].(map[string]any)["staff"].(map[string]any)["id"] != s.staff.String() {
			t.Fatalf("%s self scope = %v", ch.Name, scoped)
		}
	}
}

func TestWalkinListQueryCount(t *testing.T) {
	s := openBooking(t)
	item := s.positionedItem("主做烫发", 60)
	s.SetNow(s.at(0, "14:00"))
	s.walkinOrder(s.customer, s.staff, item)
	day := settings.Date(s.Now()).Format(settings.DateLayout)

	counter := &queryCounter{}
	pool := countingPool(t, s.Pool, counter)
	app := optest.New(t, pool)
	app.SetNow(s.Now())
	owner := s.MustCaller(s.Owner)
	count := func(ch optest.Channel, want int) int64 {
		counter.all.Store(0)
		r := ch.Call(t, owner, "list_appointments", map[string]any{"from": day, "to": day})
		if r.Status != 200 || len(r.Body["walk_ins"].([]any)) != want {
			t.Fatalf("%s walk_ins = %v", ch.Name, r)
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
		s.walkinOrder(s.customer, s.staff, item)
	}
	for _, ch := range app.Channels() {
		if many := count(ch, 5); one[ch.Name] != many {
			t.Fatalf("%s query count grew with open orders: %d vs %d", ch.Name, one[ch.Name], many)
		}
	}
}
