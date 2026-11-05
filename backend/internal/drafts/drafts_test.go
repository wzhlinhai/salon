package drafts_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
)

func call(t *testing.T, s *optest.Shop, token, op string, args any) optest.Result {
	t.Helper()
	r := s.HTTPWithToken(t, token, op, args)
	if r.Status != 200 {
		t.Fatalf("%s: %d %v %v", op, r.Status, r.Body, r.Err)
	}
	return r
}
func rejected(t *testing.T, r optest.Result, status int, detail string) {
	t.Helper()
	if r.Status != status || !strings.Contains(r.Detail(), detail) {
		t.Fatalf("got %d %s; want %d %s", r.Status, r.Detail(), status, detail)
	}
}
func create(t *testing.T, s *optest.Shop, token string) map[string]any {
	t.Helper()
	return call(t, s, token, "create_draft", map[string]any{"operation": "create_customer", "params": map[string]any{"name": "原名"}, "summary": "新建顾客", "utterance": "帮我建顾客"}).Body
}
func id(d map[string]any) map[string]any { return map[string]any{"id": d["id"]} }
func count(t *testing.T, s *optest.Shop, table string) int {
	t.Helper()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var n int
	// table 只由本测试中的固定字面量提供。
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	return n
}

func TestDraftLifecycle(t *testing.T) {
	s := optest.OpenShop(t)
	c := s.MustCaller(s.Owner)
	before := count(t, s, "customers")
	r := s.AI(t, c, "create_customer", map[string]any{"name": "原名", "summary": "我的摘要", "utterance": "员工口述"})
	if r.Status != 200 || r.Body["id"] == nil || r.Body["summary"] != "我的摘要" || r.Body["utterance"] != "员工口述" || count(t, s, "customers") != before {
		t.Fatal(r)
	}
	d := r.Body
	if d["status"] != "pending" {
		t.Fatal(d)
	}
	call(t, s, s.Owner, "update_draft_params", map[string]any{"id": d["id"], "params": map[string]any{"name": "改后"}})
	done := call(t, s, s.Owner, "confirm_draft", id(d))
	if done.Body["status"] != "confirmed_modified" || done.Body["decided_by"] != c.StaffID.String() || done.Body["decided_at"] == nil || count(t, s, "customers") != before+1 {
		t.Fatal(done)
	}
	got := call(t, s, s.Owner, "get_draft", id(d)).Body
	if got["original_params"].(map[string]any)["name"] != "原名" || got["params"].(map[string]any)["name"] != "改后" {
		t.Fatal(got)
	}
	events := got["events"].([]any)
	if len(events) != 3 || events[0].(map[string]any)["kind"] != "created" || events[1].(map[string]any)["kind"] != "modified" || events[2].(map[string]any)["kind"] != "confirmed" {
		t.Fatal(events)
	}
	kinds := map[string]int{}
	for _, e := range events {
		m := e.(map[string]any)
		kinds[m["kind"].(string)]++
		if m["kind"] == "modified" && (m["before_params"].(map[string]any)["name"] != "原名" || m["after_params"].(map[string]any)["name"] != "改后") {
			t.Fatal(m)
		}
	}
	if kinds["created"] != 1 || kinds["modified"] != 1 || kinds["confirmed"] != 1 {
		t.Fatal(got)
	}
	ids := got["audit_log_ids"].([]any)
	if len(ids) != 1 {
		t.Fatal(got)
	}
	logs := call(t, s, s.Owner, "list_audit_log", map[string]any{}).Body["entries"].([]any)
	found := false
	for _, e := range logs {
		m := e.(map[string]any)
		if m["id"] == ids[0] {
			found = m["draft_id"] == d["id"]
		}
	}
	if !found {
		t.Fatal(logs, got)
	}
	rejected(t, s.HTTPWithToken(t, s.Owner, "confirm_draft", id(d)), 409, "草稿不是待确认状态")
	rejected(t, s.HTTPWithToken(t, s.Owner, "update_draft_params", map[string]any{"id": d["id"], "params": map[string]any{}}), 409, "草稿不是待确认状态")
	rejected(t, s.AI(t, c, "confirm_draft", id(d)), 404, "unknown operation")
}

