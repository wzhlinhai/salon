package ai

import (
	"strings"
	"testing"
	"time"

	"github.com/wzhlinhai/salon/backend/internal/ops"
)

func TestSystemPromptSanitizesCallerIdentity(t *testing.T) {
	name := "王\n老\t板" + strings.Repeat("名", 40)
	role := "店\n长" + strings.Repeat("角", 30)
	caller := ops.Caller{Name: name, Roles: []string{role, "\x00", "老板"}}
	prompt := systemPrompt(caller, time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC))

	wantName := "王老板" + strings.Repeat("名", 27)
	wantRole := "店长" + strings.Repeat("角", 18)
	if !strings.Contains(prompt, "员工："+wantName+"；") {
		t.Fatalf("prompt name not sanitized: %q", prompt)
	}
	if !strings.Contains(prompt, "角色："+wantRole+"、老板。") {
		t.Fatalf("prompt roles not sanitized: %q", prompt)
	}
	for _, leaked := range []string{"王\n老", "\t", strings.Repeat("名", 28), strings.Repeat("角", 19)} {
		if strings.Contains(prompt, leaked) {
			t.Fatalf("prompt leaked %q: %q", leaked, prompt)
		}
	}
}
