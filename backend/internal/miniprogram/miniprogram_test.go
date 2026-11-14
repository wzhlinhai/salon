package miniprogram_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/ai"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

func call(t *testing.T, s *optest.Shop, op string, args any, want int) map[string]any {
	t.Helper()
	return s.Call(t, s.Owner, op, args, want).Body
}

func login(t *testing.T, s *optest.Shop, code, phone string) (int, map[string]any) {
	t.Helper()
	return s.CustomerLogin(t, s.Res.StoreID, code, phone)
}

func mustLogin(t *testing.T, s *optest.Shop, code, phone string) (string, map[string]any) {
	t.Helper()
	status, body := login(t, s, code, phone)
	if status != http.StatusOK || body["token"] == nil || body["token"] == "" {
		t.Fatalf("customer login: %d %v", status, body)
	}
	return body["token"].(string), body["customer"].(map[string]any)
}

func customerCaller(t *testing.T, s *optest.Shop, token string) ops.Caller {
	t.Helper()
	caller, ok := s.CustomerCallerFor(t, token)
	if !ok {
		t.Fatal("customer token does not authenticate")
	}
	return caller
}

func TestCustomerLoginFlows(t *testing.T) {
	s := optest.OpenShop(t)
	unconfigured := optest.NewWithoutWechat(t, s.Pool)
	if status, body := unconfigured.CustomerLogin(t, s.Res.StoreID, "fake-openid:x", ""); status != http.StatusServiceUnavailable || body["detail"] != "小程序登录未启用" {
		t.Fatalf("unconfigured login: %d %v", status, body)
	}
	status, body := login(t, s, "fake-openid:need-phone", "")
	if status != http.StatusOK || body["need_phone"] != true || body["token"] != nil {
		t.Fatalf("need phone: %d %v", status, body)
	}

	token1, customer := mustLogin(t, s, "fake-openid:new", "fake-phone:13800130001")
	if customer["phone"] != "138****0001" || customer["name"] != nil {
		t.Fatalf("new customer = %v", customer)
	}
	if _, ok := s.CustomerCallerFor(t, token1); !ok {
		t.Fatal("new customer token does not authenticate")
	}

	token2, customer2 := mustLogin(t, s, "fake-openid:new", "")
	if customer2["id"] != customer["id"] || token2 == "" {
		t.Fatalf("existing binding = %v", customer2)
	}

	existing := call(t, s, "create_customer", map[string]any{"phone": "13900130002"}, 200)["customer"].(map[string]any)
	_, bound := mustLogin(t, s, "fake-openid:bind-existing", "fake-phone:13900130002")
	if bound["id"] != existing["id"] {
		t.Fatalf("bound customer = %v, want %v", bound, existing)
	}

	if status, body := s.CustomerLogin(t, uuid.New(), "fake-openid:x", ""); status != http.StatusNotFound || body["detail"] != "门店不存在" {
		t.Fatalf("unknown store: %d %v", status, body)
	}
	if status, body := login(t, s, "bogus", ""); status != http.StatusUnauthorized || body["detail"] != "微信登录失败" {
		t.Fatalf("bad session code: %d %v", status, body)
	}
	if status, body := login(t, s, "fake-openid:bad-phone", "bad"); status != http.StatusUnauthorized || body["detail"] != "微信登录失败" {
		t.Fatalf("bad phone code: %d %v", status, body)
	}

	var customers, logins, createdLogs, boundLogs int
	if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM customers").Scan(&customers); err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM customer_logins").Scan(&logins); err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM audit_log WHERE action='create_customer' AND actor_customer_id IS NOT NULL").Scan(&createdLogs); err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM audit_log WHERE action='bind_wechat_login' AND actor_customer_id IS NOT NULL").Scan(&boundLogs); err != nil {
		t.Fatal(err)
	}
	if customers != 2 || logins != 2 || createdLogs != 1 || boundLogs != 2 {
		t.Fatalf("customers=%d logins=%d created=%d bound=%d", customers, logins, createdLogs, boundLogs)
	}
}

func TestCustomerLoginMergedSurvivor(t *testing.T) {
	s := optest.OpenShop(t)
	keep := call(t, s, "create_customer", map[string]any{"name": "保留"}, 200)["customer"].(map[string]any)
	merged := call(t, s, "create_customer", map[string]any{"name": "并入", "phone": "13700130003"}, 200)["customer"].(map[string]any)
	token, customer := mustLogin(t, s, "fake-openid:merged", "fake-phone:13700130003")
	if customer["id"] != merged["id"] {
		t.Fatal(customer)
	}
	call(t, s, "merge_customers", map[string]any{"keep_id": keep["id"], "merged_id": merged["id"]}, 200)
	var bound uuid.UUID
	if err := s.Pool.QueryRow(context.Background(), "SELECT customer_id FROM customer_logins WHERE openid='merged'").Scan(&bound); err != nil {
		t.Fatal(err)
	}
	if bound != uuid.MustParse(keep["id"].(string)) {
		t.Fatalf("binding after merge = %v, want survivor", bound)
	}
	_, after := mustLogin(t, s, "fake-openid:merged", "")
	if after["id"] != keep["id"] {
		t.Fatalf("merged login = %v, want survivor %v", after, keep)
	}
	caller := customerCaller(t, s, token)
	if caller.CustomerID == nil || *caller.CustomerID != uuid.MustParse(keep["id"].(string)) {
		t.Fatalf("authenticated customer = %v, want survivor", caller.CustomerID)
	}
	if _, err := s.Pool.Exec(context.Background(), "UPDATE customer_logins SET customer_id=$1 WHERE openid='merged'", merged["id"]); err != nil {
		t.Fatal(err)
	}
	resolved, ok := s.CustomerCallerFor(t, token)
	if !ok || resolved.CustomerID == nil || *resolved.CustomerID != uuid.MustParse(keep["id"].(string)) {
		t.Fatalf("auth merge resolution = %+v, ok=%v", resolved.CustomerID, ok)
	}
}

