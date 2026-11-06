package ai

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wzhlinhai/salon/backend/internal/ops"
)

type blockingModel struct{}

func (blockingModel) Chat(ctx context.Context, _ []Message, _ []ops.Tool) (Message, error) {
	time.Sleep(100 * time.Millisecond)
	return Message{Role: RoleAssistant, Content: "太慢了"}, nil
}

func TestChatAskTimeout(t *testing.T) {
	chat := &Chat{
		registry: ops.NewRegistry(nil, nil, time.Now),
		model:    func() Model { return blockingModel{} },
		now:      time.Now,
		timeout:  20 * time.Millisecond,
	}
	if _, err := chat.Ask(context.Background(), ops.Caller{}, nil); !errors.Is(err, ErrTimeout) {
		t.Fatalf("Ask err = %v, want ErrTimeout", err)
	}
}
