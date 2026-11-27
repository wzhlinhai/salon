package reminders

import (
	"context"
	"testing"
	"time"

	"github.com/wzhlinhai/salon/backend/internal/ai"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

type blockingModel struct{}

func (blockingModel) Chat(ctx context.Context, _ []ai.Message, _ []ops.Tool) (ai.Message, error) {
	<-ctx.Done()
	return ai.Message{}, ctx.Err()
}

func TestGenerateTextTimeout(t *testing.T) {
	previous := writerTimeout
	writerTimeout = 20 * time.Millisecond
	defer func() { writerTimeout = previous }()
	if _, ok := GenerateText(context.Background(), blockingModel{}, Rule{Name: "测试规则"}, []byte(`{"n":1}`)); ok {
		t.Fatal("blocking model returned text")
	}
}

func TestGenerateTextRejectsInvalidFacts(t *testing.T) {
	model := &countingModel{reply: "1"}
	if _, ok := GenerateText(context.Background(), model, Rule{Name: "测试规则"}, []byte(`not json`)); ok {
		t.Fatal("invalid facts accepted")
	}
}

type countingModel struct{ reply string }

func (m *countingModel) Chat(context.Context, []ai.Message, []ops.Tool) (ai.Message, error) {
	return ai.Message{Role: ai.RoleAssistant, Content: m.reply}, nil
}
