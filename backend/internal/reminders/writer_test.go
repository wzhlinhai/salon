package reminders_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/reminders"
)

func writerRule(f *fixture) reminders.Rule {
	return matchRule("writer_rule", func(*reminders.Rule) {}, func(_ uuid.UUID, _ time.Time) []reminders.Match {
		return []reminders.Match{ownerMatch(f.Shop, "2026-11-26", map[string]any{"date": "2026-11-26", "count": 1})}
	})
}

func TestWriterStoresAIText(t *testing.T) {
	f := open(t)
	model := &fakeModel{reply: "昨天（11月26日）删单 1 笔。"}
	runRules(t, f.Shop, at("2026-11-27", 10, 0), model, []reminders.Rule{writerRule(f)})
	rows := remindersByRule(t, f.Shop, "writer_rule")
	if len(rows) != 1 || rows[0].AiText == nil || *rows[0].AiText != model.reply {
		t.Fatalf("rows = %+v", rows)
	}
	if strings.Contains(model.prompt(), "王老板") {
		t.Fatalf("prompt contains recipient name: %s", model.prompt())
	}
}

func TestWriterRejectsUnknownNumber(t *testing.T) {
	f := open(t)
	model := &fakeModel{reply: "昨天（11月26日）删单 7 笔。"}
	runRules(t, f.Shop, at("2026-11-27", 10, 0), model, []reminders.Rule{writerRule(f)})
	rows := remindersByRule(t, f.Shop, "writer_rule")
	if len(rows) != 1 || rows[0].AiText != nil {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestWriterKeepsReminderOnModelError(t *testing.T) {
	f := open(t)
	model := &fakeModel{err: errors.New("模型不可用")}
	runRules(t, f.Shop, at("2026-11-27", 10, 0), model, []reminders.Rule{writerRule(f)})
	rows := remindersByRule(t, f.Shop, "writer_rule")
	if len(rows) != 1 || rows[0].AiText != nil || rows[0].Outcome != "emitted" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestWriterKeepsReminderWithoutModel(t *testing.T) {
	f := open(t)
	runRules(t, f.Shop, at("2026-11-27", 10, 0), nil, []reminders.Rule{writerRule(f)})
	rows := remindersByRule(t, f.Shop, "writer_rule")
	if len(rows) != 1 || rows[0].AiText != nil || rows[0].Outcome != "emitted" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestWriterAllowsDatesAndAmountsFromFacts(t *testing.T) {
	facts := []byte(`{"date":"2026-11-26","amount":"12.50","count":1}`)
	rule := reminders.Rule{Name: "测试规则"}
	for _, reply := range []string{
		"11月26日收到 12.50 元，共 1 笔。",
		"昨天（11月26日）有 1 笔，12.5 元。",
		"2026年11月26日，1 笔 12.50 元。",
	} {
		model := &fakeModel{reply: reply}
		if text, ok := reminders.GenerateText(t.Context(), model, rule, facts); !ok || text != reply {
			t.Fatalf("reply %q rejected", reply)
		}
	}
}

func TestWriterRejectsEmptyAndLongText(t *testing.T) {
	facts := []byte(`{"date":"2026-11-26","count":1}`)
	rule := reminders.Rule{Name: "测试规则"}
	for _, reply := range []string{"", strings.Repeat("字", 121)} {
		model := &fakeModel{reply: reply}
		if _, ok := reminders.GenerateText(t.Context(), model, rule, facts); ok {
			t.Fatalf("reply %q accepted", reply)
		}
	}
}
