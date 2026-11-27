package storeai_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/storeai"
)

func TestEmitDeduplicatesSourceKey(t *testing.T) {
	shop := open(t)
	inbox := storeInbox(t, shop)
	now := at("2026-11-27", 10, 0)
	subject := uniqueSubject("coupon_redemption")
	if !emitEvent(t, shop, inbox, now, storeai.EventFollowUpDue, "same-source", subject, `{"n":1}`) {
		t.Fatal("first emit should insert")
	}
	if emitEvent(t, shop, inbox, now, storeai.EventFollowUpDue, "same-source", subject, `{"n":2}`) {
		t.Fatal("second emit should not insert")
	}
	events := allEvents(t, shop, inbox.ID)
	if len(events) != 1 || events[0].SourceKey != "same-source" || string(events[0].Payload) != `{"n": 1}` {
		t.Fatalf("events = %+v", events)
	}
}

func TestEmitRejectsInvalidInput(t *testing.T) {
	shop := open(t)
	inbox := storeInbox(t, shop)
	now := at("2026-11-27", 10, 0)
	cases := []struct {
		name    string
		kind    string
		payload string
		want    error
	}{
		{"empty kind", "", `{}`, storeai.ErrInvalidEvent},
		{"array payload", storeai.EventRequest, `[]`, storeai.ErrInvalidPayload},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
				_, err := storeai.Emit(context.Background(), q, now, storeai.EmitInput{
					TenantID: shop.Res.TenantID, StoreID: shop.Res.StoreID, InboxID: inbox.ID,
					Kind: tc.kind, SourceKey: source("bad"), Payload: []byte(tc.payload),
				})
				return err
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("Emit = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestEnsureInboxConcurrentOneRow(t *testing.T) {
	shop := open(t)
	staff, _ := shop.Hire("小李", "002", "li01", shop.Role("发型师"))
	const workers = 6
	var wg sync.WaitGroup
	ids := make(chan uuid.UUID, workers)
	errs := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var inbox dbgen.AiInbox
			err := db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
				var err error
				inbox, err = storeai.EnsureInbox(context.Background(), q, shop.Res.StoreID, &staff)
				return err
			})
			if err != nil {
				errs <- err
				return
			}
			ids <- inbox.ID
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	seen := map[uuid.UUID]bool{}
	for id := range ids {
		seen[id] = true
	}
	if len(seen) != 1 {
		t.Fatalf("inbox ids = %v", seen)
	}
	tx := dbtest.AppTx(t, shop.Pool, shop.Res.TenantID)
	var count int
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM ai_inboxes WHERE staff_id=$1", staff).Scan(&count); err != nil {
		dbtest.Rollback(t, tx)
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	if count != 1 {
		t.Fatalf("inbox rows = %d", count)
	}
}
