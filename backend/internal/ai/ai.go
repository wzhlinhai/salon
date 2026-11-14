// Package ai 是 AI 对话的模型适配与工具循环。
//
// 模型只通过 Model 接口访问；生产实现使用 openai-go 访问 DeepSeek 的 OpenAI 兼容接口，
// 测试使用脚本化的假模型，不访问网络。对话提供读工具、有权限的起草工具和修改草稿参数（ADR-0001、ADR-0004）。
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/drafts"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// Role 是对话消息的角色。
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

const (
	// MaxModelCalls 是一次对话最多调用模型的次数，超过后返回错误，避免工具循环失控。
	MaxModelCalls = 6
	// MaxToolCallsPerTurn 是模型一次响应最多携带的工具调用数。
	MaxToolCallsPerTurn = 8
	// MaxToolResultRunes 是一次工具结果放入模型上下文前最多保留的字符数。
	MaxToolResultRunes = 16000
	// MaxDraftCallsPerAsk 是一次对话最多起草或修改草稿的次数，包括失败的调用。
	MaxDraftCallsPerAsk = 5
	// AskTimeout 是一次对话的总期限，覆盖提示构造、模型调用和全部工具执行。
	AskTimeout = 120 * time.Second
	// toolResultTruncated 是工具结果被截断时追加的说明。
	toolResultTruncated = "（结果过长，已截断）"
	// internalToolError 是不向模型和 sources 暴露内部错误原文时的固定文字。
	internalToolError = "工具执行失败"
)

var (
	// ErrNotConfigured 表示没有配置 AI 模型，HTTP 返回 503。
	ErrNotConfigured = errors.New("AI 未启用")
	// ErrUnavailable 表示模型不可达或返回了无法使用的结果。
	ErrUnavailable = errors.New("AI 暂时不可用")
	// ErrToolLimit 表示模型在允许的次数内没有给出最终回答。
	ErrToolLimit = errors.New("AI 调用工具次数超过上限")
	// ErrTimeout 表示一次对话超过了总期限。
	ErrTimeout = errors.New("AI 对话超时")
	// ErrMalformedResponse 表示模型响应缺少选项或工具参数不是合法 JSON。
	ErrMalformedResponse = errors.New("AI 返回了无法解析的结果")
)

// ToolCall 是模型请求的一次函数调用。
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Message 是对话中的一条消息。assistant 消息可带 tool_calls，tool 消息带 tool_call_id。
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// Model 是一次聊天补全的模型适配接口。
type Model interface {
	Chat(ctx context.Context, messages []Message, tools []ops.Tool) (Message, error)
}

// ModelError 是模型访问失败的类型化错误。Status 是上游 HTTP 状态码，拿不到时为 0。
type ModelError struct {
	Status int
	Err    error
}

func (e *ModelError) Error() string { return ErrUnavailable.Error() }

func (e *ModelError) Unwrap() error { return e.Err }

// Is 让调用方用 errors.Is(err, ErrUnavailable) 判断模型不可用，同时保留底层错误。
func (e *ModelError) Is(target error) bool {
	return target == ErrUnavailable || (e.Err != nil && errors.Is(e.Err, target))
}

// Source 是一次工具调用的来源记录，用于把回答追溯到操作、参数和结果。
type Source struct {
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments"`
	OK        bool           `json:"ok"`
	Error     string         `json:"error,omitempty"`
}

// DraftView 是本次对话创建或修改的草稿及其只读预览。
type DraftView struct {
	drafts.Draft
	Preview      json.RawMessage `json:"preview,omitempty"`
	PreviewError string          `json:"preview_error,omitempty"`
}

// Answer 是一次对话的最终回答、按顺序记录的全部工具调用来源和本次创建或修改的草稿。
type Answer struct {
	Reply   string      `json:"reply"`
	Sources []Source    `json:"sources"`
	Drafts  []DraftView `json:"drafts"`
}

// Chat 执行工具循环（读、起草和修改草稿参数）。模型由 provider 提供，未配置时返回 ErrNotConfigured。
type Chat struct {
	registry     *ops.Registry
	model        func() Model
	now          func() time.Time
	businessDate BusinessDateFunc
	timeout      time.Duration
}

// BusinessDateFunc 把一个时刻换算成调用者门店的营业日。
type BusinessDateFunc func(ctx context.Context, caller ops.Caller, now time.Time) (time.Time, error)

// NewChat 返回对话服务。model 在每次请求时读取，便于测试替换且不把密钥放进注册表。
func NewChat(registry *ops.Registry, model func() Model, now func() time.Time, businessDate BusinessDateFunc) *Chat {
	return &Chat{registry: registry, model: model, now: now, businessDate: businessDate, timeout: AskTimeout}
}