func TestCustomerLoginConcurrency(t *testing.T) {
	s := optest.OpenShop(t)
	var wg sync.WaitGroup
	results := make(chan int, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, _ := login(t, s, "fake-openid:race", "fake-phone:13800130009")
			results <- status
		}()
	}
	wg.Wait()
	close(results)
	for status := range results {
		if status != http.StatusOK {
			t.Fatalf("concurrent login status = %d", status)
		}
	}
	var customers, logins int
	if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM customers").Scan(&customers); err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM customer_logins").Scan(&logins); err != nil {
		t.Fatal(err)
	}
	if customers != 1 || logins != 1 {
		t.Fatalf("customers=%d logins=%d, want 1 and 1", customers, logins)
	}
}

func TestCustomerLoginConcurrencyDifferentPhones(t *testing.T) {
	s := optest.OpenShop(t)
	var wg sync.WaitGroup
	for _, phone := range []string{"fake-phone:13800130017", "fake-phone:13800130018"} {
		wg.Add(1)
		go func(phone string) {
			defer wg.Done()
			if status, body := login(t, s, "fake-openid:race-different", phone); status != http.StatusOK {
				t.Errorf("login %s: %d %v", phone, status, body)
			}
		}(phone)
	}
	wg.Wait()
	var customers, logins int
	if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM customers").Scan(&customers); err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM customer_logins").Scan(&logins); err != nil {
		t.Fatal(err)
	}
	if customers != 1 || logins != 1 {
		t.Fatalf("customers=%d logins=%d, want one binding to one customer", customers, logins)
	}
}

func TestCustomerLoginBindingUnique(t *testing.T) {
	s := optest.OpenShop(t)
	_, _ = mustLogin(t, s, "fake-openid:unique", "fake-phone:13800130019")
	var customerID uuid.UUID
	if err := s.Pool.QueryRow(context.Background(), "SELECT customer_id FROM customer_logins WHERE openid='unique'").Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	_, err := tx.Exec(context.Background(), `INSERT INTO customer_logins (tenant_id, store_id, openid, customer_id) VALUES ($1,$2,'unique',$3)`, s.Res.TenantID, s.Res.StoreID, customerID)
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "23505" {
		t.Fatalf("duplicate binding err = %v", err)
	}
}

func TestCustomerTokenInvalidation(t *testing.T) {
	s := optest.OpenShop(t)
	t.Run("version change", func(t *testing.T) {
		token, _ := mustLogin(t, s, "fake-openid:version", "fake-phone:13800130020")
		if _, ok := s.CustomerCallerFor(t, token); !ok {
			t.Fatal("token should authenticate before version change")
		}
		if _, err := s.Pool.Exec(context.Background(), "UPDATE customer_logins SET token_version=token_version+1 WHERE openid='version'"); err != nil {
			t.Fatal(err)
		}
		if _, ok := s.CustomerCallerFor(t, token); ok {
			t.Fatal("token authenticated after version change")
		}
	})
	t.Run("deleted binding", func(t *testing.T) {
		token, _ := mustLogin(t, s, "fake-openid:deleted-binding", "fake-phone:13800130026")
		if _, err := s.Pool.Exec(context.Background(), "DELETE FROM customer_logins WHERE openid='deleted-binding'"); err != nil {
			t.Fatal(err)
		}
		if _, ok := s.CustomerCallerFor(t, token); ok {
			t.Fatal("token authenticated after binding deletion")
		}
	})
	t.Run("deleted customer", func(t *testing.T) {
		token, customer := mustLogin(t, s, "fake-openid:deleted-customer", "fake-phone:13800130027")
		customerID := uuid.MustParse(customer["id"].(string))
		tx, err := s.Pool.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		if _, err := tx.Exec(context.Background(), "SET LOCAL session_replication_role = replica"); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(context.Background(), "DELETE FROM customers WHERE id=$1", customerID); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, ok := s.CustomerCallerFor(t, token); ok {
			t.Fatal("token authenticated after customer deletion")
		}
	})
}

func TestCustomerLoginConcurrentOpenIDsSamePhone(t *testing.T) {
	s := optest.OpenShop(t)
	var wg sync.WaitGroup
	for _, openid := range []string{"fake-openid:same-phone-a", "fake-openid:same-phone-b"} {
		wg.Add(1)
		go func(code string) {
			defer wg.Done()
			if status, body := login(t, s, code, "fake-phone:13800130028"); status != http.StatusOK {
				t.Errorf("login %s: %d %v", code, status, body)
			}
		}(openid)
	}
	wg.Wait()
	var customers, logins int
	if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM customers").Scan(&customers); err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.QueryRow(context.Background(), "SELECT count(DISTINCT customer_id) FROM customer_logins").Scan(&logins); err != nil {
		t.Fatal(err)
	}
	var bindings int
	if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM customer_logins").Scan(&bindings); err != nil {
		t.Fatal(err)
	}
	if customers != 1 || bindings != 2 || logins != 1 {
		t.Fatalf("customers=%d bindings=%d distinct customers=%d, want 1/2/1", customers, bindings, logins)
	}
}

