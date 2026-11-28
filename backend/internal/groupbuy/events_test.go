package groupbuy_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/groupbuy"
	"github.com/wzhlinhai/salon/backend/internal/groupbuy/fakeprovider"
	"github.com/wzhlinhai/salon/backend/internal/orders"
	"github.com/wzhlinhai/salon/backend/internal/storeai"
)

func couponStoreInbox(t *testing.T, e *orderEnv) dbgen.AiInbox {
	t.Helper()
	var inbox dbgen.AiInbox
	err := db.InTenant(context.Background(), e.Pool, e.Res.TenantID, func(q *dbgen.Queries) error {
		var err error
		inbox, err = storeai.EnsureInbox(context.Background(), q, e.Res.StoreID, nil)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return inbox
}

func couponStoreEvents(t *testing.T, e *orderEnv) []dbgen.AiInboxEvent {
	t.Helper()
	inbox := couponStoreInbox(t, e)
	var events []dbgen.AiInboxEvent
	err := db.InTenant(context.Background(), e.Pool, e.Res.TenantID, func(q *dbgen.Queries) error {
		var err error
		events, err = q.ListInboxEvents(context.Background(), inbox.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func eventsOfKind(t *testing.T, e *orderEnv, kind string) []dbgen.AiInboxEvent {
	t.Helper()
	out := []dbgen.AiInboxEvent{}
	for _, event := range couponStoreEvents(t, e) {
		if event.Kind == kind {
			out = append(out, event)
		}
	}
	return out
}

func couponPayload(t *testing.T, event dbgen.AiInboxEvent) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func putCouponReminder(t *testing.T, e *orderEnv, recipient uuid.UUID, subject storeai.Subject) {
	t.Helper()
	err := db.InTenant(context.Background(), e.Pool, e.Res.TenantID, func(q *dbgen.Queries) error {
		_, err := storeai.PutReminder(context.Background(), q, e.Now(), storeai.PutReminderInput{
			StoreID: e.Res.StoreID, RecipientID: recipient, Tier: "now", Subject: subject, Body: "测试提醒",
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func scheduleCouponFollowUp(t *testing.T, e *orderEnv, subject storeai.Subject) {
	t.Helper()
	inbox := couponStoreInbox(t, e)
	err := db.InTenant(context.Background(), e.Pool, e.Res.TenantID, func(q *dbgen.Queries) error {
		_, err := storeai.ScheduleFollowUp(context.Background(), q, e.Now(), inbox, subject, e.Now().Add(30*time.Minute), json.RawMessage(`{}`))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func createCouponRequest(t *testing.T, e *orderEnv, subject storeai.Subject) dbgen.AiRequest {
	t.Helper()
	var request dbgen.AiRequest
	err := db.InTenant(context.Background(), e.Pool, e.Res.TenantID, func(q *dbgen.Queries) error {
		var err error
		request, err = storeai.CreateRequest(context.Background(), q, e.Now(), storeai.CreateRequestInput{
			StoreID: e.Res.StoreID, ToStaffID: e.staff, Kind: "inform", Subject: subject,
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func couponOutbox(t *testing.T, e *orderEnv, recipient uuid.UUID) []dbgen.AiOutbox {
	t.Helper()
	var rows []dbgen.AiOutbox
	err := db.InTenant(context.Background(), e.Pool, e.Res.TenantID, func(q *dbgen.Queries) error {
		var err error
		rows, err = q.ListOutboxForRecipient(context.Background(), dbgen.ListOutboxForRecipientParams{
			StoreID: e.Res.StoreID, RecipientID: recipient,
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func couponTimers(t *testing.T, e *orderEnv) []dbgen.AiTimer {
	t.Helper()
	var timers []dbgen.AiTimer
	err := db.InTenant(context.Background(), e.Pool, e.Res.TenantID, func(q *dbgen.Queries) error {
		var err error
		timers, err = q.ListTimers(context.Background(), couponStoreInbox(t, e).ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return timers
}

func couponRequest(t *testing.T, e *orderEnv, id uuid.UUID) dbgen.AiRequest {
	t.Helper()
	var request dbgen.AiRequest
	err := db.InTenant(context.Background(), e.Pool, e.Res.TenantID, func(q *dbgen.Queries) error {
		var err error
		request, err = q.GetRequest(context.Background(), id)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func TestRedeemEmitsCouponRedeemed(t *testing.T) {
	e := newOrderEnv(t)
	key := "redeem-event"
	r := e.Call(t, e.Owner, "redeem_coupon", groupbuy.CouponRequest{Platform: "douyin", CouponCode: key, Key: key}, 200)
	id := guid(r.Body["id"])
	events := eventsOfKind(t, e, storeai.EventCouponRedeemed)
	if len(events) != 1 {
		t.Fatalf("events = %+v", events)
	}
	event := events[0]
	if event.SourceKey != "coupon_redeemed:"+id.String() || event.SubjectKind == nil || *event.SubjectKind != storeai.SubjectCouponRedemption || *event.SubjectID != id {
		t.Fatalf("event = %+v", event)
	}
	payload := couponPayload(t, event)
	if payload["redemption_id"] != id.String() || payload["platform"] != "douyin" || payload["redeemed_by"] != e.Res.OwnerID.String() {
		t.Fatalf("payload = %v", payload)
	}
	redeemedAt, err := time.Parse(time.RFC3339Nano, payload["redeemed_at"].(string))
	if err != nil || !redeemedAt.Equal(instant) {
		t.Fatalf("redeemed_at = %v", payload["redeemed_at"])
	}
	until, err := time.Parse(time.RFC3339Nano, payload["revocable_until"].(string))
	if err != nil || !until.Equal(instant.Add(time.Hour)) {
		t.Fatalf("revocable_until = %v", payload["revocable_until"])
	}
	e.Call(t, e.Owner, "redeem_coupon", groupbuy.CouponRequest{Platform: "douyin", CouponCode: key, Key: key}, 200)
	if events := eventsOfKind(t, e, storeai.EventCouponRedeemed); len(events) != 1 {
		t.Fatalf("idempotent replay emitted: %+v", events)
	}
}

func TestRedeemFailureDoesNotEmit(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"failed", errors.New("provider refused")},
		{"unknown", groupbuy.ErrUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newOrderEnv(t)
			e.fake.Replies["redeem"] = []fakeprovider.Reply{{Err: tc.err}}
			key := "redeem-" + tc.name
			status := 409
			detail := "团购平台操作失败"
			if tc.err == groupbuy.ErrUnknown {
				detail = "结果未知，请稍后查询"
			}
			rejectionDetail(t, e.Call(t, e.Owner, "redeem_coupon", groupbuy.CouponRequest{Platform: "douyin", CouponCode: key, Key: key}, status), status, detail)
			if events := eventsOfKind(t, e, storeai.EventCouponRedeemed); len(events) != 0 {
				t.Fatalf("failed redeem emitted: %+v", events)
			}
		})
	}
}

func TestRedeemMeituanHasNoRevocableUntil(t *testing.T) {
	e := newOrderEnv(t)
	e.Groupbuy.Register("meituan", fakeprovider.New("meituan", instant))
	key := "meituan-event"
	e.Call(t, e.Owner, "redeem_coupon", groupbuy.CouponRequest{Platform: "meituan", CouponCode: key, Key: key}, 200)
	events := eventsOfKind(t, e, storeai.EventCouponRedeemed)
	if len(events) != 1 {
		t.Fatalf("events = %+v", events)
	}
	if payload := couponPayload(t, events[0]); payload["revocable_until"] != nil {
		t.Fatalf("revocable_until = %v", payload["revocable_until"])
	}
}

func TestLinkResolvesAndEmits(t *testing.T) {
	e := newOrderEnv(t)
	deal := e.deal("douyin", "关联券", "99.00", e.item("关联项目", "298.00"))
	c := e.coupon("link-event")
	order := e.order(e.item("履约", "398.00"))
	subject := storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: c.id}
	putCouponReminder(t, e, e.staff, subject)
	putCouponReminder(t, e, e.Res.OwnerID, subject)
	scheduleCouponFollowUp(t, e, subject)
	request := createCouponRequest(t, e, subject)
	if r := e.link(c.id, deal, order); r.Status != 200 {
		t.Fatal(r)
	}
	events := eventsOfKind(t, e, storeai.EventCouponLinked)
	if len(events) != 1 || !strings.HasPrefix(events[0].SourceKey, "coupon_linked:"+c.id.String()+":") {
		t.Fatalf("events = %+v", events)
	}
	if payload := couponPayload(t, events[0]); payload["redemption_id"] != c.id.String() || payload["order_id"] != order.String() {
		t.Fatalf("payload = %v", payload)
	}
	for _, recipient := range []uuid.UUID{e.staff, e.Res.OwnerID} {
		for _, row := range couponOutbox(t, e, recipient) {
			if row.SubjectID == c.id && row.Status != "withdrawn" {
				t.Fatalf("reminder not withdrawn: %+v", row)
			}
		}
	}
	for _, timer := range couponTimers(t, e) {
		if timer.SubjectID != nil && *timer.SubjectID == c.id && timer.Status != "cancelled" {
			t.Fatalf("timer not cancelled: %+v", timer)
		}
	}
	if got := couponRequest(t, e, request.ID); got.Status != "cancelled" {
		t.Fatalf("request = %+v", got)
	}
}

func TestLinkFailureLeavesThingsAlone(t *testing.T) {
	e := newOrderEnv(t)
	c := e.coupon("link-failure")
	subject := storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: c.id}
	putCouponReminder(t, e, e.staff, subject)
	scheduleCouponFollowUp(t, e, subject)
	request := createCouponRequest(t, e, subject)
	r := e.link(c.id, uuid.New(), e.order(e.item("履约", "100.00")))
	if r.Status == 200 {
		t.Fatal(r)
	}
	if events := eventsOfKind(t, e, storeai.EventCouponLinked); len(events) != 0 {
		t.Fatalf("failed link emitted: %+v", events)
	}
	for _, row := range couponOutbox(t, e, e.staff) {
		if row.SubjectID == c.id && row.Status != "queued" {
			t.Fatalf("reminder changed: %+v", row)
		}
	}
	for _, timer := range couponTimers(t, e) {
		if timer.SubjectID != nil && *timer.SubjectID == c.id && timer.Status != "scheduled" {
			t.Fatalf("timer changed: %+v", timer)
		}
	}
	if got := couponRequest(t, e, request.ID); got.Status != "open" {
		t.Fatalf("request changed: %+v", got)
	}
}

func TestUnlinkEmitsWithoutWithdrawing(t *testing.T) {
	e := newOrderEnv(t)
	deal := e.deal("douyin", "取消关联券", "99.00", e.item("取消关联项目", "298.00"))
	c := e.coupon("unlink-event")
	order := e.order(e.item("履约", "398.00"))
	if r := e.link(c.id, deal, order); r.Status != 200 {
		t.Fatal(r)
	}
	subject := storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: c.id}
	putCouponReminder(t, e, e.staff, subject)
	scheduleCouponFollowUp(t, e, subject)
	request := createCouponRequest(t, e, subject)
	e.Call(t, e.Owner, "unlink_coupon_order", groupbuy.UnlinkCouponOrderInput{RedemptionID: c.id}, 200)
	events := eventsOfKind(t, e, storeai.EventCouponUnlinked)
	if len(events) != 1 || !strings.HasPrefix(events[0].SourceKey, "coupon_unlinked:"+c.id.String()+":") {
		t.Fatalf("events = %+v", events)
	}
	found := false
	for _, row := range couponOutbox(t, e, e.staff) {
		if row.SubjectID == c.id {
			found = true
			if row.Status != "queued" {
				t.Fatalf("reminder withdrawn by unlink: %+v", row)
			}
		}
	}
	if !found {
		t.Fatal("reminder missing")
	}
	for _, timer := range couponTimers(t, e) {
		if timer.SubjectID != nil && *timer.SubjectID == c.id && timer.Status != "scheduled" {
			t.Fatalf("timer changed: %+v", timer)
		}
	}
	if got := couponRequest(t, e, request.ID); got.Status != "open" {
		t.Fatalf("request changed: %+v", got)
	}
}

func TestRevokeResolvesAndEmits(t *testing.T) {
	e := newOrderEnv(t)
	c := e.coupon("revoke-event")
	subject := storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: c.id}
	putCouponReminder(t, e, e.staff, subject)
	scheduleCouponFollowUp(t, e, subject)
	request := createCouponRequest(t, e, subject)
	e.Call(t, e.Owner, "revoke_coupon", groupbuy.LookupInput{Key: c.key}, 200)
	if events := eventsOfKind(t, e, storeai.EventCouponRevoked); len(events) != 1 || events[0].SourceKey != "coupon_revoked:"+c.id.String() {
		t.Fatalf("events = %+v", events)
	}
	for _, row := range couponOutbox(t, e, e.staff) {
		if row.SubjectID == c.id && row.Status != "withdrawn" {
			t.Fatalf("reminder not withdrawn: %+v", row)
		}
	}
	for _, timer := range couponTimers(t, e) {
		if timer.SubjectID != nil && *timer.SubjectID == c.id && timer.Status != "cancelled" {
			t.Fatalf("timer not cancelled: %+v", timer)
		}
	}
	if got := couponRequest(t, e, request.ID); got.Status != "cancelled" {
		t.Fatalf("request = %+v", got)
	}
}

func TestVoidOrderWritesCouponUnlinked(t *testing.T) {
	e := newOrderEnv(t)
	deal := e.deal("douyin", "撤单释放券", "99.00", e.item("撤单释放项目", "298.00"))
	c := e.coupon("void-event")
	order := e.order(e.item("履约", "398.00"))
	if r := e.link(c.id, deal, order); r.Status != 200 {
		t.Fatal(r)
	}
	e.checkout(order, cash("100.00"))
	rejectionDetail(t, e.Call(t, e.Owner, "void_order", orders.VoidOrderInput{OrderID: order, Reason: ""}, 0), 409, "请填写不超过200字的撤单原因")
	if events := eventsOfKind(t, e, storeai.EventCouponUnlinked); len(events) != 0 {
		t.Fatalf("failed void emitted: %+v", events)
	}
	e.Call(t, e.Owner, "void_order", orders.VoidOrderInput{OrderID: order, Reason: "测试撤单"}, 200)
	events := eventsOfKind(t, e, storeai.EventCouponUnlinked)
	if len(events) != 1 || events[0].SourceKey == "" || events[0].SubjectKind == nil || *events[0].SubjectID != c.id {
		t.Fatalf("events = %+v", events)
	}
	var payload map[string]any
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["redemption_id"] != c.id.String() || payload["order_id"] != order.String() {
		t.Fatalf("payload = %v", payload)
	}
}
