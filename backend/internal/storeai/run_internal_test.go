package storeai

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

func TestToolErrorMessages(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{ErrRecipientNotFound, "收件人不是本店在职员工"},
		{ErrInvalidRequest, "交办参数不合法"},
		{ErrInvalidAnswer, "参数不合法"},
		{ErrInvalidReminder, "参数不合法"},
		{ErrRequestNotFound, "交办不在本店或不存在"},
		{ErrRequestClosed, "交办已经结束"},
		{ErrNoHandoverTarget, "没有可改交的在职员工"},
		{ErrFollowUpDue, "跟进时间要晚于现在"},
		{ErrInvalidEvent, "对象标识不合法"},
	}
	for _, tc := range cases {
		err := toolStoreError(tc.err)
		if err == nil || err.Error() != tc.want {
			t.Fatalf("toolStoreError(%v) = %v, want %s", tc.err, err, tc.want)
		}
	}
	if got := toolStoreError(errors.New("database down")); got == nil || got.Error() != "database down" {
		t.Fatalf("unknown error = %v", got)
	}
}

func TestToolSubjectValidation(t *testing.T) {
	if _, err := toolSubject("", uuid.Nil); err == nil || err.Error() != "对象要同时给出类别和编号" {
		t.Fatalf("empty subject = %v", err)
	}
	if err := subjectInStore(context.Background(), nil, uuid.New(), Subject{Kind: "other", ID: uuid.New()}); err == nil || err.Error() != "不认识的对象" {
		t.Fatalf("unknown subject kind = %v", err)
	}
}

func TestExecuteValidationMessages(t *testing.T) {
	ctx := context.Background()
	id := uuid.New()
	registry := &ops.Registry{}
	store := &runContext{runner: &Runner{Registry: registry}, storeRun: true}
	employee := &runContext{runner: &Runner{Registry: registry}}
	cases := []struct {
		rc   *runContext
		name string
		args string
		want string
	}{
		{store, "finish", `{"status":"bogus"}`, "finish 的 status 只能是 closed 或 waiting"},
		{store, "schedule_follow_up", `{"subject_kind":"coupon_redemption","subject_id":"` + id.String() + `","minutes":4}`, "跟进分钟须在 5 到 1440 之间"},
		{store, "create_request", `{"subject_kind":"coupon_redemption","subject_id":"` + id.String() + `","kind":"inform"}`, "要指定收件人"},
		{store, "bogus", `{}`, "工具不存在或不可用：bogus"},
		{employee, "remind_me", `{"tier":"later","subject_kind":"coupon_redemption","subject_id":"` + id.String() + `","body":"提醒"}`, "提醒档位只能是 now、daily 或 weekly"},
		{employee, "remind_me", `{"tier":"now","subject_kind":"coupon_redemption","subject_id":"` + id.String() + `","body":""}`, "提醒正文须为 1 到 200 字"},
	}
	for _, tc := range cases {
		_, err := tc.rc.execute(ctx, tc.name, tc.args)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s = %v, want %s", tc.name, err, tc.want)
		}
	}
}

func TestFinishWaitingForLimit(t *testing.T) {
	rc := &runContext{}
	if _, err := rc.finish(`{"status":"waiting","waiting_for":"` + strings.Repeat("字", 101) + `"}`); err == nil || err.Error() != "waiting_for 不能超过 100 字" {
		t.Fatalf("finish = %v", err)
	}
}

