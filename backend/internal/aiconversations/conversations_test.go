package aiconversations_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/ai"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

type fakeModel struct {
	mu       sync.Mutex
	requests [][]ai.Message
	reply    func(call int, messages []ai.Message, tools []ops.Tool) (ai.Message, error)
}

func (m *fakeModel) Chat(_ context.Context, messages []ai.Message, tools []ops.Tool) (ai.Message, error) {
	m.mu.Lock()
	m.requests = append(m.requests, slices.Clone(messages))
	call, reply := len(m.requests), m.reply
	m.mu.Unlock()
	if reply == nil {
		return ai.Message{}, errors.New("fake model has no reply")
	}
	return reply(call, messages, tools)
}

func (m *fakeModel) captured() [][]ai.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.requests)
}

func chat(t *testing.T, s *optest.Shop, token string, body map[string]any) optest.Result {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/ai/chat", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode chat response %q: %v", rec.Body.String(), err)
		}
	}
	return optest.Result{Status: rec.Code, Body: out}
}

func send(t *testing.T, s *optest.Shop, token, content string, extra map[string]any) map[string]any {
	t.Helper()
	body := map[string]any{"content": content}
	for key, value := range extra {
		body[key] = value
	}
	r := chat(t, s, token, body)
	if r.Status != http.StatusOK {
		t.Fatalf("chat = %d %v %v", r.Status, r.Body, r.Err)
	}
	return r.Body
}

func call(t *testing.T, s *optest.Shop, token, op string, args any) optest.Result {
	t.Helper()
	r := s.HTTPWithToken(t, token, op, args)
	if r.Status != http.StatusOK {
		t.Fatalf("%s: %d %v", op, r.Status, r.Body)
	}
	return r
}

func rejected(t *testing.T, r optest.Result, status int, detail string) {
	t.Helper()
	if r.Status != status || !strings.Contains(r.Detail(), detail) {
		t.Fatalf("got %d %s; want %d %s", r.Status, r.Detail(), status, detail)
	}
}

func count(t *testing.T, s *optest.Shop, table string) int {
	t.Helper()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var n int
	// table 只由本测试中的固定字面量提供。
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	return n
}

type seedMessage struct {
	role     string
	content  string
	sent     *string
	editing  *string
	draftIDs []uuid.UUID
	at       time.Time
}

