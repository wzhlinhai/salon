package ai_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/wzhlinhai/salon/backend/internal/ai"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

type internalFailureInput struct{}
type internalFailureOutput struct{}
type largeOutput struct {
	Text string `json:"text"`
}

func TestChatHidesInternalToolErrors(t *testing.T) {
	s := optest.OpenShop(t)
	ops.Register(s.Registry, ops.Operation[internalFailureInput, internalFailureOutput]{
		Name: "internal_failure", Summary: "测试内部错误脱敏", Kind: ops.Read, Permission: ops.LoggedIn, AI: ops.AIDirect,
		Handle: func(context.Context, ops.Env, *internalFailureInput) (*internalFailureOutput, error) {
			return nil, errors.New("database connection failed: password=secret db=salon")
		},
	})
	var toolText string
	fake := &fakeModel{reply: func(call int, messages []ai.Message, _ []ops.Tool) (ai.Message, error) {
		if call == 1 {
			return toolCall("call_1", "internal_failure", "{}"), nil
		}
		toolText = lastToolResult(messages)
		return assistant(toolText), nil
	}}
	s.SetAIModel(fake)
	r := chatCall(t, s, s.Owner, []map[string]any{user("触发内部错误")})
	if r.Status != http.StatusOK {
		t.Fatalf("chat = %d %v", r.Status, r.Body)
	}
	source := r.Body["sources"].([]any)[0].(map[string]any)
	if source["ok"] != false || source["error"] != "工具执行失败" {
		t.Fatalf("source = %v", source)
	}
	raw, err := json.Marshal(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"database connection", "password", "secret", "salon"} {
		if strings.Contains(string(raw), leaked) || strings.Contains(toolText, leaked) {
			t.Fatalf("internal error leaked %q: response=%s tool=%s", leaked, raw, toolText)
		}
	}
	if toolText != "工具执行失败" {
		t.Fatalf("model saw %q", toolText)
	}
}

func TestChatToolCallLimitPerResponse(t *testing.T) {
	s := optest.OpenShop(t)
	fake := &fakeModel{reply: func(int, []ai.Message, []ops.Tool) (ai.Message, error) {
		calls := make([]ai.ToolCall, ai.MaxToolCallsPerTurn+1)
		for i := range calls {
			calls[i] = ai.ToolCall{ID: fmt.Sprintf("call_%d", i+1), Name: "get_me", Arguments: "{}"}
		}
		return ai.Message{Role: ai.RoleAssistant, ToolCalls: calls}, nil
	}}
	s.SetAIModel(fake)
	r := chatCall(t, s, s.Owner, []map[string]any{user("一次调用很多工具")})
	if r.Status != http.StatusBadGateway || r.Body["detail"] != "AI 调用工具次数超过上限，请换个问法" {
		t.Fatalf("limit = %d %v", r.Status, r.Body)
	}
	if got := len(fake.requests()); got != 1 {
		t.Fatalf("model calls = %d, want 1", got)
	}
}

func TestChatTimeoutReturnsBadGateway(t *testing.T) {
	s := optest.OpenShop(t)
	s.SetAIModel(&fakeModel{reply: func(int, []ai.Message, []ops.Tool) (ai.Message, error) {
		return ai.Message{}, ai.ErrTimeout
	}})
	r := chatCall(t, s, s.Owner, []map[string]any{user("慢一点")})
	if r.Status != http.StatusBadGateway || r.Body["detail"] != "AI 对话超时，请稍后再试" {
		t.Fatalf("timeout = %d %v", r.Status, r.Body)
	}
}

func TestChatTruncatesToolResult(t *testing.T) {
	s := optest.OpenShop(t)
	ops.Register(s.Registry, ops.Operation[internalFailureInput, largeOutput]{
		Name: "large_result", Summary: "测试工具结果截断", Kind: ops.Read, Permission: ops.LoggedIn, AI: ops.AIDirect,
		Handle: func(context.Context, ops.Env, *internalFailureInput) (*largeOutput, error) {
			return &largeOutput{Text: strings.Repeat("字", ai.MaxToolResultRunes)}, nil
		},
	})
	var toolText string
	fake := &fakeModel{reply: func(call int, messages []ai.Message, _ []ops.Tool) (ai.Message, error) {
		if call == 1 {
			return toolCall("call_1", "large_result", "{}"), nil
		}
		toolText = lastToolResult(messages)
		return assistant("结果已处理。"), nil
	}}
	s.SetAIModel(fake)
	r := chatCall(t, s, s.Owner, []map[string]any{user("取一个很长的结果")})
	if r.Status != http.StatusOK {
		t.Fatalf("chat = %d %v", r.Status, r.Body)
	}
	marker := "（结果过长，已截断）"
	if !strings.HasSuffix(toolText, marker) || utf8.RuneCountInString(toolText) != ai.MaxToolResultRunes+utf8.RuneCountInString(marker) {
		t.Fatalf("tool result runes = %d, suffix marker = %v", utf8.RuneCountInString(toolText), strings.HasSuffix(toolText, marker))
	}
	source := r.Body["sources"].([]any)[0].(map[string]any)
	if source["ok"] != true || source["error"] != nil || strings.Contains(fmt.Sprint(source), marker) {
		t.Fatalf("source was truncated: %v", source)
	}
}

func TestChatPromptUsesBusinessDate(t *testing.T) {
	s := optest.OpenShop(t)
	day := settings.Date(s.Now())
	effective := day.AddDate(0, 0, 1)
	late := day.AddDate(0, 0, 2)
	s.Call(t, s.Owner, "change_setting", map[string]any{"key": "business_day_end", "value": "02:00", "effective_from": effective.Format(settings.DateLayout)}, 200)
	s.SetNow(time.Date(late.Year(), late.Month(), late.Day(), 1, 0, 0, 0, settings.Location))
	fake := &fakeModel{reply: func(int, []ai.Message, []ops.Tool) (ai.Message, error) { return assistant("好。"), nil }}
	s.SetAIModel(fake)
	if r := chatCall(t, s, s.Owner, []map[string]any{user("现在几号")}); r.Status != http.StatusOK {
		t.Fatal(r)
	}
	prompt := fake.requests()[0].Messages[0].Content
	if !strings.Contains(prompt, effective.Format(settings.DateLayout)) || strings.Contains(prompt, late.Format(settings.DateLayout)) {
		t.Fatalf("prompt = %q, want business date %s not natural date %s", prompt, effective.Format(settings.DateLayout), late.Format(settings.DateLayout))
	}
}