func TestSubjectLockAndStatus(t *testing.T) {
	shop := openInternalShop(t)
	pool, res := shop.pool, shop.res
	now := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	key := uuid.NewString()
	var id uuid.UUID
	err := db.InTenant(context.Background(), pool, res.TenantID, func(q *dbgen.Queries) error {
		if _, err := q.CreateRedemption(context.Background(), dbgen.CreateRedemptionParams{
			TenantID: res.TenantID, StoreID: res.StoreID, Platform: "douyin",
			CouponCode: key, IdempotencyKey: key, RedeemedBy: res.OwnerID,
			CreatedAt: pgtype.Timestamptz{Time: now, Valid: true},
		}); err != nil {
			return err
		}
		row, err := q.GetRedemption(context.Background(), dbgen.GetRedemptionParams{StoreID: res.StoreID, IdempotencyKey: key})
		id = row.ID
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	tx := dbtest.AppTx(t, pool, res.TenantID)
	if _, err := dbgen.New(tx).LockRedemptionByID(context.Background(), dbgen.LockRedemptionByIDParams{StoreID: res.StoreID, ID: id}); err != nil {
		dbtest.Rollback(t, tx)
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- db.InTenant(context.Background(), pool, res.TenantID, func(q *dbgen.Queries) error {
			return subjectInStore(context.Background(), q, res.StoreID, Subject{Kind: SubjectCouponRedemption, ID: id})
		})
	}()
	waitForLockWaiter(t, pool)
	dbtest.Exec(t, tx, "UPDATE coupon_redemptions SET status='revoked', revoked_at=$1 WHERE store_id=$2 AND id=$3", now, res.StoreID, id)
	dbtest.Commit(t, tx)
	err = <-done
	if err == nil || err.Error() != "这张券已关联或已撤销" {
		t.Fatalf("subject = %v", err)
	}
}

func TestCancelRequestCancelsItsFollowUp(t *testing.T) {
	shop := openInternalShop(t)
	pool, res := shop.pool, shop.res
	now := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	staffID := res.OwnerID
	subject := Subject{Kind: SubjectCouponRedemption, ID: uuid.New()}
	var request dbgen.AiRequest
	var inbox dbgen.AiInbox
	err := db.InTenant(context.Background(), pool, res.TenantID, func(q *dbgen.Queries) error {
		var err error
		inbox, err = EnsureInbox(context.Background(), q, res.StoreID, nil)
		if err != nil {
			return err
		}
		request, err = CreateRequest(context.Background(), q, now, CreateRequestInput{StoreID: res.StoreID, ToStaffID: staffID, Kind: "inform", Subject: subject})
		if err != nil {
			return err
		}
		payload, err := json.Marshal(map[string]any{"request_id": request.ID})
		if err != nil {
			return err
		}
		_, err = ScheduleFollowUp(context.Background(), q, now, inbox, subject, now.Add(30*time.Minute), payload)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	rc := &runContext{
		runner: &Runner{}, storeRun: true,
		in: RunInput{StoreID: res.StoreID, Now: now, Inbox: inbox, Tx: func(ctx context.Context, fn func(*dbgen.Queries) error) error {
			return db.InTenant(ctx, pool, res.TenantID, fn)
		}},
	}
	raw, err := json.Marshal(map[string]any{"request_id": request.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rc.execute(context.Background(), "cancel_request", string(raw)); err != nil {
		t.Fatal(err)
	}
	var cancelled dbgen.AiRequest
	var timer dbgen.AiTimer
	err = db.InTenant(context.Background(), pool, res.TenantID, func(q *dbgen.Queries) error {
		var err error
		cancelled, err = q.GetRequest(context.Background(), request.ID)
		if err != nil {
			return err
		}
		timers, err := q.ListTimers(context.Background(), inbox.ID)
		if err != nil {
			return err
		}
		timer = timers[0]
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != "cancelled" || timer.Status != "cancelled" {
		t.Fatalf("request = %s timer = %s", cancelled.Status, timer.Status)
	}
}

func waitForLockWaiter(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		waiting, err := dbtest.LockWaits(ctx, pool)
		if err != nil {
			t.Fatal(err)
		}
		if waiting >= 1 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("expected a lock waiter")
		case <-ticker.C:
		}
	}
}

func TestIsManagerOrOwnerRequiresActive(t *testing.T) {
	staffID, roleID := uuid.New(), uuid.New()
	store := &handoverStore{
		staff:      map[uuid.UUID]dbgen.Staff{staffID: {ID: staffID, Active: false}},
		roles:      map[uuid.UUID]dbgen.Role{roleID: {ID: roleID, Name: "店长"}},
		staffRoles: map[uuid.UUID][]uuid.UUID{staffID: {roleID}},
	}
	if store.isManagerOrOwner(staffID) {
		t.Fatal("inactive manager treated as manager or owner")
	}
}
