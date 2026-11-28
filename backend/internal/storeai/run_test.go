package storeai_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/ai"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"github.com/wzhlinhai/salon/backend/internal/storeai"
)

type scriptedModel struct {
	mu       sync.Mutex
	turns    []ai.Message
	errAt    int
	err      error
	calls    int
	messages [][]ai.Message
	tools    [][]ops.Tool
}

func (m *scriptedModel) Chat(_ context.Context, messages []ai.Message, tools []ops.Tool) (ai.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, append([]ai.Message(nil), messages...))
	m.tools = append(m.tools, append([]ops.Tool(nil), tools...))
	if m.errAt > 0 && m.calls+1 == m.errAt {
		m.calls++
		return ai.Message{}, m.err
	}
	if m.calls >= len(m.turns) {
		return ai.Message{}, fmt.Errorf("unexpected model call %d", m.calls+1)
	}
	message := m.turns[m.calls]
	m.calls++
	return message, nil
}

func (m *scriptedModel) toolNames(call int) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if call >= len(m.tools) {
		return nil
	}
	out := make([]string, 0, len(m.tools[call]))
	for _, tool := range m.tools[call] {
		out = append(out, tool.Name)
	}
	return out
}

func (m *scriptedModel) systemPrompt() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, messages := range m.messages {
		for _, message := range messages {
			if message.Role == ai.RoleSystem {
				return message.Content
			}
		}
	}
	return ""
}

func toolTurn(name string, args map[string]any) ai.Message {
	raw, _ := json.Marshal(args)
	return ai.Message{Role: ai.RoleAssistant, ToolCalls: []ai.ToolCall{{ID: name, Name: name, Arguments: string(raw)}}}
}

func finalTurn(text string) ai.Message {
	return ai.Message{Role: ai.RoleAssistant, Content: text}
}

type allowingClassifier struct{}

func (allowingClassifier) Chat(context.Context, []ai.Message, []ops.Tool) (ai.Message, error) {
	return ai.Message{Role: ai.RoleAssistant, Content: `{"decision":"allow"}`}, nil
}

func newRunner(shop *optest.Shop, model ai.Model) *storeai.Runner {
	return &storeai.Runner{
		Registry: shop.Registry, Model: func() ai.Model { return model },
		Classifier: func() ai.Model { return allowingClassifier{} }, Logger: discardLogger(),
	}
}

func insertCouponRedemption(t *testing.T, shop *optest.Shop, now time.Time) uuid.UUID {
	t.Helper()
	key := uuid.NewString()
	var id uuid.UUID
	err := db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		if _, err := q.CreateRedemption(context.Background(), dbgen.CreateRedemptionParams{
			TenantID: shop.Res.TenantID, StoreID: shop.Res.StoreID, Platform: "douyin",
			CouponCode: key, IdempotencyKey: key, RedeemedBy: shop.Res.OwnerID,
			CreatedAt: pgtype.Timestamptz{Time: now, Valid: true},
		}); err != nil {
			return err
		}
		row, err := q.GetRedemption(context.Background(), dbgen.GetRedemptionParams{StoreID: shop.Res.StoreID, IdempotencyKey: key})
		if err != nil {
			return err
		}
		if _, err := q.UpdateRedemption(context.Background(), dbgen.UpdateRedemptionParams{
			StoreID: shop.Res.StoreID, IdempotencyKey: key, Status: "redeemed",
			RedeemedAt: pgtype.Timestamptz{Time: now, Valid: true},
		}); err != nil {
			return err
		}
		id = row.ID
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func emitCouponEventForTest(t *testing.T, shop *optest.Shop, now time.Time, kind, sourceKey string, id uuid.UUID, payload string) {
	t.Helper()
	inbox := storeInbox(t, shop)
	if payload == "" {
		payload = "{}"
	}
	emitEvent(t, shop, inbox, now, kind, sourceKey, storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: id}, payload)
}

func eventStatus(t *testing.T, shop *optest.Shop, inboxID uuid.UUID) []string {
	t.Helper()
	out := []string{}
	for _, event := range allEvents(t, shop, inboxID) {
		out = append(out, event.Status)
	}
	return out
}

