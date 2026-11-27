package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/ai"
	"github.com/wzhlinhai/salon/backend/internal/aiconversations"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

type chatInput struct {
	Body struct {
		ConversationID *uuid.UUID `json:"conversation_id,omitempty" format:"uuid" doc:"要接着聊的本人历史会话；不填时新建对话"`
		Content        string     `json:"content" doc:"用户说的这句话，最多 4000 字"`
		Sent           string     `json:"sent,omitempty" doc:"实际发给模型的话，修改草稿时带草稿编号；最多 4000 字"`
		Editing        string     `json:"editing,omitempty" doc:"正在修改的草稿摘要，用于显示「正在改：…」"`
		DraftID        *uuid.UUID `json:"draft_id,omitempty" doc:"人点了「修改」的那张草稿；本次对话只允许改这张和本次新起草的"`
	}
}

type chatOutput struct {
	Body aiconversations.ChatAnswer
}

func registerChat(api huma.API, chat *aiconversations.ChatService) {
	huma.Register(api, huma.Operation{
		OperationID: "ai_chat",
		Method:      http.MethodPost,
		Path:        "/api/ai/chat",
		Summary:     "AI 对话：按本人权限查询营业数据、会员余额和提成",
	}, chatHandler(chat))
}

func chatHandler(chat *aiconversations.ChatService) func(context.Context, *chatInput) (*chatOutput, error) {
	return func(ctx context.Context, in *chatInput) (*chatOutput, error) {
		caller, ok := ops.CallerFrom(ctx)
		if !ok || caller.StaffID == uuid.Nil {
			return nil, huma.Error401Unauthorized("unauthenticated")
		}
		input, err := chatRequest(in)
		if err != nil {
			return nil, err
		}
		answer, err := chat.Ask(ctx, caller, input)
		switch {
		case errors.Is(err, ai.ErrNotConfigured):
			return nil, huma.Error503ServiceUnavailable("AI 未启用")
		case errors.Is(err, ai.ErrToolLimit):
			return nil, huma.Error502BadGateway("AI 调用工具次数超过上限，请换个问法")
		case errors.Is(err, ai.ErrTimeout):
			return nil, huma.Error502BadGateway("AI 对话超时，请稍后再试")
		case err != nil:
			if status := ops.StatusOf(err); status != http.StatusInternalServerError {
				return nil, huma.NewError(status, err.Error())
			}
			return nil, huma.Error502BadGateway("AI 暂时不可用，请稍后再试")
		}
		return &chatOutput{Body: answer}, nil
	}
}

func chatRequest(in *chatInput) (aiconversations.ChatInput, error) {
	content := strings.TrimSpace(in.Body.Content)
	if content == "" {
		return aiconversations.ChatInput{}, huma.Error422UnprocessableEntity("消息内容不能为空")
	}
	if utf8.RuneCountInString(content) > aiconversations.MaxContentRunes {
		return aiconversations.ChatInput{}, huma.Error422UnprocessableEntity("单条消息最多 4000 字")
	}
	if utf8.RuneCountInString(strings.TrimSpace(in.Body.Sent)) > aiconversations.MaxContentRunes {
		return aiconversations.ChatInput{}, huma.Error422UnprocessableEntity("单条消息最多 4000 字")
	}
	return aiconversations.ChatInput{
		ConversationID: in.Body.ConversationID,
		Content:        content,
		Sent:           in.Body.Sent,
		Editing:        in.Body.Editing,
		DraftID:        in.Body.DraftID,
	}, nil
}
