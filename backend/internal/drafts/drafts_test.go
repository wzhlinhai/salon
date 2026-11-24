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
	"github.com/wzhlinhai/salon/backend/internal/db"
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
	updated := call(t, s, s.Owner, "update_draft_params", map[string]any{"id": d["id"], "params": map[string]any{"name": "改后"}, "summary": "改后摘要"})
	done := call(t, s, s.Owner, "confirm_draft", confirmArgs(updated.Body))
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
	rejected(t, s.HTTPWithToken(t, s.Owner, "confirm_draft", confirmArgs(d)), 409, "草稿不是待确认状态")
	rejected(t, s.HTTPWithToken(t, s.Owner, "update_draft_params", map[string]any{"id": d["id"], "params": map[string]any{}, "summary": "再次修改"}), 409, "草稿不是待确认状态")
	rejected(t, s.AI(t, c, "confirm_draft", confirmArgs(d)), 404, "unknown operation")
}

func TestDraftUpdateSummary(t *testing.T) {
	s := optest.OpenShop(t)
	d := create(t, s, s.Owner)
	updated := call(t, s, s.Owner, "update_draft_params", map[string]any{"id": d["id"], "params": map[string]any{"name": "改后"}, "summary": "改后摘要"})
	if updated.Body["summary"] != "改后摘要" {
		t.Fatal(updated)
	}
	got := call(t, s, s.Owner, "get_draft", id(d)).Body
	if got["summary"] != "改后摘要" || got["params"].(map[string]any)["name"] != "改后" {
		t.Fatal(got)
	}
	events := got["events"].([]any)
	if len(events) != 2 {
		t.Fatal(events)
	}
	modified := events[1].(map[string]any)
	if modified["kind"] != "modified" || modified["before_summary"] != "新建顾客" || modified["before_params"].(map[string]any)["name"] != "原名" || modified["after_params"].(map[string]any)["name"] != "改后" {
		t.Fatal(modified)
	}
	rejected(t, s.HTTPWithToken(t, s.Owner, "update_draft_params", map[string]any{"id": d["id"], "params": map[string]any{"name": "空白"}, "summary": "  \t "}), 422, "草稿摘要不能为空")
	if got := call(t, s, s.Owner, "get_draft", id(d)).Body; got["summary"] != "改后摘要" || len(got["events"].([]any)) != 2 {
		t.Fatal(got)
	}
}

func TestDraftPermission(t *testing.T) {
	s := optest.OpenShop(t)
	role := call(t, s, s.Owner, "create_role", map[string]any{"name": "起草人", "permissions": []any{map[string]any{"permission": "customer.edit"}}}).Body["id"].(string)
	staff, token := s.Hire("员工", "002", "drafter", uuid.MustParse(role))
	d := create(t, s, token)
	other := create(t, s, s.Owner)
	call(t, s, s.Owner, "set_staff_roles", map[string]any{"staff_id": staff, "role_ids": []any{}})
	rejected(t, s.HTTPWithToken(t, token, "confirm_draft", confirmArgs(d)), 403, "permission denied")
	rejected(t, s.HTTPWithToken(t, token, "confirm_draft", confirmArgs(other)), 404, "not found")
	for _, target := range []map[string]any{d, other} {
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
	rejected(t, s.HTTPWithToken(t, token, "update_draft_params", map[string]any{"id": other["id"], "params": map[string]any{}, "summary": "修改"}), 404, "not found")
	// 发起人失去执行权限后仍可修改自己的参数，但不能确认。
	call(t, s, token, "update_draft_params", map[string]any{"id": d["id"], "params": map[string]any{"name": "待复核"}, "summary": "待复核"})
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
			before := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "ai_drafts", "ai_draft_events")
			if path == "confirm_draft" {
				rejected(t, s.HTTPWithToken(t, s.Owner, path, confirmArgs(d)), 409, "已过期")
			} else {
				args := id(d)
				if path == "list_drafts" {
					args = map[string]any{}
				}
				got := call(t, s, s.Owner, path, args)
				if path == "list_drafts" {
					entries := got.Body["drafts"].([]any)
					if len(entries) != 1 || entries[0].(map[string]any)["status"] != "expired" || entries[0].(map[string]any)["decided_at"] != nil {
						t.Fatal(got)
					}
				} else if got.Body["status"] != "expired" || got.Body["decided_at"] != nil || len(got.Body["events"].([]any)) != 1 {
					t.Fatal(got)
				}
				if after := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "ai_drafts", "ai_draft_events"); after != before {
					t.Fatal("read persisted the draft expiry")
				}
				rejected(t, s.HTTPWithToken(t, s.Owner, "confirm_draft", confirmArgs(d)), 409, "已过期")
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
			rejected(t, s.HTTPWithToken(t, s.Owner, "confirm_draft", confirmArgs(d)), 409, "已过期")
			refreshed := call(t, s, s.Owner, "get_draft", id(d)).Body
			if len(refreshed["events"].([]any)) != 2 {
				t.Fatalf("repeated confirm added an expiry event: %v", refreshed)
			}
			call(t, s, s.Owner, "list_drafts", map[string]any{})
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
	rejected(t, s.HTTPWithToken(t, s.Owner, "confirm_draft", confirmArgs(d)), 409, "草稿不是待确认状态")
	rejected(t, s.HTTPWithToken(t, s.Owner, "reject_draft", map[string]any{"id": d["id"], "reason": "重复"}), 409, "草稿不是待确认状态")
}

