package storeai_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"github.com/wzhlinhai/salon/backend/internal/storeai"
)

func TestFollowUpTimerEmitsOnce(t *testing.T) {
	shop := open(t)
	inbox := storeInbox(t, shop)
	t0 := at("2026-11-27", 10, 0)
	subject := uniqueSubject("coupon_redemption")
	var timer dbgen.AiTimer
	err := db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		var err error
		timer, err = storeai.ScheduleFollowUp(context.Background(), q, t0, inbox, subject, t0.Add(10*time.Minute), []byte(`{"reason":"waiting"}`))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	runOnce(t, shop, t0, noRun)
	if events := allEvents(t, shop, inbox.ID); len(events) != 0 {
		t.Fatalf("early events = %+v", events)
	}
	due := t0.Add(11 * time.Minute)
	runOnce(t, shop, due, noRun)
	events := allEvents(t, shop, inbox.ID)
	if len(events) != 1 || events[0].Kind != storeai.EventFollowUpDue {
		t.Fatalf("events = %+v", events)
	}
	wantKey := fmt.Sprintf("timer:%s:%d", timer.ID, timer.DueAt.Time.Unix())
	if events[0].SourceKey != wantKey {
		t.Fatalf("source key = %q, want %q", events[0].SourceKey, wantKey)
	}
	timers := allTimers(t, shop, inbox.ID)
	if len(timers) != 1 || timers[0].Status != "fired" {
		t.Fatalf("timers = %+v", timers)
	}
	runOnce(t, shop, due, noRun)
	if events := allEvents(t, shop, inbox.ID); len(events) != 1 {
		t.Fatalf("timer emitted twice: %+v", events)
	}
}

func TestFollowUpReplaceCancelAndPastDue(t *testing.T) {
	shop := open(t)
	inbox := storeInbox(t, shop)
	t0 := at("2026-11-27", 10, 0)
	subject := uniqueSubject("coupon_redemption")
	err := db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		if _, err := storeai.ScheduleFollowUp(context.Background(), q, t0, inbox, subject, t0.Add(time.Hour), []byte(`{}`)); err != nil {
			return err
		}
		_, err := storeai.ScheduleFollowUp(context.Background(), q, t0, inbox, subject, t0.Add(2*time.Hour), []byte(`{"replaced":true}`))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	timers := allTimers(t, shop, inbox.ID)
	if len(timers) != 1 || !timers[0].DueAt.Time.Equal(t0.Add(2*time.Hour)) || string(timers[0].Payload) != `{"replaced": true}` {
		t.Fatalf("timers = %+v", timers)
	}
	err = db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		return storeai.CancelFollowUps(context.Background(), q, inbox, subject)
	})
	if err != nil {
		t.Fatal(err)
	}
	err = db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		_, err := storeai.ScheduleFollowUp(context.Background(), q, t0, inbox, uniqueSubject("coupon_redemption"), t0, []byte(`{}`))
		return err
	})
	if !errors.Is(err, storeai.ErrFollowUpDue) {
		t.Fatalf("past due = %v", err)
	}
	timers = allTimers(t, shop, inbox.ID)
	if len(timers) != 1 || timers[0].Status != "cancelled" {
		t.Fatalf("cancelled timers = %+v", timers)
	}
	runOnce(t, shop, t0.Add(3*time.Hour), noRun)
	if events := allEvents(t, shop, inbox.ID); len(events) != 0 {
		t.Fatalf("cancelled timer emitted: %+v", events)
	}
}

func TestPeriodicTimerMissesOneEmitThenMovesForward(t *testing.T) {
	shop := open(t)
	inbox := storeInbox(t, shop)
	t0 := at("2026-11-27", 10, 0)
	dailyFirst := time.Date(2026, 11, 24, 9, 0, 0, 0, settings.Location)
	weeklyFirst := t0.AddDate(0, 0, -1)
	err := db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		if _, err := storeai.SchedulePeriodic(context.Background(), q, inbox, "daily_digest", dailyFirst, "daily"); err != nil {
			return err
		}
		_, err := storeai.SchedulePeriodic(context.Background(), q, inbox, "weekly_digest", weeklyFirst, "weekly")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	runOnce(t, shop, t0, noRun)
	events := allEvents(t, shop, inbox.ID)
	if len(events) != 2 {
		t.Fatalf("missed periodic emits = %+v", events)
	}
	for _, event := range events {
		if event.Kind != storeai.EventPeriodicDue {
			t.Fatalf("periodic event kind = %q", event.Kind)
		}
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["periodic_key"] != "daily_digest" && payload["periodic_key"] != "weekly_digest" {
			t.Fatalf("periodic payload = %v", payload)
		}
	}
	timers := allTimers(t, shop, inbox.ID)
	if len(timers) != 2 {
		t.Fatalf("timers = %+v", timers)
	}
	byKey := map[string]dbgen.AiTimer{}
	for _, timer := range timers {
		byKey[*timer.PeriodicKey] = timer
	}
	wantDaily := time.Date(2026, 11, 28, 9, 0, 0, 0, settings.Location)
	if !byKey["daily_digest"].DueAt.Time.Equal(wantDaily) {
		t.Fatalf("daily due = %s, want %s", byKey["daily_digest"].DueAt.Time, wantDaily)
	}
	if got := byKey["weekly_digest"].DueAt.Time.Sub(weeklyFirst); got != 7*24*time.Hour {
		t.Fatalf("weekly step = %s", got)
	}
	runOnce(t, shop, t0, noRun)
	if events := allEvents(t, shop, inbox.ID); len(events) != 2 {
		t.Fatalf("periodic emitted twice: %+v", events)
	}
}
