package storeai_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/storeai"
)

func createRequest(t *testing.T, shop *optest.Shop, now time.Time, in storeai.CreateRequestInput) dbgen.AiRequest {
	t.Helper()
	var request dbgen.AiRequest
	err := db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		var err error
		request, err = storeai.CreateRequest(context.Background(), q, now, in)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func createRequestErr(shop *optest.Shop, now time.Time, in storeai.CreateRequestInput) error {
	return db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		_, err := storeai.CreateRequest(context.Background(), q, now, in)
		return err
	})
}

func answerRequest(t *testing.T, shop *optest.Shop, now time.Time, requestID, staffID uuid.UUID, choice string) dbgen.AiRequest {
	t.Helper()
	var request dbgen.AiRequest
	err := db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		var err error
		request, err = storeai.AnswerRequest(context.Background(), q, now, requestID, staffID, choice)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func answerRequestErr(shop *optest.Shop, now time.Time, requestID, staffID uuid.UUID, choice string) error {
	return db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		_, err := storeai.AnswerRequest(context.Background(), q, now, requestID, staffID, choice)
		return err
	})
}

func reassignRequest(t *testing.T, shop *optest.Shop, now time.Time, requestID, staffID uuid.UUID) dbgen.AiRequest {
	t.Helper()
	var request dbgen.AiRequest
	err := db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		var err error
		request, err = storeai.ReassignRequest(context.Background(), q, now, requestID, staffID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func reassignRequestErr(shop *optest.Shop, now time.Time, requestID, staffID uuid.UUID) error {
	return db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		_, err := storeai.ReassignRequest(context.Background(), q, now, requestID, staffID)
		return err
	})
}

func TestCreateRequestEmitsStructuredEvent(t *testing.T) {
	shop := open(t)
	staff, _ := shop.Hire("小李", "002", "li01", shop.Role("发型师"))
	now := at("2026-11-27", 10, 0)
	subject := uniqueSubject("coupon_redemption")
	due := now.Add(2 * time.Hour)
	request := createRequest(t, shop, now, storeai.CreateRequestInput{
		StoreID: shop.Res.StoreID, ToStaffID: staff, Kind: "ask", Subject: subject,
		Options: []string{"yes", "no"}, DueAt: &due,
	})
	if request.Status != "open" || request.ToStaffID != staff || request.Answer != nil {
		t.Fatalf("request = %+v", request)
	}
	inbox := ensureInbox(t, shop, &staff)
	events := allEvents(t, shop, inbox.ID)
	if len(events) != 1 || events[0].Kind != storeai.EventRequest || events[0].SourceKey != "request:"+request.ID.String() {
		t.Fatalf("events = %+v", events)
	}
	var payload map[string]any
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 5 || payload["request_id"] != request.ID.String() || payload["kind"] != "ask" {
		t.Fatalf("payload = %v", payload)
	}
	subjectPayload := payload["subject"].(map[string]any)
	if subjectPayload["kind"] != subject.Kind || subjectPayload["id"] != subject.ID.String() {
		t.Fatalf("payload subject = %v", subjectPayload)
	}
	if options := payload["options"].([]any); len(options) != 2 || options[0] != "yes" {
		t.Fatalf("payload options = %v", payload["options"])
	}
	if payload["due_at"] != due.Format(time.RFC3339) && payload["due_at"] != due.Format(time.RFC3339Nano) {
		t.Fatalf("payload due_at = %v", payload["due_at"])
	}
}

func TestCreateRequestValidation(t *testing.T) {
	shop := open(t)
	staff, _ := shop.Hire("小李", "002", "li01", shop.Role("发型师"))
	now := at("2026-11-27", 10, 0)
	subject := uniqueSubject("coupon_redemption")
	cases := []struct {
		name string
		in   storeai.CreateRequestInput
	}{
		{"ask without options", storeai.CreateRequestInput{StoreID: shop.Res.StoreID, ToStaffID: staff, Kind: "ask", Subject: subject}},
		{"bad option", storeai.CreateRequestInput{StoreID: shop.Res.StoreID, ToStaffID: staff, Kind: "ask", Subject: subject, Options: []string{"Bad"}}},
		{"too many options", storeai.CreateRequestInput{StoreID: shop.Res.StoreID, ToStaffID: staff, Kind: "ask", Subject: subject, Options: []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}}},
		{"bad kind", storeai.CreateRequestInput{StoreID: shop.Res.StoreID, ToStaffID: staff, Kind: "note", Subject: subject}},
	}
	for _, tc := range cases {
		if err := createRequestErr(shop, now, tc.in); !errors.Is(err, storeai.ErrInvalidRequest) {
			t.Fatalf("%s = %v", tc.name, err)
		}
	}
	inactive, _ := shop.Hire("小王", "003", "wang01", shop.Role("发型师"))
	shop.Call(t, shop.Owner, "deactivate_staff", map[string]any{"staff_id": inactive}, 200)
	err := createRequestErr(shop, now, storeai.CreateRequestInput{
		StoreID: shop.Res.StoreID, ToStaffID: inactive, Kind: "inform", Subject: subject,
	})
	if !errors.Is(err, storeai.ErrRecipientNotFound) {
		t.Fatalf("inactive recipient = %v", err)
	}
	inform := createRequest(t, shop, now, storeai.CreateRequestInput{
		StoreID: shop.Res.StoreID, ToStaffID: staff, Kind: "inform", Subject: subject,
	})
	if string(inform.Options) != "[]" {
		t.Fatalf("inform options = %s, want []", inform.Options)
	}
}

