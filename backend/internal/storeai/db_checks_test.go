package storeai_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/storeai"
)

func expectSQLError(t *testing.T, tx pgx.Tx, code string, sql string, args ...any) {
	t.Helper()
	_, err := tx.Exec(context.Background(), sql, args...)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != code {
		t.Fatalf("SQL error = %v, want SQLSTATE %s", err, code)
	}
}

func TestDatabaseChecks(t *testing.T) {
	shop := open(t)
	inbox := storeInbox(t, shop)
	now := at("2026-11-27", 10, 0)
	subject := uniqueSubject("coupon_redemption")
	cases := []struct {
		name string
		sql  string
		args []any
	}{
		{
			name: "outbox body too long",
			sql: `INSERT INTO ai_outbox (tenant_id,store_id,recipient_id,tier,subject_kind,subject_id,body,status,queued_at)
			      VALUES ($1,$2,$3,'now','coupon_redemption',$4,repeat('a',201),'queued',$5)`,
			args: []any{shop.Res.TenantID, shop.Res.StoreID, shop.Res.OwnerID, subject.ID, now},
		},
		{
			name: "memory path",
			sql:  `INSERT INTO ai_memory_files (tenant_id,store_id,inbox_id,path,content,updated_at) VALUES ($1,$2,$3,'foo.md','x',$4)`,
			args: []any{shop.Res.TenantID, shop.Res.StoreID, inbox.ID, now},
		},
		{
			name: "memory size",
			sql:  `INSERT INTO ai_memory_files (tenant_id,store_id,inbox_id,path,content,updated_at) VALUES ($1,$2,$3,'notes/big.md',repeat('a',16385),$4)`,
			args: []any{shop.Res.TenantID, shop.Res.StoreID, inbox.ID, now},
		},
		{
			name: "event subject pair",
			sql: `INSERT INTO ai_inbox_events (tenant_id,store_id,inbox_id,kind,source_key,subject_kind,payload,status,attempts,not_before,created_at)
			      VALUES ($1,$2,$3,'request','bad-subject','coupon_redemption','{}'::jsonb,'pending',0,$4,$4)`,
			args: []any{shop.Res.TenantID, shop.Res.StoreID, inbox.ID, now},
		},
		{
			name: "follow-up needs subject",
			sql:  `INSERT INTO ai_timers (tenant_id,store_id,inbox_id,kind,due_at,status,created_at) VALUES ($1,$2,$3,'follow_up',$4,'scheduled',$4)`,
			args: []any{shop.Res.TenantID, shop.Res.StoreID, inbox.ID, now},
		},
		{
			name: "request status",
			sql: `INSERT INTO ai_requests (tenant_id,store_id,to_staff_id,kind,subject_kind,subject_id,options,status,created_at)
			      VALUES ($1,$2,$3,'ask','coupon_redemption',$4,'[]'::jsonb,'bogus',$5)`,
			args: []any{shop.Res.TenantID, shop.Res.StoreID, shop.Res.OwnerID, subject.ID, now},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx := dbtest.AppTx(t, shop.Pool, shop.Res.TenantID)
			expectSQLError(t, tx, "23514", tc.sql, tc.args...)
			dbtest.Rollback(t, tx)
		})
	}
}

