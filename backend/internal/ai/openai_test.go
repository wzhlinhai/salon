package ai_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wzhlinhai/salon/backend/internal/ai"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

func TestOpenAIRequestAndToolCalls(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-1","object":"chat.completion","created":1,"model":"deepseek-flash",
			"choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","content":"","tool_calls":[
				{"id":"call_1","type":"function","function":{"name":"list_performance","arguments":"{\"date_from\":\"2026-11-05\",\"date_to\":\"2026-11-05\"}"}}
			]}}]
		}`))
	}))
	defer server.Close()

	model := ai.NewOpenAIModel(ai.OpenAIConfig{BaseURL: server.URL, APIKey: "test-key", Model: "deepseek-flash"})
	message, err := model.Chat(context.Background(), []ai.Message{{Role: ai.RoleUser, Content: "查今天业绩"}}, []ops.Tool{{
		Name:        "list_performance",
		Description: "按营业日查询业绩",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"date_from": map[string]any{"type": "string", "format": "date"},
				"date_to":   map[string]any{"type": "string", "format": "date"},
			},
			"required": []string{"date_from", "date_to"},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if message.Content != "" || len(message.ToolCalls) != 1 || message.ToolCalls[0].ID != "call_1" || message.ToolCalls[0].Name != "list_performance" || message.ToolCalls[0].Arguments != `{"date_from":"2026-11-05","date_to":"2026-11-05"}` {
		t.Fatalf("message = %+v", message)
	}
	if request["model"] != "deepseek-flash" {
		t.Fatalf("model = %v", request["model"])
	}
	if _, ok := request["thinking"]; ok {
		t.Fatal("DeepSeek-specific thinking must not be sent")
	}
	tools := request["tools"].([]any)
	function := tools[0].(map[string]any)["function"].(map[string]any)
	if tools[0].(map[string]any)["type"] != "function" || function["name"] != "list_performance" {
		t.Fatalf("tools = %v", tools)
	}
	if _, ok := function["strict"]; ok {
		t.Fatal("strict mode is not enabled in P0-30")
	}
	parameters := function["parameters"].(map[string]any)
	if parameters["type"] != "object" || parameters["properties"].(map[string]any)["date_from"] == nil {
		t.Fatalf("parameters = %v", parameters)
	}
}

func TestOpenAIRejectsMalformedToolArguments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","created":1,"model":"deepseek-flash","choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"broken","arguments":"{"}}]}}]}`))
	}))
	defer server.Close()
	model := ai.NewOpenAIModel(ai.OpenAIConfig{BaseURL: server.URL, APIKey: "test-key"})
	_, err := model.Chat(context.Background(), []ai.Message{{Role: ai.RoleUser, Content: "x"}}, nil)
	var modelErr *ai.ModelError
	if !errors.As(err, &modelErr) {
		t.Fatalf("not a ModelError: %T %v", err, err)
	}
	if !errors.Is(err, ai.ErrMalformedResponse) {
		t.Fatalf("not malformed: %T %v", modelErr.Err, modelErr.Err)
	}
}

func TestOpenAIHTTPErrorsAreTyped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"boom","type":"server_error","param":null,"code":null}}`))
	}))
	defer server.Close()
	model := ai.NewOpenAIModel(ai.OpenAIConfig{BaseURL: server.URL, APIKey: "test-key"})
	_, err := model.Chat(context.Background(), []ai.Message{{Role: ai.RoleUser, Content: "x"}}, nil)
	var modelErr *ai.ModelError
	if !errors.As(err, &modelErr) || modelErr.Status != http.StatusInternalServerError || !errors.Is(err, ai.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenAITimeoutIsTyped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	model := ai.NewOpenAIModel(ai.OpenAIConfig{BaseURL: server.URL, APIKey: "test-key", HTTPClient: &http.Client{Timeout: 20 * time.Millisecond}})
	_, err := model.Chat(context.Background(), []ai.Message{{Role: ai.RoleUser, Content: "x"}}, nil)
	var modelErr *ai.ModelError
	if !errors.As(err, &modelErr) || !errors.Is(err, ai.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenAIEmptyChoicesIsMalformed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","created":1,"model":"deepseek-flash","choices":[]}`))
	}))
	defer server.Close()
	model := ai.NewOpenAIModel(ai.OpenAIConfig{BaseURL: server.URL, APIKey: "test-key"})
	_, err := model.Chat(context.Background(), []ai.Message{{Role: ai.RoleUser, Content: "x"}}, nil)
	if !errors.Is(err, ai.ErrMalformedResponse) {
		t.Fatalf("err = %v, want ErrMalformedResponse", err)
	}
}

func TestOpenAIEmptyToolArgumentsBecomeObject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","created":1,"model":"deepseek-flash","choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_me","arguments":""}}]}}]}`))
	}))
	defer server.Close()
	model := ai.NewOpenAIModel(ai.OpenAIConfig{BaseURL: server.URL, APIKey: "test-key"})
	message, err := model.Chat(context.Background(), []ai.Message{{Role: ai.RoleUser, Content: "x"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(message.ToolCalls) != 1 || message.ToolCalls[0].Arguments != "{}" {
		t.Fatalf("tool calls = %+v", message.ToolCalls)
	}
}

func TestOpenAIOutboundMessages(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","created":1,"model":"deepseek-flash","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"完成"}}]}`))
	}))
	defer server.Close()
	model := ai.NewOpenAIModel(ai.OpenAIConfig{BaseURL: server.URL, APIKey: "test-key"})
	message, err := model.Chat(context.Background(), []ai.Message{
		{Role: ai.RoleSystem, Content: "系统"},
		{Role: ai.RoleUser, Content: "问题"},
		{Role: ai.RoleAssistant, Content: "先查一下", ToolCalls: []ai.ToolCall{{ID: "call_1", Name: "get_me", Arguments: "{}"}}},
		{Role: ai.RoleTool, Content: "结果", ToolCallID: "call_1"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if message.Content != "完成" || len(message.ToolCalls) != 0 {
		t.Fatalf("message = %+v", message)
	}
	messages := request["messages"].([]any)
	if len(messages) != 4 {
		t.Fatalf("messages = %v", messages)
	}
	if messages[0].(map[string]any)["role"] != "system" || messages[0].(map[string]any)["content"] != "系统" ||
		messages[1].(map[string]any)["role"] != "user" || messages[1].(map[string]any)["content"] != "问题" {
		t.Fatalf("messages = %v", messages)
	}
	assistant := messages[2].(map[string]any)
	if assistant["role"] != "assistant" || assistant["content"] != "先查一下" {
		t.Fatalf("assistant = %v", assistant)
	}
	calls := assistant["tool_calls"].([]any)
	function := calls[0].(map[string]any)["function"].(map[string]any)
	if calls[0].(map[string]any)["id"] != "call_1" || function["name"] != "get_me" || function["arguments"] != "{}" {
		t.Fatalf("tool calls = %v", calls)
	}
	tool := messages[3].(map[string]any)
	if tool["role"] != "tool" || tool["content"] != "结果" || tool["tool_call_id"] != "call_1" {
		t.Fatalf("tool message = %v", tool)
	}
}