func TestDraftPermission(t *testing.T) {
	s := optest.OpenShop(t)
	role := call(t, s, s.Owner, "create_role", map[string]any{"name": "起草人", "permissions": []any{map[string]any{"permission": "customer.edit"}}}).Body["id"].(string)
	staff, token := s.Hire("员工", "002", "drafter", uuid.MustParse(role))
	d := create(t, s, token)
	other := create(t, s, s.Owner)
	call(t, s, s.Owner, "set_staff_roles", map[string]any{"staff_id": staff, "role_ids": []any{}})
	for _, target := range []map[string]any{d, other} {
		rejected(t, s.HTTPWithToken(t, token, "confirm_draft", id(target)), 403, "permission denied")
		got := call(t, s, s.Owner, "get_draft", id(target)).Body
		if got["status"] != "pending" || len(got["events"].([]any)) != 1 {
			t.Fatal(got)
		}
	}
	for _, ch := range s.Channels() {
		c := s.MustCaller(token)
		rejected(t, ch.Call(t, c, "create_draft", map[string]any{"operation": "create_customer", "params": map[string]any{}, "utterance": "", "summary": ""}), 403, "permission denied")
	}
	rejected(t, s.AI(t, s.MustCaller(token), "create_customer", map[string]any{}), 403, "permission denied")
	rejected(t, s.HTTPWithToken(t, token, "get_draft", id(other)), 404, "not found")
	rejected(t, s.HTTPWithToken(t, token, "update_draft_params", map[string]any{"id": other["id"], "params": map[string]any{}}), 404, "not found")
	// 发起人失去执行权限后仍可修改自己的参数，但不能确认。
	call(t, s, token, "update_draft_params", map[string]any{"id": d["id"], "params": map[string]any{"name": "待复核"}})
	for _, ch := range s.Channels() {
		list := ch.Call(t, s.MustCaller(token), "list_drafts", map[string]any{})
		if list.Status != 200 || len(list.Body["drafts"].([]any)) != 1 {
			t.Fatal(list)
		}
		rejected(t, ch.Call(t, s.MustCaller(token), "get_draft", id(other)), 404, "not found")
	}
}

func TestDraftExpiry(t *testing.T) {
	for _, path := range []string{"confirm_draft", "get_draft", "list_drafts"} {
		t.Run(path, func(t *testing.T) {
			s := optest.OpenShop(t)
			s.SetNow(time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC))
			call(t, s, s.Owner, "change_setting", map[string]any{"key": "business_day_end", "value": "03:30", "effective_from": "2026-11-05"})
			s.Advance(24 * time.Hour)
			d := create(t, s, s.Owner)
			deadline, err := time.Parse(time.RFC3339, d["expires_at"].(string))
			if err != nil {
				t.Fatal(err)
			}
			if d["business_date"] != "2026-11-05" || deadline.In(time.FixedZone("CST", 8*3600)).Format("2006-01-02 15:04") != "2026-11-06 03:30" {
				t.Fatal(d)
			}
			s.Advance(deadline.Sub(s.Now()))
			if path == "confirm_draft" {
				rejected(t, s.HTTPWithToken(t, s.Owner, path, id(d)), 409, "已过期")
			} else {
				args := id(d)
				if path == "list_drafts" {
					args = map[string]any{}
				}
				call(t, s, s.Owner, path, args)
			}
			got := call(t, s, s.Owner, "get_draft", id(d)).Body
			if got["status"] != "expired" || got["decided_at"] == nil {
				t.Fatal(got)
			}
			n := 0
			for _, e := range got["events"].([]any) {
				if e.(map[string]any)["kind"] == "expired" {
					n++
				}
			}
			if n != 1 {
				t.Fatal(got)
			}
			rejected(t, s.HTTPWithToken(t, s.Owner, "confirm_draft", id(d)), 409, "已过期")
			for range 2 {
				call(t, s, s.Owner, "list_drafts", map[string]any{})
			}
			refreshed := call(t, s, s.Owner, "get_draft", id(d)).Body
			if len(refreshed["events"].([]any)) != 2 {
				t.Fatal(refreshed)
			}
		})
	}
}

