package redpackets_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/redpackets"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

type fixture struct {
	*optest.Shop
	t        *testing.T
	customer uuid.UUID
}

func open(t *testing.T) *fixture {
	s := &fixture{Shop: optest.OpenShop(t), t: t}
	s.customer = id(s.call("create_customer", map[string]any{"name": "红包顾客", "phone": "13800131234"}, 200)["customer"].(map[string]any)["id"])
	return s
}
func id(v any) uuid.UUID { return uuid.MustParse(v.(string)) }
func (s *fixture) call(op string, in any, status int) map[string]any {
	s.t.Helper()
	return s.Call(s.t, s.Owner, op, in, status).Body
}
func (s *fixture) input(kind string) map[string]any {
	in := map[string]any{"customer_id": s.customer, "kind": kind, "face_value": "100", "expires_on": "2999-01-01"}
	if kind == "sold" {
		in["price"] = "80"
		in["sales_staff_ids"] = []uuid.UUID{s.Res.OwnerID}
	}
	return in
}
func (s *fixture) packet(kind string) map[string]any {
	s.t.Helper()
	return s.call("issue_red_packet", s.input(kind), 200)
}
func (s *fixture) snapshot() string {
	return dbtest.Snapshot(s.t, s.Pool, s.Res.TenantID, "red_packets", "red_packet_sales", "audit_log")
}
func (s *fixture) refuse(c ops.Caller, op string, in any, status int, detail string) {
	s.t.Helper()
	before := s.snapshot()
	r := s.HTTP(s.t, &c, op, in)
	if r.Status != status || detail != "" && r.Body["detail"] != detail || s.snapshot() != before {
		s.t.Fatal(op, r)
	}
}
func revoke(p map[string]any) map[string]any {
	return map[string]any{"red_packet_id": p["id"], "reason": "发错了"}
}
func TestIssueRules(t *testing.T) {
	s := open(t)
	c := s.MustCaller(s.Owner)
	staff, _ := s.Hire("归属乙", "002", "second")
	in := s.input("sold")
	in["sales_staff_ids"] = []uuid.UUID{s.Res.OwnerID, staff}
	p := s.call("issue_red_packet", in, 200)
	if p["kind"] != "sold" || p["price"] != "80.00" || p["face_value"] != "100.00" || p["issued_by"] != s.Res.OwnerID.String() || len(p["sales_staff"].([]any)) != 2 {
		t.Fatal(p)
	}
	g := s.packet("gift")
	if g["price"] != nil || len(g["sales_staff"].([]any)) != 0 || g["status"] != "available" {
		t.Fatal(g)
	}
	for _, tc := range []struct {
		kind, key string
		value     any
		status    int
		detail    string
	}{
		{"gift", "face_value", "0", 409, "面额须大于零"},
		{"sold", "price", nil, 409, "卖红包须填写大于零的售价"},
		{"sold", "price", "0", 409, "卖红包须填写大于零的售价"},
		{"sold", "sales_staff_ids", []uuid.UUID{}, 409, "请选择一到十名不同的业绩归属人"},
		{"sold", "sales_staff_ids", []uuid.UUID{staff, staff}, 422, ""},
		{"sold", "sales_staff_ids", make([]uuid.UUID, 11), 422, ""},
		{"gift", "price", "0", 409, "送红包不能填写售价或业绩归属人"},
		{"gift", "sales_staff_ids", []uuid.UUID{staff}, 409, "送红包不能填写售价或业绩归属人"},
		{"gift", "expires_on", "2020-01-01", 409, "有效期不能早于今天"},
	} {
		t.Run(tc.kind+tc.key+tc.detail, func(t *testing.T) {
			in := s.input(tc.kind)
			in[tc.key] = tc.value
			s.refuse(c, "issue_red_packet", in, tc.status, tc.detail)
		})
	}
	s.call("deactivate_staff", map[string]any{"staff_id": staff}, 200)
	in = s.input("sold")
	in["sales_staff_ids"] = []uuid.UUID{staff}
	s.refuse(c, "issue_red_packet", in, 409, "业绩归属人必须是本店在职员工")
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	otherStore, err := dbgen.New(tx).CreateStore(context.Background(), dbgen.CreateStoreParams{TenantID: s.Res.TenantID, Name: "员工别店"})
	if err != nil {
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)
	foreign := s.MustCaller(s.Owner)
	foreign.StoreID = otherStore.ID
	otherStaff := s.HTTP(t, &foreign, "create_staff", map[string]any{"name": "别店员工", "number": "other", "role_ids": []uuid.UUID{}})
	if otherStaff.Status != 200 {
		t.Fatal(otherStaff)
	}
	in["sales_staff_ids"] = []uuid.UUID{id(otherStaff.Body["id"])}
	s.refuse(c, "issue_red_packet", in, 409, "业绩归属人必须是本店在职员工")
	hidden := s.MustCaller(s.Owner)
	hidden.Permissions[perm.CustomerView] = ops.Grant{Scope: ops.ScopeSelf}
	s.refuse(hidden, "issue_red_packet", s.input("gift"), 404, "")
	unidentified := s.call("create_customer", map[string]any{}, 200)["customer"].(map[string]any)
	in = s.input("gift")
	in["customer_id"] = unidentified["id"]
	s.call("issue_red_packet", in, 200)
	s.call("merge_customers", map[string]any{"keep_id": s.customer, "merged_id": unidentified["id"]}, 200)
	s.refuse(c, "issue_red_packet", in, 409, "该顾客已合并到另一位顾客")
	for _, ch := range s.Channels() {
		r := ch.Call(t, c, "list_customer_red_packets", map[string]any{"customer_id": unidentified["id"]})
		if r.Status != 404 {
			t.Fatal(r)
		}
	}
}
func TestIssueStaffValidation(t *testing.T) {
	s := open(t)
	caller := s.MustCaller(s.Owner)
	for _, ids := range [][]uuid.UUID{nil, make([]uuid.UUID, 11), {s.Res.OwnerID, s.Res.OwnerID}} {
		err := db.InTenant(context.Background(), s.Pool, s.Res.TenantID, func(q *dbgen.Queries) error {
			var amount money.Amount
			if err := amount.Scan("1"); err != nil {
				return err
			}
			_, err := redpackets.IssueRedPacketAtForTest(context.Background(), ops.Env{Caller: caller, Q: q}, &redpackets.IssueRedPacketInput{CustomerID: s.customer, Kind: "sold", FaceValue: amount, Price: &amount, ExpiresOn: "2999-01-01", SalesStaffIDs: ids}, time.Now())
			return err
		})
		if ops.StatusOf(err) != 409 || err.Error() != "请选择一到十名不同的业绩归属人" {
			t.Fatal(err)
		}
	}
}
func TestIssueBusinessDate(t *testing.T) {
	s := open(t)
	day := settings.Date(time.Now()).AddDate(0, 0, 2)
	s.call("change_setting", map[string]any{"key": "business_day_end", "value": "02:00", "effective_from": day.Format(time.DateOnly)}, 200)
	at := time.Date(day.Year(), day.Month(), day.Day()+1, 1, 30, 0, 0, settings.Location)
	var a money.Amount
	if err := a.Scan("10"); err != nil {
		t.Fatal(err)
	}
	for _, offset := range []int{0, -1} {
		before := s.snapshot()
		err := db.InTenant(context.Background(), s.Pool, s.Res.TenantID, func(q *dbgen.Queries) error {
			p, err := redpackets.IssueRedPacketAtForTest(context.Background(), ops.Env{Caller: s.MustCaller(s.Owner), Q: q}, &redpackets.IssueRedPacketInput{CustomerID: s.customer, Kind: "gift", FaceValue: a, ExpiresOn: day.AddDate(0, 0, offset).Format(time.DateOnly)}, at)
			if err == nil && (p.BusinessDate != day.Format(time.DateOnly) || p.Status != "available") {
				t.Fatal(p)
			}
			return err
		})
		if offset == 0 && err != nil || offset == -1 && (ops.StatusOf(err) != 409 || err.Error() != "有效期不能早于今天" || s.snapshot() != before) {
			t.Fatal(err)
		}
	}
	// 固定营业日验证过期：自然日已是次日，但到期当日仍可用。
	if err := db.InTenant(context.Background(), s.Pool, s.Res.TenantID, func(q *dbgen.Queries) error {
		env := ops.Env{Caller: s.MustCaller(s.Owner), Q: q}
		rows, err := q.ListCustomerRedPackets(context.Background(), dbgen.ListCustomerRedPacketsParams{StoreID: s.Res.StoreID, CustomerID: s.customer})
		if err != nil {
			return err
		}
		for _, d := range []time.Time{day, day.AddDate(0, 0, 1)} {
			v, err := redpackets.ViewForTest(context.Background(), env, rows[0], d)
			if err != nil {
				return err
			}
			want := "available"
			if d.After(day) {
				want = "expired"
			}
			if v.Status != want {
				t.Fatal(v)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
func (s *fixture) expired() map[string]any {
	s.t.Helper()
	tx := dbtest.AppTx(s.t, s.Pool, s.Res.TenantID)
	var packet uuid.UUID
	if err := tx.QueryRow(context.Background(), `INSERT INTO red_packets(tenant_id,store_id,customer_id,kind,face_value,expires_on,business_date,issued_by) VALUES($1,$2,$3,'gift',10,'2020-01-01','2020-01-01',$4) RETURNING id`, s.Res.TenantID, s.Res.StoreID, s.customer, s.Res.OwnerID).Scan(&packet); err != nil {
		s.t.Fatal(err)
	}
	dbtest.Commit(s.t, tx)
	return map[string]any{"id": packet.String()}
}
func TestRevokeRules(t *testing.T) {
	s := open(t)
	c := s.MustCaller(s.Owner)
	p := s.packet("sold")
	hidden := s.MustCaller(s.Owner)
	hidden.Permissions[perm.CustomerView] = ops.Grant{Scope: ops.ScopeSelf}
	s.refuse(hidden, "revoke_red_packet", revoke(p), 404, "")
	in := revoke(p)
	in["reason"] = "   "
	s.refuse(c, "revoke_red_packet", in, 409, "请填写不超过200字的撤回原因")
	in["reason"] = strings.Repeat("字", 201)
	s.refuse(c, "revoke_red_packet", in, 422, "")
	out := s.call("revoke_red_packet", revoke(p), 200)
	if out["status"] != "revoked" || out["revoke_reason"] != "发错了" || out["revoked_by"] != s.Res.OwnerID.String() || out["revoked_at"] == nil {
		t.Fatal(out)
	}
	s.refuse(c, "revoke_red_packet", revoke(p), 409, "红包已撤回")
	out = s.call("revoke_red_packet", revoke(s.expired()), 200)
	if out["status"] != "revoked" {
		t.Fatal(out)
	}
	other := c
	other.StoreID = uuid.New()
	s.refuse(other, "revoke_red_packet", revoke(p), 404, "")
}
func TestRedPacketList(t *testing.T) {
	s := open(t)
	a := s.packet("sold")
	b := s.packet("gift")
	s.call("revoke_red_packet", revoke(b), 200)
	expired := s.expired()
	// 同一顾客在另一门店的记录也不能出现在列表。
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	st, err := dbgen.New(tx).CreateStore(context.Background(), dbgen.CreateStoreParams{TenantID: s.Res.TenantID, Name: "别店"})
	if err != nil {
		t.Fatal(err)
	}
	dbtest.Exec(t, tx, `INSERT INTO red_packets(tenant_id,store_id,customer_id,kind,face_value,expires_on,business_date,issued_by) VALUES($1,$2,$3,'gift',99,'2999-01-01','2020-01-01',$4)`, s.Res.TenantID, st.ID, s.customer, s.Res.OwnerID)
	dbtest.Commit(t, tx)
	for _, ch := range s.Channels() {
		c := s.MustCaller(s.Owner)
		delete(c.Permissions, perm.CustomerFullPhone)
		r := ch.Call(t, c, "list_customer_red_packets", map[string]any{"customer_id": s.customer})
		if r.Status != 200 {
			t.Fatal(r)
		}
		items := r.Body["items"].([]any)
		if len(items) != 3 {
			t.Fatal(r)
		}
		for i, want := range []struct {
			id     any
			status string
		}{{expired["id"], "expired"}, {b["id"], "revoked"}, {a["id"], "available"}} {
			p := items[i].(map[string]any)
			if p["id"] != want.id || p["status"] != want.status {
				t.Fatal(p)
			}
			if i == 2 && len(p["sales_staff"].([]any)) != 1 {
				t.Fatal(p)
			}
			for _, key := range []string{"phone", "customer", "name"} {
				if _, ok := p[key]; ok {
					t.Fatal(key, p)
				}
			}
		}
		c.Permissions[perm.CustomerView] = ops.Grant{Scope: ops.ScopeSelf}
		if r := ch.Call(t, c, "list_customer_red_packets", map[string]any{"customer_id": s.customer}); r.Status != 404 {
			t.Fatal(r)
		}
		c = s.MustCaller(s.Owner)
		c.StoreID = st.ID
		if r := ch.Call(t, c, "list_customer_red_packets", map[string]any{"customer_id": s.customer}); r.Status != 404 {
			t.Fatal(r)
		}
	}
}
func TestRedPacketPermissionsAndAI(t *testing.T) {
	s := open(t)
	p := s.packet("gift")
	for _, tc := range []struct {
		op string
		in any
		p  ops.Permission
	}{{"issue_red_packet", s.input("gift"), perm.CardSell}, {"revoke_red_packet", revoke(p), perm.CardAdjust}, {"list_customer_red_packets", map[string]any{"customer_id": s.customer}, perm.CustomerView}} {
		c := s.MustCaller(s.Owner)
		delete(c.Permissions, tc.p)
		for _, ch := range s.Channels() {
			before := s.snapshot()
			r := ch.Call(t, c, tc.op, tc.in)
			if r.Status != 403 || r.Detail() != "permission denied" || s.snapshot() != before {
				t.Fatal(r)
			}
		}
		if tc.op != "list_customer_red_packets" {
			before := s.snapshot()
			r := s.AI(t, s.MustCaller(s.Owner), tc.op, tc.in)
			if r.Status != 409 || !errors.Is(r.Err, ops.ErrDraftRequired) || s.snapshot() != before {
				t.Fatal(r)
			}
		}
	}
}
func TestRedPacketAudit(t *testing.T) {
	s := open(t)
	p := s.packet("sold")
	s.call("revoke_red_packet", revoke(p), 200)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var count int
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM audit_log WHERE target_kind=$1 AND target_id=$2 AND customer_id=$3", "red_packet", p["id"], s.customer).Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}

	for _, ch := range s.Channels() {
		c := s.MustCaller(s.Owner)
		delete(c.Permissions, perm.CustomerFullPhone)
		r := ch.Call(t, c, "list_audit_log", map[string]any{"target_id": p["id"]})
		if r.Status != 200 {
			t.Fatal(r)
		}
		customerLog := ch.Call(t, c, "list_audit_log", map[string]any{"target_id": s.customer})
		raw, err := json.Marshal(customerLog.Body)
		if err != nil || strings.Contains(string(raw), "13800131234") || !strings.Contains(string(raw), "138****1234") {
			t.Fatal(string(raw), err)
		}
		seen := map[string]bool{}
		for _, raw := range r.Body["entries"].([]any) {
			entry := raw.(map[string]any)
			action := entry["action"].(string)
			if action != "issue_red_packet" && action != "revoke_red_packet" {
				continue
			}
			seen[action] = true
			if entry["target_kind"] != "red_packet" || entry["target_id"] != p["id"] {
				t.Fatal(entry)
			}
			after := entry["after"].(map[string]any)
			if after["id"] != p["id"] {
				t.Fatal(entry)
			}
			if action == "revoke_red_packet" && (after["status"] != "revoked" || entry["before"].(map[string]any)["status"] != "available") {
				t.Fatal(entry)
			}
			for _, v := range []any{entry["before"], entry["after"]} {
				if m, ok := v.(map[string]any); ok && m["phone"] != nil {
					t.Fatal(entry)
				}
			}
		}
		if !seen["issue_red_packet"] || !seen["revoke_red_packet"] {
			t.Fatal(r)
		}
	}
}
func TestRevokeLock(t *testing.T) {
	s := open(t)
	p := s.packet("gift")
	c := s.MustCaller(s.Owner)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "SELECT id FROM red_packets WHERE id=$1 FOR NO KEY UPDATE", p["id"])
	dbtest.Exec(t, tx, "UPDATE red_packets SET revoked_by=$1,revoked_at=clock_timestamp(),revoke_reason='其他撤回' WHERE id=$2", s.Res.OwnerID, p["id"])
	done := make(chan optest.Result, 1)
	go func() { done <- s.HTTP(t, &c, "revoke_red_packet", revoke(p)) }()
	optest.AssertWaiting(t, done)
	dbtest.Commit(t, tx)
	r := optest.WaitResult(t, done, 409)
	if r.Body["detail"] != "红包已撤回" {
		t.Fatal(r)
	}
}
func TestMergeCustomerRedPackets(t *testing.T) {
	s := open(t)
	p := s.packet("gift")
	keep := s.call("create_customer", map[string]any{}, 200)["customer"].(map[string]any)
	s.call("merge_customers", map[string]any{"keep_id": keep["id"], "merged_id": s.customer}, 200)
	for _, ch := range s.Channels() {
		if r := ch.Call(t, s.MustCaller(s.Owner), "list_customer_red_packets", map[string]any{"customer_id": s.customer}); r.Status != 404 {
			t.Fatal(ch.Name, r)
		}
		r := ch.Call(t, s.MustCaller(s.Owner), "list_customer_red_packets", map[string]any{"customer_id": keep["id"]})
		if r.Status != 200 || len(r.Body["items"].([]any)) != 1 || r.Body["items"].([]any)[0].(map[string]any)["id"] != p["id"] {
			t.Fatal(r)
		}
	}
}

func TestIssueReferenceLocks(t *testing.T) {
	for _, table := range []string{"customers", "staff"} {
		t.Run(table, func(t *testing.T) {
			s := open(t)
			staff, _ := s.Hire("业绩锁", "003", "sales-lock")
			in := s.input("sold")
			in["sales_staff_ids"] = []uuid.UUID{staff}
			c := s.MustCaller(s.Owner)
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			if table == "customers" {
				keep := s.call("create_customer", map[string]any{}, 200)["customer"].(map[string]any)
				dbtest.Exec(t, tx, "SELECT id FROM customers WHERE id=$1 FOR NO KEY UPDATE", s.customer)
				dbtest.Exec(t, tx, "UPDATE customers SET merged_into=$2 WHERE id=$1", s.customer, keep["id"])
			} else {
				dbtest.Exec(t, tx, "SELECT id FROM staff WHERE id=$1 FOR NO KEY UPDATE", staff)
				dbtest.Exec(t, tx, "UPDATE staff SET active=false,deactivated_at=clock_timestamp() WHERE id=$1", staff)
			}
			expected := dbtest.SnapshotTx(t, tx, "red_packets", "red_packet_sales", "audit_log")
			done := make(chan optest.Result, 1)
			go func() { done <- s.HTTP(t, &c, "issue_red_packet", in) }()
			optest.AssertWaiting(t, done)
			dbtest.Commit(t, tx)
			r := optest.WaitResult(t, done, 409)
			want := "该顾客已合并到另一位顾客"
			if table == "staff" {
				want = "业绩归属人必须是本店在职员工"
			}
			if r.Body["detail"] != want || s.snapshot() != expected {
				t.Fatal(r)
			}
		})
	}
}

func TestRedPacketListBusinessDate(t *testing.T) {
	s := open(t)
	// 模拟昨天已经生效的营业日划分；API 不允许当天临时改变划分。
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, `INSERT INTO setting_changes(tenant_id,store_id,key,value,is_default,old_value,old_is_default,effective_from,changed_by) VALUES($1,$2,'business_day_end','"23:59"',false,'"00:00"',true,'2000-01-01',$3)`, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID)
	day, err := settings.BusinessDate(context.Background(), dbgen.New(tx), s.Res.StoreID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var available, expired uuid.UUID
	for i, expiry := range []time.Time{day, day.AddDate(0, 0, -1)} {
		var packet uuid.UUID
		if err := tx.QueryRow(context.Background(), `INSERT INTO red_packets(tenant_id,store_id,customer_id,kind,face_value,expires_on,business_date,issued_by) VALUES($1,$2,$3,'gift',1,$4,$4,$5) RETURNING id`, s.Res.TenantID, s.Res.StoreID, s.customer, expiry, s.Res.OwnerID).Scan(&packet); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			available = packet
		} else {
			expired = packet
		}
	}
	dbtest.Commit(t, tx)
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "list_customer_red_packets", map[string]any{"customer_id": s.customer})
		if r.Status != 200 {
			t.Fatal(r)
		}
		items := r.Body["items"].([]any)
		if len(items) != 2 || items[0].(map[string]any)["id"] != expired.String() || items[0].(map[string]any)["status"] != "expired" || items[1].(map[string]any)["id"] != available.String() || items[1].(map[string]any)["status"] != "available" {
			t.Fatal(r)
		}
	}
}
