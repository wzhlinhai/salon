package customers_test

import (
	"context"
	"testing"
	"time"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
)

// waitResult 检查并发 HTTP 请求在提交后成功返回。
func waitResult(t *testing.T, done <-chan optest.Result, status int) optest.Result {
	t.Helper()
	select {
	case r := <-done:
		if r.Status != status {
			t.Fatalf("concurrent request: %d %v %v, want %d", r.Status, r.Body, r.Err, status)
		}
		return r
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent HTTP request timed out")
		return optest.Result{}
	}
}

// assertWaiting 检查 HTTP 请求在锁释放前不能返回。
func assertWaiting(t *testing.T, done <-chan optest.Result) {
	t.Helper()
	select {
	case r := <-done:
		t.Fatalf("request returned while transaction holds lock: %+v", r)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestCustomerStaffLocksAllowAuditForeignKey(t *testing.T) {
	for _, action := range []string{"assign_customer_responsible", "hand_over_customers"} {
		t.Run(action, func(t *testing.T) {
			s := open(t)
			from, _ := s.Hire("原负责人", "002", "lock-from")
			c := s.create(map[string]any{"name": "原名"})
			s.call("assign_customer_responsible", map[string]any{"customer_ids": []any{c["id"]}, "staff_id": from}, 200)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			q := dbgen.New(tx)
			if _, err := q.LockCustomer(ctx, aID(c)); err != nil {
				t.Fatal(err)
			}
			done := make(chan optest.Result, 1)
			args := map[string]any{"customer_ids": []any{c["id"]}, "staff_id": s.Res.OwnerID}
			if action == "hand_over_customers" {
				args = map[string]any{"from_staff_id": from, "to_staff_id": s.Res.OwnerID}
			}
			go func() { done <- s.HTTPWithToken(t, s.Owner, action, args) }()
			dbtest.WaitForLockWait(t, s.Pool)
			if _, err := tx.Exec(ctx, "UPDATE customers SET name='事务改名' WHERE id=$1", aID(c)); err != nil {
				t.Fatal(err)
			}
			if err := audit.Record(ctx, ops.Env{Caller: s.MustCaller(s.Owner), Q: q}, "update_customer_profile", aID(c), c, map[string]any{"name": "事务改名"}); err != nil {
				t.Fatalf("audit must coexist with staff share lock: %v", err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			waitResult(t, done, 200)
			after := s.get(c["id"])
			assertEqual(t, after["name"], "事务改名")
			assertEqual(t, after["responsible"].(map[string]any)["id"], s.Res.OwnerID.String())
		})
	}
}

func TestCustomerStaffShareLockWaitsForDeactivation(t *testing.T) {
	for _, action := range []string{"assign_customer_responsible", "hand_over_customers"} {
		t.Run(action, func(t *testing.T) {
			s := open(t)
			id, _ := s.Hire("待离职", "002", "deactivating")
			c := s.create(map[string]any{})
			s.call("assign_customer_responsible", map[string]any{"customer_ids": []any{c["id"]}, "staff_id": s.Res.OwnerID}, 200)
			ctx := context.Background()
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			q := dbgen.New(tx)
			if _, err := q.LockStaff(ctx, id); err != nil {
				t.Fatal(err)
			}
			args := map[string]any{"customer_ids": []any{c["id"]}, "staff_id": id}
			if action == "hand_over_customers" {
				args = map[string]any{"from_staff_id": s.Res.OwnerID, "to_staff_id": id}
			}
			done := make(chan optest.Result, 1)
			go func() { done <- s.HTTPWithToken(t, s.Owner, action, args) }()
			dbtest.WaitForLockWait(t, s.Pool)
			assertWaiting(t, done)
			if _, err := q.DeactivateStaff(ctx, id); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			r := waitResult(t, done, 409)
			assertEqual(t, r.Body["detail"], "负责人必须是在职员工")
			assertEqual(t, s.get(c["id"])["responsible"].(map[string]any)["id"], s.Res.OwnerID.String())
		})
	}
}

func TestConcurrentCustomerCreation(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	q := dbgen.New(tx)
	phone := "13800131234"
	if err := q.LockCustomerPhone(ctx, phone); err != nil {
		t.Fatal(err)
	}
	row, err := q.CreateCustomer(ctx, dbgen.CreateCustomerParams{TenantID: s.Res.TenantID, StoreID: s.Res.StoreID, CreatedBy: s.Res.OwnerID, Phone: &phone})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan optest.Result, 1)
	go func() { done <- s.HTTPWithToken(t, s.Owner, "create_customer", map[string]any{"phone": phone}) }()
	dbtest.WaitForLockWait(t, s.Pool)
	assertWaiting(t, done)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	result := waitResult(t, done, 200)
	assertEqual(t, result.Body["existed"], true)
	assertEqual(t, result.Body["customer"].(map[string]any)["id"], row.ID.String())
	var count int
	if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM customers WHERE phone=$1", phone).Scan(&count); err != nil {
		t.Fatal(err)
	}
	assertEqual(t, count, 1)
}

func TestMergeAndPhoneChangeKeepAuditBefore(t *testing.T) {
	s := open(t)
	keep := s.create(map[string]any{})
	merged := s.create(map[string]any{"phone": "13900131234", "name": "合并后姓名"})
	ctx := context.Background()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	q := dbgen.New(tx)
	row, err := q.LockCustomer(ctx, aID(keep))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "change_customer_phone", map[string]any{"id": keep["id"], "phone": "13700131234"})
	}()
	dbtest.WaitForLockWait(t, s.Pool)
	assertWaiting(t, done)
	if _, err := tx.Exec(ctx, "UPDATE customers SET phone=NULL, merged_into=$2 WHERE id=$1", aID(merged), aID(keep)); err != nil {
		t.Fatal(err)
	}
	row.Phone = new("13900131234")
	row.Name = new("合并后姓名")
	if err := q.SaveCustomer(ctx, dbgen.SaveCustomerParams{ID: row.ID, Name: row.Name, Phone: row.Phone}); err != nil {
		t.Fatal(err)
	}
	after := map[string]any{"name": "合并后姓名", "phone": "13900131234"}
	if err := audit.Record(ctx, ops.Env{Caller: s.MustCaller(s.Owner), Q: q}, "merge_customers", row.ID, keep, after); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	waitResult(t, done, 200)
	result := s.get(keep["id"])
	assertEqual(t, result["phone"], "13700131234")
	assertEqual(t, result["name"], "合并后姓名")
	assertEqual(t, s.get(merged["id"])["merged_into"], keep["id"])
	logs := s.call("list_audit_log", map[string]any{"target_id": keep["id"]}, 200)["entries"].([]any)
	latest := logs[0].(map[string]any)
	assertEqual(t, latest["action"], "change_customer_phone")
	assertEqual(t, latest["before"].(map[string]any)["phone"], "13900131234")
	assertEqual(t, latest["before"].(map[string]any)["name"], "合并后姓名")
	var duplicate bool
	if err := s.Pool.QueryRow(ctx, "SELECT EXISTS (SELECT phone FROM customers WHERE merged_into IS NULL AND phone IS NOT NULL GROUP BY phone HAVING count(*)>1)").Scan(&duplicate); err != nil {
		t.Fatal(err)
	}
	assertEqual(t, duplicate, false)
}