func seedConversation(t *testing.T, s *optest.Shop, caller ops.Caller, title string, at time.Time, messages []seedMessage) uuid.UUID {
	t.Helper()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	q := dbgen.New(tx)
	c, err := q.CreateAIConversation(context.Background(), dbgen.CreateAIConversationParams{
		TenantID: caller.TenantID, StoreID: caller.StoreID, StaffID: caller.StaffID, Title: title, CreatedAt: pgtypeStamp(at),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range messages {
		ids := m.draftIDs
		if ids == nil {
			ids = []uuid.UUID{}
		}
		if _, err := q.AddAIConversationMessage(context.Background(), dbgen.AddAIConversationMessageParams{
			TenantID: caller.TenantID, StoreID: caller.StoreID, ConversationID: c.ID,
			Role: m.role, Content: m.content, Sent: m.sent, Editing: m.editing, DraftIds: ids, CreatedAt: pgtypeStamp(m.at),
		}); err != nil {
			t.Fatal(err)
		}
	}
	dbtest.Commit(t, tx)
	return c.ID
}

// pgtypeStamp 把 time.Time 转成 dbgen 接受的 pgtype.Timestamptz 值。
func pgtypeStamp(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func seedDraft(t *testing.T, s *optest.Shop, initiator uuid.UUID, operation, status string) uuid.UUID {
	t.Helper()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var id uuid.UUID
	err := tx.QueryRow(context.Background(), `
		INSERT INTO ai_drafts (tenant_id,store_id,initiator,operation,utterance,params,original_params,summary,status,business_date,expires_at,created_at,decided_at,decided_by,reject_reason)
		VALUES ($1,$2,$3,$4,'','{}','{}','测试草稿',$5,current_date,now(),now(),now(),$3,'测试')
		RETURNING id`, s.Res.TenantID, s.Res.StoreID, initiator, operation, status).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)
	return id
}

func TestChatSavesConversationAndTitle(t *testing.T) {
	s := optest.OpenShop(t)
	s.SetNow(time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC))
	model := &fakeModel{reply: func(int, []ai.Message, []ops.Tool) (ai.Message, error) {
		return ai.Message{Role: ai.RoleAssistant, Content: "好的"}, nil
	}}
	s.SetAIModel(model)
	body := send(t, s, s.Owner, "  第一句\n用户的话  ", nil)
	if body["reply"] != "好的" {
		t.Fatal(body)
	}
	if body["title"] != "第一句 用户的话" || body["conversation_id"] == nil {
		t.Fatal(body)
	}
	conversationID := uuid.MustParse(body["conversation_id"].(string))
	caller := s.MustCaller(s.Owner)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var tenantID, storeID, staffID uuid.UUID
	var title string
	var archived *time.Time
	var created, updated time.Time
	if err := tx.QueryRow(context.Background(), "SELECT tenant_id,store_id,staff_id,title,archived_at,created_at,updated_at FROM ai_conversations WHERE id=$1", conversationID).Scan(&tenantID, &storeID, &staffID, &title, &archived, &created, &updated); err != nil {
		t.Fatal(err)
	}
	if tenantID != caller.TenantID || storeID != caller.StoreID || staffID != caller.StaffID || title != "第一句 用户的话" || archived != nil || !created.Equal(s.Now()) || !updated.Equal(s.Now()) {
		t.Fatalf("conversation = %s %s %s %q %v %v %v", tenantID, storeID, staffID, title, archived, created, updated)
	}
	rows, err := tx.Query(context.Background(), "SELECT tenant_id,store_id,ordinal,role,content,sent,editing,draft_ids,created_at FROM ai_conversation_messages WHERE conversation_id=$1 ORDER BY ordinal", conversationID)
	if err != nil {
		t.Fatal(err)
	}
	type messageRow struct {
		tenantID, storeID uuid.UUID
		ordinal           int64
		role, content     string
		sent, editing     *string
		drafts            []uuid.UUID
		created           time.Time
	}
	var messages []messageRow
	for rows.Next() {
		var m messageRow
		if err := rows.Scan(&m.tenantID, &m.storeID, &m.ordinal, &m.role, &m.content, &m.sent, &m.editing, &m.drafts, &m.created); err != nil {
			t.Fatal(err)
		}
		messages = append(messages, m)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	if len(messages) != 2 || messages[0].tenantID != caller.TenantID || messages[0].storeID != caller.StoreID || messages[1].tenantID != caller.TenantID || messages[1].storeID != caller.StoreID || messages[0].ordinal <= 0 || messages[1].ordinal <= messages[0].ordinal || messages[0].role != ai.RoleUser || messages[0].content != "第一句\n用户的话" || messages[0].sent != nil || messages[0].editing != nil || len(messages[0].drafts) != 0 || messages[1].role != ai.RoleAssistant || messages[1].content != "好的" || !messages[0].created.Equal(s.Now()) || !messages[1].created.Equal(s.Now()) {
		t.Fatalf("messages = %+v", messages)
	}
	detail := call(t, s, s.Owner, "get_ai_conversation", map[string]any{"id": conversationID}).Body
	gotMessages := detail["messages"].([]any)
	if len(gotMessages) != 2 || gotMessages[0].(map[string]any)["role"] != ai.RoleUser || gotMessages[1].(map[string]any)["role"] != ai.RoleAssistant {
		t.Fatalf("get order = %v", gotMessages)
	}
	list := call(t, s, s.Owner, "list_ai_conversations", map[string]any{}).Body
	conversations := list["conversations"].([]any)
	if len(conversations) != 1 {
		t.Fatal(list)
	}
	item := conversations[0].(map[string]any)
	if item["id"] != conversationID.String() || item["title"] != "第一句 用户的话" || item["archived"] != false || item["updated_at"] == nil {
		t.Fatal(item)
	}
	long := send(t, s, s.Owner, strings.Repeat("字", 40), nil)
	if long["title"] != strings.Repeat("字", 30) {
		t.Fatalf("title = %q", long["title"])
	}
}

func TestChatHistoryWindow(t *testing.T) {
	t.Run("nineteen plus new and sent wins", func(t *testing.T) {
		s := optest.OpenShop(t)
		caller := s.MustCaller(s.Owner)
		base := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
		s.SetNow(base.Add(24 * time.Hour))
		long := strings.Repeat("长", 4001)
		sentLong := &long
		sentOne := "发给模型的甲"
		messages := make([]seedMessage, 19)
		for i := range messages {
			role := ai.RoleUser
			if i%2 == 1 {
				role = ai.RoleAssistant
			}
			messages[i] = seedMessage{role: role, content: fmt.Sprintf("显示%d", i), at: base.Add(time.Duration(i) * time.Second)}
		}
		messages[1].sent = &sentOne
		messages[3].sent = sentLong
		conversationID := seedConversation(t, s, caller, "历史", base, messages)
		other := seedConversation(t, s, caller, "别的会话", base, []seedMessage{{role: ai.RoleUser, content: "别的会话内容", at: base}})
		model := &fakeModel{reply: func(int, []ai.Message, []ops.Tool) (ai.Message, error) {
			return ai.Message{Role: ai.RoleAssistant, Content: "收到"}, nil
		}}
		s.SetAIModel(model)
		send(t, s, s.Owner, "本次新话", map[string]any{"conversation_id": conversationID.String()})
		requests := model.captured()
		if len(requests) != 1 {
			t.Fatalf("model calls = %d", len(requests))
		}
		history := requests[0][1:]
		if len(history) != 20 {
			t.Fatalf("history = %d %v", len(history), history)
		}
		if history[0].Role != ai.RoleUser || history[0].Content != "显示0" || history[1].Content != "发给模型的甲" || history[3].Content != strings.Repeat("长", 4000) || history[19].Content != "本次新话" || history[19].Role != ai.RoleUser {
			t.Fatalf("history = %+v", history)
		}
		for _, message := range history {
			if strings.Contains(message.Content, "别的会话内容") {
				t.Fatal("history leaked another conversation")
			}
		}
		if other == conversationID {
			t.Fatal("seed reused a conversation")
		}
	})

	t.Run("truncates and starts with user", func(t *testing.T) {
		s := optest.OpenShop(t)
		caller := s.MustCaller(s.Owner)
		base := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
		messages := make([]seedMessage, 25)
		for i := range messages {
			role := ai.RoleUser
			if i == 6 || i%2 == 0 {
				role = ai.RoleAssistant
			}
			messages[i] = seedMessage{role: role, content: fmt.Sprintf("第%d条", i), at: base.Add(time.Duration(i) * time.Second)}
		}
		conversationID := seedConversation(t, s, caller, "较长历史", base, messages)
		model := &fakeModel{reply: func(int, []ai.Message, []ops.Tool) (ai.Message, error) {
			return ai.Message{Role: ai.RoleAssistant, Content: "收到"}, nil
		}}
		s.SetAIModel(model)
		send(t, s, s.Owner, "继续", map[string]any{"conversation_id": conversationID.String()})
		history := model.captured()[0][1:]
		if len(history) != 19 || history[0].Role != ai.RoleUser || history[0].Content != "第7条" || history[18].Content != "继续" {
			t.Fatalf("history = %+v", history)
		}
	})

	t.Run("new turn sent wins", func(t *testing.T) {
		s := optest.OpenShop(t)
		model := &fakeModel{reply: func(int, []ai.Message, []ops.Tool) (ai.Message, error) {
			return ai.Message{Role: ai.RoleAssistant, Content: "收到"}, nil
		}}
		s.SetAIModel(model)
		send(t, s, s.Owner, "显示的新话", map[string]any{"sent": "发给模型的新话"})
		requests := model.captured()
		if len(requests) != 1 || requests[0][len(requests[0])-1].Content != "发给模型的新话" {
			t.Fatalf("requests = %+v", requests)
		}
	})

	t.Run("empty sent is dropped", func(t *testing.T) {
		s := optest.OpenShop(t)
		caller := s.MustCaller(s.Owner)
		base := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
		empty := "   "
		messages := []seedMessage{
			{role: ai.RoleUser, content: "第一条", at: base},
			{role: ai.RoleAssistant, content: "显示内容", sent: &empty, at: base.Add(time.Second)},
			{role: ai.RoleUser, content: "第三条", at: base.Add(2 * time.Second)},
		}
		conversationID := seedConversation(t, s, caller, "空消息", base, messages)
		model := &fakeModel{reply: func(int, []ai.Message, []ops.Tool) (ai.Message, error) {
			return ai.Message{Role: ai.RoleAssistant, Content: "收到"}, nil
		}}
		s.SetAIModel(model)
		send(t, s, s.Owner, "新话", map[string]any{"conversation_id": conversationID.String()})
		history := model.captured()[0][1:]
		if len(history) != 3 || history[1].Content != "第三条" || history[2].Content != "新话" {
			t.Fatalf("history = %+v", history)
		}
	})
}

func TestChatValidation(t *testing.T) {
	s := optest.OpenShop(t)
	if r := chat(t, s, "", map[string]any{"content": "你好"}); r.Status != http.StatusUnauthorized || r.Body["detail"] != "unauthenticated" {
		t.Fatal(r)
	}
	if r := chat(t, s, s.Owner, map[string]any{"content": "   "}); r.Status != http.StatusUnprocessableEntity || r.Body["detail"] != "消息内容不能为空" {
		t.Fatal(r)
	}
	if r := chat(t, s, s.Owner, map[string]any{"content": strings.Repeat("字", 4001)}); r.Status != http.StatusUnprocessableEntity || r.Body["detail"] != "单条消息最多 4000 字" {
		t.Fatal(r)
	}
	if r := chat(t, s, s.Owner, map[string]any{"content": "你好", "sent": strings.Repeat("字", 4001)}); r.Status != http.StatusUnprocessableEntity || r.Body["detail"] != "单条消息最多 4000 字" {
		t.Fatal(r)
	}
}

func TestConversationOwnership(t *testing.T) {
	s := optest.OpenShop(t)
	_, stylist := s.Hire("发型师", "102", "stylist", s.Role("发型师"))
	_, assistant := s.Hire("助理", "103", "assistant", s.Role("助理"))
	model := &fakeModel{reply: func(int, []ai.Message, []ops.Tool) (ai.Message, error) {
		return ai.Message{Role: ai.RoleAssistant, Content: "回答"}, nil
	}}
	s.SetAIModel(model)
	stylistConversation := send(t, s, stylist, "发型师的私密对话", nil)["conversation_id"].(string)
	ownerConversation := send(t, s, s.Owner, "老板的私密对话", nil)["conversation_id"].(string)
	stylistID := uuid.MustParse(stylistConversation)
	ownerID := uuid.MustParse(ownerConversation)
	if got := call(t, s, stylist, "list_ai_conversations", map[string]any{}).Body["conversations"].([]any); len(got) != 1 {
		t.Fatal(got)
	}
	if got := call(t, s, s.Owner, "list_ai_conversations", map[string]any{}).Body["conversations"].([]any); len(got) != 1 {
		t.Fatal(got)
	}
	before := count(t, s, "ai_conversation_messages")
	for _, op := range []string{"get_ai_conversation", "archive_ai_conversation", "unarchive_ai_conversation"} {
		rejected(t, s.HTTPWithToken(t, s.Owner, op, map[string]any{"id": stylistConversation}), 404, "not found")
		rejected(t, s.HTTPWithToken(t, stylist, op, map[string]any{"id": ownerConversation}), 404, "not found")
		rejected(t, s.HTTPWithToken(t, assistant, op, map[string]any{"id": stylistConversation}), 404, "not found")
	}
	for _, token := range []string{s.Owner, assistant} {
		rejected(t, chat(t, s, token, map[string]any{"conversation_id": stylistConversation, "content": "插一句"}), 404, "not found")
	}
	if count(t, s, "ai_conversation_messages") != before {
		t.Fatal("chat with another person's conversation inserted messages")
	}

	if _, err := staff.Bootstrap(context.Background(), s.Pool, staff.BootstrapInput{
		TenantName: "别家", StoreName: "别家", OwnerName: "李老板", OwnerNumber: "001", OwnerAccount: "other-boss", OwnerPassword: optest.OwnerPassword,
	}); err != nil {
		t.Fatal(err)
	}
	otherToken := s.MustLogin("other-boss", optest.OwnerPassword)
	if got := call(t, s, otherToken, "list_ai_conversations", map[string]any{}).Body["conversations"].([]any); len(got) != 0 {
		t.Fatal(got)
	}
	for _, op := range []string{"get_ai_conversation", "archive_ai_conversation", "unarchive_ai_conversation"} {
		rejected(t, s.HTTPWithToken(t, otherToken, op, map[string]any{"id": stylistConversation}), 404, "not found")
	}
	rejected(t, chat(t, s, otherToken, map[string]any{"conversation_id": stylistConversation, "content": "跨租户"}), 404, "not found")
	if count(t, s, "ai_conversation_messages") != before {
		t.Fatal("cross-tenant chat inserted messages")
	}
	if slices.Contains(toolNames(s.Registry.ChatTools(s.MustCaller(s.Owner))), "list_ai_conversations") {
		t.Fatal("history tools are visible to AI")
	}
	for _, op := range []string{"list_ai_conversations", "get_ai_conversation", "archive_ai_conversation", "unarchive_ai_conversation"} {
		rejected(t, s.AI(t, s.MustCaller(s.Owner), op, map[string]any{"id": stylistID.String()}), 404, "unknown operation")
		rejected(t, s.AI(t, s.MustCaller(s.Owner), op, map[string]any{"id": ownerID.String()}), 404, "unknown operation")
	}
}

func toolNames(tools []ops.Tool) []string {
	out := make([]string, len(tools))
	for i, tool := range tools {
		out[i] = tool.Name
	}
	return out
}

func TestAskFailureSavesNothing(t *testing.T) {
	s := optest.OpenShop(t)
	s.SetAIModel(&fakeModel{reply: func(int, []ai.Message, []ops.Tool) (ai.Message, error) {
		return ai.Message{}, errors.New("模型失败")
	}})
	r := chat(t, s, s.Owner, map[string]any{"content": "新建会话"})
	if r.Status != http.StatusBadGateway || r.Body["detail"] != "AI 暂时不可用，请稍后再试" {
		t.Fatal(r)
	}
	if count(t, s, "ai_conversations") != 0 || count(t, s, "ai_conversation_messages") != 0 {
		t.Fatal("failed new conversation was saved")
	}

	s.SetAIModel(&fakeModel{reply: func(int, []ai.Message, []ops.Tool) (ai.Message, error) {
		return ai.Message{Role: ai.RoleAssistant, Content: "成功"}, nil
	}})
	body := send(t, s, s.Owner, "已有会话", nil)
	conversationID := body["conversation_id"].(string)
	before := count(t, s, "ai_conversation_messages")
	s.SetAIModel(&fakeModel{reply: func(int, []ai.Message, []ops.Tool) (ai.Message, error) {
		return ai.Message{}, errors.New("再次失败")
	}})
	if r := chat(t, s, s.Owner, map[string]any{"conversation_id": conversationID, "content": "失败的一句"}); r.Status != http.StatusBadGateway {
		t.Fatal(r)
	}
	if count(t, s, "ai_conversation_messages") != before {
		t.Fatal("failed continuation was saved")
	}
}

func TestArchivedConversationResumes(t *testing.T) {
	s := optest.OpenShop(t)
	s.SetNow(time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC))
	s.SetAIModel(&fakeModel{reply: func(int, []ai.Message, []ops.Tool) (ai.Message, error) {
		return ai.Message{Role: ai.RoleAssistant, Content: "继续回答"}, nil
	}})
	body := send(t, s, s.Owner, "第一句", nil)
	conversationID := body["conversation_id"].(string)
	s.Advance(time.Hour)
	archived := call(t, s, s.Owner, "archive_ai_conversation", map[string]any{"id": conversationID}).Body
	if archived["archived"] != true {
		t.Fatal(archived)
	}
	if got := call(t, s, s.Owner, "get_ai_conversation", map[string]any{"id": conversationID}).Body; got["archived"] != true {
		t.Fatal(got)
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var archivedAt *time.Time
	if err := tx.QueryRow(context.Background(), "SELECT archived_at FROM ai_conversations WHERE id=$1", conversationID).Scan(&archivedAt); err != nil {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	if archivedAt == nil || !archivedAt.Equal(s.Now()) {
		t.Fatalf("archived_at = %v, want %v", archivedAt, s.Now())
	}
	if got := call(t, s, s.Owner, "list_ai_conversations", map[string]any{}).Body["conversations"].([]any); len(got) != 0 {
		t.Fatal(got)
	}
	if got := call(t, s, s.Owner, "list_ai_conversations", map[string]any{"archived": true}).Body["conversations"].([]any); len(got) != 1 {
		t.Fatal(got)
	}
	before := count(t, s, "ai_conversation_messages")
	s.Advance(time.Hour)
	send(t, s, s.Owner, "接着聊", map[string]any{"conversation_id": conversationID})
	if count(t, s, "ai_conversation_messages") != before+2 {
		t.Fatal("continuation did not append two messages")
	}
	if got := call(t, s, s.Owner, "get_ai_conversation", map[string]any{"id": conversationID}).Body; got["archived"] != false {
		t.Fatal(got)
	}
	if got := call(t, s, s.Owner, "list_ai_conversations", map[string]any{}).Body["conversations"].([]any); len(got) != 1 || got[0].(map[string]any)["archived"] != false {
		t.Fatal(got)
	}
	if got := call(t, s, s.Owner, "list_ai_conversations", map[string]any{"archived": true}).Body["conversations"].([]any); len(got) != 0 {
		t.Fatal(got)
	}
	if got := call(t, s, s.Owner, "unarchive_ai_conversation", map[string]any{"id": conversationID}).Body; got["archived"] != false {
		t.Fatal(got)
	}
}

func TestListSearchAndArchiveFilter(t *testing.T) {
	s := optest.OpenShop(t)
	s.SetNow(time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC))
	s.SetAIModel(&fakeModel{reply: func(int, []ai.Message, []ops.Tool) (ai.Message, error) {
		return ai.Message{Role: ai.RoleAssistant, Content: "搜索命中"}, nil
	}})
	first := send(t, s, s.Owner, "Hello World", nil)["conversation_id"].(string)
	s.Advance(time.Minute)
	second := send(t, s, s.Owner, "另一个话题", nil)["conversation_id"].(string)
	call(t, s, s.Owner, "archive_ai_conversation", map[string]any{"id": second})
	list := call(t, s, s.Owner, "list_ai_conversations", map[string]any{}).Body["conversations"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["id"] != first {
		t.Fatal(list)
	}
	if got := call(t, s, s.Owner, "list_ai_conversations", map[string]any{"q": "hello"}).Body["conversations"].([]any); len(got) != 1 || got[0].(map[string]any)["id"] != first {
		t.Fatal(got)
	}
	if got := call(t, s, s.Owner, "list_ai_conversations", map[string]any{"q": "搜索命中"}).Body["conversations"].([]any); len(got) != 1 || got[0].(map[string]any)["id"] != first {
		t.Fatal(got)
	}
	archived := call(t, s, s.Owner, "list_ai_conversations", map[string]any{"archived": true}).Body["conversations"].([]any)
	if len(archived) != 1 || archived[0].(map[string]any)["id"] != second {
		t.Fatal(archived)
	}
	if got := call(t, s, s.Owner, "list_ai_conversations", map[string]any{"archived": true, "q": "另一个"}).Body["conversations"].([]any); len(got) != 1 || got[0].(map[string]any)["id"] != second {
		t.Fatal(got)
	}
	rejected(t, s.HTTPWithToken(t, s.Owner, "list_ai_conversations", map[string]any{"q": strings.Repeat("字", 51)}), 422, "搜索内容最多 50 字")
}

func TestListReturnsAtMostOneHundred(t *testing.T) {
	s := optest.OpenShop(t)
	caller := s.MustCaller(s.Owner)
	base := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	q := dbgen.New(tx)
	for i := range 101 {
		if _, err := q.CreateAIConversation(context.Background(), dbgen.CreateAIConversationParams{
			TenantID: caller.TenantID, StoreID: caller.StoreID, StaffID: caller.StaffID,
			Title: fmt.Sprintf("会话%d", i), CreatedAt: pgtypeStamp(base.Add(time.Duration(i) * time.Second)),
		}); err != nil {
			t.Fatal(err)
		}
	}
	dbtest.Commit(t, tx)
	if got := call(t, s, s.Owner, "list_ai_conversations", map[string]any{}).Body["conversations"].([]any); len(got) != 100 {
		t.Fatalf("listed %d", len(got))
	}
}

func TestGetConversationMessagesAndDraftStatus(t *testing.T) {
	s := optest.OpenShop(t)
	s.SetNow(time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC))
	var draftID uuid.UUID
	model := &fakeModel{reply: func(call int, messages []ai.Message, _ []ops.Tool) (ai.Message, error) {
		if call == 1 {
			return ai.Message{Role: ai.RoleAssistant, ToolCalls: []ai.ToolCall{{ID: "call_1", Name: "create_customer", Arguments: `{"name":"新顾客","summary":"建顾客"}`}}}, nil
		}
		return ai.Message{Role: ai.RoleAssistant, Content: "已经起草"}, nil
	}}
	s.SetAIModel(model)
	body := send(t, s, s.Owner, "帮我建顾客", map[string]any{"editing": "正在改：建顾客", "sent": "请修改草稿"})
	conversationID := body["conversation_id"].(string)
	drafts := body["drafts"].([]any)
	if len(drafts) != 1 {
		t.Fatal(body)
	}
	draftID = uuid.MustParse(drafts[0].(map[string]any)["id"].(string))
	call(t, s, s.Owner, "reject_draft", map[string]any{"id": draftID, "reason": "不需要"})
	detail := call(t, s, s.Owner, "get_ai_conversation", map[string]any{"id": conversationID}).Body
	messages := detail["messages"].([]any)
	if len(messages) != 2 {
		t.Fatal(detail)
	}
	user := messages[0].(map[string]any)
	assistant := messages[1].(map[string]any)
	if user["role"] != "user" || user["editing"] != "正在改：建顾客" || user["content"] != "帮我建顾客" {
		t.Fatal(user)
	}
	if _, ok := user["sent"]; ok {
		t.Fatal("get returned sent")
	}
	views := assistant["drafts"].([]any)
	if len(views) != 1 || views[0].(map[string]any)["id"] != draftID.String() || views[0].(map[string]any)["status"] != "rejected" {
		t.Fatal(views)
	}
}

func TestGetHidesInvisibleAndMissingDrafts(t *testing.T) {
	s := optest.OpenShop(t)
	_, assistant := s.Hire("助理", "104", "assistant-2", s.Role("助理"))
	ownerCaller := s.MustCaller(s.Owner)
	assistantCaller := s.MustCaller(assistant)
	hidden := seedDraft(t, s, ownerCaller.StaffID, "open_order", "rejected")
	missing := uuid.New()
	conversationID := seedConversation(t, s, assistantCaller, "助理会话", time.Now(), []seedMessage{
		{role: "user", content: "你好", at: time.Now()},
		{role: "assistant", content: "回答", draftIDs: []uuid.UUID{hidden, missing}, at: time.Now().Add(time.Second)},
	})
	detail := call(t, s, assistant, "get_ai_conversation", map[string]any{"id": conversationID}).Body
	assistantMessage := detail["messages"].([]any)[1].(map[string]any)
	if got := assistantMessage["drafts"].([]any); len(got) != 0 {
		t.Fatal(got)
	}
	ownerDetail := call(t, s, s.Owner, "get_ai_conversation", map[string]any{"id": seedOwnConversation(t, s, ownerCaller, hidden)}).Body
	visible := ownerDetail["messages"].([]any)[0].(map[string]any)["drafts"].([]any)
	if len(visible) != 1 || visible[0].(map[string]any)["id"] != hidden.String() {
		t.Fatal(visible)
	}
	if _, ok := visible[0].(map[string]any)["preview"]; ok {
		t.Fatal("rejected draft carried a preview")
	}
	if _, ok := visible[0].(map[string]any)["preview_error"]; ok {
		t.Fatal("rejected draft carried a preview error")
	}
}

func seedOwnConversation(t *testing.T, s *optest.Shop, caller ops.Caller, draftID uuid.UUID) uuid.UUID {
	t.Helper()
	return seedConversation(t, s, caller, "老板会话", time.Now(), []seedMessage{
		{role: "assistant", content: "回答", draftIDs: []uuid.UUID{draftID}, at: time.Now()},
	})
}

func TestGetReturnsAllMessages(t *testing.T) {
	s := optest.OpenShop(t)
	caller := s.MustCaller(s.Owner)
	base := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	messages := make([]seedMessage, 105)
	for i := range messages {
		messages[i] = seedMessage{role: "user", content: fmt.Sprintf("消息%d", i), at: base.Add(time.Duration(i) * time.Second)}
	}
	conversationID := seedConversation(t, s, caller, "长会话", base, messages)
	detail := call(t, s, s.Owner, "get_ai_conversation", map[string]any{"id": conversationID}).Body
	gotMessages := detail["messages"].([]any)
	if len(gotMessages) != 105 || gotMessages[0].(map[string]any)["content"] != "消息0" || gotMessages[104].(map[string]any)["content"] != "消息104" {
		t.Fatalf("messages = %d %v", len(gotMessages), detail)
	}
}

type queryCounter struct{ all int64 }

func (c *queryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.all++
	return ctx
}

func (*queryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestGetConversationQueryCount(t *testing.T) {
	s := optest.OpenShop(t)
	caller := s.MustCaller(s.Owner)
	base := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	oneDraft := seedDraft(t, s, caller.StaffID, "create_customer", "rejected")
	manyDrafts := make([]uuid.UUID, 10)
	for i := range manyDrafts {
		manyDrafts[i] = seedDraft(t, s, caller.StaffID, "create_customer", "rejected")
	}
	one := seedConversation(t, s, caller, "一条", base, []seedMessage{{role: "assistant", content: "回答", draftIDs: []uuid.UUID{oneDraft}, at: base}})
	manyMessages := make([]seedMessage, len(manyDrafts))
	for i, draftID := range manyDrafts {
		manyMessages[i] = seedMessage{role: "assistant", content: fmt.Sprintf("回答%d", i), draftIDs: []uuid.UUID{draftID}, at: base.Add(time.Duration(i) * time.Second)}
	}
	many := seedConversation(t, s, caller, "多条", base, manyMessages)

	counter := &queryCounter{}
	config := s.Pool.Config().Copy()
	config.ConnConfig.Tracer = counter
	pool, err := db.NewPoolWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	app := optest.New(t, pool)
	counts := []int64{}
	for i, conversationID := range []uuid.UUID{one, many} {
		counter.all = 0
		r := app.HTTP(t, &caller, "get_ai_conversation", map[string]any{"id": conversationID})
		if r.Status != http.StatusOK || len(r.Body["messages"].([]any)) != []int{1, 10}[i] {
			t.Fatal(r)
		}
		drafts := 0
		for _, raw := range r.Body["messages"].([]any) {
			drafts += len(raw.(map[string]any)["drafts"].([]any))
		}
		if drafts != []int{1, 10}[i] {
			t.Fatalf("drafts = %d, want %d", drafts, []int{1, 10}[i])
		}
		if counter.all == 0 {
			t.Fatal("query counter observed no SQL")
		}
		counts = append(counts, counter.all)
	}
	if counts[0] != counts[1] {
		t.Fatalf("query count grew with messages and drafts: %v", counts)
	}
}

func TestArchiveDoesNotWriteAudit(t *testing.T) {
	s := optest.OpenShop(t)
	s.SetAIModel(&fakeModel{reply: func(int, []ai.Message, []ops.Tool) (ai.Message, error) {
		return ai.Message{Role: ai.RoleAssistant, Content: "回答"}, nil
	}})
	conversationID := send(t, s, s.Owner, "归档", nil)["conversation_id"].(string)
	before := count(t, s, "audit_log")
	call(t, s, s.Owner, "archive_ai_conversation", map[string]any{"id": conversationID})
	call(t, s, s.Owner, "unarchive_ai_conversation", map[string]any{"id": conversationID})
	if after := count(t, s, "audit_log"); after != before {
		t.Fatalf("audit rows = %d, want %d", after, before)
	}
}

func TestListOrderByUpdatedAt(t *testing.T) {
	s := optest.OpenShop(t)
	base := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	s.SetNow(base)
	s.SetAIModel(&fakeModel{reply: func(int, []ai.Message, []ops.Tool) (ai.Message, error) {
		return ai.Message{Role: ai.RoleAssistant, Content: "回答"}, nil
	}})
	first := send(t, s, s.Owner, "会话一", nil)["conversation_id"].(string)
	s.Advance(time.Minute)
	second := send(t, s, s.Owner, "会话二", nil)["conversation_id"].(string)
	s.Advance(time.Minute)
	send(t, s, s.Owner, "回到会话一", map[string]any{"conversation_id": first})
	list := call(t, s, s.Owner, "list_ai_conversations", map[string]any{}).Body["conversations"].([]any)
	if len(list) != 2 || list[0].(map[string]any)["id"] != first || list[1].(map[string]any)["id"] != second {
		t.Fatal(list)
	}
}

func TestConversationStoreIsolation(t *testing.T) {
	s := optest.OpenShop(t)
	caller := s.MustCaller(s.Owner)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	otherStore, err := dbgen.New(tx).CreateStore(context.Background(), dbgen.CreateStoreParams{TenantID: s.Res.TenantID, Name: "本店别店"})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := dbgen.New(tx).CreateAIConversation(context.Background(), dbgen.CreateAIConversationParams{
		TenantID: caller.TenantID, StoreID: otherStore.ID, StaffID: caller.StaffID, Title: "别店会话", CreatedAt: pgtypeStamp(time.Now()),
	})
	if err != nil {
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)
	if got := call(t, s, s.Owner, "list_ai_conversations", map[string]any{}).Body["conversations"].([]any); len(got) != 0 {
		t.Fatal(got)
	}
	for _, op := range []string{"get_ai_conversation", "archive_ai_conversation", "unarchive_ai_conversation"} {
		rejected(t, s.HTTPWithToken(t, s.Owner, op, map[string]any{"id": conversation.ID}), 404, "not found")
	}
	rejected(t, chat(t, s, s.Owner, map[string]any{"conversation_id": conversation.ID, "content": "别店"}), 404, "not found")
}

func TestChatWriteFilterRejectsChangedConversation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change string
	}{
		{"staff", "UPDATE ai_conversations SET staff_id=$1 WHERE id=$2"},
		{"store", "UPDATE ai_conversations SET store_id=$1 WHERE id=$2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := optest.OpenShop(t)
			s.SetAIModel(&fakeModel{reply: func(int, []ai.Message, []ops.Tool) (ai.Message, error) {
				return ai.Message{Role: ai.RoleAssistant, Content: "先成功"}, nil
			}})
			var conversationID string
			var target uuid.UUID
			if tc.name == "staff" {
				conversationID = send(t, s, s.Owner, "原始会话", nil)["conversation_id"].(string)
				target, _ = s.Hire("另一个员工", "105", "other-staff", s.Role("发型师"))
			} else {
				// 会话没有消息，才能在不违反消息外键的情况下把门店改走。
				conversationID = seedConversation(t, s, s.MustCaller(s.Owner), "原始会话", time.Now(), nil).String()
				tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
				store, err := dbgen.New(tx).CreateStore(context.Background(), dbgen.CreateStoreParams{TenantID: s.Res.TenantID, Name: "另一个门店"})
				if err != nil {
					t.Fatal(err)
				}
				dbtest.Commit(t, tx)
				target = store.ID
			}
			before := count(t, s, "ai_conversation_messages")
			changed := false
			s.SetAIModel(&fakeModel{reply: func(int, []ai.Message, []ops.Tool) (ai.Message, error) {
				if !changed {
					changed = true
					// 模拟读历史和写入之间会话被改，写入时仍按本人和本门店过滤。
					if _, err := s.Pool.Exec(context.Background(), tc.change, target, conversationID); err != nil {
						t.Fatal(err)
					}
				}
				return ai.Message{Role: ai.RoleAssistant, Content: "不应保存"}, nil
			}})
			rejected(t, chat(t, s, s.Owner, map[string]any{"conversation_id": conversationID, "content": "接着聊"}), 404, "not found")
			if count(t, s, "ai_conversation_messages") != before {
				t.Fatal("write filter saved messages after the conversation moved")
			}
		})
	}
}

