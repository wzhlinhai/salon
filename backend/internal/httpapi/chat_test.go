package httpapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/ai"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

type countingModel struct{ called bool }

func (m *countingModel) Chat(context.Context, []ai.Message, []ops.Tool) (ai.Message, error) {
	m.called = true
	return ai.Message{}, errors.New("model must not be called")
}

func TestChatRejectsCustomerCaller(t *testing.T) {
	_, api := humatest.New(t)
	registry := ops.NewRegistry(nil, api, time.Now)
	model := &countingModel{}
	chat := ai.NewChat(registry, func() ai.Model { return model }, time.Now, nil)
	customer := uuid.New()
	ctx := ops.WithCaller(context.Background(), ops.Caller{TenantID: uuid.New(), StoreID: uuid.New(), CustomerID: &customer})
	in := &chatInput{}
	in.Body.Messages = []chatMessage{{Role: ai.RoleUser, Content: "你好"}}
	_, err := chatHandler(chat)(ctx, in)
	var status huma.StatusError
	if !errors.As(err, &status) || status.GetStatus() != 401 || err.Error() != "unauthenticated" {
		t.Fatalf("err = %v", err)
	}
	if model.called {
		t.Fatal("chat model called for a customer caller")
	}
}
