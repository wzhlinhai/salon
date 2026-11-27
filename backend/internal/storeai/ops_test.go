package storeai_test

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
)

func TestMyRemindersListAndMarkRead(t *testing.T) {
	shop := open(t)
	staffA, tokenA := shop.Hire("小李", "002", "li01", shop.Role("发型师"))
	staffB, _ := shop.Hire("小王", "003", "wang01", shop.Role("发型师"))
	t1, t2, t3, t4 := at("2026-11-27", 10, 0), at("2026-11-27", 10, 5), at("2026-11-27", 10, 10), at("2026-11-27", 10, 15)
	subject1, subject2 := uniqueSubject("coupon_redemption"), uniqueSubject("coupon_redemption")
	parent1 := insertOutbox(t, shop, staffA, "now", subject1, "父1", "sent", nil, &t1, nil, nil)
	child1 := insertOutbox(t, shop, staffA, "now", subject1, "子1", "sent", &parent1, &t2, nil, nil)
	parent2 := insertOutbox(t, shop, staffA, "daily", subject2, "父2", "sent", nil, &t3, nil, nil)
	withdrawn := insertOutbox(t, shop, staffA, "now", uniqueSubject("coupon_redemption"), "撤回", "withdrawn", nil, &t4, nil, &t4)
	queued := insertOutbox(t, shop, staffA, "now", uniqueSubject("coupon_redemption"), "未发", "queued", nil, nil, nil, nil)
	otherParent := insertOutbox(t, shop, staffB, "now", uniqueSubject("coupon_redemption"), "别人的", "sent", nil, &t4, nil, nil)

	result := shop.Call(t, tokenA, "list_my_reminders", map[string]any{}, 200)
	items := result.Body["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %v", items)
	}
	first := items[0].(map[string]any)
	second := items[1].(map[string]any)
	if first["id"] != parent2.String() || second["id"] != parent1.String() {
		t.Fatalf("order = %v %v", first["id"], second["id"])
	}
	replies := second["replies"].([]any)
	if len(replies) != 1 || replies[0].(map[string]any)["id"] != child1.String() {
		t.Fatalf("replies = %v", replies)
	}
	if result.Body["unread_count"].(float64) != 3 {
		t.Fatalf("unread = %v", result.Body["unread_count"])
	}

	shop.Call(t, tokenA, "mark_my_reminder_read", map[string]any{"id": parent1}, 200)
	shop.Call(t, tokenA, "mark_my_reminder_read", map[string]any{"id": parent1}, 200)
	rows := outboxRows(t, shop, staffA)
	read := map[uuid.UUID]bool{}
	for _, row := range rows {
		read[row.ID] = row.ReadAt.Valid
	}
	if !read[parent1] || !read[child1] || read[parent2] {
		t.Fatalf("read flags = %v", read)
	}

	for _, id := range []uuid.UUID{otherParent, withdrawn, queued, uuid.New()} {
		r := shop.HTTPWithToken(t, tokenA, "mark_my_reminder_read", map[string]any{"id": id})
		if r.Status != http.StatusNotFound || r.Detail() != "not found" {
			t.Fatalf("mark %s = %d %q", id, r.Status, r.Detail())
		}
	}
}

func TestMyReminderChannelsRejectAI(t *testing.T) {
	shop := open(t)
	caller := shop.MustCaller(shop.Owner)
	for _, channel := range shop.Channels() {
		r := channel.Call(t, caller, "list_my_reminders", map[string]any{})
		if channel.Name == "http" {
			if r.Status != http.StatusOK {
				t.Fatalf("http list = %d %v", r.Status, r.Body)
			}
			continue
		}
		if r.Status != http.StatusNotFound || !errors.Is(r.Err, ops.ErrUnknownOperation) {
			t.Fatalf("ai list = %d %v", r.Status, r.Err)
		}
	}
	subject := uniqueSubject("coupon_redemption")
	now := at("2026-11-27", 10, 0)
	parent := insertOutbox(t, shop, shop.Res.OwnerID, "now", subject, "提醒", "sent", nil, &now, nil, nil)
	r := shop.AI(t, caller, "mark_my_reminder_read", map[string]any{"id": parent})
	if r.Status != http.StatusNotFound || !errors.Is(r.Err, ops.ErrUnknownOperation) {
		t.Fatalf("ai mark = %d %v", r.Status, r.Err)
	}
}

func TestMyReminderListQueryCount(t *testing.T) {
	shop := open(t)
	now := at("2026-11-27", 10, 0)
	insertOutbox(t, shop, shop.Res.OwnerID, "now", uniqueSubject("coupon_redemption"), "父", "sent", nil, &now, nil, nil)
	counter := &queryCounter{}
	config := shop.Pool.Config().Copy()
	config.ConnConfig.Tracer = counter
	pool, err := db.NewPoolWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	app := optest.New(t, pool)
	caller := shop.MustCaller(shop.Owner)
	counts := []int64{}
	for _, extra := range []int{0, 9} {
		for i := range extra {
			subject := uniqueSubject("coupon_redemption")
			sent := now
			parent := insertOutbox(t, shop, shop.Res.OwnerID, "now", subject, "父", "sent", nil, &sent, nil, nil)
			for j := range 2 {
				insertOutbox(t, shop, shop.Res.OwnerID, "now", subject, "子", "sent", &parent, &sent, nil, nil)
				_ = i
				_ = j
			}
		}
		counter.all.Store(0)
		r := app.HTTP(t, &caller, "list_my_reminders", map[string]any{})
		if r.Status != http.StatusOK {
			t.Fatalf("list = %d %v", r.Status, r.Body)
		}
		counts = append(counts, counter.all.Load())
	}
	if counts[0] != counts[1] {
		t.Fatalf("query count grew with reminders: %v", counts)
	}
}

type queryCounter struct{ all atomic.Int64 }

func (c *queryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.all.Add(1)
	return ctx
}

func (*queryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
