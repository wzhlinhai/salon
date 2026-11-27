package storeai

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
)

const maxRequestOptions = 8

var requestOption = regexp.MustCompile(`^[a-z_]{1,32}$`)

// CreateRequestInput 是门店给员工建一条交办所需的字段。
type CreateRequestInput struct {
	StoreID   uuid.UUID
	ToStaffID uuid.UUID
	Kind      string
	Subject   Subject
	Options   []string
	DueAt     *time.Time
}

// CreateRequest 校验收件人后建一条交办，并往该员工的收件箱写 request 事件。
// 交办和事件里只有编号和结构化字段，没有自由文字。
func CreateRequest(ctx context.Context, q *dbgen.Queries, now time.Time, in CreateRequestInput) (dbgen.AiRequest, error) {
	if !validRequestKind(in.Kind) || in.Subject.Empty() || !in.Subject.Valid() || !validOptions(in.Kind, in.Options) {
		return dbgen.AiRequest{}, ErrInvalidRequest
	}
	tenantID, err := q.StoreTenant(ctx, in.StoreID)
	if err != nil {
		return dbgen.AiRequest{}, err
	}
	if err := lockActiveStaff(ctx, q, tenantID, in.StoreID, in.ToStaffID); err != nil {
		return dbgen.AiRequest{}, err
	}
	options := in.Options
	if options == nil {
		options = []string{}
	}
	rawOptions, err := json.Marshal(options)
	if err != nil {
		return dbgen.AiRequest{}, err
	}
	request, err := q.InsertRequest(ctx, dbgen.InsertRequestParams{
		TenantID: tenantID, StoreID: in.StoreID, ToStaffID: in.ToStaffID, Kind: in.Kind,
		SubjectKind: in.Subject.Kind, SubjectID: in.Subject.ID, Options: rawOptions,
		DueAt: optionalTimestamp(in.DueAt), CreatedAt: timestamp(now),
	})
	if err != nil {
		return dbgen.AiRequest{}, err
	}
	inbox, err := EnsureInbox(ctx, q, in.StoreID, &in.ToStaffID)
	if err != nil {
		return dbgen.AiRequest{}, err
	}
	if _, err := Emit(ctx, q, now, EmitInput{
		TenantID: tenantID, StoreID: in.StoreID, InboxID: inbox.ID,
		Kind: EventRequest, SourceKey: "request:" + request.ID.String(), Subject: in.Subject,
		Payload: requestPayload(request),
	}); err != nil {
		return dbgen.AiRequest{}, err
	}
	return request, nil
}

// AnswerRequest 是收件人对一条交办的本人回答。open 的回答置 answered 并通知门店；
// reassigned 的晚到回答不改状态，作为 late_answer 交门店核对。
func AnswerRequest(ctx context.Context, q *dbgen.Queries, now time.Time, requestID, staffID uuid.UUID, choice string) (dbgen.AiRequest, error) {
	request, err := q.LockRequest(ctx, requestID)
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.AiRequest{}, ErrRequestNotFound
	}
	if err != nil {
		return dbgen.AiRequest{}, err
	}
	if request.ToStaffID != staffID {
		return dbgen.AiRequest{}, ErrRequestNotFound
	}
	if _, err := q.GetStaffInStoreForShare(ctx, dbgen.GetStaffInStoreForShareParams{
		TenantID: request.TenantID, StoreID: request.StoreID, StaffID: staffID,
	}); errors.Is(err, pgx.ErrNoRows) {
		return dbgen.AiRequest{}, ErrRequestNotFound
	} else if err != nil {
		return dbgen.AiRequest{}, err
	}
	if request.Status == "answered" || request.Status == "cancelled" {
		return dbgen.AiRequest{}, ErrRequestClosed
	}
	options := requestOptions(request.Options)
	if !containsOption(options, choice) {
		return dbgen.AiRequest{}, ErrInvalidAnswer
	}
	switch request.Status {
	case "open":
		answered, err := q.SetRequestAnswered(ctx, dbgen.SetRequestAnsweredParams{
			Answer: &choice, AnsweredAt: timestamp(now), ID: request.ID, StoreID: request.StoreID,
		})
		if err != nil {
			return dbgen.AiRequest{}, err
		}
		inbox, err := EnsureInbox(ctx, q, request.StoreID, nil)
		if err != nil {
			return dbgen.AiRequest{}, err
		}
		payload, err := json.Marshal(map[string]any{
			"request_id": request.ID, "choice": choice, "answered_at": now,
		})
		if err != nil {
			return dbgen.AiRequest{}, err
		}
		if _, err := Emit(ctx, q, now, EmitInput{
			TenantID: request.TenantID, StoreID: request.StoreID, InboxID: inbox.ID,
			Kind: EventRequestAnswered, SourceKey: "request_answer:" + request.ID.String(),
			Subject: Subject{Kind: request.SubjectKind, ID: request.SubjectID}, Payload: payload,
		}); err != nil {
			return dbgen.AiRequest{}, err
		}
		return answered, nil
	case "reassigned":
		inbox, err := EnsureInbox(ctx, q, request.StoreID, nil)
		if err != nil {
			return dbgen.AiRequest{}, err
		}
		body := map[string]any{
			"request_id": request.ID, "choice": choice, "answered_at": now,
		}
		successor, err := q.GetSuccessor(ctx, &request.ID)
		if err == nil {
			body["reassigned_request_id"] = successor.ID
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return dbgen.AiRequest{}, err
		}
		payload, err := json.Marshal(body)
		if err != nil {
			return dbgen.AiRequest{}, err
		}
		if _, err := Emit(ctx, q, now, EmitInput{
			TenantID: request.TenantID, StoreID: request.StoreID, InboxID: inbox.ID,
			Kind: EventLateAnswer, SourceKey: "late_answer:" + request.ID.String(),
			Subject: Subject{Kind: request.SubjectKind, ID: request.SubjectID}, Payload: payload,
		}); err != nil {
			return dbgen.AiRequest{}, err
		}
		return request, nil
	default:
		return dbgen.AiRequest{}, ErrRequestClosed
	}
}