func TestDraftReadsAndCreateExpiry(t *testing.T) {
	s := optest.OpenShop(t)
	s.SetNow(time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC))
	d := create(t, s, s.Owner)
	deadline, err := time.Parse(time.RFC3339, d["expires_at"].(string))
	if err != nil {
		t.Fatal(err)
	}
	s.Advance(deadline.Sub(s.Now()))
	before := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "ai_drafts", "ai_draft_events")
	for _, ch := range s.Channels() {
		list := ch.Call(t, s.MustCaller(s.Owner), "list_drafts", map[string]any{})
		if list.Status != 200 || list.Body["drafts"].([]any)[0].(map[string]any)["status"] != "expired" {
			t.Fatal(ch.Name, list)
		}
		got := ch.Call(t, s.MustCaller(s.Owner), "get_draft", id(d))
		if got.Status != 200 || got.Body["status"] != "expired" || got.Body["decided_at"] != nil || len(got.Body["events"].([]any)) != 1 {
			t.Fatal(ch.Name, got)
		}
	}
	if after := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "ai_drafts", "ai_draft_events"); after != before {
		t.Fatal("reads persisted the draft expiry")
	}
	create(t, s, s.Owner)
	got := call(t, s, s.Owner, "get_draft", id(d)).Body
	if got["status"] != "expired" || got["decided_at"] == nil || len(got["events"].([]any)) != 2 {
		t.Fatal(got)
	}
}