func TestStoreRunSchedulesFollowUp(t *testing.T) {
	shop := open(t)
	now := at("2026-11-27", 10, 0)
	id := insertCouponRedemption(t, shop, now)
	emitCouponEventForTest(t, shop, now, storeai.EventCouponRedeemed, "coupon_redeemed:"+id.String(), id, `{"redemption_id":"`+id.String()+`"}`)
	model := &scriptedModel{turns: []ai.Message{
		toolTurn("schedule_follow_up", map[string]any{"subject_kind": storeai.SubjectCouponRedemption, "subject_id": id, "minutes": 30}),
		finalTurn("已定跟进"),
	}}
	runOnce(t, shop, now, newRunner(shop, model).Run)
	inbox := storeInbox(t, shop)
	timers := allTimers(t, shop, inbox.ID)
	if len(timers) != 1 || timers[0].SubjectID == nil || *timers[0].SubjectID != id || timers[0].Status != "scheduled" {
		t.Fatalf("timers = %+v", timers)
	}
	if !timers[0].DueAt.Time.Equal(now.Add(30 * time.Minute)) {
		t.Fatalf("due = %v", timers[0].DueAt.Time)
	}
	if statuses := eventStatus(t, shop, inbox.ID); len(statuses) != 1 || statuses[0] != "done" {
		t.Fatalf("events = %v", statuses)
	}
}

