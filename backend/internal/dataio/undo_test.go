package dataio_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
)

func TestUndoRestoresSnapshotAndAllowsReimport(t *testing.T) {
	f := open(t)
	tables := []string{"customers", "cards", "card_items", "card_ledger", "card_count_ledger", "card_points_ledger", "performance_entries", "guest_visits"}
	before := dbtest.Snapshot(t, f.Pool, f.Res.TenantID, tables...)
	batch, _ := f.previewDefault()
	f.commit(batch)
	if n := f.count("SELECT count(*) FROM cards"); n != 2 {
		t.Fatal(n)
	}
	out := f.call("undo_import", map[string]any{"batch_id": batch})
	if out["status"] != "undone" {
		t.Fatal(out)
	}
	if after := dbtest.Snapshot(t, f.Pool, f.Res.TenantID, tables...); after != before {
		t.Fatalf("undo did not restore snapshot\nbefore=%s\nafter=%s", before, after)
	}
	if n := f.count("SELECT count(*) FROM audit_log WHERE target_kind='import_batch' AND action='undo_import'"); n != 1 {
		t.Fatalf("undo audit = %d", n)
	}
	// 手机号释放后，同一文件可以重新导入并提交。
	again, _ := f.previewDefault()
	f.commit(again)
	if n := f.count("SELECT count(*) FROM cards"); n != 2 {
		t.Fatal(n)
	}
}

func TestUndoRefusedAfterCustomerGotOrder(t *testing.T) {
	f := open(t)
	batch, _ := f.previewDefault()
	f.commit(batch)
	var customerID string
	if err := f.Pool.QueryRow(context.Background(), "SELECT id::text FROM customers WHERE phone='13700139999'").Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	f.call("open_order", map[string]any{"customer_id": customerID, "gender": "female", "designated": false, "items": []any{}})
	r := f.Call(f.t, f.Owner, "undo_import", map[string]any{"batch_id": batch}, 0)
	if r.Status != http.StatusConflict || !strings.Contains(r.Detail(), "不能撤销这次导入：") || !strings.Contains(r.Detail(), "已有单据") {
		t.Fatalf("undo = %d %q", r.Status, r.Detail())
	}
	if n := f.count("SELECT count(*) FROM cards"); n != 2 {
		t.Fatal(n)
	}
}

func TestUndoRefusedAfterCardLedgerEntry(t *testing.T) {
	f := open(t)
	batch, _ := f.previewDefault()
	f.commit(batch)
	var cardID string
	if err := f.Pool.QueryRow(context.Background(), "SELECT id::text FROM cards WHERE card_type_id=$1", f.storedType).Scan(&cardID); err != nil {
		t.Fatal(err)
	}
	f.call("recharge_card", map[string]any{"card_id": cardID, "principal": "1", "bonus": "0", "sales_staff_ids": []any{f.Res.OwnerID}})
	r := f.Call(f.t, f.Owner, "undo_import", map[string]any{"batch_id": batch}, 0)
	if r.Status != http.StatusConflict || !strings.Contains(r.Detail(), "不能撤销这次导入：") || !strings.Contains(r.Detail(), "有其他余额流水") {
		t.Fatalf("undo = %d %q", r.Status, r.Detail())
	}
}

func TestUndoRefusedAfterCustomerEdit(t *testing.T) {
	f := open(t)
	batch, _ := f.previewDefault()
	f.commit(batch)
	var customerID string
	if err := f.Pool.QueryRow(context.Background(), "SELECT id::text FROM customers WHERE phone='13700139999'").Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	f.call("update_customer_profile", map[string]any{"id": customerID, "name": "改过"})
	r := f.Call(f.t, f.Owner, "undo_import", map[string]any{"batch_id": batch}, 0)
	if r.Status != http.StatusConflict || !strings.Contains(r.Detail(), "已被修改") {
		t.Fatalf("undo = %d %q", r.Status, r.Detail())
	}
}

func TestUndoRejectsNonCommittedBatch(t *testing.T) {
	f := open(t)
	batch, _ := f.previewDefault()
	r := f.Call(f.t, f.Owner, "undo_import", map[string]any{"batch_id": batch}, 0)
	if r.Status != http.StatusConflict || r.Detail() != "只能撤销已提交的导入批次" {
		t.Fatalf("undo = %d %q", r.Status, r.Detail())
	}
}

func TestCommitRejectsCommittedBatch(t *testing.T) {
	f := open(t)
	batch, _ := f.previewDefault()
	f.commit(batch)
	r := f.Call(f.t, f.Owner, "commit_import", map[string]any{"batch_id": batch}, 0)
	if r.Status != http.StatusConflict || r.Detail() != "只能提交预览状态的导入批次" {
		t.Fatalf("commit = %d %q", r.Status, r.Detail())
	}
}