func TestDraftDualConfirm(t *testing.T) {
	s := optest.OpenShop(t)
	s.SetNow(time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC))
	// 逐个起草金额类操作；虚构业务目标使检查通过后在原 Handle 返回不存在。
	for i, name := range []string{"adjust_card", "void_order", "refund_card", "reprice_order_item"} {
		d := call(t, s, s.Owner, "create_draft", map[string]any{"operation": name, "params": monetaryParams(name), "summary": "金额", "utterance": "金额调整"}).Body
		if name == "adjust_card" {
			rejected(t, s.HTTPWithToken(t, s.Owner, "confirm_draft", confirmArgs(d)), 404, "not found")
		}
		call(t, s, s.Owner, "change_setting", map[string]any{"key": "draft_dual_confirm", "value": "on", "effective_from": "2026-11-04"})
		rejected(t, s.HTTPWithToken(t, s.Owner, "confirm_draft", confirmArgs(d)), 409, "金额草稿必须由另一人确认")
		_, second := s.Hire("确认人", strconv.Itoa(i+2), "confirmer"+strconv.Itoa(i), s.Role("老板"))
		r := s.HTTPWithToken(t, second, "confirm_draft", confirmArgs(d))
		rejected(t, r, 404, "not found") // 通过双人检查，原 Handle 找不到虚构业务目标。
		call(t, s, s.Owner, "change_setting", map[string]any{"key": "draft_dual_confirm", "value": "off", "effective_from": "2026-11-04"})
		rejected(t, s.HTTPWithToken(t, s.Owner, "confirm_draft", confirmArgs(d)), 404, "not found")
	}
	call(t, s, s.Owner, "change_setting", map[string]any{"key": "draft_dual_confirm", "value": "on", "effective_from": "2026-11-04"})
	d := create(t, s, s.Owner)
	if call(t, s, s.Owner, "confirm_draft", confirmArgs(d)).Body["status"] != "confirmed" {
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
		go func() { done <- s.HTTPWithToken(t, s.Owner, "confirm_draft", confirmArgs(d)) }()
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
	rejected(t, s.HTTPWithToken(t, s.Owner, "confirm_draft", confirmArgs(d)), 409, "草稿不是待确认状态")
	bad := call(t, s, s.Owner, "create_draft", map[string]any{"operation": "create_customer", "params": map[string]any{"phone": "bad"}, "summary": "错误手机号", "utterance": ""}).Body
	rejected(t, s.HTTPWithToken(t, s.Owner, "confirm_draft", confirmArgs(bad)), 409, "手机号")
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
	rejected(t, s.HTTPWithToken(t, s.Owner, "update_draft_params", map[string]any{"id": d["id"], "params": map[string]any{"gender": "invalid"}, "summary": "改成无效性别"}), 422, "gender")
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
	rejected(t, s.HTTP(t, &c, "update_draft_params", map[string]any{"id": d["id"], "params": map[string]any{"name": "跨店"}, "summary": "跨店修改"}), 404, "not found")
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
	updated := call(t, s, token, "update_draft_params", map[string]any{"id": d["id"], "params": map[string]any{"phone": "13912345678"}, "summary": "换手机号"})
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
	confirmed := call(t, s, token, "confirm_draft", confirmArgs(updated.Body))
	if phone(confirmed.Body, "params") != "139****5678" {
		t.Fatal(confirmed)
	}
	raw := call(t, s, token, "create_draft", map[string]any{"operation": "create_customer", "params": map[string]any{"phone": "13712345678"}, "summary": "草稿读取", "utterance": ""})
	if phone(raw.Body, "params") != "137****5678" {
		t.Fatal(raw)
	}
	rejectedDraft := call(t, s, token, "reject_draft", map[string]any{"id": raw.Body["id"], "reason": "不需要"})
	if phone(rejectedDraft.Body, "params") != "137****5678" {
		t.Fatal(rejectedDraft)
	}
}

func TestDraftCreateAppointmentPhoneMask(t *testing.T) {
	s := optest.OpenShop(t)
	staff, token := s.Hire("发型师", "002", "stylist", s.Role("发型师"))
	params := func(phone string) map[string]any {
		return map[string]any{
			"customer_name":  "李四",
			"customer_phone": phone,
			"staff_id":       staff.String(),
			"starts_at":      "2026-11-20T15:00:00+08:00",
		}
	}
	phoneOf := func(m map[string]any, key string) string {
		t.Helper()
		params, ok := m[key].(map[string]any)
		if !ok {
			t.Fatalf("%s: %v", key, m)
		}
		return params["customer_phone"].(string)
	}
	c := s.MustCaller(token)
	r := s.AI(t, c, "create_appointment", params("13800001234"))
	if r.Status != 200 {
		t.Fatal(r)
	}
	d := r.Body
	if phoneOf(d, "params") != "138****1234" || phoneOf(d, "original_params") != "138****1234" {
		t.Fatal(d)
	}
	updated := call(t, s, s.Owner, "update_draft_params", map[string]any{"id": d["id"], "params": params("13900001234"), "summary": "换成 139 的号码"})
	if phoneOf(updated.Body, "params") != "13900001234" || phoneOf(updated.Body, "original_params") != "13800001234" {
		t.Fatal(updated)
	}
	for _, ch := range s.Channels() {
		list := ch.Call(t, c, "list_drafts", map[string]any{})
		if list.Status != 200 {
			t.Fatal(ch.Name, list)
		}
		entries := list.Body["drafts"].([]any)
		if len(entries) != 1 {
			t.Fatal(ch.Name, list)
		}
		entry := entries[0].(map[string]any)
		if phoneOf(entry, "params") != "139****1234" || phoneOf(entry, "original_params") != "138****1234" {
			t.Fatal(ch.Name, list)
		}
		got := ch.Call(t, c, "get_draft", id(d))
		if got.Status != 200 {
			t.Fatal(ch.Name, got)
		}
		if phoneOf(got.Body, "params") != "139****1234" || phoneOf(got.Body, "original_params") != "138****1234" {
			t.Fatal(ch.Name, got)
		}
		events := got.Body["events"].([]any)
		if len(events) != 2 {
			t.Fatal(ch.Name, got)
		}
		created, modified := events[0].(map[string]any), events[1].(map[string]any)
		if created["before_params"] != nil || phoneOf(created, "after_params") != "138****1234" {
			t.Fatal(ch.Name, created)
		}
		if phoneOf(modified, "before_params") != "138****1234" || phoneOf(modified, "after_params") != "139****1234" {
			t.Fatal(ch.Name, modified)
		}
	}
	ownerView := call(t, s, s.Owner, "get_draft", id(d)).Body
	if phoneOf(ownerView, "params") != "13900001234" || phoneOf(ownerView, "original_params") != "13800001234" {
		t.Fatal(ownerView)
	}
	for _, raw := range ownerView["events"].([]any) {
		event := raw.(map[string]any)
		if event["before_params"] != nil && !strings.HasSuffix(phoneOf(event, "before_params"), "00001234") {
			t.Fatal(event)
		}
		if !strings.HasSuffix(phoneOf(event, "after_params"), "00001234") {
			t.Fatal(event)
		}
	}
	createdDraft := call(t, s, token, "create_draft", map[string]any{"operation": "create_appointment", "params": params("13700001234"), "summary": "起草预约", "utterance": ""})
	if phoneOf(createdDraft.Body, "params") != "137****1234" {
		t.Fatal(createdDraft)
	}
}