func TestStoreRunCreatesRequestAndFollowUp(t *testing.T) {
	shop := open(t)
	now := at("2026-11-27", 10, 0)
	id := insertCouponRedemption(t, shop, now)
	staff, _ := shop.Hire("小李", "002", "li01", shop.Role("发型师"))
	emitCouponEventForTest(t, shop, now, storeai.EventCouponRedeemed, "coupon_redeemed:"+id.String(), id, `{"redemption_id":"`+id.String()+`","redeemed_by":"`+staff.String()+`"}`)
	model := &scriptedModel{turns: []ai.Message{
		toolTurn("create_request", map[string]any{"to_staff_id": staff, "kind": "inform", "subject_kind": storeai.SubjectCouponRedemption, "subject_id": id}),
		finalTurn("已交办"),
	}}
	runOnce(t, shop, now, newRunner(shop, model).Run)
	inbox := storeInbox(t, shop)
	timers := allTimers(t, shop, inbox.ID)
	if len(timers) != 1 || timers[0].Status != "scheduled" {
		t.Fatalf("timers = %+v", timers)
	}
	var payload map[string]any
	if err := json.Unmarshal(timers[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	requestID, err := uuid.Parse(payload["request_id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	request := requestByID(t, shop, requestID)
	if request.Status != "open" || request.ToStaffID != staff || request.Kind != "inform" {
		t.Fatalf("request = %+v", request)
	}
	employee := staffInbox(t, shop, staff)
	events := allEvents(t, shop, employee.ID)
	if len(events) != 1 || events[0].Kind != storeai.EventRequest || events[0].SourceKey != "request:"+requestID.String() {
		t.Fatalf("employee events = %+v", events)
	}
}

func TestStoreRunHasNoRemindMe(t *testing.T) {
	shop := open(t)
	now := at("2026-11-27", 10, 0)
	id := insertCouponRedemption(t, shop, now)
	emitCouponEventForTest(t, shop, now, storeai.EventCouponRedeemed, "coupon_redeemed:"+id.String(), id, "{}")
	model := &scriptedModel{turns: []ai.Message{finalTurn("安静")}}
	runOnce(t, shop, now, newRunner(shop, model).Run)
	names := model.toolNames(0)
	for _, want := range []string{"schedule_follow_up", "create_request", "hand_over", "cancel_request", "finish", "get_redemption"} {
		if !contains(names, want) {
			t.Fatalf("tools = %v, missing %s", names, want)
		}
	}
	for _, forbidden := range []string{"remind_me", "put_reminder", "list_my_reminders", "list_my_attendance", "get_me"} {
		if contains(names, forbidden) {
			t.Fatalf("tools = %v, has %s", names, forbidden)
		}
	}
	caller := ops.Caller{TenantID: shop.Res.TenantID, StoreID: shop.Res.StoreID, Owner: true, Permissions: perm.All(), Channel: ops.ChannelAI}
	if _, err := shop.Registry.CallReadTool(context.Background(), caller, "list_my_attendance", []byte(`{}`)); !errors.Is(err, ops.ErrUnknownOperation) {
		t.Fatalf("self tool = %v", err)
	}
}

func TestStoreRunRejectsForeignTargets(t *testing.T) {
	shop := open(t)
	other := open(t)
	now := at("2026-11-27", 10, 0)
	local := insertCouponRedemption(t, shop, now)
	foreign := insertCouponRedemption(t, other, now)
	localStaff, _ := shop.Hire("小李", "002", "li01", shop.Role("发型师"))
	foreignStaff, _ := other.Hire("小王", "002", "wang01", other.Role("发型师"))
	otherStore, sameTenantStaff := uuid.New(), uuid.New()
	if _, err := shop.Pool.Exec(context.Background(), "INSERT INTO stores (id,tenant_id,name,created_at) VALUES ($1,$2,'第二家',$3)", otherStore, shop.Res.TenantID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := shop.Pool.Exec(context.Background(), "INSERT INTO staff (id,tenant_id,store_id,name,number) VALUES ($1,$2,$3,'外店员工','009')", sameTenantStaff, shop.Res.TenantID, otherStore); err != nil {
		t.Fatal(err)
	}
	emitCouponEventForTest(t, shop, now, storeai.EventCouponRedeemed, "coupon_redeemed:"+local.String(), local, "{}")
	model := &scriptedModel{turns: []ai.Message{
		toolTurn("create_request", map[string]any{"to_staff_id": sameTenantStaff, "kind": "inform", "subject_kind": storeai.SubjectCouponRedemption, "subject_id": local}),
		toolTurn("create_request", map[string]any{"to_staff_id": foreignStaff, "kind": "inform", "subject_kind": storeai.SubjectCouponRedemption, "subject_id": local}),
		toolTurn("create_request", map[string]any{"to_staff_id": localStaff, "kind": "inform", "subject_kind": storeai.SubjectCouponRedemption, "subject_id": foreign}),
		finalTurn("知道了"),
	}}
	runOnce(t, shop, now, newRunner(shop, model).Run)
	final := model.messages[len(model.messages)-1]
	texts := []string{}
	for _, message := range final {
		if message.Role == ai.RoleTool {
			texts = append(texts, message.Content)
		}
	}
	if len(texts) < 2 || !strings.Contains(texts[0], "收件人不是本店在职员工") || !strings.Contains(texts[len(texts)-1], "对象不在本店") {
		t.Fatalf("tool messages = %v", texts)
	}
	if events := allEvents(t, shop, staffInbox(t, shop, localStaff).ID); len(events) != 0 {
		t.Fatalf("request emitted: %+v", events)
	}
}

func TestEmployeeRunRemindsSelfWithOwnPermissions(t *testing.T) {
	shop := open(t)
	now := at("2026-11-27", 10, 0)
	id := insertCouponRedemption(t, shop, now)
	staff, token := shop.Hire("小李", "002", "li01", shop.Role("发型师"))
	createRequest(t, shop, now, storeai.CreateRequestInput{StoreID: shop.Res.StoreID, ToStaffID: staff, Kind: "inform", Subject: storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: id}})
	model := &scriptedModel{turns: []ai.Message{
		toolTurn("remind_me", map[string]any{"tier": "now", "subject_kind": storeai.SubjectCouponRedemption, "subject_id": id, "body": "这张券还没关联单据"}),
		finalTurn("已提醒"),
	}}
	runOnce(t, shop, now, newRunner(shop, model).Run)
	rows := outboxRows(t, shop, staff)
	if len(rows) != 1 || rows[0].SubjectID != id || rows[0].RecipientID != staff || rows[0].Tier != "now" || rows[0].Body != "这张券还没关联单据" {
		t.Fatalf("outbox = %+v", rows)
	}
	names := model.toolNames(0)
	for _, forbidden := range []string{"create_request", "hand_over", "schedule_follow_up", "cancel_request"} {
		if contains(names, forbidden) {
			t.Fatalf("tools = %v, has %s", names, forbidden)
		}
	}
	if !contains(names, "remind_me") || !contains(names, "finish") {
		t.Fatalf("tools = %v", names)
	}
	caller := shop.MustCaller(token)
	expected := []string{}
	for _, tool := range shop.Registry.ReadTools(caller) {
		expected = append(expected, tool.Name)
	}
	got := []string{}
	for _, name := range names {
		if name != "remind_me" && name != "finish" {
			got = append(got, name)
		}
	}
	sort.Strings(expected)
	sort.Strings(got)
	if strings.Join(expected, ",") != strings.Join(got, ",") {
		t.Fatalf("read tools = %v, want %v", got, expected)
	}
}

func TestRunToolLimitSavesMessages(t *testing.T) {
	shop := open(t)
	now := at("2026-11-27", 10, 0)
	id := insertCouponRedemption(t, shop, now)
	emitCouponEventForTest(t, shop, now, storeai.EventCouponRedeemed, "coupon_redeemed:"+id.String(), id, "{}")
	turns := []ai.Message{}
	for range ai.MaxModelCalls {
		turns = append(turns, toolTurn("schedule_follow_up", map[string]any{"subject_kind": storeai.SubjectCouponRedemption, "subject_id": id, "minutes": 30}))
	}
	model := &scriptedModel{turns: turns}
	runOnce(t, shop, now, newRunner(shop, model).Run)
	inbox := storeInbox(t, shop)
	sessions := allSessions(t, shop, inbox.ID)
	if len(sessions) != 1 || sessions[0].Status != "active" || string(sessions[0].Messages) == "[]" {
		t.Fatalf("sessions = %+v", sessions)
	}
	if statuses := eventStatus(t, shop, inbox.ID); len(statuses) != 1 || statuses[0] != "done" {
		t.Fatalf("events = %v", statuses)
	}
}

func TestRunWithoutModelRetries(t *testing.T) {
	shop := open(t)
	now := at("2026-11-27", 10, 0)
	id := insertCouponRedemption(t, shop, now)
	emitCouponEventForTest(t, shop, now, storeai.EventCouponRedeemed, "coupon_redeemed:"+id.String(), id, "{}")
	runner := &storeai.Runner{Registry: shop.Registry, Model: func() ai.Model { return nil }, Logger: discardLogger()}
	runOnce(t, shop, now, runner.Run)
	events := allEvents(t, shop, storeInbox(t, shop).ID)
	if len(events) != 1 || events[0].Status != "pending" || events[0].Attempts != 1 {
		t.Fatalf("events = %+v", events)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestRunFinishSetsSessionStatus(t *testing.T) {
	for _, tc := range []struct {
		status  string
		waiting string
		want    string
	}{
		{"closed", "", "closed"},
		{"waiting", "等员工", "waiting"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			shop := open(t)
			now := at("2026-11-27", 10, 0)
			id := insertCouponRedemption(t, shop, now)
			emitCouponEventForTest(t, shop, now, storeai.EventCouponLinked, "coupon_linked:"+id.String(), id, "{}")
			args := map[string]any{"status": tc.status}
			if tc.waiting != "" {
				args["waiting_for"] = tc.waiting
			}
			model := &scriptedModel{turns: []ai.Message{toolTurn("finish", args), finalTurn("完成")}}
			runOnce(t, shop, now, newRunner(shop, model).Run)
			sessions := allSessions(t, shop, storeInbox(t, shop).ID)
			if len(sessions) != 1 || sessions[0].Status != tc.want {
				t.Fatalf("sessions = %+v", sessions)
			}
			if tc.waiting != "" && (sessions[0].WaitingFor == nil || *sessions[0].WaitingFor != tc.waiting) {
				t.Fatalf("waiting_for = %v", sessions[0].WaitingFor)
			}
		})
	}
}

func TestStorePromptUsesAISettings(t *testing.T) {
	shop := open(t)
	now := at("2026-11-27", 10, 0)
	setAIHandoverSetting(t, shop, now, settings.KeyAICouponLinkWaitMinutes, 45)
	id := insertCouponRedemption(t, shop, now)
	emitCouponEventForTest(t, shop, now, storeai.EventCouponRedeemed, "coupon_redeemed:"+id.String(), id, "{}")
	model := &scriptedModel{turns: []ai.Message{finalTurn("安静")}}
	runOnce(t, shop, now, newRunner(shop, model).Run)
	prompt := model.systemPrompt()
	if !strings.Contains(prompt, "45 分钟") || !strings.Contains(prompt, "2026-11-27") {
		t.Fatalf("prompt = %s", prompt)
	}
	if !strings.Contains(prompt, "redeemed_by") || !strings.Contains(prompt, "unlinked_only=true") {
		t.Fatalf("prompt lacks follow-up redeemer instructions: %s", prompt)
	}
}

func TestEmployeePromptIncludesRequestFlow(t *testing.T) {
	shop := open(t)
	now := at("2026-11-27", 10, 0)
	id := insertCouponRedemption(t, shop, now)
	staff, _ := shop.Hire("小李", "002", "li01", shop.Role("发型师"))
	createRequest(t, shop, now, storeai.CreateRequestInput{StoreID: shop.Res.StoreID, ToStaffID: staff, Kind: "inform", Subject: storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: id}})
	model := &scriptedModel{turns: []ai.Message{finalTurn("安静")}}
	runOnce(t, shop, now, newRunner(shop, model).Run)
	prompt := model.systemPrompt()
	for _, want := range []string{"收到 request 事件", "remind_me", "finish waiting", "不写顾客姓名和手机号", "读不到也不影响写提醒"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt lacks %q: %s", want, prompt)
		}
	}
}

func TestEmployeeRunDropsStaleRequestEvents(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stale func(t *testing.T, shop *optest.Shop, request dbgen.AiRequest)
	}{
		{"cancelled", func(t *testing.T, shop *optest.Shop, request dbgen.AiRequest) {
			cancelHandoverRequest(t, shop, request.ID)
		}},
		{"reassigned", func(t *testing.T, shop *optest.Shop, request dbgen.AiRequest) {
			next, _ := hireStaff(t, shop, "小王", "003", "wang01", shop.Role("发型师"), nil)
			successor := reassignRequest(t, shop, shop.Now(), request.ID, next)
			cancelHandoverRequest(t, shop, successor.ID)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shop := open(t)
			now := at("2026-11-27", 10, 0)
			id := insertCouponRedemption(t, shop, now)
			staff, _ := shop.Hire("小李", "002", "li01", shop.Role("发型师"))
			request := createRequest(t, shop, now, storeai.CreateRequestInput{StoreID: shop.Res.StoreID, ToStaffID: staff, Kind: "inform", Subject: storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: id}})
			tc.stale(t, shop, request)
			model := &scriptedModel{turns: []ai.Message{
				toolTurn("remind_me", map[string]any{"tier": "now", "subject_kind": storeai.SubjectCouponRedemption, "subject_id": id, "body": "不该写"}),
				finalTurn("结束"),
			}}
			runOnce(t, shop, now, newRunner(shop, model).Run)
			if model.calls != 0 {
				t.Fatalf("model calls = %d", model.calls)
			}
			if rows := outboxRows(t, shop, staff); len(rows) != 0 {
				t.Fatalf("outbox = %+v", rows)
			}
			events := allEvents(t, shop, staffInbox(t, shop, staff).ID)
			if len(events) != 1 || events[0].Status != "done" {
				t.Fatalf("events = %+v", events)
			}
		})
	}
}

func TestEmployeeRemindMeRequiresOpenRequest(t *testing.T) {
	shop := open(t)
	now := at("2026-11-27", 10, 0)
	id := insertCouponRedemption(t, shop, now)
	staff, _ := shop.Hire("小李", "002", "li01", shop.Role("发型师"))
	inbox := staffInbox(t, shop, staff)
	emitEvent(t, shop, inbox, now, storeai.EventPeriodicDue, "periodic:test", storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: id}, "{}")
	model := &scriptedModel{turns: []ai.Message{
		toolTurn("remind_me", map[string]any{"tier": "now", "subject_kind": storeai.SubjectCouponRedemption, "subject_id": id, "body": "不该写"}),
		finalTurn("结束"),
	}}
	runOnce(t, shop, now, newRunner(shop, model).Run)
	final := model.messages[len(model.messages)-1]
	found := false
	for _, message := range final {
		if message.Role == ai.RoleTool && strings.Contains(message.Content, "这个对象没有交给本人的待办") {
			found = true
		}
	}
	if !found {
		t.Fatalf("tool messages = %+v", final)
	}
	if rows := outboxRows(t, shop, staff); len(rows) != 0 {
		t.Fatalf("outbox = %+v", rows)
	}
}

