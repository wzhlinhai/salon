package stores_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/stores"
)

type env struct {
	app       *optest.App
	tenant    uuid.UUID
	store     uuid.UUID
	db        *pgxpool.Pool
	storeName func() string // 读取当前门店名称
	manager   ops.Caller    // 有查看和管理权限
	viewer    ops.Caller    // 只有查看权限
	nobody    ops.Caller    // 没有权限
}

func setup(t *testing.T) env {
	t.Helper()
	pool := dbtest.NewPool(t)
	tenant, store := dbtest.NewTenant(t, pool, "本店")
	caller := func(perms ...ops.Permission) ops.Caller {
		return ops.Caller{TenantID: tenant, StoreID: store, StaffID: uuid.New(), Permissions: perms}
	}
	return env{
		app:    optest.New(pool),
		tenant: tenant,
		store:  store,
		db:     pool,
		storeName: func() string {
			var name string
			err := db.InTenant(context.Background(), pool, tenant, func(q *dbgen.Queries) error {
				s, err := q.GetStore(context.Background(), store)
				name = s.Name
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			return name
		},
		manager: caller(stores.PermView, stores.PermManage),
		viewer:  caller(stores.PermView),
		nobody:  caller(),
	}
}

func TestGetStore(t *testing.T) {
	e := setup(t)
	for _, ch := range e.app.Channels() {
		t.Run(ch.Name, func(t *testing.T) {
			r := ch.Call(t, e.viewer, "get_store", map[string]any{})
			if r.Status != http.StatusOK || r.Body["id"] != e.store.String() || r.Body["name"] != "本店门店" {
				t.Fatalf("got %d %v %v, want 200 with current store", r.Status, r.Body, r.Err)
			}
		})
	}
}

func TestPermissionDeniedOnBothChannels(t *testing.T) {
	e := setup(t)
	cases := []struct {
		name   string
		caller ops.Caller
		op     string
		args   any
	}{
		{"read without store.view", e.nobody, "get_store", map[string]any{}},
		{"write without store.manage", e.viewer, "rename_store", map[string]any{"name": "新名字"}},
	}
	for _, ch := range e.app.Channels() {
		for _, c := range cases {
			t.Run(ch.Name+"/"+c.name, func(t *testing.T) {
				if r := ch.Call(t, c.caller, c.op, c.args); r.Status != http.StatusForbidden {
					t.Fatalf("got %d %v %v, want 403", r.Status, r.Body, r.Err)
				}
			})
		}
	}
	if got := e.storeName(); got != "本店门店" {
		t.Fatalf("store name = %q after denied calls, want unchanged", got)
	}
}

func TestUnauthenticatedHTTPIsRejected(t *testing.T) {
	e := setup(t)
	if r := e.app.HTTP(t, nil, "get_store", map[string]any{}); r.Status != http.StatusUnauthorized {
		t.Fatalf("got %d %v, want 401", r.Status, r.Body)
	}
}

func TestRenameStoreRunsOverHTTP(t *testing.T) {
	e := setup(t)
	r := e.app.HTTP(t, &e.manager, "rename_store", map[string]any{"name": "新名字"})
	if r.Status != http.StatusOK || r.Body["name"] != "新名字" {
		t.Fatalf("got %d %v, want 200 renamed", r.Status, r.Body)
	}
	if got := e.storeName(); got != "新名字" {
		t.Fatalf("store name = %q, want 新名字", got)
	}
}

func TestDraftOperationCannotBeExecutedByAI(t *testing.T) {
	e := setup(t)
	r := e.app.AI(t, e.manager, "rename_store", map[string]any{"name": "AI 改的名字"})
	if r.Status != http.StatusConflict {
		t.Fatalf("got %d %v %v, want draft required", r.Status, r.Body, r.Err)
	}
	if got := e.storeName(); got != "本店门店" {
		t.Fatalf("store name = %q after AI call, want unchanged", got)
	}
}

func TestInvalidArgumentsRejectedOnBothChannels(t *testing.T) {
	e := setup(t)
	cases := map[string]any{
		"empty name":    map[string]any{"name": ""},
		"missing name":  map[string]any{},
		"unknown field": map[string]any{"name": "新名字", "store_id": uuid.NewString()},
	}
	for _, ch := range e.app.Channels() {
		for name, args := range cases {
			t.Run(ch.Name+"/"+name, func(t *testing.T) {
				if r := ch.Call(t, e.manager, "rename_store", args); r.Status != http.StatusUnprocessableEntity {
					t.Fatalf("got %d %v %v, want 422", r.Status, r.Body, r.Err)
				}
			})
		}
	}
}

func TestOtherTenantsStoreIsNotFound(t *testing.T) {
	e := setup(t)
	// 调用者属于本租户，但门店 ID 指向另一个租户的门店：行级安全使它不可见。
	_, otherStore := dbtest.NewTenant(t, e.db, "别的租户")
	other := e.viewer
	other.StoreID = otherStore
	for _, ch := range e.app.Channels() {
		t.Run(ch.Name, func(t *testing.T) {
			if r := ch.Call(t, other, "get_store", map[string]any{}); r.Status != http.StatusNotFound {
				t.Fatalf("got %d %v %v, want 404", r.Status, r.Body, r.Err)
			}
		})
	}
}

func openAPI(t *testing.T, app *optest.App) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	app.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /openapi.json = %d", rec.Code)
	}
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestOperationsAppearInOpenAPI(t *testing.T) {
	e := setup(t)
	paths := openAPI(t, e.app)["paths"].(map[string]any)
	want := map[string][2]string{
		"get_store":    {"store.view", "direct"},
		"rename_store": {"store.manage", "draft"},
	}
	for name, w := range want {
		item, ok := paths[ops.PathPrefix+name].(map[string]any)
		if !ok {
			t.Fatalf("%s missing from OpenAPI paths", name)
		}
		post := item["post"].(map[string]any)
		if post["operationId"] != name || post["x-permission"] != w[0] || post["x-ai"] != w[1] {
			t.Errorf("%s: operationId=%v x-permission=%v x-ai=%v, want %s %s %s",
				name, post["operationId"], post["x-permission"], post["x-ai"], name, w[0], w[1])
		}
	}
}

func TestToolParametersMatchHTTPRequestSchema(t *testing.T) {
	e := setup(t)
	doc := openAPI(t, e.app)
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	httpSchema := schemas["RenameStoreInput"]

	var tool *ops.Tool
	for _, tl := range e.app.Registry.Tools(e.manager) {
		if tl.Name == "rename_store" {
			tool = &tl
		}
	}
	if tool == nil {
		t.Fatal("rename_store tool missing")
	}
	if !reflect.DeepEqual(tool.Parameters, httpSchema) {
		t.Fatalf("tool parameters differ from HTTP request schema\n tool: %v\n http: %v", tool.Parameters, httpSchema)
	}
	props := tool.Parameters["properties"].(map[string]any)
	name := props["name"].(map[string]any)
	if name["minLength"] != 1.0 || name["description"] != "新的门店名称" {
		t.Fatalf("name schema = %v, want minLength 1 and the field doc", name)
	}
}

func TestToolsFollowCallerPermissions(t *testing.T) {
	e := setup(t)
	names := func(c ops.Caller) map[string]bool {
		m := map[string]bool{}
		for _, tl := range e.app.Registry.Tools(c) {
			m[tl.Name] = tl.Draft
		}
		return m
	}
	if got := names(e.nobody); len(got) != 0 {
		t.Errorf("no permissions: tools = %v, want none", got)
	}
	if got := names(e.viewer); !reflect.DeepEqual(got, map[string]bool{"get_store": false}) {
		t.Errorf("viewer: tools = %v, want only get_store", got)
	}
	if got := names(e.manager); !reflect.DeepEqual(got, map[string]bool{"get_store": false, "rename_store": true}) {
		t.Errorf("manager: tools = %v, want get_store and draft rename_store", got)
	}
}
