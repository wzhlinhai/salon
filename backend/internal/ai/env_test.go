package ai_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/ai"
)

func TestOpenAIConfigFromEnv(t *testing.T) {
	var headers http.Header
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = r.Header.Clone()
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","created":1,"model":"deepseek-v4.1-flash","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"完成"}}]}`))
	}))
	defer server.Close()
	t.Setenv("AI_API_KEY", "test-key")
	t.Setenv("AI_BASE_URL", server.URL)
	t.Setenv("AI_MODEL", "deepseek-v4.1-flash")
	t.Setenv("AI_EXTRA_HEADERS", `{"x-opencode-session":"salon"}`)

	cfg, err := ai.OpenAIConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKey != "test-key" || cfg.BaseURL != server.URL || cfg.Model != "deepseek-v4.1-flash" || cfg.Headers["X-Opencode-Session"] != "salon" {
		t.Fatalf("config = %+v", cfg)
	}
	model := ai.NewOpenAIModel(cfg)
	if _, err := model.Chat(context.Background(), []ai.Message{{Role: ai.RoleUser, Content: "x"}}, nil); err != nil {
		t.Fatal(err)
	}
	if got := headers.Get("x-opencode-session"); got != "salon" {
		t.Fatalf("x-opencode-session = %q", got)
	}
	if got := headers.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("Authorization = %q", got)
	}
	if request["model"] != "deepseek-v4.1-flash" {
		t.Fatalf("model = %v", request["model"])
	}
}