func TestTool5xxRetriesEvent(t *testing.T) {
	shop := open(t)
	now := at("2026-11-27", 10, 0)
	ops.Register(shop.Registry, ops.Operation[struct{}, struct{}]{
		Name: "test_internal_error", Summary: "测试内部错误", Kind: ops.Read,
		Permission: perm.GroupBuyRedeem, AI: ops.AIDirect,
		Handle: func(context.Context, ops.Env, *struct{}) (*struct{}, error) { return nil, errors.New("boom") },
	})
	id := insertCouponRedemption(t, shop, now)
	emitCouponEventForTest(t, shop, now, storeai.EventCouponRedeemed, "coupon_redeemed:"+id.String(), id, "{}")
	model := &scriptedModel{turns: []ai.Message{toolTurn("test_internal_error", map[string]any{}), finalTurn("不应到这里")}}
	runOnce(t, shop, now, newRunner(shop, model).Run)
	events := allEvents(t, shop, storeInbox(t, shop).ID)
	if len(events) != 1 || events[0].Status != "pending" || events[0].Attempts != 1 {
		t.Fatalf("events = %+v", events)
	}
}

func TestChatErrorAfterWriteFinishesEvents(t *testing.T) {
	shop := open(t)
	now := at("2026-11-27", 10, 0)
	id := insertCouponRedemption(t, shop, now)
	staff, _ := shop.Hire("小李", "002", "li01", shop.Role("发型师"))
	emitCouponEventForTest(t, shop, now, storeai.EventCouponRedeemed, "coupon_redeemed:"+id.String(), id, "{}")
	model := &scriptedModel{
		turns: []ai.Message{toolTurn("create_request", map[string]any{"to_staff_id": staff, "kind": "inform", "subject_kind": storeai.SubjectCouponRedemption, "subject_id": id})},
		errAt: 2, err: errors.New("model down"),
	}
	runOnce(t, shop, now, newRunner(shop, model).Run)
	inbox := storeInbox(t, shop)
	if statuses := eventStatus(t, shop, inbox.ID); len(statuses) != 1 || statuses[0] != "done" {
		t.Fatalf("events = %v", statuses)
	}
	sessions := allSessions(t, shop, inbox.ID)
	if len(sessions) != 1 || sessions[0].Status != "active" {
		t.Fatalf("sessions = %+v", sessions)
	}
	if events := allEvents(t, shop, staffInbox(t, shop, staff).ID); len(events) != 1 || events[0].Kind != storeai.EventRequest {
		t.Fatalf("employee events = %+v", events)
	}
}

