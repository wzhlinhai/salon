package ai

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/drafts"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

type previewProbeInput struct{}
type previewProbeOutput struct{}

func TestChatDraftViewPreviewPermissionError(t *testing.T) {
	_, api := humatest.New(t)
	registry := ops.NewRegistry(nil, api, time.Now)
	ops.Register(registry, ops.Operation[previewProbeInput, previewProbeOutput]{
		Name: "preview_probe", Summary: "预览测试", Kind: ops.Write, Permission: perm.OrderCreate, AI: ops.AIDraft,
		Handle: func(context.Context, ops.Env, *previewProbeInput) (*previewProbeOutput, error) {
			return &previewProbeOutput{}, nil
		},
	})
	registry.SetDraftPreview("preview_probe", func(context.Context, ops.Env, json.RawMessage) (any, error) {
		return map[string]any{"live": true}, nil
	})
	raw, err := json.Marshal(drafts.Draft{ID: uuid.New(), Operation: "preview_probe", Params: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	caller := ops.Caller{TenantID: uuid.New(), StoreID: uuid.New(), StaffID: uuid.New(), Permissions: map[ops.Permission]ops.Grant{}}
	view, ok := NewChat(registry, nil, time.Now, nil).draftView(context.Background(), caller, time.Now(), raw)
	if !ok || view.Preview != nil || view.PreviewError != "已无该操作权限，无法预览" {
		t.Fatalf("draftView = %+v, ok = %v", view, ok)
	}
}
