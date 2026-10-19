package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/httpapi"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

func getHealth(t *testing.T, h http.Handler) (int, map[string]string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	return rec.Code, body
}

func TestHealthzOK(t *testing.T) {
	pool := dbtest.NewPool(t)
	code, body := getHealth(t, newServer(pool))
	if code != http.StatusOK || body["status"] != "ok" || body["database"] != "ok" {
		t.Fatalf("got %d %v, want 200 status=ok database=ok", code, body)
	}
}

func TestHealthzReportsUnreachableDatabase(t *testing.T) {
	pool := dbtest.NewPool(t)
	pool.Close()
	code, body := getHealth(t, newServer(pool))
	if code != http.StatusServiceUnavailable || body["database"] != "unreachable" {
		t.Fatalf("got %d %v, want 503 database=unreachable", code, body)
	}
}

func newServer(pool *pgxpool.Pool) http.Handler {
	e, _ := httpapi.New(pool, httpapi.Config{
		Authenticate: func(*http.Request) (ops.Caller, bool) { return ops.Caller{}, false },
		Login:        func(context.Context, string, string) (string, error) { return "", errors.New("no login") },
	})
	return e
}

func TestEveryOperationPermissionIsInCatalogue(t *testing.T) {
	pool := dbtest.NewPool(t)
	_, reg := httpapi.New(pool, httpapi.Config{
		Authenticate: func(*http.Request) (ops.Caller, bool) { return ops.Caller{}, false },
		Login:        func(context.Context, string, string) (string, error) { return "", errors.New("no login") },
	})
	for _, p := range reg.Permissions() {
		if _, ok := perm.Lookup(p); !ok {
			t.Errorf("operation permission %q is not in perm.Catalogue", p)
		}
	}
}