func TestDraftReject(t *testing.T) {
	s := optest.OpenShop(t)
	d := create(t, s, s.Owner)
	call(t, s, s.Owner, "reject_draft", map[string]any{"id": d["id"], "reason": "听错了"})
	got := call(t, s, s.Owner, "get_draft", id(d)).Body
	if got["status"] != "rejected" || got["reject_reason"] != "听错了" || got["decided_by"] != s.Res.OwnerID.String() || len(got["events"].([]any)) != 2 {
		t.Fatal(got)
	}
	found := false
	for _, e := range got["events"].([]any) {
		if e.(map[string]any)["kind"] == "rejected" {
			found = true
		}
	}
	if !found {
		t.Fatal(got)
	}
	rejected(t, s.HTTPWithToken(t, s.Owner, "confirm_draft", id(d)), 409, "草稿不是待确认状态")
	rejected(t, s.HTTPWithToken(t, s.Owner, "reject_draft", map[string]any{"id": d["id"], "reason": "重复"}), 409, "草稿不是待确认状态")
}

func TestDraftDualConfirm(t *testing.T) {
	s := optest.OpenShop(t)
	s.SetNow(time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC))
	// 逐个起草金额类操作；虚构业务目标使检查通过后在原 Handle 返回不存在。
	for i, name := range []string{"adjust_card", "void_order", "refund_card", "reprice_order_item"} {
		d := call(t, s, s.Owner, "create_draft", map[string]any{"operation": name, "params": monetaryParams(name), "summary": "金额", "utterance": "金额调整"}).Body
		if name == "adjust_card" {
			rejected(t, s.HTTPWithToken(t, s.Owner, "confirm_draft", id(d)), 404, "not found")
		}
		call(t, s, s.Owner, "change_setting", map[string]any{"key": "draft_dual_confirm", "value": "on", "effective_from": "2026-11-04"})
		rejected(t, s.HTTPWithToken(t, s.Owner, "confirm_draft", id(d)), 409, "金额草稿必须由另一人确认")
		_, second := s.Hire("确认人", strconv.Itoa(i+2), "confirmer"+strconv.Itoa(i), s.Role("老板"))
		r := s.HTTPWithToken(t, second, "confirm_draft", id(d))
		rejected(t, r, 404, "not found") // 通过双人检查，原 Handle 找不到虚构业务目标。
		call(t, s, s.Owner, "change_setting", map[string]any{"key": "draft_dual_confirm", "value": "off", "effective_from": "2026-11-04"})
		rejected(t, s.HTTPWithToken(t, s.Owner, "confirm_draft", id(d)), 404, "not found")
	}
	call(t, s, s.Owner, "change_setting", map[string]any{"key": "draft_dual_confirm", "value": "on", "effective_from": "2026-11-04"})
	d := create(t, s, s.Owner)
	if call(t, s, s.Owner, "confirm_draft", id(d)).Body["status"] != "confirmed" {
		t.Fatal(d)
	}
}
func monetaryParams(name string) map[string]any {
	x := uuid.NewString()
	switch name {
	case "adjust_card":
		return map[string]any{"card_id": x, "reason": "修正"}
	case "void_order":
		return map[string]any{"order_id": x, "reason": "撤单"}
	case "refund_card":
		return map[string]any{"card_id": x, "method": "cash", "amount": "0.00"}
	default:
		return map[string]any{"order_id": x, "order_item_id": x, "price": "1.00", "reason": "改价"}
	}
}