func TestChatErrorBeforeWriteRetries(t *testing.T) {
	shop := open(t)
	now := at("2026-11-27", 10, 0)
	id := insertCouponRedemption(t, shop, now)
	emitCouponEventForTest(t, shop, now, storeai.EventCouponRedeemed, "coupon_redeemed:"+id.String(), id, "{}")
	model := &scriptedModel{
		turns: []ai.Message{toolTurn("get_redemption", map[string]any{"key": "missing"})},
		errAt: 2, err: errors.New("model down"),
	}
	runOnce(t, shop, now, newRunner(shop, model).Run)
	events := allEvents(t, shop, storeInbox(t, shop).ID)
	if len(events) != 1 || events[0].Status != "pending" || events[0].Attempts != 1 {
		t.Fatalf("events = %+v", events)
	}
}

func TestCreateRequestDeduplicatesOpenRequest(t *testing.T) {
	shop := open(t)
	now := at("2026-11-27", 10, 0)
	id := insertCouponRedemption(t, shop, now)
	staff, _ := shop.Hire("小李", "002", "li01", shop.Role("发型师"))
	emitCouponEventForTest(t, shop, now, storeai.EventCouponRedeemed, "coupon_redeemed:"+id.String(), id, "{}")
	args := map[string]any{"to_staff_id": staff, "kind": "inform", "subject_kind": storeai.SubjectCouponRedemption, "subject_id": id}
	model := &scriptedModel{turns: []ai.Message{toolTurn("create_request", args), toolTurn("create_request", args), finalTurn("结束")}}
	runOnce(t, shop, now, newRunner(shop, model).Run)
	if events := allEvents(t, shop, staffInbox(t, shop, staff).ID); len(events) != 1 || events[0].Kind != storeai.EventRequest {
		t.Fatalf("employee events = %+v", events)
	}
	if timers := allTimers(t, shop, storeInbox(t, shop).ID); len(timers) != 1 {
		t.Fatalf("timers = %+v", timers)
	}
}