func TestUniqueIndexes(t *testing.T) {
	shop := open(t)
	inbox := storeInbox(t, shop)
	staffInbox(t, shop, shop.Res.OwnerID)
	now := at("2026-11-27", 10, 0)
	subject := uniqueSubject("coupon_redemption")

	t.Run("store inbox", func(t *testing.T) {
		tx := dbtest.AppTx(t, shop.Pool, shop.Res.TenantID)
		expectSQLError(t, tx, "23505", `INSERT INTO ai_inboxes (tenant_id,store_id) VALUES ($1,$2)`, shop.Res.TenantID, shop.Res.StoreID)
		dbtest.Rollback(t, tx)
	})
	t.Run("staff inbox", func(t *testing.T) {
		tx := dbtest.AppTx(t, shop.Pool, shop.Res.TenantID)
		expectSQLError(t, tx, "23505", `INSERT INTO ai_inboxes (tenant_id,store_id,staff_id) VALUES ($1,$2,$3)`, shop.Res.TenantID, shop.Res.StoreID, shop.Res.OwnerID)
		dbtest.Rollback(t, tx)
	})
	t.Run("events source key", func(t *testing.T) {
		tx := dbtest.AppTx(t, shop.Pool, shop.Res.TenantID)
		dbtest.Exec(t, tx, `INSERT INTO ai_inbox_events (tenant_id,store_id,inbox_id,kind,source_key,payload,status,attempts,not_before,created_at)
			VALUES ($1,$2,$3,'request','dup','{}'::jsonb,'pending',0,$4,$4)`, shop.Res.TenantID, shop.Res.StoreID, inbox.ID, now)
		expectSQLError(t, tx, "23505", `INSERT INTO ai_inbox_events (tenant_id,store_id,inbox_id,kind,source_key,payload,status,attempts,not_before,created_at)
			VALUES ($1,$2,$3,'request','dup','{}'::jsonb,'pending',0,$4,$4)`, shop.Res.TenantID, shop.Res.StoreID, inbox.ID, now)
		dbtest.Rollback(t, tx)
	})
	t.Run("session open", func(t *testing.T) {
		tx := dbtest.AppTx(t, shop.Pool, shop.Res.TenantID)
		dbtest.Exec(t, tx, `INSERT INTO ai_sessions (tenant_id,store_id,inbox_id,subject_kind,subject_id,status,created_at,updated_at)
			VALUES ($1,$2,$3,'coupon_redemption',$4,'active',$5,$5)`, shop.Res.TenantID, shop.Res.StoreID, inbox.ID, subject.ID, now)
		dbtest.Exec(t, tx, "SAVEPOINT duplicate")
		expectSQLError(t, tx, "23505", `INSERT INTO ai_sessions (tenant_id,store_id,inbox_id,subject_kind,subject_id,status,created_at,updated_at)
			VALUES ($1,$2,$3,'coupon_redemption',$4,'active',$5,$5)`, shop.Res.TenantID, shop.Res.StoreID, inbox.ID, subject.ID, now)
		dbtest.Exec(t, tx, "ROLLBACK TO SAVEPOINT duplicate")
		dbtest.Exec(t, tx, `UPDATE ai_sessions SET status='closed' WHERE inbox_id=$1 AND subject_id=$2`, inbox.ID, subject.ID)
		dbtest.Exec(t, tx, `INSERT INTO ai_sessions (tenant_id,store_id,inbox_id,subject_kind,subject_id,status,created_at,updated_at)
			VALUES ($1,$2,$3,'coupon_redemption',$4,'active',$5,$5)`, shop.Res.TenantID, shop.Res.StoreID, inbox.ID, subject.ID, now)
		dbtest.Rollback(t, tx)
	})
	t.Run("timer follow-up", func(t *testing.T) {
		tx := dbtest.AppTx(t, shop.Pool, shop.Res.TenantID)
		dbtest.Exec(t, tx, `INSERT INTO ai_timers (tenant_id,store_id,inbox_id,kind,subject_kind,subject_id,due_at,status,created_at)
			VALUES ($1,$2,$3,'follow_up','coupon_redemption',$4,$5,'scheduled',$5)`, shop.Res.TenantID, shop.Res.StoreID, inbox.ID, subject.ID, now)
		expectSQLError(t, tx, "23505", `INSERT INTO ai_timers (tenant_id,store_id,inbox_id,kind,subject_kind,subject_id,due_at,status,created_at)
			VALUES ($1,$2,$3,'follow_up','coupon_redemption',$4,$5,'scheduled',$5)`, shop.Res.TenantID, shop.Res.StoreID, inbox.ID, subject.ID, now)
		dbtest.Rollback(t, tx)
	})
	t.Run("timer periodic", func(t *testing.T) {
		tx := dbtest.AppTx(t, shop.Pool, shop.Res.TenantID)
		dbtest.Exec(t, tx, `INSERT INTO ai_timers (tenant_id,store_id,inbox_id,kind,periodic_key,repeat,due_at,status,created_at)
			VALUES ($1,$2,$3,'periodic','daily','daily',$4,'scheduled',$4)`, shop.Res.TenantID, shop.Res.StoreID, inbox.ID, now)
		expectSQLError(t, tx, "23505", `INSERT INTO ai_timers (tenant_id,store_id,inbox_id,kind,periodic_key,repeat,due_at,status,created_at)
			VALUES ($1,$2,$3,'periodic','daily','daily',$4,'scheduled',$4)`, shop.Res.TenantID, shop.Res.StoreID, inbox.ID, now)
		dbtest.Rollback(t, tx)
	})
}

func TestSchedulerSkipsLockedTimer(t *testing.T) {
	shop := open(t)
	inbox := storeInbox(t, shop)
	now := at("2026-11-27", 10, 0)
	var timer dbgen.AiTimer
	err := db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		var err error
		timer, err = storeai.ScheduleFollowUp(context.Background(), q, now.Add(-time.Minute), inbox, uniqueSubject("coupon_redemption"), now, []byte(`{}`))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	lock := dbtest.AppTx(t, shop.Pool, shop.Res.TenantID)
	dbtest.Exec(t, lock, "SELECT 1 FROM ai_timers WHERE id=$1 FOR UPDATE", timer.ID)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runOnce(t, shop, now, noRun)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler blocked on a locked timer; SKIP LOCKED missing")
	}
	dbtest.Rollback(t, lock)
	if events := allEvents(t, shop, inbox.ID); len(events) != 0 {
		t.Fatalf("locked timer emitted: %+v", events)
	}
}

func TestOutboxParentColumnNotUpdatable(t *testing.T) {
	shop := open(t)
	staff, _ := shop.Hire("小李", "002", "li01", shop.Role("发型师"))
	now := at("2026-11-27", 10, 0)
	subject := uniqueSubject("coupon_redemption")
	parent := insertOutbox(t, shop, staff, "now", subject, "父", "sent", nil, &now, nil, nil)
	child := insertOutbox(t, shop, staff, "now", subject, "子", "queued", &parent, nil, nil, nil)
	tx := dbtest.AppTx(t, shop.Pool, shop.Res.TenantID)
	_, err := tx.Exec(context.Background(), "UPDATE ai_outbox SET parent_id=$1 WHERE id=$2", parent, child)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		dbtest.Rollback(t, tx)
		t.Fatalf("update parent_id = %v, want privilege error", err)
	}
	dbtest.Rollback(t, tx)
}
