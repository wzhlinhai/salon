package ops_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
)

func TestCommitRuleKeepsWrites(t *testing.T) {
	s := optest.OpenShop(t)
	mux, api := humatest.New(t)
	r := ops.NewRegistry(s.Pool, api)
	c := s.MustCaller(s.Owner)
	var rule *ops.RuleError
	// rejection-detail: 测试 CommitRule 的错误类型和状态映射，提示文字在下方 HTTP 断言。
	if !errors.As(ops.CommitRule("x"), &rule) || ops.StatusOf(rule) != 409 {
		t.Fatal(rule)
	}
	for _, commit := range []bool{false, true} {
		name := "plain_rule"
		want := "回滚"
		if commit {
			name = "commit_rule"
			want = "保留"
		}
		var created uuid.UUID
		ops.Register(r, ops.Operation[in, out]{Name: name, Kind: ops.Write, Permission: ops.LoggedIn, AI: ops.AIDraft, Handle: func(ctx context.Context, env ops.Env, _ *in) (*out, error) {
			st, err := env.Q.CreateStore(ctx, dbgen.CreateStoreParams{TenantID: env.Caller.TenantID, Name: name})
			if err != nil {
				return nil, err
			}
			created = st.ID
			if commit {
				return nil, ops.CommitRule("保留")
			}
			return nil, ops.Rule("回滚")
		}})
		req := httptest.NewRequest(http.MethodPost, ops.PathPrefix+name, strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(ops.WithCaller(req.Context(), c))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if rec.Code != 409 || body["detail"] != want {
			t.Fatal(rec.Code, body)
		}
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		var exists bool
		err := tx.QueryRow(context.Background(), "SELECT EXISTS(SELECT 1 FROM stores WHERE id=$1)", created).Scan(&exists)
		dbtest.Rollback(t, tx)
		if err != nil || exists != commit {
			t.Fatal(exists, commit, err)
		}
	}
}

func TestCommitRuleCommitFailure(t *testing.T) {
	s := optest.OpenShop(t)
	ctx := context.Background()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	if _, err := dbgen.New(tx).CreateStore(ctx, dbgen.CreateStoreParams{TenantID: s.Res.TenantID, Name: "提交时冲突"}); err != nil {
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)
	// 延迟唯一约束只在提交时检查，确保处理函数能完成写入并返回 CommitRule。
	if _, err := s.Pool.Exec(ctx, "ALTER TABLE stores ADD CONSTRAINT commit_failure_name UNIQUE (tenant_id, name) DEFERRABLE INITIALLY DEFERRED"); err != nil {
		t.Fatal(err)
	}
	mux, api := humatest.New(t)
	r := ops.NewRegistry(s.Pool, api)
	c := s.MustCaller(s.Owner)
	var created uuid.UUID
	ops.Register(r, ops.Operation[in, out]{Name: "commit_failure", Kind: ops.Write, Permission: ops.LoggedIn, AI: ops.AIDraft, Handle: func(ctx context.Context, env ops.Env, _ *in) (*out, error) {
		st, err := env.Q.CreateStore(ctx, dbgen.CreateStoreParams{TenantID: env.Caller.TenantID, Name: "提交时冲突"})
		if err != nil {
			return nil, err
		}
		created = st.ID
		return nil, ops.CommitRule("保留")
	}})
	req := httptest.NewRequest(http.MethodPost, ops.PathPrefix+"commit_failure", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(ops.WithCaller(req.Context(), c))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError || created == uuid.Nil {
		t.Fatal(rec.Code, rec.Body.String(), created)
	}
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var exists bool
	err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM stores WHERE id=$1)", created).Scan(&exists)
	dbtest.Rollback(t, tx)
	if err != nil || exists {
		t.Fatal(exists, err)
	}
}