func TestCustomerLoginWaitsForPhoneLock(t *testing.T) {
	s := optest.OpenShop(t)
	phone := "13800130029"
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	if _, err := tx.Exec(context.Background(), "SELECT pg_advisory_xact_lock(hashtextextended($1 || $2, 0))", s.Res.TenantID.String(), phone); err != nil {
		t.Fatal(err)
	}
	done := make(chan optest.Result, 1)
	go func() {
		status, body := login(t, s, "fake-openid:phone-lock", "fake-phone:"+phone)
		done <- optest.Result{Status: status, Body: body}
	}()
	optest.AssertWaiting(t, s.Pool, done, 1)
	dbtest.Rollback(t, tx)
	if r := <-done; r.Status != http.StatusOK {
		t.Fatalf("login after phone lock = %d %v", r.Status, r.Body)
	}
}

func TestCustomerLoginStoreScoped(t *testing.T) {
	s := optest.OpenShop(t)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	store, err := dbgen.New(tx).CreateStore(context.Background(), dbgen.CreateStoreParams{TenantID: s.Res.TenantID, Name: "本租户别店"})
	if err != nil {
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)
	firstStatus, first := s.CustomerLogin(t, s.Res.StoreID, "fake-openid:store-scope", "fake-phone:13800130021")
	secondStatus, second := s.CustomerLogin(t, store.ID, "fake-openid:store-scope", "fake-phone:13800130022")
	if firstStatus != http.StatusOK || secondStatus != http.StatusOK {
		t.Fatal(firstStatus, first, secondStatus, second)
	}
	if first["customer"].(map[string]any)["id"] == second["customer"].(map[string]any)["id"] {
		t.Fatal("same openid in another store reused the first store customer")
	}
	profile := s.HTTPWithToken(t, second["token"].(string), "my_profile", map[string]any{})
	if profile.Status != 200 || profile.Body["store_name"] != "本租户别店" {
		t.Fatal(profile)
	}
	firstCustomer := uuid.MustParse(first["customer"].(map[string]any)["id"].(string))
	forged := ops.Caller{TenantID: s.Res.TenantID, StoreID: store.ID, CustomerID: &firstCustomer}
	if r := s.HTTP(t, &forged, "my_profile", map[string]any{}); r.Status != http.StatusNotFound || r.Detail() != "not found" {
		t.Fatalf("forged store caller = %d %q", r.Status, r.Detail())
	}
}

func TestCustomerLoginRejectsOtherStorePhone(t *testing.T) {
	s := optest.OpenShop(t)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	store, err := dbgen.New(tx).CreateStore(context.Background(), dbgen.CreateStoreParams{TenantID: s.Res.TenantID, Name: "手机号别店"})
	if err != nil {
		t.Fatal(err)
	}
	dbtest.Exec(t, tx, `INSERT INTO customers (tenant_id,store_id,phone,created_by) VALUES ($1,$2,'13800130023',$3)`, s.Res.TenantID, store.ID, s.Res.OwnerID)
	dbtest.Commit(t, tx)
	status, body := s.CustomerLogin(t, s.Res.StoreID, "fake-openid:other-store-phone", "fake-phone:13800130023")
	if status != http.StatusConflict || body["detail"] != "这个手机号已属于其他门店的顾客" {
		t.Fatalf("other store phone: %d %v", status, body)
	}
}

func TestCustomerLoginRejectsCrossStoreMergedSurvivor(t *testing.T) {
	s := optest.OpenShop(t)
	keep := call(t, s, "create_customer", map[string]any{"phone": "13800130030"}, 200)["customer"].(map[string]any)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	store, err := dbgen.New(tx).CreateStore(context.Background(), dbgen.CreateStoreParams{TenantID: s.Res.TenantID, Name: "合并别店"})
	if err != nil {
		t.Fatal(err)
	}
	var merged uuid.UUID
	if err := tx.QueryRow(context.Background(), `INSERT INTO customers (tenant_id,store_id,phone,created_by) VALUES ($1,$2,'13800130031',$3) RETURNING id`, s.Res.TenantID, store.ID, s.Res.OwnerID).Scan(&merged); err != nil {
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)
	call(t, s, "merge_customers", map[string]any{"keep_id": keep["id"], "merged_id": merged}, 200)
	status, body := s.CustomerLogin(t, store.ID, "fake-openid:cross-store-merge", "fake-phone:13800130031")
	if status != http.StatusConflict || body["detail"] != "该顾客已合并到其他门店的档案，不能登录" {
		t.Fatalf("cross-store merged login: %d %v", status, body)
	}
}

type failingModel struct {
	t      *testing.T
	called atomic.Bool
}

