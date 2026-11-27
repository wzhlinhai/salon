package storeai_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/storeai"
)

func TestSingleWorkerPerInbox(t *testing.T) {
	shop := open(t)
	inbox := storeInbox(t, shop)
	now := at("2026-11-27", 10, 0)
	emitEvent(t, shop, inbox, now, storeai.EventRequest, source("single"), storeai.Subject{}, "{}")

	tx := dbtest.AppTx(t, shop.Pool, shop.Res.TenantID)
	dbtest.Exec(t, tx, "SELECT 1 FROM ai_inboxes WHERE id=$1 FOR UPDATE", inbox.ID)

	var calls atomic.Int32
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	run := func(context.Context, storeai.RunInput) (storeai.RunResult, error) {
		calls.Add(1)
		started <- struct{}{}
		<-release
		return storeai.RunResult{Status: "active"}, nil
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); runOnce(t, shop, now, run) }()
	go func() { defer wg.Done(); runOnce(t, shop, now, run) }()

	waitLockWaiters(t, shop, 2)
	dbtest.Rollback(t, tx)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("RunFunc was not called")
	}
	close(release)
	wg.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("RunFunc calls = %d, want 1", got)
	}
}

func TestClaimInboxLeaseHonorsActiveLease(t *testing.T) {
	shop := open(t)
	inbox := storeInbox(t, shop)
	now := at("2026-11-27", 10, 0)
	first, second := uuid.New(), uuid.New()
	err := db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		_, err := q.ClaimInboxLease(context.Background(), dbgen.ClaimInboxLeaseParams{
			LeaseToken: &first, LeaseUntil: timestampValue(now.Add(5 * time.Minute)),
			ID: inbox.ID, StoreID: shop.Res.StoreID, Now: timestampValue(now),
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed := false
	err = db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		_, err := q.ClaimInboxLease(context.Background(), dbgen.ClaimInboxLeaseParams{
			LeaseToken: &second, LeaseUntil: timestampValue(now.Add(5 * time.Minute)),
			ID: inbox.ID, StoreID: shop.Res.StoreID, Now: timestampValue(now),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		claimed = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("claimed an inbox whose lease is still active")
	}
}

func timestampValue(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func TestCrashRetryRunningEvent(t *testing.T) {
	shop := open(t)
	inbox := storeInbox(t, shop)
	now := at("2026-11-27", 10, 0)
	emitEvent(t, shop, inbox, now, storeai.EventRequest, source("crash"), storeai.Subject{}, "{}")
	tx := dbtest.AppTx(t, shop.Pool, shop.Res.TenantID)
	dbtest.Exec(t, tx, "UPDATE ai_inbox_events SET status='running', attempts=1 WHERE inbox_id=$1", inbox.ID)
	dbtest.Exec(t, tx, "UPDATE ai_inboxes SET lease_until=$1 WHERE id=$2", now.Add(-time.Minute), inbox.ID)
	dbtest.Commit(t, tx)

	var calls int
	runOnce(t, shop, now, func(context.Context, storeai.RunInput) (storeai.RunResult, error) {
		calls++
		return storeai.RunResult{Status: "active"}, nil
	})
	if calls != 1 {
		t.Fatalf("RunFunc calls = %d, want 1", calls)
	}
	events := allEvents(t, shop, inbox.ID)
	if len(events) != 1 || events[0].Status != "done" || events[0].Attempts != 2 {
		t.Fatalf("events = %+v", events)
	}
}

func TestEventDuringRunWaitsForNextBatch(t *testing.T) {
	shop := open(t)
	inbox := storeInbox(t, shop)
	now := at("2026-11-27", 10, 0)
	emitEvent(t, shop, inbox, now, storeai.EventRequest, "batch-a", storeai.Subject{}, "{}")
	var batches [][]string
	run := func(ctx context.Context, in storeai.RunInput) (storeai.RunResult, error) {
		var keys []string
		for _, event := range in.Events {
			keys = append(keys, event.SourceKey)
		}
		batches = append(batches, keys)
		err := in.Tx(ctx, func(q *dbgen.Queries) error {
			_, err := storeai.Emit(ctx, q, now, storeai.EmitInput{
				TenantID: in.TenantID, StoreID: in.StoreID, InboxID: in.Inbox.ID,
				Kind: storeai.EventRequest, SourceKey: "batch-b", Payload: []byte(`{}`),
			})
			return err
		})
		return storeai.RunResult{Status: "active"}, err
	}
	runOnce(t, shop, now, run)
	events := allEvents(t, shop, inbox.ID)
	if len(batches) != 1 || len(batches[0]) != 1 || batches[0][0] != "batch-a" {
		t.Fatalf("first batches = %v", batches)
	}
	if len(events) != 2 || events[0].Status != "done" || events[1].SourceKey != "batch-b" || events[1].Status != "pending" {
		t.Fatalf("events after first run = %+v", events)
	}
	runOnce(t, shop, now, run)
	if len(batches) != 2 || len(batches[1]) != 1 || batches[1][0] != "batch-b" {
		t.Fatalf("second batches = %v", batches)
	}
}

func TestBatchSameSubject(t *testing.T) {
	shop := open(t)
	inbox := storeInbox(t, shop)
	now := at("2026-11-27", 10, 0)
	subject := uniqueSubject("coupon_redemption")
	for i := range 3 {
		emitEvent(t, shop, inbox, now, storeai.EventRequest, fmt.Sprintf("same-%d", i), subject, "{}")
	}
	var batches int
	var received int
	runOnce(t, shop, now, func(_ context.Context, in storeai.RunInput) (storeai.RunResult, error) {
		batches++
		received = len(in.Events)
		return storeai.RunResult{Status: "active"}, nil
	})
	if batches != 1 || received != 3 {
		t.Fatalf("batches = %d, events = %d", batches, received)
	}
}

func TestSessionsReuseBySubject(t *testing.T) {
	shop := open(t)
	inbox := storeInbox(t, shop)
	now := at("2026-11-27", 10, 0)
	subjectA, subjectB := uniqueSubject("customer"), uniqueSubject("customer")
	var inputs []storeai.RunInput
	step := 0
	run := func(_ context.Context, in storeai.RunInput) (storeai.RunResult, error) {
		step++
		inputs = append(inputs, in)
		status := "active"
		if step == 4 {
			status = "closed"
		}
		return storeai.RunResult{Messages: []byte(fmt.Sprintf(`[{"step":%d}]`, step)), Status: status}, nil
	}
	emitEvent(t, shop, inbox, now, storeai.EventRequest, source("a1"), subjectA, "{}")
	runOnce(t, shop, now, run)
	emitEvent(t, shop, inbox, now, storeai.EventRequest, source("a2"), subjectA, "{}")
	runOnce(t, shop, now, run)
	emitEvent(t, shop, inbox, now, storeai.EventRequest, source("b1"), subjectB, "{}")
	runOnce(t, shop, now, run)
	emitEvent(t, shop, inbox, now, storeai.EventRequest, source("a3"), subjectA, "{}")
	runOnce(t, shop, now, run)
	emitEvent(t, shop, inbox, now, storeai.EventRequest, source("a4"), subjectA, "{}")
	runOnce(t, shop, now, run)
	if len(inputs) != 5 {
		t.Fatalf("runs = %d", len(inputs))
	}
	if inputs[0].Session.ID != inputs[1].Session.ID {
		t.Fatal("same subject should reuse session")
	}
	if string(inputs[1].Session.Messages) != `[{"step": 1}]` {
		t.Fatalf("resumed messages = %s", inputs[1].Session.Messages)
	}
	if inputs[2].Session.ID == inputs[0].Session.ID {
		t.Fatal("different subject should start a new session")
	}
	if inputs[3].Session.ID != inputs[0].Session.ID {
		t.Fatal("open session should be reused")
	}
	if inputs[4].Session.ID == inputs[0].Session.ID || inputs[4].Session.ID == inputs[2].Session.ID {
		t.Fatal("closed session should not be reused")
	}
}

func TestFailureRetryThenFailed(t *testing.T) {
	shop := open(t)
	inbox := storeInbox(t, shop)
	t0 := at("2026-11-27", 10, 0)
	emitEvent(t, shop, inbox, t0, storeai.EventRequest, source("fail"), storeai.Subject{}, "{}")
	var calls int
	run := func(context.Context, storeai.RunInput) (storeai.RunResult, error) {
		calls++
		return storeai.RunResult{}, errors.New(strings.Repeat("错", 300))
	}
	runOnce(t, shop, t0, run)
	events := allEvents(t, shop, inbox.ID)
	if len(events) != 1 || events[0].Attempts != 1 || events[0].Status != "pending" || !events[0].NotBefore.Time.Equal(t0.Add(time.Minute)) {
		t.Fatalf("first failure = %+v", events)
	}
	if events[0].LastError == nil || len([]rune(*events[0].LastError)) != 200 {
		t.Fatalf("last error = %v", events[0].LastError)
	}
	runOnce(t, shop, t0, run)
	if calls != 1 {
		t.Fatalf("retried before not_before, calls = %d", calls)
	}
	runOnce(t, shop, t0.Add(2*time.Minute), run)
	events = allEvents(t, shop, inbox.ID)
	if events[0].Attempts != 2 || !events[0].NotBefore.Time.Equal(t0.Add(4*time.Minute)) {
		t.Fatalf("second failure = %+v", events)
	}
	runOnce(t, shop, t0.Add(5*time.Minute), run)
	events = allEvents(t, shop, inbox.ID)
	if events[0].Attempts != 3 || events[0].Status != "failed" {
		t.Fatalf("third failure = %+v", events)
	}
	runOnce(t, shop, t0.Add(10*time.Minute), run)
	if calls != 3 {
		t.Fatalf("failed event retried, calls = %d", calls)
	}
}

func TestPanicIsolation(t *testing.T) {
	shop := open(t)
	staffA, _ := shop.Hire("小李", "002", "li01", shop.Role("发型师"))
	staffB, _ := shop.Hire("小王", "003", "wang01", shop.Role("发型师"))
	inboxA, inboxB := staffInbox(t, shop, staffA), staffInbox(t, shop, staffB)
	now := at("2026-11-27", 10, 0)
	emitEvent(t, shop, inboxA, now, storeai.EventRequest, source("a"), storeai.Subject{}, "{}")
	emitEvent(t, shop, inboxB, now, storeai.EventRequest, source("b"), storeai.Subject{}, "{}")
	runOnce(t, shop, now, func(_ context.Context, in storeai.RunInput) (storeai.RunResult, error) {
		if in.Inbox.StaffID != nil && *in.Inbox.StaffID == staffA {
			panic("boom")
		}
		return storeai.RunResult{Status: "active"}, nil
	})
	eventsA, eventsB := allEvents(t, shop, inboxA.ID), allEvents(t, shop, inboxB.ID)
	if len(eventsA) != 1 || eventsA[0].Status != "pending" || eventsA[0].Attempts != 1 || eventsA[0].LastError == nil || *eventsA[0].LastError != "panic: boom" {
		t.Fatalf("panicked inbox = %+v", eventsA)
	}
	if len(eventsB) != 1 || eventsB[0].Status != "done" {
		t.Fatalf("other inbox = %+v", eventsB)
	}
}

func TestSaveWithLostLeaseDoesNotWrite(t *testing.T) {
	shop := open(t)
	inbox := storeInbox(t, shop)
	now := at("2026-11-27", 10, 0)
	emitEvent(t, shop, inbox, now, storeai.EventRequest, source("lease"), uniqueSubject("customer"), "{}")
	runOnce(t, shop, now, func(ctx context.Context, in storeai.RunInput) (storeai.RunResult, error) {
		if _, err := shop.Pool.Exec(ctx, "UPDATE ai_inboxes SET lease_token=$1 WHERE id=$2", uuid.New(), inbox.ID); err != nil {
			return storeai.RunResult{}, err
		}
		return storeai.RunResult{Messages: []byte(`[{"saved":1}]`), Status: "active"}, nil
	})
	events := allEvents(t, shop, inbox.ID)
	if len(events) != 1 || events[0].Status != "running" || events[0].SessionID != nil || events[0].DoneAt.Valid {
		t.Fatalf("events = %+v", events)
	}
	sessions := allSessions(t, shop, inbox.ID)
	if len(sessions) != 1 || string(sessions[0].Messages) != `[]` {
		t.Fatalf("sessions = %+v", sessions)
	}
}

func TestTenantIsolation(t *testing.T) {
	shop := open(t)
	inboxA := storeInbox(t, shop)
	now := at("2026-11-27", 10, 0)
	emitEvent(t, shop, inboxA, now, storeai.EventRequest, "tenant-a", storeai.Subject{}, "{}")

	tenantB, storeB := uuid.New(), uuid.New()
	if _, err := shop.Pool.Exec(context.Background(), "INSERT INTO tenants (id,name) VALUES ($1,'第二家')", tenantB); err != nil {
		t.Fatal(err)
	}
	if _, err := shop.Pool.Exec(context.Background(), "INSERT INTO stores (id,tenant_id,name,created_at) VALUES ($1,$2,'第二家',$3)", storeB, tenantB, now); err != nil {
		t.Fatal(err)
	}
	var inboxB dbgen.AiInbox
	err := db.InTenant(context.Background(), shop.Pool, tenantB, func(q *dbgen.Queries) error {
		var err error
		inboxB, err = storeai.EnsureInbox(context.Background(), q, storeB, nil)
		if err != nil {
			return err
		}
		_, err = storeai.Emit(context.Background(), q, now, storeai.EmitInput{
			TenantID: tenantB, StoreID: storeB, InboxID: inboxB.ID, Kind: storeai.EventRequest,
			SourceKey: "tenant-b", Payload: []byte(`{}`),
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[uuid.UUID][]string{}
	var mu sync.Mutex
	runOnce(t, shop, now, func(_ context.Context, in storeai.RunInput) (storeai.RunResult, error) {
		mu.Lock()
		defer mu.Unlock()
		for _, event := range in.Events {
			seen[in.TenantID] = append(seen[in.TenantID], event.SourceKey)
		}
		return storeai.RunResult{Status: "active"}, nil
	})
	if len(seen[shop.Res.TenantID]) != 1 || seen[shop.Res.TenantID][0] != "tenant-a" {
		t.Fatalf("tenant A saw %v", seen[shop.Res.TenantID])
	}
	if len(seen[tenantB]) != 1 || seen[tenantB][0] != "tenant-b" {
		t.Fatalf("tenant B saw %v", seen[tenantB])
	}
	if events := allEvents(t, shop, inboxB.ID); len(events) != 0 {
		t.Fatalf("tenant A read tenant B events: %+v", events)
	}
}

func advancingClock(base time.Time, step time.Duration) func() time.Time {
	var mu sync.Mutex
	calls := 0
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return base.Add(time.Duration(calls) * step)
	}
}

func TestRenewLeaseUsesCurrentClock(t *testing.T) {
	shop := open(t)
	inbox := storeInbox(t, shop)
	now := at("2026-11-27", 10, 0)
	emitEvent(t, shop, inbox, now, storeai.EventRequest, "clock-a", uniqueSubject("customer"), "{}")
	emitEvent(t, shop, inbox, now, storeai.EventRequest, "clock-b", uniqueSubject("customer"), "{}")
	clock := advancingClock(now, 2*time.Minute)
	other := uuid.New()
	rolledBack := errors.New("rollback")
	claimed, stale := false, false
	run := func(ctx context.Context, in storeai.RunInput) (storeai.RunResult, error) {
		var lease pgtype.Timestamptz
		err := db.InTenant(ctx, shop.Pool, in.TenantID, func(q *dbgen.Queries) error {
			row, err := q.GetInbox(ctx, in.Inbox.ID)
			if err != nil {
				return err
			}
			lease = row.LeaseUntil
			current := clock()
			_, err = q.ClaimInboxLease(ctx, dbgen.ClaimInboxLeaseParams{
				LeaseToken: &other, LeaseUntil: timestampValue(current.Add(storeai.LeaseDuration)),
				ID: in.Inbox.ID, StoreID: in.StoreID, Now: timestampValue(current),
			})
			if errors.Is(err, pgx.ErrNoRows) {
				return rolledBack
			}
			if err != nil {
				return err
			}
			claimed = true
			return rolledBack
		})
		if !errors.Is(err, rolledBack) {
			return storeai.RunResult{}, err
		}
		if lease.Time.Before(in.Now.Add(storeai.LeaseDuration)) {
			stale = true
		}
		return storeai.RunResult{Status: "active"}, nil
	}
	runOnceClock(t, shop, clock, run)
	if claimed {
		t.Fatal("another worker claimed an inbox whose lease should still be active")
	}
	if stale {
		t.Fatal("lease was renewed with an old clock")
	}
}

func TestNilRunOnlySchedulesAndDispatches(t *testing.T) {
	shop := open(t)
	inbox := storeInbox(t, shop)
	now := at("2026-11-27", 10, 0)
	emitEvent(t, shop, inbox, now, storeai.EventRequest, "pending", storeai.Subject{}, "{}")
	err := db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		_, err := storeai.ScheduleFollowUp(context.Background(), q, now.Add(-time.Minute), inbox, uniqueSubject("coupon_redemption"), now, []byte(`{}`))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	putReminder(t, shop, now, storeai.PutReminderInput{
		StoreID: shop.Res.StoreID, RecipientID: shop.Res.OwnerID, Tier: "now",
		Subject: uniqueSubject("coupon_redemption"), Body: "提醒",
	})
	runOnceClock(t, shop, func() time.Time { return now }, nil)
	events := allEvents(t, shop, inbox.ID)
	if len(events) != 2 {
		t.Fatalf("events = %+v", events)
	}
	for _, event := range events {
		if event.Status != "pending" || event.Attempts != 0 {
			t.Fatalf("nil run processed event: %+v", event)
		}
	}
	if sessions := allSessions(t, shop, inbox.ID); len(sessions) != 0 {
		t.Fatalf("nil run created sessions: %+v", sessions)
	}
	rows := outboxRows(t, shop, shop.Res.OwnerID)
	if len(rows) != 1 || rows[0].Status != "sent" {
		t.Fatalf("dispatcher did not run: %+v", rows)
	}
}

func TestEventsStayInOrder(t *testing.T) {
	shop := open(t)
	inbox := storeInbox(t, shop)
	t0 := at("2026-11-27", 10, 0)
	subject := uniqueSubject("customer")
	for i, key := range []string{"first", "second", "third"} {
		emitEvent(t, shop, inbox, t0.Add(time.Duration(i)*time.Second), storeai.EventRequest, key, subject, "{}")
	}
	var got []string
	runOnce(t, shop, t0.Add(3*time.Second), func(_ context.Context, in storeai.RunInput) (storeai.RunResult, error) {
		for _, event := range in.Events {
			got = append(got, event.SourceKey)
		}
		return storeai.RunResult{Status: "active"}, nil
	})
	if len(got) != 3 || got[0] != "first" || got[1] != "second" || got[2] != "third" {
		t.Fatalf("event order = %v", got)
	}
}

func TestTakePendingAllowsKeyShare(t *testing.T) {
	shop := open(t)
	inbox := storeInbox(t, shop)
	now := at("2026-11-27", 10, 0)
	emitEvent(t, shop, inbox, now, storeai.EventRequest, "key-share", storeai.Subject{}, "{}")
	event := allEvents(t, shop, inbox.ID)[0]
	tx := dbtest.AppTx(t, shop.Pool, shop.Res.TenantID)
	dbtest.Exec(t, tx, "SELECT 1 FROM ai_inbox_events WHERE id=$1 FOR KEY SHARE", event.ID)
	var rows []dbgen.TakePendingEventsRow
	err := db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		var err error
		rows, err = q.TakePendingEvents(context.Background(), dbgen.TakePendingEventsParams{
			StoreID: shop.Res.StoreID, InboxID: inbox.ID, Now: timestampValue(now),
			MaxAttempts: storeai.MaxAttempts, LeaseToken: new(uuid.UUID),
		})
		return err
	})
	dbtest.Rollback(t, tx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Status != "running" {
		t.Fatalf("TakePendingEvents with KEY SHARE = %+v", rows)
	}
}

func TestTakePendingSkipsExhausted(t *testing.T) {
	shop := open(t)
	inbox := storeInbox(t, shop)
	now := at("2026-11-27", 10, 0)
	emitEvent(t, shop, inbox, now, storeai.EventRequest, "exhausted", storeai.Subject{}, "{}")
	tx := dbtest.AppTx(t, shop.Pool, shop.Res.TenantID)
	dbtest.Exec(t, tx, "UPDATE ai_inbox_events SET attempts=$1 WHERE inbox_id=$2", storeai.MaxAttempts, inbox.ID)
	dbtest.Commit(t, tx)
	calls := 0
	runOnce(t, shop, now, func(context.Context, storeai.RunInput) (storeai.RunResult, error) {
		calls++
		return storeai.RunResult{Status: "active"}, nil
	})
	if calls != 0 {
		t.Fatalf("RunFunc called %d times for an exhausted event", calls)
	}
	events := allEvents(t, shop, inbox.ID)
	if len(events) != 1 || events[0].Status != "pending" || events[0].Attempts != storeai.MaxAttempts {
		t.Fatalf("event = %+v", events)
	}
}
