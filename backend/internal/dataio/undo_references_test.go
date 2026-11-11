package dataio_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

type importedIDs struct {
	batch    string
	customer string
	stored   string
	count    string
}

func imported(t *testing.T) (*fixture, importedIDs) {
	t.Helper()
	f := open(t)
	batch, _ := f.previewDefault()
	f.commit(batch)
	ids := importedIDs{batch: batch}
	ctx := context.Background()
	if err := f.Pool.QueryRow(ctx, "SELECT id::text FROM customers WHERE phone='13700139999'").Scan(&ids.customer); err != nil {
		t.Fatal(err)
	}
	if err := f.Pool.QueryRow(ctx, "SELECT id::text FROM cards WHERE card_type_id=$1", f.storedType).Scan(&ids.stored); err != nil {
		t.Fatal(err)
	}
	if err := f.Pool.QueryRow(ctx, "SELECT id::text FROM cards WHERE card_type_id=$1", f.countType).Scan(&ids.count); err != nil {
		t.Fatal(err)
	}
	return f, ids
}

func (f *fixture) appExec(t *testing.T, query string, args ...any) {
	t.Helper()
	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	if _, err := tx.Exec(context.Background(), query, args...); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func requireUndoBlocked(t *testing.T, f *fixture, batch, want string) {
	t.Helper()
	r := f.Call(t, f.Owner, "undo_import", map[string]any{"batch_id": batch}, 0)
	if r.Status != http.StatusConflict || !strings.Contains(r.Detail(), "不能撤销这次导入：") || !strings.Contains(r.Detail(), want) {
		t.Fatalf("undo = %d %q, want %q", r.Status, r.Detail(), want)
	}
}

func TestUndoReferenceBranches(t *testing.T) {
	t.Run("debt", func(t *testing.T) {
		f, ids := imported(t)
		f.appExec(t, `INSERT INTO debts (tenant_id,store_id,customer_id,amount,business_date,recorded_by) VALUES ($1,$2,$3,1,'2020-01-01',$4)`, f.Res.TenantID, f.Res.StoreID, ids.customer, f.Res.OwnerID)
		requireUndoBlocked(t, f, ids.batch, "已有欠款")
	})
	t.Run("red packet", func(t *testing.T) {
		f, ids := imported(t)
		f.appExec(t, `INSERT INTO red_packets (tenant_id,store_id,customer_id,kind,face_value,expires_on,business_date,issued_by) VALUES ($1,$2,$3,'gift',1,'2030-01-01','2020-01-01',$4)`, f.Res.TenantID, f.Res.StoreID, ids.customer, f.Res.OwnerID)
		requireUndoBlocked(t, f, ids.batch, "已有红包")
	})
	t.Run("safety label", func(t *testing.T) {
		f, ids := imported(t)
		f.appExec(t, `INSERT INTO customer_safety_labels (tenant_id,store_id,customer_id,kind,source,observed_on,recorded_by) VALUES ($1,$2,$3,'allergy','customer_said','2020-01-01',$4)`, f.Res.TenantID, f.Res.StoreID, ids.customer, f.Res.OwnerID)
		requireUndoBlocked(t, f, ids.batch, "已有安全标签")
	})
	t.Run("other card", func(t *testing.T) {
		f, ids := imported(t)
		f.call("open_card", map[string]any{"customer_id": ids.customer, "card_type_id": f.storedType, "principal": "5", "bonus": "0", "sales_staff_ids": []any{f.Res.OwnerID}})
		requireUndoBlocked(t, f, ids.batch, "还有其他卡")
	})
	t.Run("adjustment", func(t *testing.T) {
		f, ids := imported(t)
		f.appExec(t, `INSERT INTO card_adjustments (tenant_id,store_id,card_id,reason,expires_before,expires_after,expiry_changed,business_date,actor_id) VALUES ($1,$2,$3,'调整','2030-01-01','2031-01-01',true,'2020-01-01',$4)`, f.Res.TenantID, f.Res.StoreID, ids.stored, f.Res.OwnerID)
		requireUndoBlocked(t, f, ids.batch, "有调整单")
	})
	t.Run("upgrade", func(t *testing.T) {
		f, ids := imported(t)
		f.appExec(t, `INSERT INTO card_upgrades (tenant_id,store_id,card_id,type_before,type_after,trigger,business_date,actor_id) VALUES ($1,$2,$3,$4,$5,'manual','2020-01-01',$6)`, f.Res.TenantID, f.Res.StoreID, ids.stored, f.storedType, f.countType, f.Res.OwnerID)
		requireUndoBlocked(t, f, ids.batch, "有升级记录")
	})
	t.Run("refund", func(t *testing.T) {
		f, ids := imported(t)
		f.appExec(t, `INSERT INTO card_refunds (tenant_id,store_id,card_id,method,principal,bonus,counts_amount,bonus_policy,bonus_deducted,fee_percent,fee,amount,business_date,refunded_by) VALUES ($1,$2,$3,'cash',0,0,0,'forfeit',0,0,0,0,'2020-01-01',$4)`, f.Res.TenantID, f.Res.StoreID, ids.stored, f.Res.OwnerID)
		requireUndoBlocked(t, f, ids.batch, "有退款记录")
	})
	t.Run("transfer", func(t *testing.T) {
		f, ids := imported(t)
		var fromLedger, toLedger string
		ctx := context.Background()
		if err := f.Pool.QueryRow(ctx, "SELECT id::text FROM card_ledger WHERE card_id=$1 AND kind='import'", ids.stored).Scan(&fromLedger); err != nil {
			t.Fatal(err)
		}
		if err := f.Pool.QueryRow(ctx, "SELECT id::text FROM card_ledger WHERE card_id=$1 AND kind='import'", ids.count).Scan(&toLedger); err != nil {
			t.Fatal(err)
		}
		f.appExec(t, `INSERT INTO card_transfers (tenant_id,store_id,from_card_id,to_card_id,reason,from_ledger_id,to_ledger_id,business_date,actor_id) VALUES ($1,$2,$3,$4,'转余额',$5,$6,'2020-01-01',$7)`, f.Res.TenantID, f.Res.StoreID, ids.stored, ids.count, fromLedger, toLedger, f.Res.OwnerID)
		requireUndoBlocked(t, f, ids.batch, "有转余额记录")
	})
	t.Run("appointment", func(t *testing.T) {
		f, ids := imported(t)
		f.SetNow(time.Date(2026, 6, 12, 12, 0, 0, 0, settings.Location))
		start := time.Date(2026, 6, 12, 13, 0, 0, 0, settings.Location)
		f.call("create_appointment", map[string]any{"customer_id": ids.customer, "staff_id": f.Res.OwnerID, "starts_at": start})
		requireUndoBlocked(t, f, ids.batch, "已有预约")
		if n := f.count("SELECT count(*) FROM appointments"); n != 1 {
			t.Fatalf("appointments after rejected undo = %d, want 1", n)
		}
		if n := f.count("SELECT count(*) FROM customers WHERE id='" + ids.customer + "'"); n != 1 {
			t.Fatalf("customer after rejected undo = %d, want 1", n)
		}
	})
	t.Run("merged", func(t *testing.T) {
		f, ids := imported(t)
		f.call("merge_customers", map[string]any{"keep_id": f.nativeCustomer, "merged_id": ids.customer})
		requireUndoBlocked(t, f, ids.batch, "已合并")
	})
}

func TestUndoWaitsForConcurrentReferenceAndReturns409(t *testing.T) {
	f, ids := imported(t)
	ctx := context.Background()
	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	if _, err := tx.Exec(ctx, `INSERT INTO debts (tenant_id,store_id,customer_id,amount,business_date,recorded_by) VALUES ($1,$2,$3,1,'2020-01-01',$4)`, f.Res.TenantID, f.Res.StoreID, ids.customer, f.Res.OwnerID); err != nil {
		t.Fatal(err)
	}
	done := make(chan optest.Result, 1)
	go func() {
		done <- f.HTTPWithToken(t, f.Owner, "undo_import", map[string]any{"batch_id": ids.batch})
	}()
	dbtest.WaitForLockWait(t, f.Pool)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	r := optest.WaitResult(t, done, http.StatusConflict)
	if !strings.Contains(r.Detail(), "已有欠款") {
		t.Fatalf("concurrent undo = %q", r.Detail())
	}
}
