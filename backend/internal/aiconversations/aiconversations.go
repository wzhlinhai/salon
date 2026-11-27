// Package aiconversations 保存员工本人的 AI 对话历史会话，并提供列表、查看和归档。
package aiconversations

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/ai"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/drafts"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

const (
	// MaxContentRunes 是一条消息最多的字符数。
	MaxContentRunes = 4000
	// MaxQueryRunes 是列表搜索内容最多的字符数。
	MaxQueryRunes = 50
	// historyMessages 是发给模型的历史消息条数，不含本次新话。
	historyMessages = 19
	// maxTitleRunes 是自动生成的会话标题最多的字符数。
	maxTitleRunes = 30
)

// ChatInput 是一次 AI 对话的输入。
type ChatInput struct {
	ConversationID *uuid.UUID
	Content        string
	Sent           string
	Editing        string
	DraftID        *uuid.UUID
}

// ChatAnswer 是一次对话的最终回答和保存到的历史会话。
type ChatAnswer struct {
	ai.Answer
	ConversationID uuid.UUID `json:"conversation_id"`
	Title          string    `json:"title"`
}

// ConversationSummary 是本人历史会话列表里的一项。
type ConversationSummary struct {
	ID        uuid.UUID `json:"id"`
	Title     string    `json:"title"`
	UpdatedAt time.Time `json:"updated_at"`
	Archived  bool      `json:"archived"`
}

// ListConversationsInput 是历史会话列表的参数。
type ListConversationsInput struct {
	Archived bool   `json:"archived,omitempty" doc:"是否只看已归档的会话"`
	Q        string `json:"q,omitempty" doc:"按标题和消息内容搜索，最多 50 字"`
}

// ConversationList 是本人的历史会话列表，最近更新的在前。
type ConversationList struct {
	Conversations []ConversationSummary `json:"conversations"`
}

// ConversationIDInput 指定一个本人历史会话。
type ConversationIDInput struct {
	ID uuid.UUID `json:"id"`
}

// ConversationMessage 是历史会话里的一条消息，含它创建或修改的草稿当前状态。
type ConversationMessage struct {
	ID        uuid.UUID          `json:"id"`
	Role      string             `json:"role" enum:"user,assistant"`
	Content   string             `json:"content"`
	Editing   *string            `json:"editing"`
	CreatedAt time.Time          `json:"created_at"`
	Drafts    []drafts.DraftView `json:"drafts"`
}

// ConversationDetail 是一个历史会话和它的全部消息。
type ConversationDetail struct {
	ID       uuid.UUID             `json:"id"`
	Title    string                `json:"title"`
	Archived bool                  `json:"archived"`
	Messages []ConversationMessage `json:"messages"`
}

// ChatService 执行一次 AI 对话：读取会话历史、调用模型，并在成功后保存消息。
type ChatService struct {
	pool *pgxpool.Pool
	chat *ai.Chat
	now  func() time.Time
}

// NewChatService 返回 AI 对话服务；now 是本次对话使用的时钟。
func NewChatService(pool *pgxpool.Pool, chat *ai.Chat, now func() time.Time) *ChatService {
	return &ChatService{pool: pool, chat: chat, now: now}
}