func TestDraftPreviewRedacted(t *testing.T) {
	s := optest.OpenShop(t)
	role := call(t, s, s.Owner, "create_role", map[string]any{"name": "预览无完整手机", "permissions": []any{map[string]any{"permission": "customer.edit"}}}).Body["id"].(string)
	_, token := s.Hire("员工", "002", "preview", uuid.MustParse(role))
	s.Registry.SetDraftPreview("create_customer", func(context.Context, ops.Env, json.RawMessage) (any, error) {
		return map[string]any{"phone": "13800001234"}, nil
	})
	d := call(t, s, token, "create_draft", map[string]any{"operation": "create_customer", "params": map[string]any{"name": "预览"}, "summary": "", "utterance": ""}).Body
	c := s.MustCaller(token)
	for _, ch := range s.Channels() {
		got := ch.Call(t, c, "get_draft", id(d))
		if got.Status != 200 {
			t.Fatal(ch.Name, got)
		}
		preview, ok := got.Body["preview"].(map[string]any)
		if !ok || preview["phone"] != "138****1234" {
			t.Fatal(ch.Name, got)
		}
	}
	owner := call(t, s, s.Owner, "get_draft", id(d)).Body
	if owner["preview"].(map[string]any)["phone"] != "13800001234" {
		t.Fatal(owner)
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
	pool, err := db.NewPoolWithConfig(context.Background(), cfg)
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
			call(t, s, s.Owner, "update_draft_params", map[string]any{"id": d["id"], "params": map[string]any{"name": "修改"}, "summary": "第几次修改"})
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
	d := call(t, s, token, "create_draft", map[string]any{"operation": "merge_orders", "params": map[string]any{"order_id": first["id"], "merged_order_ids": []any{second["id"]}}, "summary": "合并单", "utterance": "合并"}).Body
	rejected(t, s.HTTPWithToken(t, token, "confirm_draft", confirmArgs(d)), 403, "permission denied")
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
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "create_draft", map[string]any{"operation": "create_customer", "params": map[string]any{"name": "另一个"}, "summary": "", "utterance": ""})
	}()
	optest.AssertWaiting(t, s.Pool, done, 1)
	dbtest.Commit(t, tx)
	optest.WaitResult(t, done, 200)
	got := call(t, s, s.Owner, "get_draft", id(d)).Body
	if len(got["events"].([]any)) != 1 {
		t.Fatal(got)
	}
}

