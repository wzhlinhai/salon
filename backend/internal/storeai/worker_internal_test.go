package storeai

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

type internalShop struct {
	pool *pgxpool.Pool
	res  staff.BootstrapResult
}

func openInternalShop(t *testing.T) *internalShop {
	t.Helper()
	pool := dbtest.NewPool(t)
	res, err := staff.Bootstrap(context.Background(), pool, staff.BootstrapInput{
		TenantName: "本店", StoreName: "本店", OwnerName: "王老板", OwnerNumber: "001",
		OwnerAccount: "boss", OwnerPassword: "owner-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	return &internalShop{pool: pool, res: res}
}

func internalTime(day string, hour, minute int) time.Time {
	d, err := time.ParseInLocation(settings.DateLayout, day, settings.Location)
	if err != nil {
		panic(err)
	}
	return time.Date(d.Year(), d.Month(), d.Day(), hour, minute, 0, 0, settings.Location)
}

func TestSortEventsOrder(t *testing.T) {
	t0 := internalTime("2026-11-27", 10, 0)
	events := []dbgen.AiInboxEvent{
		{ID: uuid.New(), CreatedAt: pgtype.Timestamptz{Time: t0.Add(2 * time.Second), Valid: true}},
		{ID: uuid.New(), CreatedAt: pgtype.Timestamptz{Time: t0, Valid: true}},
		{ID: uuid.New(), CreatedAt: pgtype.Timestamptz{Time: t0.Add(time.Second), Valid: true}},
	}
	sortEvents(events)
	for i := 1; i < len(events); i++ {
		if events[i].CreatedAt.Time.Before(events[i-1].CreatedAt.Time) {
			t.Fatalf("events not sorted: %+v", events)
		}
	}
}

func TestCrashedAttemptsReachFailed(t *testing.T) {
	shop := openInternalShop(t)
	ctx := context.Background()
	now := internalTime("2026-11-27", 10, 0)
	var inbox dbgen.AiInbox
	err := db.InTenant(ctx, shop.pool, shop.res.TenantID, func(q *dbgen.Queries) error {
		var err error
		inbox, err = EnsureInbox(ctx, q, shop.res.StoreID, nil)
		if err != nil {
			return err
		}
		_, err = Emit(ctx, q, now, EmitInput{
			TenantID: shop.res.TenantID, StoreID: shop.res.StoreID, InboxID: inbox.ID,
			Kind: EventRequest, SourceKey: "crash", Payload: []byte(`{}`),
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		_, events, ok, err := claimInbox(ctx, shop.pool, shop.res.TenantID, shop.res.StoreID, inbox.ID, uuid.New(), now)
		if err != nil || !ok || len(events) != 1 {
			t.Fatalf("claim = ok %v events %d err %v", ok, len(events), err)
		}
		tx := dbtest.AppTx(t, shop.pool, shop.res.TenantID)
		dbtest.Exec(t, tx, "UPDATE ai_inboxes SET lease_until=$1 WHERE id=$2", now.Add(-time.Minute), inbox.ID)
		dbtest.Commit(t, tx)
	}
	_, events, ok, err := claimInbox(ctx, shop.pool, shop.res.TenantID, shop.res.StoreID, inbox.ID, uuid.New(), now)
	if err != nil || !ok || len(events) != 0 {
		t.Fatalf("fourth claim = ok %v events %d err %v", ok, len(events), err)
	}
	var listed []dbgen.AiInboxEvent
	err = db.InTenant(ctx, shop.pool, shop.res.TenantID, func(q *dbgen.Queries) error {
		var err error
		listed, err = q.ListInboxEvents(ctx, inbox.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Status != "failed" || listed[0].Attempts != 3 || listed[0].LastError == nil || *listed[0].LastError != "多次运行中断" {
		t.Fatalf("event = %+v", listed)
	}
	calls := 0
	RunOnce(ctx, shop.pool, func() time.Time { return now }, func(context.Context, RunInput) (RunResult, error) {
		calls++
		return RunResult{}, nil
	}, slog.New(slog.DiscardHandler))
	if calls != 0 {
		t.Fatalf("RunFunc called %d times after attempts exhausted", calls)
	}
}

func TestMarkReadWithdrawRace(t *testing.T) {
	shop := openInternalShop(t)
	ctx := context.Background()
	now := internalTime("2026-11-27", 10, 0)
	tx := dbtest.AppTx(t, shop.pool, shop.res.TenantID)
	var parent uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO ai_outbox (tenant_id,store_id,recipient_id,tier,subject_kind,subject_id,body,status,queued_at,sent_at)
		VALUES ($1,$2,$3,'now','coupon_redemption',$4,'提醒','sent',$5,$5) RETURNING id`,
		shop.res.TenantID, shop.res.StoreID, shop.res.OwnerID, uuid.New(), now).Scan(&parent)
	if err != nil {
		dbtest.Rollback(t, tx)
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)
	tx = dbtest.AppTx(t, shop.pool, shop.res.TenantID)
	dbtest.Exec(t, tx, "UPDATE ai_outbox SET status='withdrawn', withdrawn_at=$1 WHERE id=$2", now, parent)
	done := make(chan error, 1)
	go func() {
		done <- db.InTenant(ctx, shop.pool, shop.res.TenantID, func(q *dbgen.Queries) error {
			_, err := markMyReminderRead(ctx, ops.Env{
				Now: now, Caller: ops.Caller{TenantID: shop.res.TenantID, StoreID: shop.res.StoreID, StaffID: shop.res.OwnerID}, Q: q,
			}, &MarkMyReminderReadInput{ID: parent})
			return err
		})
	}()
	dbtest.WaitForLockWait(t, shop.pool)
	dbtest.Commit(t, tx)
	if err := <-done; !errors.Is(err, ops.ErrNotFound) {
		t.Fatalf("mark read during withdraw = %v", err)
	}
}