func TestSentPersistsIntoNextHistory(t *testing.T) {
	s := optest.OpenShop(t)
	s.SetNow(time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC))
	model := &fakeModel{reply: func(int, []ai.Message, []ops.Tool) (ai.Message, error) {
		return ai.Message{Role: ai.RoleAssistant, Content: "回答"}, nil
	}}
	s.SetAIModel(model)
	conversationID := send(t, s, s.Owner, "显示的第一句", map[string]any{"sent": "发给模型的第一句"})["conversation_id"].(string)
	send(t, s, s.Owner, "第二句", map[string]any{"conversation_id": conversationID})
	requests := model.captured()
	if len(requests) != 2 {
		t.Fatalf("model calls = %d", len(requests))
	}
	history := requests[1][1:]
	if len(history) != 3 || history[0].Role != ai.RoleUser || history[0].Content != "发给模型的第一句" || history[1].Role != ai.RoleAssistant || history[2].Role != ai.RoleUser || history[2].Content != "第二句" {
		t.Fatalf("history = %+v", history)
	}
}

func TestGetConversationPendingDraftHasPreview(t *testing.T) {
	s := optest.OpenShop(t)
	s.SetNow(time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC))
	position := uuid.MustParse(call(t, s, s.Owner, "create_position", map[string]any{"name": "大工"}).Body["id"].(string))
	level := uuid.MustParse(call(t, s, s.Owner, "create_level", map[string]any{"name": "首席"}).Body["id"].(string))
	stylist, _ := s.Hire("王师傅", "002", "preview-stylist")
	call(t, s, s.Owner, "update_staff", map[string]any{"staff_id": stylist, "name": "王师傅", "number": "002", "level_id": level, "position_id": nil})
	item := uuid.MustParse(call(t, s, s.Owner, "create_item", map[string]any{
		"name": "剪发", "price": "100.00", "discountable": true, "labor_fee_amount": nil, "labor_fee_percent": nil,
		"positions":    []any{map[string]any{"position_id": position, "required": true}},
		"level_prices": []any{map[string]any{"level_id": level, "price": "150.00"}},
	}).Body["id"].(string))
	customer := uuid.MustParse(call(t, s, s.Owner, "create_customer", map[string]any{"name": "王姐", "phone": "13800000009"}).Body["customer"].(map[string]any)["id"].(string))
	args, err := json.Marshal(map[string]any{
		"customer_id": customer, "gender": "female", "designated": true,
		"items":   []any{map[string]any{"item_id": item, "staff": []any{map[string]any{"slot": 1, "staff_id": stylist}}}},
		"summary": "给王姐剪发", "utterance": "给王姐开一单剪发",
	})
	if err != nil {
		t.Fatal(err)
	}
	model := &fakeModel{reply: func(call int, _ []ai.Message, _ []ops.Tool) (ai.Message, error) {
		if call == 1 {
			return ai.Message{Role: ai.RoleAssistant, ToolCalls: []ai.ToolCall{{ID: "call_1", Name: "open_order", Arguments: string(args)}}}, nil
		}
		return ai.Message{Role: ai.RoleAssistant, Content: "已起草，请确认"}, nil
	}}
	s.SetAIModel(model)
	conversationID := send(t, s, s.Owner, "给王姐开一单剪发", nil)["conversation_id"].(string)
	detail := call(t, s, s.Owner, "get_ai_conversation", map[string]any{"id": conversationID}).Body
	views := detail["messages"].([]any)[1].(map[string]any)["drafts"].([]any)
	if len(views) != 1 {
		t.Fatal(views)
	}
	view := views[0].(map[string]any)
	preview, ok := view["preview"].(map[string]any)
	if view["status"] != "pending" || !ok || preview["lines"] == nil {
		t.Fatalf("pending draft = %+v", view)
	}
}

