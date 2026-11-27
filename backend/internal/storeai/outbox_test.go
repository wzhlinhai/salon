package storeai_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/storeai"
)

func putReminder(t *testing.T, shop *optest.Shop, now time.Time, in storeai.PutReminderInput) dbgen.AiOutbox {
	t.Helper()
	var row dbgen.AiOutbox
	err := db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		var err error
		row, err = storeai.PutReminder(context.Background(), q, now, in)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func putReminderErr(shop *optest.Shop, now time.Time, in storeai.PutReminderInput) error {
	return db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		_, err := storeai.PutReminder(context.Background(), q, now, in)
		return err
	})
}

func withdrawRemindersErr(shop *optest.Shop, now time.Time, storeID uuid.UUID, subject storeai.Subject) error {
	return db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		return storeai.WithdrawReminders(context.Background(), q, now, storeID, subject)
	})
}

func TestPutReminderReplacesQueuedThenThreadsSent(t *testing.T) {
	shop := open(t)
	staff, _ := shop.Hire("小李", "002", "li01", shop.Role("发型师"))
	now := at("2026-11-27", 10, 0)
	subject := uniqueSubject("coupon_redemption")
	input := storeai.PutReminderInput{StoreID: shop.Res.StoreID, RecipientID: staff, Tier: "now", Subject: subject, Body: "第一次"}
	parent := putReminder(t, shop, now, input)
	input.Tier, input.Body = "daily", "替换"
	replaced := putReminder(t, shop, now.Add(time.Minute), input)
	rows := outboxRows(t, shop, staff)
	if len(rows) != 1 || rows[0].ID != parent.ID || replaced.ID != parent.ID || rows[0].Body != "替换" || rows[0].Tier != "daily" {
		t.Fatalf("replaced rows = %+v", rows)
	}
	if !rows[0].QueuedAt.Time.Equal(now.Add(time.Minute)) {
		t.Fatalf("queued_at = %s", rows[0].QueuedAt.Time)
	}
	input.Tier, input.Body = "now", "第三次"
	putReminder(t, shop, now.Add(2*time.Minute), input)
	runOnce(t, shop, now.Add(3*time.Minute), noRun)
	rows = outboxRows(t, shop, staff)
	if len(rows) != 1 || rows[0].Status != "sent" || !rows[0].SentAt.Valid {
		t.Fatalf("sent rows = %+v", rows)
	}
	input.Body = "接着这条"
	child := putReminder(t, shop, now.Add(4*time.Minute), input)
	rows = outboxRows(t, shop, staff)
	if len(rows) != 2 || child.ParentID == nil || *child.ParentID != parent.ID || rows[0].Status != "sent" || rows[1].Status != "queued" {
		t.Fatalf("thread rows = %+v", rows)
	}
}

func TestDispatcherOnlySendsNow(t *testing.T) {
	shop := open(t)
	staff, _ := shop.Hire("小李", "002", "li01", shop.Role("发型师"))
	now := at("2026-11-27", 10, 0)
	statuses := map[string]string{}
	for _, tier := range []string{"now", "daily", "weekly"} {
		subject := uniqueSubject("coupon_redemption")
		statuses[subject.ID.String()] = tier
		putReminder(t, shop, now, storeai.PutReminderInput{
			StoreID: shop.Res.StoreID, RecipientID: staff, Tier: tier, Subject: subject, Body: tier,
		})
	}
	runOnce(t, shop, now, noRun)
	rows := outboxRows(t, shop, staff)
	if len(rows) != 3 {
		t.Fatalf("rows = %+v", rows)
	}
	for _, row := range rows {
		want := "queued"
		if statuses[row.SubjectID.String()] == "now" {
			want = "sent"
		}
		if row.Status != want {
			t.Fatalf("tier %s status = %s, want %s", statuses[row.SubjectID.String()], row.Status, want)
		}
	}
}

func TestWithdrawRemindersAllRows(t *testing.T) {
	shop := open(t)
	staff, _ := shop.Hire("小李", "002", "li01", shop.Role("发型师"))
	now := at("2026-11-27", 10, 0)
	subject := uniqueSubject("coupon_redemption")
	putReminder(t, shop, now, storeai.PutReminderInput{StoreID: shop.Res.StoreID, RecipientID: staff, Tier: "now", Subject: subject, Body: "父"})
	runOnce(t, shop, now, noRun)
	putReminder(t, shop, now.Add(time.Minute), storeai.PutReminderInput{StoreID: shop.Res.StoreID, RecipientID: staff, Tier: "now", Subject: subject, Body: "子"})
	if err := db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		return storeai.WithdrawReminders(context.Background(), q, now.Add(2*time.Minute), shop.Res.StoreID, subject)
	}); err != nil {
		t.Fatal(err)
	}
	rows := outboxRows(t, shop, staff)
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	for _, row := range rows {
		if row.Status != "withdrawn" || !row.WithdrawnAt.Valid {
			t.Fatalf("row = %+v", row)
		}
	}
}

