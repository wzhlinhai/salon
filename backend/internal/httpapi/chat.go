package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"

	"github.com/wzhlinhai/salon/backend/internal/ai"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

const (
	// MaxChatMessages 是一次 AI 对话最多携带的历史消息条数。
	MaxChatMessages = 20
	// MaxChatContentLength 是单条消息最多的字符数。
	MaxChatContentLength = 4000
)

type chatMessage struct {
	Role    string `json:"role" enum:"user,assistant" doc:"user 或 assistant"`
	Content string `json:"content" doc:"消息内容，最多 4000 字"`
}

type chatInput struct {
	Body struct {
		Messages []chatMessage `json:"messages" doc:"对话历史，最多 20 条"`
	}
}

type chatOutput struct {
	Body ai.Answer
}

func registerChat(api huma.API, chat *ai.Chat) {
	huma.Register(api, huma.Operation{
		OperationID: "ai_chat",
		Method:      http.MethodPost,
		Path:        "/api/ai/chat",
		Summary:     "AI 对话：按本人权限查询营业数据、会员余额和提成",
	}, func(ctx context.Context, in *chatInput) (*chatOutput, error) {
		caller, ok := ops.CallerFrom(ctx)
		if !ok {
			return nil, huma.Error401Unauthorized("unauthenticated")
		}
		history, err := chatHistory(in)
		if err != nil {
			return nil, err
		}
		answer, err := chat.Ask(ctx, caller, history)
		switch {
		case errors.Is(err, ai.ErrNotConfigured):
			return nil, huma.Error503ServiceUnavailable("AI 未启用")
		case errors.Is(err, ai.ErrToolLimit):
			return nil, huma.Error502BadGateway("AI 调用工具次数超过上限，请换个问法")
		case errors.Is(err, ai.ErrTimeout):
			return nil, huma.Error502BadGateway("AI 对话超时，请稍后再试")
		case err != nil:
			return nil, huma.Error502BadGateway("AI 暂时不可用，请稍后再试")
		}
		return &chatOutput{Body: answer}, nil
	})
}

func chatHistory(in *chatInput) ([]ai.Message, error) {
	if len(in.Body.Messages) == 0 || len(in.Body.Messages) > MaxChatMessages {
		return nil, huma.Error422UnprocessableEntity("消息最多 20 条，且至少一条")
	}
	out := make([]ai.Message, 0, len(in.Body.Messages))
	for _, message := range in.Body.Messages {
		if message.Role != ai.RoleUser && message.Role != ai.RoleAssistant {
			return nil, huma.Error422UnprocessableEntity("消息角色只能是 user 或 assistant")
		}
		if strings.TrimSpace(message.Content) == "" {
			return nil, huma.Error422UnprocessableEntity("消息内容不能为空")
		}
		if utf8.RuneCountInString(message.Content) > MaxChatContentLength {
			return nil, huma.Error422UnprocessableEntity("单条消息最多 4000 字")
		}
		out = append(out, ai.Message{Role: message.Role, Content: message.Content})
	}
	return out, nil
}