func TestDraftScopeVisibility(t *testing.T) {
	s := optest.OpenShop(t)
	s.SetNow(time.Date(2026, 11, 19, 12, 0, 0, 0, time.FixedZone("CST", 8*3600)))
	customer := call(t, s, s.Owner, "create_customer", map[string]any{"name": "顾客", "phone": "13800000001"}).Body["customer"].(map[string]any)["id"]
	first, firstToken := s.Hire("发型师甲", "002", "scope-a", s.Role("发型师"))
	secondStaff, secondToken := s.Hire("发型师乙", "003", "scope-b", s.Role("发型师"))
	params := map[string]any{"customer_id": customer, "staff_id": first.String(), "starts_at": "2026-11-20T15:00:00+08:00"}
	draft := func() map[string]any {
		return call(t, s, firstToken, "create_draft", map[string]any{"operation": "create_appointment", "params": params, "summary": "预约", "utterance": "预约"}).Body
	}
	d := draft()
	second := s.MustCaller(secondToken)
	for _, ch := range s.Channels() {
		list := ch.Call(t, second, "list_drafts", map[string]any{})
		if list.Status != 200 || len(list.Body["drafts"].([]any)) != 0 {
			t.Fatal(ch.Name, list)
		}
		rejected(t, ch.Call(t, second, "get_draft", id(d)), 404, "not found")
		rejected(t, ch.Call(t, second, "update_draft_params", map[string]any{"id": d["id"], "params": params, "summary": "越权修改"}), 404, "not found")
		rejected(t, ch.Call(t, second, "reject_draft", map[string]any{"id": d["id"], "reason": "越权"}), 404, "not found")
	}
	rejected(t, s.HTTPWithToken(t, secondToken, "confirm_draft", confirmArgs(d)), 404, "not found")

	role := call(t, s, s.Owner, "create_role", map[string]any{"name": "全店预约", "permissions": []any{map[string]any{"permission": "booking.manage", "scope": "store"}, map[string]any{"permission": "customer.view", "scope": "store"}}}).Body["id"].(string)
	_, storeToken := s.Hire("店长", "004", "scope-store", uuid.MustParse(role))
	store := s.MustCaller(storeToken)
	for _, ch := range s.Channels() {
		list := ch.Call(t, store, "list_drafts", map[string]any{})
		if list.Status != 200 || len(list.Body["drafts"].([]any)) != 1 {
			t.Fatal(ch.Name, list)
		}
		if got := ch.Call(t, store, "get_draft", id(d)); got.Status != 200 {
			t.Fatal(ch.Name, got)
		}
	}
	updated := call(t, s, storeToken, "update_draft_params", map[string]any{"id": d["id"], "params": params, "summary": "店长改预约"})
	if updated.Body["version"] == d["version"] {
		t.Fatal(updated)
	}
	call(t, s, storeToken, "reject_draft", map[string]any{"id": d["id"], "reason": "换人处理"})
	confirmed := call(t, s, storeToken, "confirm_draft", confirmArgs(draft()))
	if confirmed.Body["status"] != "confirmed" {
		t.Fatal(confirmed)
	}
	selfScoped := call(t, s, firstToken, "create_draft", map[string]any{"operation": "create_appointment", "params": map[string]any{"customer_id": customer, "staff_id": secondStaff.String(), "starts_at": "2026-11-20T16:00:00+08:00"}, "summary": "预约", "utterance": "预约"}).Body
	rejected(t, s.HTTPWithToken(t, secondToken, "confirm_draft", confirmArgs(selfScoped)), 404, "not found")
}

func TestDraftPreviewRequiresCurrentPermission(t *testing.T) {
	s := optest.OpenShop(t)
	role := call(t, s, s.Owner, "create_role", map[string]any{"name": "开单人", "permissions": []any{map[string]any{"permission": "order.create"}}}).Body["id"].(string)
	staff, token := s.Hire("开单员", "002", "preview-revoked", uuid.MustParse(role))
	d := call(t, s, token, "create_draft", map[string]any{"operation": "open_order", "params": map[string]any{"gender": "female", "designated": false, "items": []any{}}, "summary": "开单", "utterance": "开单"}).Body
	c := s.MustCaller(token)
	for _, ch := range s.Channels() {
		before := ch.Call(t, c, "get_draft", id(d))
		if before.Status != 200 || before.Body["preview"] == nil || before.Body["preview_error"] != nil {
			t.Fatal(ch.Name, before)
		}
	}
	call(t, s, s.Owner, "set_staff_roles", map[string]any{"staff_id": staff, "role_ids": []any{}})
	revoked := s.MustCaller(token)
	for _, ch := range s.Channels() {
		got := ch.Call(t, revoked, "get_draft", id(d))
		if got.Status != 200 || got.Body["status"] != "pending" || got.Body["preview"] != nil || got.Body["preview_error"] != "已无该操作权限，无法预览" || got.Body["params"] == nil {
			t.Fatal(ch.Name, got)
		}
		list := ch.Call(t, revoked, "list_drafts", map[string]any{})
		if list.Status != 200 || len(list.Body["drafts"].([]any)) != 1 || list.Body["drafts"].([]any)[0].(map[string]any)["status"] != "pending" {
			t.Fatal(ch.Name, list)
		}
	}
}