func TestAnswerRequestRules(t *testing.T) {
	shop := open(t)
	staff, _ := shop.Hire("小李", "002", "li01", shop.Role("发型师"))
	other, _ := shop.Hire("小王", "003", "wang01", shop.Role("发型师"))
	now := at("2026-11-27", 10, 0)
	subject := uniqueSubject("coupon_redemption")
	request := createRequest(t, shop, now, storeai.CreateRequestInput{
		StoreID: shop.Res.StoreID, ToStaffID: staff, Kind: "ask", Subject: subject, Options: []string{"yes", "no"},
	})
	store := storeInbox(t, shop)
	if err := answerRequestErr(shop, now, request.ID, other, "yes"); !errors.Is(err, storeai.ErrRequestNotFound) {
		t.Fatalf("other staff answer = %v", err)
	}
	if events := allEvents(t, shop, store.ID); len(events) != 0 {
		t.Fatalf("other staff emitted: %+v", events)
	}
	if err := answerRequestErr(shop, now, request.ID, staff, "maybe"); !errors.Is(err, storeai.ErrInvalidAnswer) {
		t.Fatalf("invalid choice = %v", err)
	}
	answered := answerRequest(t, shop, now, request.ID, staff, "yes")
	if answered.Status != "answered" || answered.Answer == nil || *answered.Answer != "yes" || !answered.AnsweredAt.Valid {
		t.Fatalf("answered = %+v", answered)
	}
	events := allEvents(t, shop, store.ID)
	if len(events) != 1 || events[0].Kind != storeai.EventRequestAnswered || events[0].SourceKey != "request_answer:"+request.ID.String() {
		t.Fatalf("store events = %+v", events)
	}
	var payload map[string]any
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 3 || payload["request_id"] != request.ID.String() || payload["choice"] != "yes" {
		t.Fatalf("answer payload = %v", payload)
	}
	if err := answerRequestErr(shop, now, request.ID, staff, "yes"); !errors.Is(err, storeai.ErrRequestClosed) {
		t.Fatalf("answer again = %v", err)
	}
	if err := answerRequestErr(shop, now, request.ID, staff, "not-an-option"); !errors.Is(err, storeai.ErrRequestClosed) {
		t.Fatalf("closed answer with unknown choice = %v", err)
	}
	if events := allEvents(t, shop, store.ID); len(events) != 1 {
		t.Fatalf("second answer emitted: %+v", events)
	}
	cancelled := createRequest(t, shop, now, storeai.CreateRequestInput{
		StoreID: shop.Res.StoreID, ToStaffID: staff, Kind: "ask", Subject: uniqueSubject("coupon_redemption"), Options: []string{"ok"},
	})
	if err := db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		_, err := storeai.CancelRequest(context.Background(), q, cancelled.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := answerRequestErr(shop, now, cancelled.ID, staff, "ok"); !errors.Is(err, storeai.ErrRequestClosed) {
		t.Fatalf("answer cancelled = %v", err)
	}
	if err := answerRequestErr(shop, now, cancelled.ID, staff, "not-an-option"); !errors.Is(err, storeai.ErrRequestClosed) {
		t.Fatalf("cancelled answer with unknown choice = %v", err)
	}
}

func TestReassignAndLateAnswer(t *testing.T) {
	shop := open(t)
	staffA, _ := shop.Hire("小李", "002", "li01", shop.Role("发型师"))
	staffB, _ := shop.Hire("小王", "003", "wang01", shop.Role("发型师"))
	now := at("2026-11-27", 10, 0)
	subject := uniqueSubject("coupon_redemption")
	original := createRequest(t, shop, now, storeai.CreateRequestInput{
		StoreID: shop.Res.StoreID, ToStaffID: staffA, Kind: "ask", Subject: subject, Options: []string{"yes", "no"},
	})
	next := reassignRequest(t, shop, now, original.ID, staffB)
	if next.PreviousID == nil || *next.PreviousID != original.ID || next.ToStaffID != staffB || next.Status != "open" {
		t.Fatalf("next = %+v", next)
	}
	if got := requestByID(t, shop, original.ID); got.Status != "reassigned" {
		t.Fatalf("original = %+v", got)
	}
	inboxB := ensureInbox(t, shop, &staffB)
	if events := allEvents(t, shop, inboxB.ID); len(events) != 1 || events[0].SourceKey != "request:"+next.ID.String() {
		t.Fatalf("staff B events = %+v", events)
	}
	late := answerRequest(t, shop, now, original.ID, staffA, "yes")
	if late.Status != "reassigned" {
		t.Fatalf("late answer changed status: %+v", late)
	}
	store := storeInbox(t, shop)
	events := allEvents(t, shop, store.ID)
	if len(events) != 1 || events[0].Kind != storeai.EventLateAnswer || events[0].SourceKey != "late_answer:"+original.ID.String() {
		t.Fatalf("store events = %+v", events)
	}
	var payload map[string]any
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["reassigned_request_id"] != next.ID.String() || payload["request_id"] != original.ID.String() {
		t.Fatalf("late payload = %v", payload)
	}
	if got := requestByID(t, shop, next.ID); got.Status != "open" {
		t.Fatalf("new request = %+v", got)
	}
	answerRequest(t, shop, now, next.ID, staffB, "no")
	if got := requestByID(t, shop, next.ID); got.Status != "answered" {
		t.Fatalf("new request after answer = %+v", got)
	}
	if err := reassignRequestErr(shop, now, next.ID, staffA); !errors.Is(err, storeai.ErrRequestClosed) {
		t.Fatalf("reassign answered = %v", err)
	}
}

func TestReassignToInactiveStaff(t *testing.T) {
	shop := open(t)
	staff, _ := shop.Hire("小李", "002", "li01", shop.Role("发型师"))
	inactive, _ := shop.Hire("小王", "003", "wang01", shop.Role("发型师"))
	shop.Call(t, shop.Owner, "deactivate_staff", map[string]any{"staff_id": inactive}, 200)
	now := at("2026-11-27", 10, 0)
	request := createRequest(t, shop, now, storeai.CreateRequestInput{
		StoreID: shop.Res.StoreID, ToStaffID: staff, Kind: "ask", Subject: uniqueSubject("coupon_redemption"), Options: []string{"yes"},
	})
	if err := reassignRequestErr(shop, now, request.ID, inactive); !errors.Is(err, storeai.ErrRecipientNotFound) {
		t.Fatalf("reassign inactive = %v", err)
	}
	if got := requestByID(t, shop, request.ID); got.Status != "open" {
		t.Fatalf("original after failed reassign = %+v", got)
	}
}

func TestAnswerRequestRejectsStaffFromAnotherStore(t *testing.T) {
	shop := open(t)
	now := at("2026-11-27", 10, 0)
	otherStore, otherStaff := uuid.New(), uuid.New()
	if _, err := shop.Pool.Exec(context.Background(),
		"INSERT INTO stores (id,tenant_id,name,created_at) VALUES ($1,$2,'第二家',$3)", otherStore, shop.Res.TenantID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := shop.Pool.Exec(context.Background(),
		"INSERT INTO staff (id,tenant_id,store_id,name,number) VALUES ($1,$2,$3,'外店员工','001')", otherStaff, shop.Res.TenantID, otherStore); err != nil {
		t.Fatal(err)
	}
	subject := uniqueSubject("coupon_redemption")
	var requestID uuid.UUID
	tx := dbtest.AppTx(t, shop.Pool, shop.Res.TenantID)
	err := tx.QueryRow(context.Background(), `
		INSERT INTO ai_requests (tenant_id,store_id,to_staff_id,kind,subject_kind,subject_id,options,status,created_at)
		VALUES ($1,$2,$3,'ask','coupon_redemption',$4,'["yes"]'::jsonb,'open',$5) RETURNING id`,
		shop.Res.TenantID, shop.Res.StoreID, otherStaff, subject.ID, now).Scan(&requestID)
	if err != nil {
		dbtest.Rollback(t, tx)
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)
	if err := answerRequestErr(shop, now, requestID, otherStaff, "yes"); !errors.Is(err, storeai.ErrRequestNotFound) {
		t.Fatalf("answer from another store = %v", err)
	}
}
