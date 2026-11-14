package main

import (
	"strings"
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/ai/eval"
)

func TestRunRequiresAPIKey(t *testing.T) {
	t.Setenv("AI_API_KEY", "")
	err := run([]string{"run"})
	if err == nil || !strings.Contains(err.Error(), "AI_API_KEY 未设置") {
		t.Fatalf("err = %v", err)
	}
}

func TestHostOnly(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://api.deepseek.com/v1", "api.deepseek.com"},
		{"api.deepseek.com", "api.deepseek.com"},
		{"api.example.com/v1", "api.example.com"},
		{"http://127.0.0.1:8080/chat", "127.0.0.1:8080"},
		{"https://example.com:8443/v1/chat?key=value", "example.com:8443"},
	} {
		if got := hostOnly(tc.in); got != tc.want {
			t.Fatalf("hostOnly(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestReplayAndCompareArgs(t *testing.T) {
	cassette, out, err := replayArgs([]string{"case.json", "--out", "report.json"})
	if err != nil || cassette != "case.json" || out != "report.json" {
		t.Fatalf("replayArgs = %q %q %v", cassette, out, err)
	}
	files, allow, err := compareArgs([]string{"old.json", "new.json", "--allow-different-cases"})
	if err != nil || !allow || len(files) != 2 || files[0] != "old.json" || files[1] != "new.json" {
		t.Fatalf("compareArgs = %v %v %v", files, allow, err)
	}
	if _, _, err := replayArgs(nil); err == nil {
		t.Fatal("replayArgs should require a cassette")
	}
	if _, _, err := compareArgs([]string{"one.json"}); err == nil {
		t.Fatal("compareArgs should require two reports")
	}
}

func TestCleanupErrorsNonZero(t *testing.T) {
	tb := eval.NewCLITB()
	tb.Error("cleanup boom")
	err := checkCleanup(tb)
	if err == nil || !strings.Contains(err.Error(), "cleanup boom") || !strings.Contains(err.Error(), "清理失败") {
		t.Fatalf("err = %v", err)
	}
}
