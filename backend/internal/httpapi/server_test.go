package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/httpapi"
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
	code, body := getHealth(t, httpapi.New(pool))
	if code != http.StatusOK || body["status"] != "ok" || body["database"] != "ok" {
		t.Fatalf("got %d %v, want 200 status=ok database=ok", code, body)
	}
}

func TestHealthzReportsUnreachableDatabase(t *testing.T) {
	pool := dbtest.NewPool(t)
	pool.Close()
	code, body := getHealth(t, httpapi.New(pool))
	if code != http.StatusServiceUnavailable || body["database"] != "unreachable" {
		t.Fatalf("got %d %v, want 503 database=unreachable", code, body)
	}
}
