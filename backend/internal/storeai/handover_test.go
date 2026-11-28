package storeai_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"github.com/wzhlinhai/salon/backend/internal/storeai"
)

func createPosition(t *testing.T, shop *optest.Shop, name string) uuid.UUID {
	t.Helper()
	return uuid.MustParse(shop.Call(t, shop.Owner, "create_position", map[string]any{"name": name}, 200).Body["id"].(string))
}

func hireStaff(t *testing.T, shop *optest.Shop, name, number, account string, role uuid.UUID, position *uuid.UUID) (uuid.UUID, string) {
	t.Helper()
	args := map[string]any{"name": name, "number": number, "role_ids": []any{role}}
	if position != nil {
		args["position_id"] = *position
	}
	id := uuid.MustParse(shop.Call(t, shop.Owner, "create_staff", args, 200).Body["id"].(string))
	password := "password-" + account
	shop.Call(t, shop.Owner, "set_staff_login", map[string]any{"staff_id": id, "login_account": account, "password": password}, 200)
	return id, shop.MustLogin(account, password)
}

func clockAt(t *testing.T, shop *optest.Shop, token string, now time.Time) {
	t.Helper()
	shop.SetNow(now)
	shop.Call(t, token, "clock_in", map[string]any{}, 200)
}

func nextHandover(t *testing.T, shop *optest.Shop, now time.Time, requestID uuid.UUID) storeai.HandoverResult {
	t.Helper()
	var result storeai.HandoverResult
	err := db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		var err error
		result, err = storeai.NextHandover(context.Background(), q, now, requestID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func cancelHandoverRequest(t *testing.T, shop *optest.Shop, id uuid.UUID) {
	t.Helper()
	err := db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		_, err := storeai.CancelRequest(context.Background(), q, id)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func setAIHandoverSetting(t *testing.T, shop *optest.Shop, now time.Time, key string, value int) {
	t.Helper()
	shop.SetNow(now)
	shop.Call(t, shop.Owner, "change_setting", map[string]any{
		"key": key, "value": value, "effective_from": settings.Date(now).Format(settings.DateLayout),
	}, 200)
}

func handoverSubject() storeai.Subject {
	return storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: uuid.New()}
}

func TestNextHandoverClosedDoesNothing(t *testing.T) {
	shop := open(t)
	staff, _ := hireStaff(t, shop, "小李", "002", "li01", shop.Role("发型师"), nil)
	now := at("2026-11-27", 10, 0)
	subject := handoverSubject()
	request := createRequest(t, shop, now, storeai.CreateRequestInput{StoreID: shop.Res.StoreID, ToStaffID: staff, Kind: "inform", Subject: subject})
	cancelHandoverRequest(t, shop, request.ID)
	inbox := staffInbox(t, shop, staff)
	before := len(allEvents(t, shop, inbox.ID))
	result := nextHandover(t, shop, now, request.ID)
	if result.Action != storeai.HandoverNone || result.Request.ID != request.ID || result.NextFollowUp != nil {
		t.Fatalf("result = %+v", result)
	}
	if after := len(allEvents(t, shop, inbox.ID)); after != before {
		t.Fatalf("events changed: %d -> %d", before, after)
	}
}

func TestNextHandoverFirstRemindsSamePerson(t *testing.T) {
	shop := open(t)
	staff, _ := hireStaff(t, shop, "小李", "002", "li01", shop.Role("发型师"), nil)
	now := at("2026-11-27", 10, 0)
	due := now.Add(2 * time.Hour)
	subject := handoverSubject()
	request := createRequest(t, shop, now, storeai.CreateRequestInput{StoreID: shop.Res.StoreID, ToStaffID: staff, Kind: "inform", Subject: subject, DueAt: &due})
	result := nextHandover(t, shop, now, request.ID)
	if result.Action != storeai.HandoverRemind || result.Request.ID != request.ID || result.Request.Attempt != 1 || result.Request.Status != "open" {
		t.Fatalf("result = %+v", result)
	}
	if result.NextFollowUp == nil || !result.NextFollowUp.Equal(now.Add(30*time.Minute)) {
		t.Fatalf("next = %v", result.NextFollowUp)
	}
	inbox := staffInbox(t, shop, staff)
	events := allEvents(t, shop, inbox.ID)
	if len(events) != 2 || events[1].SourceKey != "request:"+request.ID.String()+":remind:1" || events[1].Kind != storeai.EventRequest {
		t.Fatalf("events = %+v", events)
	}
	var payload map[string]any
	if err := json.Unmarshal(events[1].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["request_id"] != request.ID.String() || payload["attempt"] != float64(1) {
		t.Fatalf("payload = %v", payload)
	}
}

func TestNextHandoverSecondReassignsSamePosition(t *testing.T) {
	shop := open(t)
	position := createPosition(t, shop, "大工")
	a, aToken := hireStaff(t, shop, "小李", "002", "li01", shop.Role("发型师"), &position)
	b, _ := hireStaff(t, shop, "小王", "003", "wang01", shop.Role("发型师"), &position)
	now := at("2026-11-27", 10, 0)
	clockAt(t, shop, aToken, now.Add(-time.Hour))
	clockAt(t, shop, shop.MustLogin("wang01", "password-wang01"), now.Add(-2*time.Hour))
	subject := handoverSubject()
	due := now.Add(2 * time.Hour)
	request := createRequest(t, shop, now, storeai.CreateRequestInput{StoreID: shop.Res.StoreID, ToStaffID: a, Kind: "inform", Subject: subject, DueAt: &due})
	putReminder(t, shop, now, storeai.PutReminderInput{StoreID: shop.Res.StoreID, RecipientID: a, Tier: "now", Subject: subject, Body: "给小李"})
	putReminder(t, shop, now, storeai.PutReminderInput{StoreID: shop.Res.StoreID, RecipientID: b, Tier: "now", Subject: subject, Body: "给小王"})
	if result := nextHandover(t, shop, now, request.ID); result.Action != storeai.HandoverRemind {
		t.Fatalf("first = %+v", result)
	}
	result := nextHandover(t, shop, now, request.ID)
	if result.Action != storeai.HandoverReassign || result.Request.ID == request.ID || result.Request.ToStaffID != b || result.Request.Status != "open" {
		t.Fatalf("second = %+v", result)
	}
	if result.Request.PreviousID == nil || *result.Request.PreviousID != request.ID {
		t.Fatalf("previous = %v", result.Request.PreviousID)
	}
	if result.NextFollowUp == nil || !result.NextFollowUp.Equal(now.Add(30*time.Minute)) {
		t.Fatalf("next = %v", result.NextFollowUp)
	}
	if got := requestByID(t, shop, request.ID); got.Status != "reassigned" {
		t.Fatalf("old status = %s", got.Status)
	}
	for _, row := range outboxRows(t, shop, a) {
		if row.SubjectID == subject.ID && row.Status != "withdrawn" {
			t.Fatalf("old recipient reminder = %+v", row)
		}
	}
	for _, row := range outboxRows(t, shop, b) {
		if row.SubjectID == subject.ID && row.Status != "queued" {
			t.Fatalf("new recipient reminder = %+v", row)
		}
	}
}

func TestNextHandoverFallsBackToManager(t *testing.T) {
	shop := open(t)
	position := createPosition(t, shop, "大工")
	a, _ := hireStaff(t, shop, "小李", "002", "li01", shop.Role("发型师"), &position)
	manager, managerToken := hireStaff(t, shop, "张店长", "003", "manager", shop.Role("店长"), nil)
	now := at("2026-11-27", 10, 0)
	clockAt(t, shop, managerToken, now.Add(-time.Hour))
	subject := handoverSubject()
	due := now.Add(2 * time.Hour)
	request := createRequest(t, shop, now, storeai.CreateRequestInput{StoreID: shop.Res.StoreID, ToStaffID: a, Kind: "inform", Subject: subject, DueAt: &due})
	if result := nextHandover(t, shop, now, request.ID); result.Action != storeai.HandoverRemind {
		t.Fatalf("first = %+v", result)
	}
	result := nextHandover(t, shop, now, request.ID)
	if result.Action != storeai.HandoverReassign || result.Request.ToStaffID != manager {
		t.Fatalf("result = %+v", result)
	}
}

func TestNextHandoverFallsBackToOwner(t *testing.T) {
	shop := open(t)
	a, _ := hireStaff(t, shop, "小李", "002", "li01", shop.Role("发型师"), nil)
	now := at("2026-11-27", 10, 0)
	subject := handoverSubject()
	due := now.Add(2 * time.Hour)
	request := createRequest(t, shop, now, storeai.CreateRequestInput{StoreID: shop.Res.StoreID, ToStaffID: a, Kind: "inform", Subject: subject, DueAt: &due})
	if result := nextHandover(t, shop, now, request.ID); result.Action != storeai.HandoverRemind {
		t.Fatalf("first = %+v", result)
	}
	result := nextHandover(t, shop, now, request.ID)
	if result.Action != storeai.HandoverReassign || result.Request.ToStaffID != shop.Res.OwnerID {
		t.Fatalf("result = %+v", result)
	}
}

func TestNextHandoverNearDueGoesToManager(t *testing.T) {
	shop := open(t)
	a, _ := hireStaff(t, shop, "小李", "002", "li01", shop.Role("发型师"), nil)
	manager, managerToken := hireStaff(t, shop, "张店长", "003", "manager", shop.Role("店长"), nil)
	now := at("2026-11-27", 10, 0)
	clockAt(t, shop, managerToken, now.Add(-time.Hour))
	subject := handoverSubject()
	due := now.Add(10 * time.Minute)
	request := createRequest(t, shop, now, storeai.CreateRequestInput{StoreID: shop.Res.StoreID, ToStaffID: a, Kind: "inform", Subject: subject, DueAt: &due})
	result := nextHandover(t, shop, now, request.ID)
	if result.Action != storeai.HandoverReassign || result.Request.ToStaffID != manager {
		t.Fatalf("result = %+v", result)
	}
	if result.NextFollowUp == nil || !result.NextFollowUp.Equal(due) {
		t.Fatalf("next = %v, want due %v", result.NextFollowUp, due)
	}
}

func TestNextHandoverManagerRecipientReminds(t *testing.T) {
	shop := open(t)
	manager, _ := hireStaff(t, shop, "张店长", "002", "manager", shop.Role("店长"), nil)
	now := at("2026-11-27", 10, 0)
	subject := handoverSubject()
	due := now.Add(10 * time.Minute)
	request := createRequest(t, shop, now, storeai.CreateRequestInput{StoreID: shop.Res.StoreID, ToStaffID: manager, Kind: "inform", Subject: subject, DueAt: &due})
	result := nextHandover(t, shop, now, request.ID)
	if result.Action != storeai.HandoverRemind || result.Request.ID != request.ID || result.Request.Attempt != 1 {
		t.Fatalf("result = %+v", result)
	}
}

func TestNextHandoverNoFollowUpAfterDue(t *testing.T) {
	shop := open(t)
	a, _ := hireStaff(t, shop, "小李", "002", "li01", shop.Role("发型师"), nil)
	now := at("2026-11-27", 10, 0)
	subject := handoverSubject()
	due := now.Add(-time.Minute)
	request := createRequest(t, shop, now, storeai.CreateRequestInput{StoreID: shop.Res.StoreID, ToStaffID: a, Kind: "inform", Subject: subject, DueAt: &due})
	result := nextHandover(t, shop, now, request.ID)
	if result.NextFollowUp != nil {
		t.Fatalf("next = %v", result.NextFollowUp)
	}
}

func TestNextHandoverUsesNewRemindSetting(t *testing.T) {
	shop := open(t)
	a, _ := hireStaff(t, shop, "小李", "002", "li01", shop.Role("发型师"), nil)
	now := at("2026-11-27", 10, 0)
	setAIHandoverSetting(t, shop, now, settings.KeyAIHandoverRemindMinutes, 45)
	subject := handoverSubject()
	due := now.Add(3 * time.Hour)
	request := createRequest(t, shop, now, storeai.CreateRequestInput{StoreID: shop.Res.StoreID, ToStaffID: a, Kind: "inform", Subject: subject, DueAt: &due})
	result := nextHandover(t, shop, now, request.ID)
	if result.NextFollowUp == nil || !result.NextFollowUp.Equal(now.Add(45*time.Minute)) {
		t.Fatalf("next = %v", result.NextFollowUp)
	}
}

func TestNextHandoverUsesNewMarginSetting(t *testing.T) {
	shop := open(t)
	a, _ := hireStaff(t, shop, "小李", "002", "li01", shop.Role("发型师"), nil)
	now := at("2026-11-27", 10, 0)
	setAIHandoverSetting(t, shop, now, settings.KeyAIHandoverManagerMarginMinutes, 20)
	subject := handoverSubject()
	due := now.Add(18 * time.Minute)
	request := createRequest(t, shop, now, storeai.CreateRequestInput{StoreID: shop.Res.StoreID, ToStaffID: a, Kind: "inform", Subject: subject, DueAt: &due})
	result := nextHandover(t, shop, now, request.ID)
	if result.Action != storeai.HandoverReassign || result.Request.ToStaffID != shop.Res.OwnerID {
		t.Fatalf("result = %+v", result)
	}
	if result.NextFollowUp == nil || !result.NextFollowUp.Equal(due) {
		t.Fatalf("next = %v, want due %v", result.NextFollowUp, due)
	}
}

func TestNextHandoverNextFollowUpTakesEarlierEdge(t *testing.T) {
	shop := open(t)
	a, _ := hireStaff(t, shop, "小李", "002", "li01", shop.Role("发型师"), nil)
	now := at("2026-11-27", 10, 0)
	subject := handoverSubject()
	due := now.Add(40 * time.Minute)
	request := createRequest(t, shop, now, storeai.CreateRequestInput{StoreID: shop.Res.StoreID, ToStaffID: a, Kind: "inform", Subject: subject, DueAt: &due})
	result := nextHandover(t, shop, now, request.ID)
	if result.NextFollowUp == nil || !result.NextFollowUp.Equal(now.Add(25*time.Minute)) {
		t.Fatalf("next = %v", result.NextFollowUp)
	}
}

func TestNextHandoverSkipsClockedOutSamePosition(t *testing.T) {
	shop := open(t)
	position := createPosition(t, shop, "大工")
	a, _ := hireStaff(t, shop, "小李", "002", "li01", shop.Role("发型师"), &position)
	b, bToken := hireStaff(t, shop, "小王", "003", "wang01", shop.Role("发型师"), &position)
	c, cToken := hireStaff(t, shop, "小赵", "004", "zhao01", shop.Role("发型师"), &position)
	now := at("2026-11-27", 10, 0)
	clockAt(t, shop, bToken, now.Add(-2*time.Hour))
	clockAt(t, shop, bToken, now.Add(-time.Hour))
	clockAt(t, shop, cToken, now.Add(-30*time.Minute))
	subject := handoverSubject()
	due := now.Add(2 * time.Hour)
	request := createRequest(t, shop, now, storeai.CreateRequestInput{StoreID: shop.Res.StoreID, ToStaffID: a, Kind: "inform", Subject: subject, DueAt: &due})
	if result := nextHandover(t, shop, now, request.ID); result.Action != storeai.HandoverRemind {
		t.Fatalf("first = %+v", result)
	}
	result := nextHandover(t, shop, now, request.ID)
	if result.Action != storeai.HandoverReassign || result.Request.ToStaffID != c {
		t.Fatalf("result = %+v, want %s not %s", result, c, b)
	}
}

func TestNextHandoverInactiveOriginalGoesToOwner(t *testing.T) {
	shop := open(t)
	manager, _ := hireStaff(t, shop, "张店长", "002", "manager", shop.Role("店长"), nil)
	now := at("2026-11-27", 10, 0)
	subject := handoverSubject()
	request := createRequest(t, shop, now, storeai.CreateRequestInput{StoreID: shop.Res.StoreID, ToStaffID: manager, Kind: "inform", Subject: subject})
	shop.Call(t, shop.Owner, "deactivate_staff", map[string]any{"staff_id": manager}, 200)
	result := nextHandover(t, shop, now, request.ID)
	if result.Action != storeai.HandoverReassign || result.Request.ToStaffID != shop.Res.OwnerID {
		t.Fatalf("result = %+v", result)
	}
}

func TestNextHandoverExcludesPreviousSamePositionRecipients(t *testing.T) {
	shop := open(t)
	position := createPosition(t, shop, "大工")
	a, aToken := hireStaff(t, shop, "小李", "002", "li01", shop.Role("发型师"), &position)
	b, bToken := hireStaff(t, shop, "小王", "003", "wang01", shop.Role("发型师"), &position)
	now := at("2026-11-27", 10, 0)
	clockAt(t, shop, aToken, now.Add(-2*time.Hour))
	clockAt(t, shop, bToken, now.Add(-time.Hour))
	subject := handoverSubject()
	due := now.Add(2 * time.Hour)
	request := createRequest(t, shop, now, storeai.CreateRequestInput{StoreID: shop.Res.StoreID, ToStaffID: a, Kind: "inform", Subject: subject, DueAt: &due})
	if result := nextHandover(t, shop, now, request.ID); result.Action != storeai.HandoverRemind {
		t.Fatalf("remind A = %+v", result)
	}
	toB := nextHandover(t, shop, now, request.ID)
	if toB.Action != storeai.HandoverReassign || toB.Request.ToStaffID != b {
		t.Fatalf("reassign B = %+v", toB)
	}
	if result := nextHandover(t, shop, now, toB.Request.ID); result.Action != storeai.HandoverRemind || result.Request.ID != toB.Request.ID {
		t.Fatalf("remind B = %+v", result)
	}
	result := nextHandover(t, shop, now, toB.Request.ID)
	if result.Action != storeai.HandoverReassign || result.Request.ToStaffID != shop.Res.OwnerID {
		t.Fatalf("to owner = %+v", result)
	}
}