func (m *failingModel) Chat(context.Context, []ai.Message, []ops.Tool) (ai.Message, error) {
	m.called.Store(true)
	m.t.Error("AI model must not be called for a customer token")
	return ai.Message{}, errors.New("model must not be called")
}

func doCustomerRequest(s *optest.Shop, token, method, path, body string) (int, string) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func assertUnauthenticated(t *testing.T, label string, status int, body string) {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("%s body = %q: %v", label, body, err)
	}
	if status != http.StatusUnauthorized || out["detail"] != "unauthenticated" {
		t.Fatalf("%s = %d %q", label, status, body)
	}
}

func TestCustomerTokenOnlyForOpsRoutes(t *testing.T) {
	s := optest.OpenShop(t)
	token, _ := mustLogin(t, s, "fake-openid:route-scope", "fake-phone:13800130032")
	// Echo v5.4.0 不公开已注册路由清单；下面列出 httpapi 注册的全部非 ops、非顾客登录路由。
	routes := []struct{ method, path, body string }{
		{http.MethodGet, "/healthz", ""},
		{http.MethodGet, "/openapi.json", ""},
		{http.MethodGet, "/docs", ""},
		{http.MethodPost, "/api/auth/login", "{}"},
		{http.MethodPost, "/api/ai/chat", `{"messages":[{"role":"user","content":"你好"}]}`},
		{http.MethodPost, "/api/import/preview", "{}"},
		{http.MethodGet, "/api/export/all.xlsx", ""},
	}
	for _, route := range routes {
		withoutStatus, withoutBody := doCustomerRequest(s, "", route.method, route.path, route.body)
		withStatus, withBody := doCustomerRequest(s, token, route.method, route.path, route.body)
		if withoutStatus != withStatus || withoutBody != withBody {
			t.Fatalf("%s %s treated customer token differently: without=%d %q with=%d %q", route.method, route.path, withoutStatus, withoutBody, withStatus, withBody)
		}
	}
}

func TestCustomerTokenRejectedByRawRoutes(t *testing.T) {
	s := optest.OpenShop(t)
	token, _ := mustLogin(t, s, "fake-openid:raw-routes", "fake-phone:13800130033")
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/import/preview", "{}"},
		{http.MethodGet, "/api/export/all.xlsx", ""},
	} {
		status, body := doCustomerRequest(s, token, tc.method, tc.path, tc.body)
		assertUnauthenticated(t, tc.method+" "+tc.path, status, body)
	}
	status, body := doCustomerRequest(s, token, http.MethodPost, "/api/ai/chat", `{"messages":[{"role":"user","content":"你好"}]}`)
	assertUnauthenticated(t, "ai chat", status, body)
}

func TestCustomerTokenDoesNotReachChatModel(t *testing.T) {
	s := optest.OpenShop(t)
	model := &failingModel{t: t}
	s.SetAIModel(model)
	token, _ := mustLogin(t, s, "fake-openid:chat-model", "fake-phone:13800130034")
	status, body := doCustomerRequest(s, token, http.MethodPost, "/api/ai/chat", `{"messages":[{"role":"user","content":"你好"}]}`)
	assertUnauthenticated(t, "ai chat", status, body)
	if model.called.Load() {
		t.Fatal("customer token reached the chat model")
	}
}

