package dataio_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
)

func TestPreviewEmptySourceKeys(t *testing.T) {
	f := open(t)
	data := workbook(t,
		[][]string{{"来源顾客ID", "姓名", "手机号(可空)", "性别(男/女/空)"}, {"", "缺编号", "", ""}},
		[][]string{{"来源卡号", "来源顾客ID", "卡种", "本金", "赠送金", "到期日(YYYY-MM-DD,可空)", "积分(整数,可空)"}, {"", "C1", "储值卡A", "1", "0", "", ""}},
		[][]string{{"来源卡号", "来源顾客ID", "卡种", "项目", "剩余购买次数", "剩余赠送次数", "到期日(可空)"}},
		[][]string{{"顾客数", "储值卡数", "本金合计", "赠送金合计", "次卡套餐卡数"}, {"0", "0", "0", "0", "0"}},
	)
	r := f.upload(f.Owner, data, "empty.xlsx")
	if r.Status != http.StatusOK {
		t.Fatalf("preview = %d %v", r.Status, r.Body)
	}
	if got := rowError(t, rowsBySheet(t, r.Body, "顾客"), ""); got != "来源顾客ID不能为空" {
		t.Fatal(got)
	}
	if got := rowError(t, rowsBySheet(t, r.Body, "储值卡"), ""); got != "来源卡号不能为空" {
		t.Fatal(got)
	}
}

func TestImportLocksCustomerBeforePhone(t *testing.T) {
	f := open(t)
	batch, _ := f.previewDefault()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	q := dbgen.New(tx)
	if _, err := q.LockCustomer(ctx, uuid.MustParse(f.nativeCustomer)); err != nil {
		t.Fatal(err)
	}
	done := make(chan optest.Result, 1)
	go func() { done <- f.HTTPWithToken(t, f.Owner, "commit_import", map[string]any{"batch_id": batch}) }()
	optest.AssertWaiting(t, f.Pool, done, 1)
	if err := q.LockCustomerPhone(ctx, "13800131234"); err != nil {
		t.Fatalf("手机号锁被等待顾客的导入占用: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	optest.WaitResult(t, done, http.StatusOK)
}

func TestExportSnapshotIsStable(t *testing.T) {
	f := open(t)
	ctx := context.Background()
	err := db.InTenantSnapshot(ctx, f.Pool, f.Res.TenantID, func(q *dbgen.Queries) error {
		before, err := q.GetStore(ctx, f.Res.StoreID)
		if err != nil {
			return err
		}
		if err := db.InTenant(ctx, f.Pool, f.Res.TenantID, func(other *dbgen.Queries) error {
			_, err := other.RenameStore(ctx, dbgen.RenameStoreParams{ID: f.Res.StoreID, Name: "快照之后"})
			return err
		}); err != nil {
			return err
		}
		after, err := q.GetStore(ctx, f.Res.StoreID)
		if err != nil {
			return err
		}
		if before.Name != after.Name {
			t.Errorf("快照内数据变化: %q -> %q", before.Name, after.Name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPreviewRejectsCrossSheetCardKey(t *testing.T) {
	f := open(t)
	data := workbook(t,
		[][]string{{"来源顾客ID", "姓名", "手机号(可空)", "性别(男/女/空)"}, {"C1", "顾客", "", ""}},
		[][]string{{"来源卡号", "来源顾客ID", "卡种", "本金", "赠送金", "到期日(YYYY-MM-DD,可空)", "积分(整数,可空)"}, {"K1", "C1", "储值卡A", "10", "0", "", ""}},
		[][]string{{"来源卡号", "来源顾客ID", "卡种", "项目", "剩余购买次数", "剩余赠送次数", "到期日(可空)"}, {"K1", "C1", "剪发十次", "剪发", "1", "0", ""}},
		[][]string{{"顾客数", "储值卡数", "本金合计", "赠送金合计", "次卡套餐卡数"}, {"1", "1", "10", "0", "1"}},
	)
	r := f.upload(f.Owner, data, "duplicate.xlsx")
	if r.Status != http.StatusOK {
		t.Fatalf("preview = %d %v", r.Status, r.Body)
	}
	if got := rowError(t, rowsBySheet(t, r.Body, "次卡套餐"), "K1"); got != "来源卡号与储值卡重复" {
		t.Fatal(got)
	}
	result := f.commit(r.Body["id"].(string))
	if got := rowError(t, rowsBySheet(t, result, "次卡套餐"), "K1"); got != "来源卡号与储值卡重复" {
		t.Fatalf("commit row error = %q", got)
	}
	if result["written"].(map[string]any)["stored_value_cards"] != float64(1) || result["written"].(map[string]any)["count_package_cards"] != float64(0) {
		t.Fatal(result)
	}
}