// Ask 用调用者本人的权限回答一组 user/assistant 消息。
func (c *Chat) Ask(ctx context.Context, caller ops.Caller, history []Message) (Answer, error) {
	model := c.model()
	if model == nil {
		return Answer{}, ErrNotConfigured
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	caller.Channel = ops.ChannelAI
	now := c.now()
	day := settings.Date(now)
	if c.businessDate != nil {
		d, err := c.businessDate(ctx, caller, now)
		if err != nil {
			return Answer{}, askError(ctx, err)
		}
		day = d
	}
	tools := c.registry.ChatTools(caller)
	messages := make([]Message, 0, len(history)+1)
	messages = append(messages, Message{Role: RoleSystem, Content: systemPrompt(caller, day)})
	messages = append(messages, history...)
	sources := []Source{}
	answerDrafts := []DraftView{}
	draftIndex := map[uuid.UUID]int{}
	allowedDrafts := map[uuid.UUID]bool{}
	draftCalls := 0
	for range MaxModelCalls {
		message, err := model.Chat(ctx, messages, tools)
		if err != nil {
			return Answer{}, askError(ctx, err)
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return Answer{}, ErrTimeout
		}
		message.Role = RoleAssistant
		messages = append(messages, message)
		if len(message.ToolCalls) == 0 {
			return Answer{Reply: message.Content, Sources: sources, Drafts: answerDrafts}, nil
		}
		if len(message.ToolCalls) > MaxToolCallsPerTurn {
			return Answer{}, ErrToolLimit
		}
		for _, call := range message.ToolCalls {
			source := Source{Tool: call.Name, Arguments: objectArguments(call.Arguments)}
			draftCall := call.Name == "update_draft_params" || c.registry.DraftTool(call.Name)
			var raw json.RawMessage
			var err error
			if draftCall {
				draftCalls++
			}
			switch {
			case draftCall && draftCalls > MaxDraftCallsPerAsk:
				err = ops.Rule("一次对话最多起草 5 张草稿")
			case call.Name == "update_draft_params":
				draftID, ok := draftIDFromArguments(call.Arguments)
				if !ok || !allowedDrafts[draftID] {
					err = ops.Rule("只能修改本次对话起草的草稿")
				} else {
					raw, err = c.registry.CallChatTool(ctx, caller, call.Name, []byte(call.Arguments))
				}
			default:
				raw, err = c.registry.CallChatTool(ctx, caller, call.Name, []byte(call.Arguments))
			}
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return Answer{}, ErrTimeout
			}
			result := string(raw)
			if errors.Is(err, ops.ErrUnknownOperation) {
				source.Error = "工具不存在或不可用：" + call.Name
			} else if err != nil {
				source.Error = toolErrorText(err)
			} else {
				source.OK = true
			}
			if source.Error != "" {
				result = source.Error
			}
			if draftCall && err == nil {
				if view, ok := c.draftView(ctx, caller, now, raw); ok {
					allowedDrafts[view.ID] = true
					if i, exists := draftIndex[view.ID]; exists {
						answerDrafts[i] = view
					} else {
						draftIndex[view.ID] = len(answerDrafts)
						answerDrafts = append(answerDrafts, view)
					}
				}
			}
			sources = append(sources, source)
			messages = append(messages, Message{Role: RoleTool, Content: truncateToolResult(result), ToolCallID: call.ID})
		}
	}
	return Answer{}, ErrToolLimit
}

func draftIDFromArguments(raw string) (uuid.UUID, bool) {
	var in struct {
		ID uuid.UUID `json:"id"`
	}
	if json.Unmarshal([]byte(raw), &in) != nil || in.ID == uuid.Nil {
		return uuid.Nil, false
	}
	return in.ID, true
}

func (c *Chat) draftView(ctx context.Context, caller ops.Caller, now time.Time, raw json.RawMessage) (DraftView, bool) {
	var d drafts.Draft
	if json.Unmarshal(raw, &d) != nil {
		return DraftView{}, false
	}
	view := DraftView{Draft: d}
	preview, err := c.registry.PreviewDraft(ctx, ops.Env{Now: now, Caller: caller}, d.Operation, d.Params)
	if err != nil {
		if ops.StatusOf(err) >= 500 {
			slog.Error("draft preview failed", "err", err, "draft_id", d.ID, "operation", d.Operation)
			view.PreviewError = "预览生成失败"
		} else {
			view.PreviewError = err.Error()
		}
	} else {
		view.Preview = preview
	}
	return view, true
}

func askError(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ErrTimeout
	}
	return err
}

func toolErrorText(err error) string {
	if ops.StatusOf(err) >= 500 {
		return internalToolError
	}
	return err.Error()
}

func truncateToolResult(result string) string {
	if utf8.RuneCountInString(result) <= MaxToolResultRunes {
		return result
	}
	runes := []rune(result)
	return string(runes[:MaxToolResultRunes]) + toolResultTruncated
}

func objectArguments(raw string) map[string]any {
	out := map[string]any{}
	_ = json.Unmarshal([]byte(raw), &out)
	if out == nil {
		out = map[string]any{}
	}
	return out
}