func TestCustomerOperationsAllowKeyShare(t *testing.T) {
	for _, action := range []string{
		"update_customer_profile", "change_customer_phone", "add_safety_label",
		"merge_customers", "assign_customer_responsible", "hand_over_customers",
	} {
		t.Run(action, func(t *testing.T) {
			s := open(t)
			c := s.create(map[string]any{"phone": "13800131234"})
			ids := []any{c["id"]}
			args := map[string]any{"id": c["id"], "name": "新名"}
			switch action {
			case "change_customer_phone":
				args = map[string]any{"id": c["id"], "phone": "13900131234"}
			case "add_safety_label":
				args = map[string]any{"customer_id": c["id"], "kind": "allergy", "source": "customer_said", "observed_on": "2020-01-01"}
			case "merge_customers":
				merged := s.create(map[string]any{})
				ids = append(ids, merged["id"])
				args = map[string]any{"keep_id": c["id"], "merged_id": merged["id"]}
			case "assign_customer_responsible":
				args = map[string]any{"customer_ids": ids, "staff_id": s.Res.OwnerID}
			case "hand_over_customers":
				from, _ := s.Hire("原负责人", "002", "key-share-from")
				s.call("assign_customer_responsible", map[string]any{"customer_ids": ids, "staff_id": from}, 200)
				args = map[string]any{"from_staff_id": from, "to_staff_id": s.Res.OwnerID}
			}
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			for _, id := range ids {
				if _, err := tx.Exec(context.Background(), "SELECT id FROM customers WHERE id=$1 FOR KEY SHARE", id); err != nil {
					t.Fatal(err)
				}
			}
			done := make(chan optest.Result, 1)
			go func() { done <- s.HTTPWithToken(t, s.Owner, action, args) }()
			waitResult(t, done, 200)
		})
	}
}

func TestCustomerSourceChecksAllowShare(t *testing.T) {
	for _, action := range []string{"create_customer", "update_customer_profile"} {
		t.Run(action, func(t *testing.T) {
			s := open(t)
			source := s.call("create_customer_source", map[string]any{"name": "共享来源"}, 200)
			args := map[string]any{"source_id": source["id"]}
			if action == "update_customer_profile" {
				args["id"] = s.create(map[string]any{})["id"]
			}
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			if _, err := dbgen.New(tx).ShareCustomerSource(context.Background(), aID(source)); err != nil {
				t.Fatal(err)
			}
			done := make(chan optest.Result, 1)
			go func() { done <- s.HTTPWithToken(t, s.Owner, action, args) }()
			waitResult(t, done, 200)
		})
	}
}