func TestPutReminderRejectsInactiveRecipient(t *testing.T) {
	shop := open(t)
	staff, _ := shop.Hire("小李", "002", "li01", shop.Role("发型师"))
	shop.Call(t, shop.Owner, "deactivate_staff", map[string]any{"staff_id": staff}, 200)
	err := putReminderErr(shop, at("2026-11-27", 10, 0), storeai.PutReminderInput{
		StoreID: shop.Res.StoreID, RecipientID: staff, Tier: "now", Subject: uniqueSubject("coupon_redemption"), Body: "提醒",
	})
	if !errors.Is(err, storeai.ErrRecipientNotFound) {
		t.Fatalf("inactive recipient = %v", err)
	}
}

func TestPutReminderAdvisoryLock(t *testing.T) {
	shop := open(t)
	staff, _ := shop.Hire("小李", "002", "li01", shop.Role("发型师"))
	now := at("2026-11-27", 10, 0)
	subject := uniqueSubject("coupon_redemption")
	lock := dbtest.AppTx(t, shop.Pool, shop.Res.TenantID)
	key := fmt.Sprintf("%s/ai_outbox/%s/%s", shop.Res.StoreID, subject.Kind, subject.ID)
	dbtest.Exec(t, lock, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", key)
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results <- putReminderErr(shop, now, storeai.PutReminderInput{
				StoreID: shop.Res.StoreID, RecipientID: staff, Tier: "now", Subject: subject, Body: fmt.Sprintf("提醒%d", i),
			})
		}(i)
	}
	waitLockWaiters(t, shop, 2)
	dbtest.Rollback(t, lock)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if rows := outboxRows(t, shop, staff); len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestPutReminderDispatchRaceCreatesChild(t *testing.T) {
	shop := open(t)
	staff, _ := shop.Hire("小李", "002", "li01", shop.Role("发型师"))
	now := at("2026-11-27", 10, 0)
	subject := uniqueSubject("coupon_redemption")
	parent := putReminder(t, shop, now, storeai.PutReminderInput{
		StoreID: shop.Res.StoreID, RecipientID: staff, Tier: "now", Subject: subject, Body: "第一次",
	})
	lock := dbtest.AppTx(t, shop.Pool, shop.Res.TenantID)
	dbtest.Exec(t, lock, "SELECT 1 FROM ai_outbox WHERE id=$1 FOR UPDATE", parent.ID)
	done := make(chan error, 1)
	go func() {
		done <- putReminderErr(shop, now.Add(time.Minute), storeai.PutReminderInput{
			StoreID: shop.Res.StoreID, RecipientID: staff, Tier: "now", Subject: subject, Body: "第二次",
		})
	}()
	dbtest.WaitForLockWait(t, shop.Pool)
	dbtest.Exec(t, lock, "UPDATE ai_outbox SET status='sent', sent_at=$1 WHERE id=$2", now.Add(time.Minute), parent.ID)
	dbtest.Commit(t, lock)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	rows := outboxRows(t, shop, staff)
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].ID != parent.ID || rows[0].Status != "sent" || rows[0].Body != "第一次" {
		t.Fatalf("parent = %+v", rows[0])
	}
	if rows[1].ParentID == nil || *rows[1].ParentID != parent.ID || rows[1].Status != "queued" || rows[1].Body != "第二次" {
		t.Fatalf("child = %+v", rows[1])
	}
}

func TestPutAndWithdrawSerialize(t *testing.T) {
	shop := open(t)
	staff, _ := shop.Hire("小李", "002", "li01", shop.Role("发型师"))
	now := at("2026-11-27", 10, 0)
	subject := uniqueSubject("coupon_redemption")
	putReminder(t, shop, now, storeai.PutReminderInput{
		StoreID: shop.Res.StoreID, RecipientID: staff, Tier: "now", Subject: subject, Body: "父",
	})
	runOnce(t, shop, now, noRun)
	lock := dbtest.AppTx(t, shop.Pool, shop.Res.TenantID)
	dbtest.Exec(t, lock, "SELECT 1 FROM staff WHERE id=$1 FOR UPDATE", staff)
	putDone := make(chan error, 1)
	withdrawDone := make(chan error, 1)
	go func() {
		putDone <- putReminderErr(shop, now.Add(time.Minute), storeai.PutReminderInput{
			StoreID: shop.Res.StoreID, RecipientID: staff, Tier: "now", Subject: subject, Body: "新内容",
		})
	}()
	waitLockWaiters(t, shop, 1)
	go func() {
		withdrawDone <- withdrawRemindersErr(shop, now.Add(time.Minute), shop.Res.StoreID, subject)
	}()
	waitLockWaiters(t, shop, 2)
	dbtest.Rollback(t, lock)
	if err := <-putDone; err != nil {
		t.Fatal(err)
	}
	if err := <-withdrawDone; err != nil {
		t.Fatal(err)
	}
	rows := outboxRows(t, shop, staff)
	if len(rows) == 0 {
		t.Fatal("no rows after withdraw")
	}
	for _, row := range rows {
		if row.Status != "withdrawn" {
			t.Fatalf("row left after withdraw: %+v", row)
		}
	}
}