// ReassignRequest 把一条 open 的交办改交给另一位本店在职员工，原条置 reassigned，
// 新建一条 previous_id 指向原条，并照 CreateRequest 写 request 事件。
func ReassignRequest(ctx context.Context, q *dbgen.Queries, now time.Time, requestID, toStaffID uuid.UUID) (dbgen.AiRequest, error) {
	request, err := q.LockRequest(ctx, requestID)
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.AiRequest{}, ErrRequestNotFound
	}
	if err != nil {
		return dbgen.AiRequest{}, err
	}
	if request.Status != "open" {
		return dbgen.AiRequest{}, ErrRequestClosed
	}
	if err := lockActiveStaff(ctx, q, request.TenantID, request.StoreID, toStaffID); err != nil {
		return dbgen.AiRequest{}, err
	}
	if _, err := q.SetRequestReassigned(ctx, dbgen.SetRequestReassignedParams{ID: request.ID, StoreID: request.StoreID}); err != nil {
		return dbgen.AiRequest{}, err
	}
	previous := request.ID
	next, err := q.InsertRequest(ctx, dbgen.InsertRequestParams{
		TenantID: request.TenantID, StoreID: request.StoreID, ToStaffID: toStaffID, Kind: request.Kind,
		SubjectKind: request.SubjectKind, SubjectID: request.SubjectID, Options: request.Options,
		DueAt: request.DueAt, PreviousID: &previous, CreatedAt: timestamp(now),
	})
	if err != nil {
		return dbgen.AiRequest{}, err
	}
	inbox, err := EnsureInbox(ctx, q, request.StoreID, &toStaffID)
	if err != nil {
		return dbgen.AiRequest{}, err
	}
	if _, err := Emit(ctx, q, now, EmitInput{
		TenantID: request.TenantID, StoreID: request.StoreID, InboxID: inbox.ID,
		Kind: EventRequest, SourceKey: "request:" + next.ID.String(),
		Subject: Subject{Kind: next.SubjectKind, ID: next.SubjectID}, Payload: requestPayload(next),
	}); err != nil {
		return dbgen.AiRequest{}, err
	}
	return next, nil
}

// CancelRequest 把一条 open 的交办置 cancelled。
func CancelRequest(ctx context.Context, q *dbgen.Queries, requestID uuid.UUID) (dbgen.AiRequest, error) {
	request, err := q.LockRequest(ctx, requestID)
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.AiRequest{}, ErrRequestNotFound
	}
	if err != nil {
		return dbgen.AiRequest{}, err
	}
	if request.Status != "open" {
		return dbgen.AiRequest{}, ErrRequestClosed
	}
	return q.SetRequestCancelled(ctx, dbgen.SetRequestCancelledParams{ID: request.ID, StoreID: request.StoreID})
}

func lockActiveStaff(ctx context.Context, q *dbgen.Queries, tenantID, storeID, staffID uuid.UUID) error {
	if _, err := q.GetActiveStaffForShare(ctx, dbgen.GetActiveStaffForShareParams{
		TenantID: tenantID, StoreID: storeID, StaffID: staffID,
	}); errors.Is(err, pgx.ErrNoRows) {
		return ErrRecipientNotFound
	} else {
		return err
	}
}

func validRequestKind(kind string) bool {
	return kind == "inform" || kind == "ask" || kind == "review"
}

func validOptions(kind string, options []string) bool {
	if len(options) > maxRequestOptions {
		return false
	}
	if kind != "inform" && len(options) == 0 {
		return false
	}
	for _, option := range options {
		if !requestOption.MatchString(option) {
			return false
		}
	}
	return true
}

func containsOption(options []string, choice string) bool {
	for _, option := range options {
		if option == choice {
			return true
		}
	}
	return false
}

func requestOptions(raw []byte) []string {
	var options []string
	if err := json.Unmarshal(raw, &options); err != nil {
		return nil
	}
	return options
}

func requestPayload(request dbgen.AiRequest) json.RawMessage {
	body := map[string]any{
		"request_id": request.ID,
		"kind":       request.Kind,
		"subject":    map[string]any{"kind": request.SubjectKind, "id": request.SubjectID},
		"options":    requestOptions(request.Options),
		"due_at":     nil,
	}
	if request.DueAt.Valid {
		body["due_at"] = request.DueAt.Time
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}