// Ask 用调用者本人的权限回答一句话，并把本次对话保存成本人的历史会话。
func (s *ChatService) Ask(ctx context.Context, caller ops.Caller, in ChatInput) (ChatAnswer, error) {
	content := strings.TrimSpace(in.Content)
	sent := trimmedOrNil(in.Sent)
	editing := trimmedOrNil(in.Editing)
	asked := content
	if sent != nil {
		asked = truncateRunes(strings.TrimSpace(*sent), MaxContentRunes)
	}
	now := s.now()

	var existing *dbgen.AiConversation
	var history []ai.Message
	err := db.InTenant(ctx, s.pool, caller.TenantID, func(q *dbgen.Queries) error {
		if in.ConversationID == nil {
			history = []ai.Message{{Role: ai.RoleUser, Content: asked}}
			return nil
		}
		c, err := q.GetAIConversation(ctx, dbgen.GetAIConversationParams{StoreID: caller.StoreID, StaffID: caller.StaffID, ID: *in.ConversationID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ops.ErrNotFound
		}
		if err != nil {
			return err
		}
		existing = &c
		rows, err := q.ListRecentAIConversationMessages(ctx, dbgen.ListRecentAIConversationMessagesParams{StoreID: caller.StoreID, ConversationID: c.ID, Limit: historyMessages})
		if err != nil {
			return err
		}
		history = buildHistory(rows, asked)
		return nil
	})
	if err != nil {
		return ChatAnswer{}, err
	}

	answer, err := s.chat.Ask(ctx, caller, in.DraftID, history)
	if err != nil {
		return ChatAnswer{}, err
	}

	var conversation dbgen.AiConversation
	err = db.InTenant(ctx, s.pool, caller.TenantID, func(q *dbgen.Queries) error {
		if existing != nil {
			c, err := q.TouchAIConversation(ctx, dbgen.TouchAIConversationParams{StoreID: caller.StoreID, StaffID: caller.StaffID, ID: existing.ID, UpdatedAt: stamp(now)})
			if errors.Is(err, pgx.ErrNoRows) {
				return ops.ErrNotFound
			}
			if err != nil {
				return err
			}
			conversation = c
		} else {
			c, err := q.CreateAIConversation(ctx, dbgen.CreateAIConversationParams{TenantID: caller.TenantID, StoreID: caller.StoreID, StaffID: caller.StaffID, Title: conversationTitle(content), CreatedAt: stamp(now)})
			if err != nil {
				return err
			}
			conversation = c
		}
		if _, err := q.AddAIConversationMessage(ctx, dbgen.AddAIConversationMessageParams{
			TenantID: caller.TenantID, StoreID: caller.StoreID, ConversationID: conversation.ID,
			Role: ai.RoleUser, Content: content, Sent: sent, Editing: editing, DraftIds: []uuid.UUID{}, CreatedAt: stamp(now),
		}); err != nil {
			return err
		}
		ids := make([]uuid.UUID, len(answer.Drafts))
		for i, view := range answer.Drafts {
			ids[i] = view.ID
		}
		_, err := q.AddAIConversationMessage(ctx, dbgen.AddAIConversationMessageParams{
			TenantID: caller.TenantID, StoreID: caller.StoreID, ConversationID: conversation.ID,
			Role: ai.RoleAssistant, Content: answer.Reply, DraftIds: ids, CreatedAt: stamp(now),
		})
		return err
	})
	if err != nil {
		return ChatAnswer{}, err
	}
	return ChatAnswer{Answer: answer, ConversationID: conversation.ID, Title: conversation.Title}, nil
}

// Register 声明历史会话的四个业务操作：查看列表、打开会话、归档和解开归档。
func Register(r *ops.Registry) {
	s := service{registry: r}
	ops.Register(r, ops.Operation[ListConversationsInput, ConversationList]{
		Name: "list_ai_conversations", Summary: "列出本人的 AI 历史会话", Kind: ops.Read, Permission: ops.LoggedIn, AI: ops.AINone, Handle: s.list,
	})
	ops.Register(r, ops.Operation[ConversationIDInput, ConversationDetail]{
		Name: "get_ai_conversation", Summary: "打开本人的 AI 历史会话", Kind: ops.Read, Permission: ops.LoggedIn, AI: ops.AINone, Handle: s.get,
	})
	ops.Register(r, ops.Operation[ConversationIDInput, ConversationSummary]{
		Name: "archive_ai_conversation", Summary: "归档本人的 AI 历史会话", Kind: ops.Write, Permission: ops.LoggedIn, AI: ops.AINone, Handle: s.archive,
	})
	ops.Register(r, ops.Operation[ConversationIDInput, ConversationSummary]{
		Name: "unarchive_ai_conversation", Summary: "解开本人 AI 历史会话的归档", Kind: ops.Write, Permission: ops.LoggedIn, AI: ops.AINone, Handle: s.unarchive,
	})
}

type service struct {
	registry *ops.Registry
}

func (s service) list(ctx context.Context, env ops.Env, in *ListConversationsInput) (*ConversationList, error) {
	query := strings.TrimSpace(in.Q)
	if utf8.RuneCountInString(query) > MaxQueryRunes {
		return nil, &ops.ToolError{Messages: []string{fmt.Sprintf("搜索内容最多 %d 字", MaxQueryRunes)}}
	}
	rows, err := env.Q.ListAIConversations(ctx, dbgen.ListAIConversationsParams{
		StoreID: env.Caller.StoreID, StaffID: env.Caller.StaffID,
		Archived: in.Archived, Query: query, Pattern: likePattern(query),
	})
	if err != nil {
		return nil, err
	}
	out := &ConversationList{Conversations: []ConversationSummary{}}
	for _, c := range rows {
		out.Conversations = append(out.Conversations, conversationSummary(c))
	}
	return out, nil
}

func (s service) get(ctx context.Context, env ops.Env, in *ConversationIDInput) (*ConversationDetail, error) {
	c, err := env.Q.GetAIConversation(ctx, dbgen.GetAIConversationParams{StoreID: env.Caller.StoreID, StaffID: env.Caller.StaffID, ID: in.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ops.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := env.Q.ListAIConversationMessages(ctx, dbgen.ListAIConversationMessagesParams{StoreID: env.Caller.StoreID, ConversationID: c.ID})
	if err != nil {
		return nil, err
	}
	ids := []uuid.UUID{}
	seen := map[uuid.UUID]bool{}
	for _, m := range rows {
		for _, id := range m.DraftIds {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	views, err := drafts.ViewsByIDs(ctx, s.registry, env, ids)
	if err != nil {
		return nil, err
	}
	out := &ConversationDetail{ID: c.ID, Title: c.Title, Archived: c.ArchivedAt.Valid, Messages: []ConversationMessage{}}
	for _, m := range rows {
		item := ConversationMessage{ID: m.ID, Role: m.Role, Content: m.Content, Editing: m.Editing, CreatedAt: m.CreatedAt.Time, Drafts: []drafts.DraftView{}}
		for _, id := range m.DraftIds {
			if view, ok := views[id]; ok {
				item.Drafts = append(item.Drafts, view)
			}
		}
		out.Messages = append(out.Messages, item)
	}
	return out, nil
}

// 归档和解开归档只是本人的界面整理，不改业务数据，按任务约定不写操作日志。
func (s service) archive(ctx context.Context, env ops.Env, in *ConversationIDInput) (*ConversationSummary, error) {
	c, err := env.Q.SetAIConversationArchived(ctx, dbgen.SetAIConversationArchivedParams{
		StoreID: env.Caller.StoreID, StaffID: env.Caller.StaffID, ID: in.ID, ArchivedAt: stamp(env.Now),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ops.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	out := conversationSummary(c)
	return &out, nil
}

func (s service) unarchive(ctx context.Context, env ops.Env, in *ConversationIDInput) (*ConversationSummary, error) {
	c, err := env.Q.SetAIConversationUnarchived(ctx, dbgen.SetAIConversationUnarchivedParams{StoreID: env.Caller.StoreID, StaffID: env.Caller.StaffID, ID: in.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ops.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	out := conversationSummary(c)
	return &out, nil
}

func conversationSummary(c dbgen.AiConversation) ConversationSummary {
	return ConversationSummary{ID: c.ID, Title: c.Title, UpdatedAt: c.UpdatedAt.Time, Archived: c.ArchivedAt.Valid}
}

func stamp(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func trimmedOrNil(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func conversationTitle(content string) string {
	title := strings.TrimSpace(content)
	title = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(title)
	if utf8.RuneCountInString(title) > maxTitleRunes {
		title = string([]rune(title)[:maxTitleRunes])
	}
	return title
}

func buildHistory(rows []dbgen.AiConversationMessage, content string) []ai.Message {
	messages := make([]ai.Message, 0, len(rows)+1)
	for _, row := range rows {
		text := row.Content
		if row.Sent != nil {
			text = *row.Sent
		}
		text = truncateRunes(strings.TrimSpace(text), MaxContentRunes)
		if text == "" {
			continue
		}
		messages = append(messages, ai.Message{Role: row.Role, Content: text})
	}
	messages = append(messages, ai.Message{Role: ai.RoleUser, Content: content})
	for len(messages) > 0 && messages[0].Role != ai.RoleUser {
		messages = messages[1:]
	}
	return messages
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

func likePattern(q string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
	return "%" + escaped + "%"
}