func TestDraftLocksAndAtomicity(t *testing.T) {
	s := optest.OpenShop(t)
	d := create(t, s, s.Owner)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	if _, err := tx.Exec(context.Background(), "SELECT id FROM ai_drafts WHERE id=$1 FOR NO KEY UPDATE", d["id"]); err != nil {
		t.Fatal(err)
	}
	done := make(chan optest.Result, 2)
	for range 2 {
		go func() { done <- s.HTTPWithToken(t, s.Owner, "confirm_draft", id(d)) }()
	}
	optest.AssertWaiting(t, s.Pool, done, 2)
	dbtest.Rollback(t, tx)
	succeeded, conflicted := 0, 0
	for range 2 {
		select {
		case result := <-done:
			if result.Status == 200 {
				succeeded++
			} else {
				rejected(t, result, 409, "草稿不是待确认状态")
				conflicted++
			}
		case <-time.After(10 * time.Second):
			t.Fatal("确认请求未返回")
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatal(succeeded, conflicted)
	}
	if count(t, s, "customers") != 1 {
		t.Fatal("目标未执行一次")
	}
	rejected(t, s.HTTPWithToken(t, s.Owner, "confirm_draft", id(d)), 409, "草稿不是待确认状态")
	bad := call(t, s, s.Owner, "create_draft", map[string]any{"operation": "create_customer", "params": map[string]any{"phone": "bad"}, "summary": "错误手机号", "utterance": ""}).Body
	rejected(t, s.HTTPWithToken(t, s.Owner, "confirm_draft", id(bad)), 409, "手机号")
	got := call(t, s, s.Owner, "get_draft", id(bad)).Body
	if got["status"] != "pending" || len(got["events"].([]any)) != 1 || len(got["audit_log_ids"].([]any)) != 0 {
		t.Fatal(got)
	}
}

func TestDraftSchema(t *testing.T) {
	s := optest.OpenShop(t)
	for _, ch := range s.Channels() {
		c := s.MustCaller(s.Owner)
		r := ch.Call(t, c, "create_draft", map[string]any{"operation": "create_customer", "params": map[string]any{"gender": "invalid"}, "summary": "", "utterance": ""})
		rejected(t, r, 422, "gender")
		rejected(t, ch.Call(t, c, "create_draft", map[string]any{"operation": "confirm_draft", "params": map[string]any{}, "summary": "", "utterance": ""}), 404, "unknown operation")
	}
	if count(t, s, "ai_drafts") != 0 {
		t.Fatal("invalid draft stored")
	}
	d := create(t, s, s.Owner)
	rejected(t, s.HTTPWithToken(t, s.Owner, "update_draft_params", map[string]any{"id": d["id"], "params": map[string]any{"gender": "invalid"}}), 422, "gender")
	raw, _ := json.Marshal(map[string]any{"summary": 1})
	_, err := s.Registry.CallTool(context.Background(), s.MustCaller(s.Owner), "create_customer", raw)
	if ops.StatusOf(err) != 422 || !strings.Contains(err.Error(), "summary must be a string") {
		t.Fatal(err)
	}
}

func TestDraftStorage(t *testing.T) {
	s := optest.OpenShop(t)
	d := create(t, s, s.Owner)
	for _, query := range []string{
		"UPDATE ai_drafts SET original_params='{}'",
		"UPDATE ai_drafts SET summary='改写'",
		"DELETE FROM ai_drafts",
		"UPDATE ai_draft_events SET kind='rejected'",
		"DELETE FROM ai_draft_events",
	} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), query)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" || !strings.Contains(pgErr.Message, "permission denied") {
			t.Fatalf("%s: %v", query, err)
		}
		dbtest.Rollback(t, tx)
	}
	// 即使知道真实门店和草稿 ID，另一租户也读不到草稿或事件。
	tx := dbtest.AppTx(t, s.Pool, uuid.New())
	for _, table := range []string{"ai_drafts", "ai_draft_events"} {
		var n int
		if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Fatal(table, n, err)
		}
	}
	dbtest.Rollback(t, tx)
	c := s.MustCaller(s.Owner)
	c.StoreID = uuid.New()
	for _, ch := range s.Channels() {
		rejected(t, ch.Call(t, c, "get_draft", id(d)), 404, "not found")
		r := ch.Call(t, c, "list_drafts", map[string]any{})
		if r.Status != 200 || len(r.Body["drafts"].([]any)) != 0 {
			t.Fatal(r)
		}
	}
	// 同租户不同门店的审计行也不能关联这个草稿。
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	other, err := dbgen.New(tx).CreateStore(context.Background(), dbgen.CreateStoreParams{TenantID: s.Res.TenantID, Name: "另一门店"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(context.Background(), `INSERT INTO audit_log (tenant_id,store_id,action,target_id,target_kind,actor_id,draft_id) VALUES ($1,$2,'create_customer',$3,'staff',$4,$5)`, s.Res.TenantID, other.ID, uuid.New(), s.Res.OwnerID, d["id"])
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" || !strings.Contains(pgErr.Message, "foreign key") {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
}

func TestDraftPrivacy(t *testing.T) {
	s := optest.OpenShop(t)
	role := call(t, s, s.Owner, "create_role", map[string]any{"name": "起草无完整手机", "permissions": []any{map[string]any{"permission": "customer.edit"}}}).Body["id"].(string)
	_, token := s.Hire("员工", "002", "masked", uuid.MustParse(role))
	c := s.MustCaller(token)
	r := s.AI(t, c, "create_customer", map[string]any{"phone": "13812345678"})
	if r.Status != 200 {
		t.Fatal(r)
	}
	phone := func(m map[string]any, key string) string { return m[key].(map[string]any)["phone"].(string) }
	if phone(r.Body, "params") != "138****5678" || phone(r.Body, "original_params") != "138****5678" {
		t.Fatal(r)
	}
	d := r.Body
	updated := call(t, s, token, "update_draft_params", map[string]any{"id": d["id"], "params": map[string]any{"phone": "13912345678"}})
	if phone(updated.Body, "params") != "139****5678" {
		t.Fatal(updated)
	}
	for _, ch := range s.Channels() {
		got := ch.Call(t, c, "get_draft", id(d))
		if got.Status != 200 {
			t.Fatal(got)
		}
		if phone(got.Body, "params") != "139****5678" || phone(got.Body, "original_params") != "138****5678" {
			t.Fatal(got)
		}
		for _, e := range got.Body["events"].([]any) {
			m := e.(map[string]any)
			if strings.Contains(phone(m, "after_params"), "1234") {
				t.Fatal(m)
			}
			if m["before_params"] != nil && strings.Contains(phone(m, "before_params"), "1234") {
				t.Fatal(m)
			}
		}
		list := ch.Call(t, c, "list_drafts", map[string]any{})
		if list.Status != 200 || phone(list.Body["drafts"].([]any)[0].(map[string]any), "params") != "139****5678" {
			t.Fatal(list)
		}
	}
	got := call(t, s, s.Owner, "get_draft", id(d)).Body
	if phone(got, "original_params") != "13812345678" || phone(got, "params") != "13912345678" {
		t.Fatal(got)
	}
	confirmed := call(t, s, token, "confirm_draft", id(d))
	if phone(confirmed.Body, "params") != "139****5678" {
		t.Fatal(confirmed)
	}
}

type queryCounter struct{ n atomic.Int64 }

func (c *queryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.n.Add(1)
	return ctx
}
func (*queryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestDraftQueryCount(t *testing.T) {
	s := optest.OpenShop(t)
	s.SetNow(time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC))
	create(t, s, s.Owner)
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
	app.SetNow(s.Now().Add(48 * time.Hour))
	c := s.MustCaller(s.Owner)
	for _, ch := range app.Channels() {
		counter.n.Store(0)
		one := ch.Call(t, c, "list_drafts", map[string]any{})
		a := counter.n.Load()
		for range 5 {
			create(t, s, s.Owner)
		}
		counter.n.Store(0)
		many := ch.Call(t, c, "list_drafts", map[string]any{})
		b := counter.n.Load()
		if one.Status != 200 || many.Status != 200 || a != b {
			t.Fatal(one, many, a, b)
		}
		for _, entry := range many.Body["drafts"].([]any) {
			if entry.(map[string]any)["status"] != "expired" {
				t.Fatal(entry)
			}
		}
	}
	app.SetNow(s.Now())
	for _, ch := range app.Channels() {
		d := create(t, s, s.Owner)
		counter.n.Store(0)
		one := ch.Call(t, c, "get_draft", id(d))
		a := counter.n.Load()
		for range 5 {
			call(t, s, s.Owner, "update_draft_params", map[string]any{"id": d["id"], "params": map[string]any{"name": "修改"}})
		}
		counter.n.Store(0)
		many := ch.Call(t, c, "get_draft", id(d))
		b := counter.n.Load()
		if one.Status != 200 || many.Status != 200 || a != b || len(many.Body["events"].([]any)) != 6 {
			t.Fatal(one, many, a, b)
		}
	}

}

func TestDraftConfirmationGrant(t *testing.T) {
	s := optest.OpenShop(t)
	first := call(t, s, s.Owner, "open_order", map[string]any{"gender": "male", "designated": false, "items": []any{}}).Body
	second := call(t, s, s.Owner, "open_order", map[string]any{"customer_id": first["customer_id"], "gender": "male", "designated": false, "items": []any{}}).Body
	role := call(t, s, s.Owner, "create_role", map[string]any{"name": "本人改单", "permissions": []any{map[string]any{"permission": "order.edit", "scope": "self"}}}).Body["id"].(string)
	_, token := s.Hire("员工", "002", "selfedit", uuid.MustParse(role))
	d := call(t, s, s.Owner, "create_draft", map[string]any{"operation": "merge_orders", "params": map[string]any{"order_id": first["id"], "merged_order_ids": []any{second["id"]}}, "summary": "合并单", "utterance": "合并"}).Body
	rejected(t, s.HTTPWithToken(t, token, "confirm_draft", id(d)), 403, "permission denied")
	if call(t, s, s.Owner, "get_draft", id(d)).Body["status"] != "pending" {
		t.Fatal(d)
	}
}

func TestDraftForeignKeys(t *testing.T) {
	s := optest.OpenShop(t)
	d := create(t, s, s.Owner)
	for _, item := range []struct{ column, expr, code, detail string }{
		{"store_id", "gen_random_uuid()", "23503", "foreign key"},
		{"initiator", "gen_random_uuid()", "23503", "foreign key"},
		{"decided_by", "gen_random_uuid()", "23503", "foreign key"},
		{"status", "'unknown'", "23514", "check constraint"},
	} {
		columns := []string{"tenant_id", "store_id", "initiator", "operation", "utterance", "params", "original_params", "summary", "status", "business_date", "expires_at", "created_at", "decided_by"}
		values := append([]string{}, columns...)
		for i, name := range columns {
			if name == item.column {
				values[i] = item.expr
			}
		}
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), "INSERT INTO ai_drafts ("+strings.Join(columns, ",")+") SELECT "+strings.Join(values, ",")+" FROM ai_drafts WHERE id=$1", d["id"])
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != item.code || !strings.Contains(pgErr.Message, item.detail) {
			t.Fatal(item, err)
		}
		dbtest.Rollback(t, tx)
	}
	for _, item := range []struct{ column, expr, code, detail string }{
		{"store_id", "gen_random_uuid()", "23503", "foreign key"},
		{"draft_id", "gen_random_uuid()", "23503", "foreign key"},
		{"actor_id", "gen_random_uuid()", "23503", "foreign key"},
		{"kind", "'unknown'", "23514", "check constraint"},
	} {
		columns := []string{"tenant_id", "store_id", "draft_id", "kind", "before_params", "after_params", "actor_id", "at"}
		values := append([]string{}, columns...)
		for i, name := range columns {
			if name == item.column {
				values[i] = item.expr
			}
		}
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), "INSERT INTO ai_draft_events ("+strings.Join(columns, ",")+") SELECT "+strings.Join(values, ",")+" FROM ai_draft_events WHERE draft_id=$1", d["id"])
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != item.code || !strings.Contains(pgErr.Message, item.detail) {
			t.Fatal(item, err)
		}
		dbtest.Rollback(t, tx)
	}
}

func TestDraftBatchLock(t *testing.T) {
	s := optest.OpenShop(t)
	d := create(t, s, s.Owner)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "UPDATE ai_drafts SET status='confirmed' WHERE id=$1", d["id"])
	s.Advance(48 * time.Hour)
	done := make(chan optest.Result, 1)
	go func() { done <- s.HTTPWithToken(t, s.Owner, "list_drafts", map[string]any{}) }()
	optest.AssertWaiting(t, s.Pool, done, 1)
	dbtest.Commit(t, tx)
	result := optest.WaitResult(t, done, 200)
	rows := result.Body["drafts"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["status"] != "confirmed" {
		t.Fatal(result)
	}
	got := call(t, s, s.Owner, "get_draft", id(d)).Body
	if len(got["events"].([]any)) != 1 {
		t.Fatal(got)
	}
}