func TestDraftSpacedPhoneMasked(t *testing.T) {
	s := optest.OpenShop(t)
	role := call(t, s, s.Owner, "create_role", map[string]any{"name": "无完整手机", "permissions": []any{map[string]any{"permission": "customer.edit"}}}).Body["id"].(string)
	_, token := s.Hire("员工", "002", "spaced", uuid.MustParse(role))
	d := call(t, s, token, "create_draft", map[string]any{"operation": "create_customer", "params": map[string]any{"name": "顾客", "phone": "138 1234 5678"}, "summary": "建档", "utterance": "建档"}).Body
	phone := func(m map[string]any, key string) string { return m[key].(map[string]any)["phone"].(string) }
	got := call(t, s, token, "get_draft", id(d))
	if phone(got.Body, "params") != "138****5678" || phone(got.Body, "original_params") != "138****5678" {
		t.Fatal(got)
	}
	event := got.Body["events"].([]any)[0].(map[string]any)
	if phone(event, "after_params") != "138****5678" {
		t.Fatal(event)
	}
	owner := call(t, s, s.Owner, "get_draft", id(d)).Body
	if phone(owner, "params") != "138 1234 5678" {
		t.Fatal(owner)
	}
}

func TestDraftVersionBoundConfirm(t *testing.T) {
	s := optest.OpenShop(t)
	s.SetNow(time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC))
	d := create(t, s, s.Owner)
	if d["version"] != float64(1) {
		t.Fatal(d)
	}
	updated := call(t, s, s.Owner, "update_draft_params", map[string]any{"id": d["id"], "params": map[string]any{"name": "改后"}, "summary": "版本二"})
	if updated.Body["version"] != float64(2) {
		t.Fatal(updated)
	}
	before := count(t, s, "customers")
	rejected(t, s.HTTPWithToken(t, s.Owner, "confirm_draft", confirmArgs(d)), 409, "草稿已被修改，请重新查看后确认")
	if got := call(t, s, s.Owner, "get_draft", id(d)).Body; got["status"] != "pending" || count(t, s, "customers") != before {
		t.Fatal(got)
	}
	rejected(t, s.HTTPWithToken(t, s.Owner, "confirm_draft", map[string]any{"id": d["id"], "version": 0}), 422, "validation failed")
	if done := call(t, s, s.Owner, "confirm_draft", confirmArgs(updated.Body)); done.Body["status"] != "confirmed_modified" {
		t.Fatal(done)
	}

	call(t, s, s.Owner, "change_setting", map[string]any{"key": "draft_dual_confirm", "value": "on", "effective_from": "2026-11-04"})
	amountDraft := call(t, s, s.Owner, "create_draft", map[string]any{"operation": "adjust_card", "params": monetaryParams("adjust_card"), "summary": "金额", "utterance": "金额调整"}).Body
	_, approver := s.Hire("审批人", "003", "version-approver", s.Role("老板"))
	before = count(t, s, "customers")
	amountUpdated := call(t, s, s.Owner, "update_draft_params", map[string]any{"id": amountDraft["id"], "params": monetaryParams("adjust_card"), "summary": "金额草稿修改"})
	rejected(t, s.HTTPWithToken(t, approver, "confirm_draft", confirmArgs(amountDraft)), 409, "草稿已被修改，请重新查看后确认")
	if got := call(t, s, s.Owner, "get_draft", id(amountDraft)).Body; got["status"] != "pending" || count(t, s, "customers") != before {
		t.Fatal(got)
	}
	rejected(t, s.HTTPWithToken(t, approver, "confirm_draft", confirmArgs(amountUpdated.Body)), 404, "not found")
}
