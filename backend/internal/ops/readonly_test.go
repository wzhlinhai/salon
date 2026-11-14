package ops_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
)

// TestReadOperationsRunReadOnly 证明 Read 操作在只读事务中执行：同一个写查询在 Read 下被 PostgreSQL 拒绝。
func TestReadOperationsRunReadOnly(t *testing.T) {
	s := optest.OpenShop(t)
	ops.Register(s.Registry, ops.Operation[in, out]{Name: "write_probe_read", Summary: "测试只读事务", Kind: ops.Read, Permission: ops.LoggedIn, AI: ops.AIDirect,
		Handle: func(ctx context.Context, env ops.Env, _ *in) (*out, error) {
			if err := env.Q.ExpireStoreDrafts(ctx, dbgen.ExpireStoreDraftsParams{StoreID: env.Caller.StoreID, Now: pgtype.Timestamptz{Time: env.Now, Valid: true}, ActorID: env.Caller.StaffID}); err != nil {
				return nil, err
			}
			return &out{}, nil
		}})
	_, err := s.Registry.CallTool(context.Background(), s.MustCaller(s.Owner), "write_probe_read", []byte("{}"))
	if err == nil || !strings.Contains(err.Error(), "read-only transaction") {
		t.Fatalf("err = %v, want read-only transaction rejection", err)
	}
}

// TestCallReadToolIncludesEveryDirectRead 证明对话读工具不再排除原来的惰性写入读操作。
func TestCallReadToolIncludesEveryDirectRead(t *testing.T) {
	s := optest.OpenShop(t)
	draft := s.Call(t, s.Owner, "create_draft", map[string]any{"operation": "create_customer", "params": map[string]any{"name": "读工具顾客"}, "summary": "", "utterance": ""}, 200).Body["id"].(string)
	queue := s.Call(t, s.Owner, "create_rotation_queue", map[string]any{"name": "读工具牌"}, 200).Body["id"].(string)
	cases := []struct {
		name    string
		args    map[string]any
		wantErr error
	}{
		{"list_drafts", map[string]any{}, nil},
		{"get_draft", map[string]any{"id": draft}, nil},
		{"get_redemption", map[string]any{"key": "missing"}, ops.ErrNotFound},
		{"get_rotation", map[string]any{"queue_id": queue}, nil},
	}
	for _, tc := range cases {
		args, err := json.Marshal(tc.args)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := s.Registry.CallReadTool(context.Background(), s.MustCaller(s.Owner), tc.name, args)
		if errors.Is(err, ops.ErrUnknownOperation) {
			t.Fatalf("%s was rejected by CallReadTool", tc.name)
		}
		if tc.wantErr != nil {
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("%s err = %v, want %v", tc.name, err, tc.wantErr)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}
}

// TestReadOperationsUseRepeatableRead 证明 Read 操作在可重复读的只读事务中执行。
func TestReadOperationsUseRepeatableRead(t *testing.T) {
	s := optest.OpenShop(t)
	ops.Register(s.Registry, ops.Operation[in, out]{Name: "isolation_probe", Summary: "测试事务隔离级别", Kind: ops.Read, Permission: ops.LoggedIn, AI: ops.AIDirect,
		Handle: func(ctx context.Context, env ops.Env, _ *in) (*out, error) {
			isolation, err := env.Q.TransactionIsolation(ctx)
			if err != nil {
				return nil, err
			}
			if isolation != "repeatable read" {
				return nil, fmt.Errorf("isolation = %s, want repeatable read", isolation)
			}
			return &out{}, nil
		}})
	if _, err := s.Registry.CallTool(context.Background(), s.MustCaller(s.Owner), "isolation_probe", []byte("{}")); err != nil {
		t.Fatal(err)
	}
}