func TestCustomerTokenRejectsOtherStoreCustomer(t *testing.T) {
	s := optest.OpenShop(t)
	customer := call(t, s, "create_customer", map[string]any{"phone": "13800130024"}, 200)["customer"].(map[string]any)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	store, err := dbgen.New(tx).CreateStore(context.Background(), dbgen.CreateStoreParams{TenantID: s.Res.TenantID, Name: "凭证别店"})
	if err != nil {
		t.Fatal(err)
	}
	var loginID uuid.UUID
	if err := tx.QueryRow(context.Background(), `INSERT INTO customer_logins (tenant_id,store_id,openid,customer_id) VALUES ($1,$2,'cross-store',$3) RETURNING id`, s.Res.TenantID, store.ID, customer["id"]).Scan(&loginID); err != nil {
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)
	token, err := s.Auth.IssueCustomerToken(s.Res.TenantID, loginID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.CustomerCallerFor(t, token); ok {
		t.Fatal("token for a customer in another store authenticated")
	}
	if status, body := s.CustomerLogin(t, store.ID, "fake-openid:cross-store", ""); status != http.StatusUnauthorized || body["detail"] != "微信登录失败" {
		t.Fatalf("cross-store binding login: %d %v", status, body)
	}
}

func TestCustomerTokenIsolation(t *testing.T) {
	s := optest.OpenShop(t)
	token, _ := mustLogin(t, s, "fake-openid:isolation", "fake-phone:13800130010")
	caller := customerCaller(t, s, token)
	if _, ok := s.CallerFor(t, token); ok {
		t.Fatal("staff authenticator accepted a customer token")
	}
	if _, ok := s.CustomerCallerFor(t, s.Owner); ok {
		t.Fatal("customer authenticator accepted a staff token")
	}
	if caller.StaffID != uuid.Nil || caller.CustomerID == nil {
		t.Fatalf("customer caller = %+v", caller)
	}
	owner := s.MustCaller(s.Owner)
	for _, op := range s.Registry.Operations() {
		if op.Permission == ops.CustomerSelf {
			if err := s.Registry.Authorize(owner, op.Name); !errors.Is(err, ops.ErrForbidden) {
				t.Fatalf("staff caller on %s: %v", op.Name, err)
			}
			continue
		}
		if err := s.Registry.Authorize(caller, op.Name); !errors.Is(err, ops.ErrForbidden) {
			t.Fatalf("customer caller on %s: %v", op.Name, err)
		}
	}
}

type fixture struct {
	*optest.Shop
	t         *testing.T
	item      uuid.UUID
	otherItem uuid.UUID
	stylist   uuid.UUID
	customerA uuid.UUID
	customerB uuid.UUID
	cardA     map[string]any
	cardB     map[string]any
	orderA    map[string]any
	orderB    map[string]any
	apptA     map[string]any
	apptB     map[string]any
	tokenA    string
	tokenB    string
}

func openFixture(t *testing.T) *fixture {
	t.Helper()
	s := &fixture{Shop: optest.OpenShop(t), t: t}
	s.SetNow(time.Date(2026, 6, 12, 10, 0, 0, 0, settings.Location))
	s.customerA = uuid.MustParse(call(t, s.Shop, "create_customer", map[string]any{"name": "顾客甲", "phone": "13800130011"}, 200)["customer"].(map[string]any)["id"].(string))
	s.customerB = uuid.MustParse(call(t, s.Shop, "create_customer", map[string]any{"name": "顾客乙", "phone": "13800130012"}, 200)["customer"].(map[string]any)["id"].(string))
	s.stylist, _ = s.Hire("发型师", "002", "stylist", s.Role("发型师"))
	s.item = s.createItem("剪发", "50.00", 30)
	s.otherItem = s.createItem("护理", "30.00", 30)
	s.cardA = s.openCard(s.customerA, "100.00", "10.00")
	s.cardB = s.openCard(s.customerB, "200.00", "20.00")
	s.orderA = s.paidOrder(s.customerA, s.item, "50.00")
	s.orderB = s.paidOrder(s.customerB, s.otherItem, "30.00")
	s.apptA = s.appointment(s.customerA, s.stylist, "13:00")
	s.apptB = s.appointment(s.customerB, s.stylist, "15:00")
	s.tokenA, _ = mustLogin(t, s.Shop, "fake-openid:customer-a", "fake-phone:13800130011")
	s.tokenB, _ = mustLogin(t, s.Shop, "fake-openid:customer-b", "fake-phone:13800130012")
	return s
}

func (s *fixture) createItem(name, price string, duration int) uuid.UUID {
	s.t.Helper()
	out := call(s.t, s.Shop, "create_item", map[string]any{
		"name": name, "price": price, "discountable": true, "duration_minutes": duration, "bookable": true,
		"labor_fee_amount": nil, "labor_fee_percent": nil, "positions": []any{}, "level_prices": []any{}, "member_prices": []any{},
	}, 200)
	return uuid.MustParse(out["id"].(string))
}

func (s *fixture) openCard(customer uuid.UUID, principal, bonus string) map[string]any {
	s.t.Helper()
	ct := call(s.t, s.Shop, "create_card_type", map[string]any{"name": "储值卡" + uuid.NewString(), "kind": "stored_value", "validity_kind": "none", "items": []any{}}, 200)
	return call(s.t, s.Shop, "open_card", map[string]any{
		"customer_id": customer, "card_type_id": ct["id"], "principal": principal, "bonus": bonus,
		"sales_staff_ids": []uuid.UUID{s.Res.OwnerID}, "payments": []any{map[string]any{"method": "cash", "amount": principal}},
	}, 200)
}

func (s *fixture) paidOrder(customer, item uuid.UUID, amount string) map[string]any {
	s.t.Helper()
	order := call(s.t, s.Shop, "open_order", map[string]any{
		"customer_id": customer, "gender": "female", "designated": true,
		"items": []any{map[string]any{"item_id": item, "staff": []any{}}},
	}, 200)
	out := call(s.t, s.Shop, "checkout_orders", map[string]any{
		"orders": []any{map[string]any{"order_id": order["id"], "payments": []any{map[string]any{"method": "cash", "amount": amount}}}},
	}, 200)
	return out["items"].([]any)[0].(map[string]any)
}

func (s *fixture) appointment(customer, staffID uuid.UUID, clock string) map[string]any {
	s.t.Helper()
	day := settings.Date(s.Now()).Format(settings.DateLayout)
	start, err := time.ParseInLocation(settings.DateLayout+" 15:04", day+" "+clock, settings.Location)
	if err != nil {
		s.t.Fatal(err)
	}
	return call(s.t, s.Shop, "create_appointment", map[string]any{
		"customer_id": customer, "staff_id": staffID, "starts_at": start, "ends_at": start.Add(30 * time.Minute),
		"items": []any{map[string]any{"item_id": s.item}},
	}, 200)
}

func (s *fixture) customerHTTP(token, op string, args any) optest.Result {
	s.t.Helper()
	return s.HTTPWithToken(s.t, token, op, args)
}

func TestCustomerOpsOnlyOwnData(t *testing.T) {
	s := openFixture(t)
	profile := s.customerHTTP(s.tokenA, "my_profile", map[string]any{})
	if profile.Status != 200 || profile.Body["phone"] != "138****0011" || profile.Body["name"] != "顾客甲" || profile.Body["phone_masked"] != true {
		t.Fatal(profile)
	}
	cards := s.customerHTTP(s.tokenA, "my_cards", map[string]any{})
	if cards.Status != 200 || len(cards.Body["items"].([]any)) != 1 || cards.Body["items"].([]any)[0].(map[string]any)["id"] != s.cardA["id"] {
		t.Fatal(cards)
	}
	if cards.Body["items"].([]any)[0].(map[string]any)["balance"] != "110.00" {
		t.Fatal(cards)
	}
	ledger := s.customerHTTP(s.tokenA, "my_card_ledger", map[string]any{"card_id": s.cardA["id"]})
	if ledger.Status != 200 || len(ledger.Body["entries"].([]any)) == 0 {
		t.Fatal(ledger)
	}
	if other := s.customerHTTP(s.tokenA, "my_card_ledger", map[string]any{"card_id": s.cardB["id"]}); other.Status != http.StatusNotFound || other.Detail() != "not found" {
		t.Fatal(other)
	}
	orders := s.customerHTTP(s.tokenA, "my_orders", map[string]any{})
	if orders.Status != 200 || len(orders.Body["entries"].([]any)) != 1 || orders.Body["entries"].([]any)[0].(map[string]any)["id"] != s.orderA["id"] {
		t.Fatal(orders)
	}
	appts := s.customerHTTP(s.tokenA, "my_appointments", map[string]any{})
	if appts.Status != 200 || len(appts.Body["entries"].([]any)) != 1 || appts.Body["entries"].([]any)[0].(map[string]any)["id"] != s.apptA["id"] {
		t.Fatal(appts)
	}
	call(t, s.Shop, "open_order", map[string]any{
		"customer_id": s.customerA, "gender": "female", "designated": false,
		"items": []any{map[string]any{"item_id": s.otherItem, "staff": []any{}}},
	}, 200)
	if orders := s.customerHTTP(s.tokenA, "my_orders", map[string]any{}); len(orders.Body["entries"].([]any)) != 1 {
		t.Fatalf("my_orders included an open order: %v", orders)
	}

	bCards := s.customerHTTP(s.tokenB, "my_cards", map[string]any{})
	if bCards.Status != 200 || len(bCards.Body["items"].([]any)) != 1 || bCards.Body["items"].([]any)[0].(map[string]any)["id"] != s.cardB["id"] {
		t.Fatal(bCards)
	}
	bOrders := s.customerHTTP(s.tokenB, "my_orders", map[string]any{})
	if bOrders.Status != 200 || len(bOrders.Body["entries"].([]any)) != 1 || bOrders.Body["entries"].([]any)[0].(map[string]any)["id"] != s.orderB["id"] {
		t.Fatal(bOrders)
	}
}

func TestCustomerOpsCrossTenantAndStore(t *testing.T) {
	s := openFixture(t)
	other, err := staff.Bootstrap(context.Background(), s.Pool, staff.BootstrapInput{
		TenantName: "别租户", StoreName: "别店", OwnerName: "别老板", OwnerNumber: "001",
		OwnerAccount: "other-boss", OwnerPassword: "owner-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	status, body := s.CustomerLogin(t, other.StoreID, "fake-openid:customer-a", "fake-phone:13800130011")
	if status != http.StatusOK || body["customer"].(map[string]any)["id"] == s.customerA.String() {
		t.Fatalf("other tenant login: %d %v", status, body)
	}
	otherToken := body["token"].(string)
	otherProfile := s.HTTPWithToken(t, otherToken, "my_profile", map[string]any{})
	if otherProfile.Status != 200 || otherProfile.Body["store_name"] != "别店" {
		t.Fatal(otherProfile)
	}
	otherCards := s.HTTPWithToken(t, otherToken, "my_cards", map[string]any{})
	if otherCards.Status != 200 || len(otherCards.Body["items"].([]any)) != 0 {
		t.Fatal(otherCards)
	}
	foreignLedger := s.HTTPWithToken(t, otherToken, "my_card_ledger", map[string]any{"card_id": s.cardA["id"]})
	if foreignLedger.Status != http.StatusNotFound || foreignLedger.Detail() != "not found" {
		t.Fatal(foreignLedger)
	}
	aOrders := s.customerHTTP(s.tokenA, "my_orders", map[string]any{})
	if aOrders.Status != 200 || len(aOrders.Body["entries"].([]any)) != 1 || aOrders.Body["entries"].([]any)[0].(map[string]any)["id"] != s.orderA["id"] {
		t.Fatal(aOrders)
	}
}

func TestCustomerOpsStoreFilters(t *testing.T) {
	s := openFixture(t)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	store, err := dbgen.New(tx).CreateStore(context.Background(), dbgen.CreateStoreParams{TenantID: s.Res.TenantID, Name: "过滤别店"})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot uuid.UUID
	if err := tx.QueryRow(context.Background(), "SELECT settings_snapshot_id FROM orders WHERE id=$1", s.orderA["id"]).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	var checkout uuid.UUID
	if err := tx.QueryRow(context.Background(), `INSERT INTO checkouts (tenant_id,store_id,business_date,checked_out_by) VALUES ($1,$2,current_date,$3) RETURNING id`, s.Res.TenantID, store.ID, s.Res.OwnerID).Scan(&checkout); err != nil {
		t.Fatal(err)
	}
	dbtest.Exec(t, tx, `INSERT INTO orders (tenant_id,store_id,customer_id,gender,designated,status,settings_snapshot_id,business_date,opened_by,checkout_id)
 VALUES ($1,$2,$3,'female',true,'paid',$4,current_date,$5,$6)`, s.Res.TenantID, store.ID, s.customerA, snapshot, s.Res.OwnerID, checkout)
	start := s.Now().Add(24 * time.Hour)
	dbtest.Exec(t, tx, `INSERT INTO appointments (tenant_id,store_id,staff_id,customer_id,kind,starts_at,ends_at,status,created_by)
 VALUES ($1,$2,$3,$4,'appointment',$5,$6,'confirmed',$7)`, s.Res.TenantID, store.ID, s.stylist, s.customerA, start, start.Add(30*time.Minute), s.Res.OwnerID)
	var otherCard uuid.UUID
	if err := tx.QueryRow(context.Background(), `INSERT INTO cards (tenant_id,store_id,customer_id,card_type_id,opened_at,expires_on,created_by)
 SELECT tenant_id,$2,customer_id,card_type_id,opened_at,expires_on,created_by FROM cards WHERE id=$1 RETURNING id`, s.cardA["id"], store.ID).Scan(&otherCard); err != nil {
		t.Fatal(err)
	}
	dbtest.Exec(t, tx, `INSERT INTO card_ledger (tenant_id,store_id,card_id,kind,principal_before,principal_delta,principal_after,bonus_before,bonus_delta,bonus_after,business_date,actor_id)
 VALUES ($1,$2,$3,'open',0,0,0,0,0,0,current_date,$4)`, s.Res.TenantID, store.ID, otherCard, s.Res.OwnerID)
	dbtest.Commit(t, tx)

	cards := s.customerHTTP(s.tokenA, "my_cards", map[string]any{})
	if cards.Status != 200 || len(cards.Body["items"].([]any)) != 1 {
		t.Fatalf("my_cards leaked another store: %v", cards)
	}
	orders := s.customerHTTP(s.tokenA, "my_orders", map[string]any{})
	if orders.Status != 200 || len(orders.Body["entries"].([]any)) != 1 {
		t.Fatalf("my_orders leaked another store: %v", orders)
	}
	appts := s.customerHTTP(s.tokenA, "my_appointments", map[string]any{})
	if appts.Status != 200 || len(appts.Body["entries"].([]any)) != 1 {
		t.Fatalf("my_appointments leaked another store: %v", appts)
	}
	if ledger := s.customerHTTP(s.tokenA, "my_card_ledger", map[string]any{"card_id": otherCard}); ledger.Status != http.StatusNotFound || ledger.Detail() != "not found" {
		t.Fatalf("my_card_ledger leaked another store: %v", ledger)
	}
}

func TestCustomerMergedSeesSurvivorData(t *testing.T) {
	s := openFixture(t)
	callerB := customerCaller(t, s.Shop, s.tokenB)
	call(t, s.Shop, "merge_customers", map[string]any{"keep_id": s.customerA, "merged_id": s.customerB}, 200)
	if callerB.CustomerID == nil || *callerB.CustomerID != s.customerB {
		t.Fatal(callerB)
	}
	cards := s.HTTP(t, &callerB, "my_cards", map[string]any{})
	if cards.Status != 200 || len(cards.Body["items"].([]any)) != 2 {
		t.Fatalf("merged customer cards = %v", cards)
	}
	profile := s.HTTP(t, &callerB, "my_profile", map[string]any{})
	if profile.Status != 200 || profile.Body["name"] != "顾客甲" {
		t.Fatalf("merged customer profile = %v", profile)
	}
}

func TestAuditActorShowsCustomerAndMasksPhone(t *testing.T) {
	s := optest.OpenShop(t)
	_, customer := mustLogin(t, s, "fake-openid:audit", "fake-phone:13800130013")
	caller := ops.Caller{TenantID: s.Res.TenantID, StoreID: s.Res.StoreID, StaffID: s.Res.OwnerID, Permissions: map[ops.Permission]ops.Grant{perm.AuditLogView: {}, perm.CustomerView: {}}}
	body := s.HTTP(t, &caller, "list_audit_log", map[string]any{"target_id": customer["id"]})
	if body.Status != 200 {
		t.Fatal(body)
	}
	entries := body.Body["entries"].([]any)
	if len(entries) < 2 {
		t.Fatal(entries)
	}
	seenCreate := false
	for _, raw := range entries {
		entry := raw.(map[string]any)
		actor := entry["actor"].(map[string]any)
		if actor["kind"] != "customer" || actor["name"] != "顾客本人" {
			t.Fatalf("actor = %v", actor)
		}
		if entry["action"] == "create_customer" {
			seenCreate = true
			if !strings.Contains(string(mustJSON(t, entry["after"])), "138****0013") {
				t.Fatalf("audit phone not masked: %v", entry["after"])
			}
		}
	}
	if !seenCreate {
		t.Fatal("missing customer create audit")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestActorCheckConstraints(t *testing.T) {
	s := optest.OpenShop(t)
	customer := uuid.MustParse(call(t, s, "create_customer", map[string]any{"phone": "13800130014"}, 200)["customer"].(map[string]any)["id"].(string))
	cases := []struct {
		name string
		sql  string
		args []any
		want string
	}{
		{"customer both actors", `INSERT INTO customers (tenant_id, store_id, created_by, created_by_customer) VALUES ($1,$2,$3,$4)`, []any{s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, customer}, "customers_actor_check"},
		{"customer no actor", `INSERT INTO customers (tenant_id, store_id) VALUES ($1,$2)`, []any{s.Res.TenantID, s.Res.StoreID}, "customers_actor_check"},
		{"audit both actors", `INSERT INTO audit_log (tenant_id, store_id, action, target_id, target_kind, customer_id, actor_id, actor_customer_id) VALUES ($1,$2,'x',$3,'customer',$4,$5,$6)`, []any{s.Res.TenantID, s.Res.StoreID, customer, customer, s.Res.OwnerID, customer}, "audit_log_actor_check"},
		{"audit no actor", `INSERT INTO audit_log (tenant_id, store_id, action, target_id, target_kind, customer_id) VALUES ($1,$2,'x',$3,'customer',$4)`, []any{s.Res.TenantID, s.Res.StoreID, customer, customer}, "audit_log_actor_check"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
			_, err := tx.Exec(context.Background(), tc.sql, tc.args...)
			var pg *pgconn.PgError
			if !errors.As(err, &pg) || pg.ConstraintName != tc.want {
				t.Fatalf("err = %v, want constraint %s", err, tc.want)
			}
		})
	}
}

func TestCustomerLoginsRLS(t *testing.T) {
	s := optest.OpenShop(t)
	other, err := staff.Bootstrap(context.Background(), s.Pool, staff.BootstrapInput{
		TenantName: "隔离租户", StoreName: "隔离店", OwnerName: "隔离老板", OwnerNumber: "001",
		OwnerAccount: "rls-boss", OwnerPassword: "owner-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	status, body := s.CustomerLogin(t, other.StoreID, "fake-openid:rls", "fake-phone:13800130015")
	if status != http.StatusOK {
		t.Fatal(status, body)
	}
	var otherID uuid.UUID
	if err := s.Pool.QueryRow(context.Background(), "SELECT id FROM customer_logins WHERE store_id=$1", other.StoreID).Scan(&otherID); err != nil {
		t.Fatal(err)
	}
	if status, body := s.CustomerLogin(t, s.Res.StoreID, "fake-openid:rls", "fake-phone:13800130015"); status != http.StatusOK {
		t.Fatal(status, body)
	}
	if err := db.InTenant(context.Background(), s.Pool, s.Res.TenantID, func(q *dbgen.Queries) error {
		if _, err := q.GetCustomerLogin(context.Background(), otherID); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("cross-tenant customer login read = %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

type queryCounter struct{ all atomic.Int64 }

func (c *queryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.all.Add(1)
	return ctx
}

func (*queryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestCustomerOpsQueryCount(t *testing.T) {
	s := openFixture(t)
	counter := &queryCounter{}
	cfg := s.Pool.Config().Copy()
	cfg.ConnConfig.Tracer = counter
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	app := optest.New(t, pool)
	app.SetNow(s.Now())
	status, body := app.CustomerLogin(t, s.Res.StoreID, "fake-openid:customer-a", "")
	if status != http.StatusOK {
		t.Fatal(status, body)
	}
	token := body["token"].(string)
	count := func(op string, args any) int64 {
		counter.all.Store(0)
		if r := app.HTTPWithToken(t, token, op, args); r.Status != http.StatusOK {
			t.Fatal(op, r)
		}
		return counter.all.Load()
	}
	cardsOne := count("my_cards", map[string]any{})
	s.openCard(s.customerA, "300.00", "30.00")
	cardsTwo := count("my_cards", map[string]any{})
	if cardsOne != cardsTwo {
		t.Fatalf("my_cards SQL grew with cards: %d vs %d", cardsOne, cardsTwo)
	}
	ordersOne := count("my_orders", map[string]any{})
	s.paidOrder(s.customerA, s.otherItem, "30.00")
	ordersTwo := count("my_orders", map[string]any{})
	if ordersOne != ordersTwo {
		t.Fatalf("my_orders SQL grew with orders: %d vs %d", ordersOne, ordersTwo)
	}
	apptsOne := count("my_appointments", map[string]any{})
	s.appointment(s.customerA, s.stylist, "16:00")
	apptsTwo := count("my_appointments", map[string]any{})
	if apptsOne != apptsTwo {
		t.Fatalf("my_appointments SQL grew with appointments: %d vs %d", apptsOne, apptsTwo)
	}
}

func TestCustomerOpsHiddenFromAI(t *testing.T) {
	s := optest.OpenShop(t)
	token, _ := mustLogin(t, s, "fake-openid:ai", "fake-phone:13800130016")
	caller := customerCaller(t, s, token)
	for _, op := range []string{"my_profile", "my_cards", "my_card_ledger", "my_orders", "my_appointments"} {
		if r := s.AI(t, caller, op, map[string]any{}); r.Status != http.StatusNotFound || !errors.Is(r.Err, ops.ErrUnknownOperation) {
			t.Fatalf("%s AI = %d %v", op, r.Status, r.Err)
		}
	}
}