func TestFailedContinuationLeavesConversationUnchanged(t *testing.T) {
	s := optest.OpenShop(t)
	base := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	s.SetNow(base)
	s.SetAIModel(&fakeModel{reply: func(int, []ai.Message, []ops.Tool) (ai.Message, error) {
		return ai.Message{Role: ai.RoleAssistant, Content: "成功"}, nil
	}})
	conversationID := send(t, s, s.Owner, "已有会话", nil)["conversation_id"].(string)
	s.Advance(time.Hour)
	call(t, s, s.Owner, "archive_ai_conversation", map[string]any{"id": conversationID})
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var beforeUpdated time.Time
	var beforeArchived *time.Time
	if err := tx.QueryRow(context.Background(), "SELECT updated_at,archived_at FROM ai_conversations WHERE id=$1", conversationID).Scan(&beforeUpdated, &beforeArchived); err != nil {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	s.Advance(time.Hour)
	s.SetAIModel(&fakeModel{reply: func(int, []ai.Message, []ops.Tool) (ai.Message, error) {
		return ai.Message{}, errors.New("续聊失败")
	}})
	if r := chat(t, s, s.Owner, map[string]any{"conversation_id": conversationID, "content": "失败的一句"}); r.Status != http.StatusBadGateway {
		t.Fatal(r)
	}
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var afterUpdated time.Time
	var afterArchived *time.Time
	if err := tx.QueryRow(context.Background(), "SELECT updated_at,archived_at FROM ai_conversations WHERE id=$1", conversationID).Scan(&afterUpdated, &afterArchived); err != nil {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	if !afterUpdated.Equal(beforeUpdated) || (beforeArchived == nil) != (afterArchived == nil) || (beforeArchived != nil && !afterArchived.Equal(*beforeArchived)) {
		t.Fatalf("updated %v -> %v, archived %v -> %v", beforeUpdated, afterUpdated, beforeArchived, afterArchived)
	}
}

func TestAssistantCanUseHistory(t *testing.T) {
	s := optest.OpenShop(t)
	_, assistant := s.Hire("助理", "106", "assistant-history", s.Role("助理"))
	s.SetAIModel(&fakeModel{reply: func(int, []ai.Message, []ops.Tool) (ai.Message, error) {
		return ai.Message{Role: ai.RoleAssistant, Content: "回答"}, nil
	}})
	conversationID := send(t, s, assistant, "助理的会话", nil)["conversation_id"].(string)
	if got := call(t, s, assistant, "list_ai_conversations", map[string]any{}).Body["conversations"].([]any); len(got) != 1 || got[0].(map[string]any)["id"] != conversationID {
		t.Fatal(got)
	}
	if got := call(t, s, assistant, "get_ai_conversation", map[string]any{"id": conversationID}).Body; got["id"] != conversationID {
		t.Fatal(got)
	}
	if got := call(t, s, assistant, "archive_ai_conversation", map[string]any{"id": conversationID}).Body; got["archived"] != true {
		t.Fatal(got)
	}
	if got := call(t, s, assistant, "list_ai_conversations", map[string]any{}).Body["conversations"].([]any); len(got) != 0 {
		t.Fatal(got)
	}
	if got := call(t, s, assistant, "list_ai_conversations", map[string]any{"archived": true}).Body["conversations"].([]any); len(got) != 1 {
		t.Fatal(got)
	}
	if got := call(t, s, assistant, "unarchive_ai_conversation", map[string]any{"id": conversationID}).Body; got["archived"] != false {
		t.Fatal(got)
	}
}

func TestListSearchEscapesWildcards(t *testing.T) {
	s := optest.OpenShop(t)
	s.SetNow(time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC))
	s.SetAIModel(&fakeModel{reply: func(int, []ai.Message, []ops.Tool) (ai.Message, error) {
		return ai.Message{Role: ai.RoleAssistant, Content: "回答"}, nil
	}})
	withPercent := send(t, s, s.Owner, "折扣 50%", nil)["conversation_id"].(string)
	send(t, s, s.Owner, "普通会话", nil)
	if got := call(t, s, s.Owner, "list_ai_conversations", map[string]any{"q": "%"}).Body["conversations"].([]any); len(got) != 1 || got[0].(map[string]any)["id"] != withPercent {
		t.Fatal(got)
	}
	if got := call(t, s, s.Owner, "list_ai_conversations", map[string]any{"q": "_"}).Body["conversations"].([]any); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestListConversationsQueryCount(t *testing.T) {
	s := optest.OpenShop(t)
	caller := s.MustCaller(s.Owner)
	base := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	q := dbgen.New(tx)
	if _, err := q.CreateAIConversation(context.Background(), dbgen.CreateAIConversationParams{
		TenantID: caller.TenantID, StoreID: caller.StoreID, StaffID: caller.StaffID, Title: "单独会话", CreatedAt: pgtypeStamp(base),
	}); err != nil {
		t.Fatal(err)
	}
	for i := range 10 {
		if _, err := q.CreateAIConversation(context.Background(), dbgen.CreateAIConversationParams{
			TenantID: caller.TenantID, StoreID: caller.StoreID, StaffID: caller.StaffID,
			Title: fmt.Sprintf("批量会话%d", i), CreatedAt: pgtypeStamp(base.Add(time.Duration(i+1) * time.Second)),
		}); err != nil {
			t.Fatal(err)
		}
	}
	dbtest.Commit(t, tx)

	counter := &queryCounter{}
	config := s.Pool.Config().Copy()
	config.ConnConfig.Tracer = counter
	pool, err := db.NewPoolWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	app := optest.New(t, pool)
	counts := []int64{}
	for i, query := range []string{"单独", "批量"} {
		counter.all = 0
		r := app.HTTP(t, &caller, "list_ai_conversations", map[string]any{"q": query})
		if r.Status != http.StatusOK || len(r.Body["conversations"].([]any)) != []int{1, 10}[i] {
			t.Fatal(r)
		}
		if counter.all == 0 {
			t.Fatal("query counter observed no SQL")
		}
		counts = append(counts, counter.all)
	}
	if counts[0] != counts[1] {
		t.Fatalf("query count grew with conversations: %v", counts)
	}
}

func TestUpdatedAtDoesNotGoBackwards(t *testing.T) {
	s := optest.OpenShop(t)
	later := time.Date(2026, 11, 10, 14, 0, 0, 0, time.UTC)
	s.SetNow(later)
	s.SetAIModel(&fakeModel{reply: func(int, []ai.Message, []ops.Tool) (ai.Message, error) {
		return ai.Message{Role: ai.RoleAssistant, Content: "回答"}, nil
	}})
	conversationID := send(t, s, s.Owner, "先写较晚时刻", nil)["conversation_id"].(string)
	s.SetNow(later.Add(-time.Hour))
	send(t, s, s.Owner, "再用较早时刻续聊", map[string]any{"conversation_id": conversationID})
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var updated time.Time
	if err := tx.QueryRow(context.Background(), "SELECT updated_at FROM ai_conversations WHERE id=$1", conversationID).Scan(&updated); err != nil {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	if !updated.Equal(later) {
		t.Fatalf("updated_at = %v, want %v", updated, later)
	}
}

func TestConversationPrivileges(t *testing.T) {
	s := optest.OpenShop(t)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var conversations, messages bool
	if err := tx.QueryRow(context.Background(), `
		SELECT has_table_privilege('salon_app','ai_conversations','SELECT')
		   AND has_table_privilege('salon_app','ai_conversations','INSERT')
		   AND NOT has_table_privilege('salon_app','ai_conversations','UPDATE')
		   AND NOT has_table_privilege('salon_app','ai_conversations','DELETE')
		   AND has_column_privilege('salon_app','ai_conversations','archived_at','UPDATE')
		   AND has_column_privilege('salon_app','ai_conversations','updated_at','UPDATE')
		   AND NOT has_column_privilege('salon_app','ai_conversations','title','UPDATE')
		   AND NOT has_column_privilege('salon_app','ai_conversations','tenant_id','UPDATE')
		   AND NOT has_column_privilege('salon_app','ai_conversations','store_id','UPDATE')
		   AND NOT has_column_privilege('salon_app','ai_conversations','staff_id','UPDATE')
		   AND NOT has_column_privilege('salon_app','ai_conversations','created_at','UPDATE')`).Scan(&conversations); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(context.Background(), `
		SELECT has_table_privilege('salon_app','ai_conversation_messages','SELECT')
		   AND has_table_privilege('salon_app','ai_conversation_messages','INSERT')
		   AND NOT has_table_privilege('salon_app','ai_conversation_messages','UPDATE')
		   AND NOT has_table_privilege('salon_app','ai_conversation_messages','DELETE')
		   AND NOT has_column_privilege('salon_app','ai_conversation_messages','content','UPDATE')`).Scan(&messages); err != nil {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	if !conversations {
		t.Fatal("ai_conversations privileges are wrong")
	}
	if !messages {
		t.Fatal("ai_conversation_messages privileges are wrong")
	}
}
