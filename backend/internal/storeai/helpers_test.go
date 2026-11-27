package storeai_test

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"github.com/wzhlinhai/salon/backend/internal/storeai"
)

func open(t *testing.T) *optest.Shop {
	t.Helper()
	return optest.OpenShop(t)
}

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func at(day string, hour, minute int) time.Time {
	d, err := time.ParseInLocation(settings.DateLayout, day, settings.Location)
	if err != nil {
		panic(err)
	}
	return time.Date(d.Year(), d.Month(), d.Day(), hour, minute, 0, 0, settings.Location)
}

func storeInbox(t *testing.T, shop *optest.Shop) dbgen.AiInbox {
	t.Helper()
	return ensureInbox(t, shop, nil)
}

func staffInbox(t *testing.T, shop *optest.Shop, staffID uuid.UUID) dbgen.AiInbox {
	t.Helper()
	return ensureInbox(t, shop, &staffID)
}

func ensureInbox(t *testing.T, shop *optest.Shop, staffID *uuid.UUID) dbgen.AiInbox {
	t.Helper()
	var inbox dbgen.AiInbox
	err := db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		var err error
		inbox, err = storeai.EnsureInbox(context.Background(), q, shop.Res.StoreID, staffID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return inbox
}

func emitEvent(t *testing.T, shop *optest.Shop, inbox dbgen.AiInbox, now time.Time, kind, sourceKey string, subject storeai.Subject, payload string) bool {
	t.Helper()
	if payload == "" {
		payload = "{}"
	}
	var inserted bool
	err := db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		var err error
		inserted, err = storeai.Emit(context.Background(), q, now, storeai.EmitInput{
			TenantID: shop.Res.TenantID, StoreID: shop.Res.StoreID, InboxID: inbox.ID,
			Kind: kind, SourceKey: sourceKey, Subject: subject, Payload: []byte(payload),
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return inserted
}

func allEvents(t *testing.T, shop *optest.Shop, inboxID uuid.UUID) []dbgen.AiInboxEvent {
	t.Helper()
	var events []dbgen.AiInboxEvent
	err := db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		var err error
		events, err = q.ListInboxEvents(context.Background(), inboxID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func allTimers(t *testing.T, shop *optest.Shop, inboxID uuid.UUID) []dbgen.AiTimer {
	t.Helper()
	var timers []dbgen.AiTimer
	err := db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		var err error
		timers, err = q.ListTimers(context.Background(), inboxID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return timers
}

func allSessions(t *testing.T, shop *optest.Shop, inboxID uuid.UUID) []dbgen.AiSession {
	t.Helper()
	var sessions []dbgen.AiSession
	err := db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		var err error
		sessions, err = q.ListSessions(context.Background(), inboxID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return sessions
}

func outboxRows(t *testing.T, shop *optest.Shop, recipientID uuid.UUID) []dbgen.AiOutbox {
	t.Helper()
	var rows []dbgen.AiOutbox
	err := db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		var err error
		rows, err = q.ListOutboxForRecipient(context.Background(), dbgen.ListOutboxForRecipientParams{
			StoreID: shop.Res.StoreID, RecipientID: recipientID,
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func requestByID(t *testing.T, shop *optest.Shop, id uuid.UUID) dbgen.AiRequest {
	t.Helper()
	var request dbgen.AiRequest
	err := db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		var err error
		request, err = q.GetRequest(context.Background(), id)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func runOnce(t *testing.T, shop *optest.Shop, now time.Time, run storeai.RunFunc) {
	t.Helper()
	runOnceClock(t, shop, func() time.Time { return now }, run)
}

func runOnceClock(t *testing.T, shop *optest.Shop, clock func() time.Time, run storeai.RunFunc) {
	t.Helper()
	storeai.RunOnce(t.Context(), shop.Pool, clock, run, discardLogger())
}

func noRun(context.Context, storeai.RunInput) (storeai.RunResult, error) {
	return storeai.RunResult{}, nil
}

func insertOutbox(t *testing.T, shop *optest.Shop, recipientID uuid.UUID, tier string, subject storeai.Subject, body, status string, parentID *uuid.UUID, sentAt, readAt, withdrawnAt *time.Time) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	tx := dbtest.AppTx(t, shop.Pool, shop.Res.TenantID)
	err := tx.QueryRow(context.Background(), `
		INSERT INTO ai_outbox (tenant_id,store_id,recipient_id,tier,subject_kind,subject_id,parent_id,body,status,queued_at,sent_at,read_at,withdrawn_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING id`,
		shop.Res.TenantID, shop.Res.StoreID, recipientID, tier, subject.Kind, subject.ID, parentID, body, status,
		at("2026-11-27", 8, 0), sentAt, readAt, withdrawnAt).Scan(&id)
	if err != nil {
		dbtest.Rollback(t, tx)
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)
	return id
}

func waitLockWaiters(t *testing.T, shop *optest.Shop, want int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		waiting, err := dbtest.LockWaits(ctx, shop.Pool)
		if err != nil {
			t.Fatal(err)
		}
		if waiting >= want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("expected at least %d lock waiters, observed %d", want, waiting)
		case <-ticker.C:
		}
	}
}

func uniqueSubject(kind string) storeai.Subject {
	return storeai.Subject{Kind: kind, ID: uuid.New()}
}

func source(prefix string) string { return fmt.Sprintf("%s:%s", prefix, uuid.NewString()) }
