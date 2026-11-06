package dataio_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
)

func TestImportKindIsNotSalePerformance(t *testing.T) {
	f := open(t)
	before := dbtest.Snapshot(t, f.Pool, f.Res.TenantID, "performance_entries", "guest_visits")
	batch, _ := f.previewDefault()
	f.commit(batch)
	if after := dbtest.Snapshot(t, f.Pool, f.Res.TenantID, "performance_entries", "guest_visits"); after != before {
		t.Fatal("import changed performance or guest visits")
	}
	today := time.Now().Format(time.DateOnly)
	perf := f.call("list_performance", map[string]any{"date_from": today, "date_to": today, "kind": "sale"})
	for _, raw := range perf["totals"].([]any) {
		total := raw.(map[string]any)
		if total["kind"] == "sale" && (total["amount"] != "0" || total["paid_amount"] != "0") {
			t.Fatalf("sale total after import = %v", total)
		}
	}
	commission := f.call("list_commissions", map[string]any{"date_from": today, "date_to": today, "source": "sale"})
	if commission["total"] != "0" && commission["total"] != "0.00" {
		t.Fatalf("sale commission after import = %v", commission["total"])
	}
}

func TestCommitIsAtomic(t *testing.T) {
	f := open(t)
	batch, _ := f.previewDefault()
	ctx := context.Background()
	if _, err := f.Pool.Exec(ctx, `CREATE OR REPLACE FUNCTION dataio_fail_import() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.kind = 'import' THEN
        RAISE EXCEPTION 'forced import failure';
    END IF;
    RETURN NEW;
END $$;`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Pool.Exec(ctx, `CREATE TRIGGER dataio_fail_import BEFORE INSERT ON card_ledger FOR EACH ROW EXECUTE FUNCTION dataio_fail_import()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = f.Pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS dataio_fail_import ON card_ledger`)
		_, _ = f.Pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS dataio_fail_import()`)
	})
	before := dbtest.Snapshot(t, f.Pool, f.Res.TenantID, "customers", "cards", "card_ledger", "card_items", "card_count_ledger", "card_points_ledger")
	r := f.Call(t, f.Owner, "commit_import", map[string]any{"batch_id": batch}, 0)
	if r.Status != http.StatusInternalServerError {
		t.Fatalf("commit = %d %v", r.Status, r.Body)
	}
	if after := dbtest.Snapshot(t, f.Pool, f.Res.TenantID, "customers", "cards", "card_ledger", "card_items", "card_count_ledger", "card_points_ledger"); after != before {
		t.Fatal("failed commit left partial writes")
	}
	if n := f.count("SELECT count(*) FROM import_batches WHERE status='previewed'"); n != 1 {
		t.Fatal(n)
	}
}

func TestImportMigrationDown(t *testing.T) {
	f := open(t)
	batch, _ := f.previewDefault()
	f.commit(batch)
	var cardID string
	if err := f.Pool.QueryRow(context.Background(), "SELECT id::text FROM cards WHERE card_type_id=$1", f.storedType).Scan(&cardID); err != nil {
		t.Fatal(err)
	}
	zero := workbook(t,
		[][]string{{"来源顾客ID", "姓名", "手机号(可空)", "性别(男/女/空)"}, {"C0", "用完的次卡", "13700131111", ""}},
		[][]string{{"来源卡号", "来源顾客ID", "卡种", "本金", "赠送金", "到期日(YYYY-MM-DD,可空)", "积分(整数,可空)"}},
		[][]string{{"来源卡号", "来源顾客ID", "卡种", "项目", "剩余购买次数", "剩余赠送次数", "到期日(可空)"}, {"K0", "C0", "剪发十次", "剪发", "0", "0", ""}},
		[][]string{{"顾客数", "储值卡数", "本金合计", "赠送金合计", "次卡套餐卡数"}, {"1", "0", "0", "0", "1"}})
	r := f.upload(f.Owner, zero, "zero-count.xlsx")
	if r.Status != http.StatusOK {
		t.Fatal(r)
	}
	f.commit(r.Body["id"].(string))
	var zeroCard string
	if err := f.Pool.QueryRow(context.Background(), `SELECT ci.card_id::text FROM card_items ci JOIN cards c ON c.id=ci.card_id
		WHERE c.card_type_id=$1 AND ci.paid_count_opened=0`, f.countType).Scan(&zeroCard); err != nil {
		t.Fatal(err)
	}
	dbtest.MigrateDownTo(t, f.Pool, 42)
	var opened int
	if err := f.Pool.QueryRow(context.Background(), "SELECT paid_count_opened FROM card_items WHERE card_id=$1", zeroCard).Scan(&opened); err != nil || opened != 1 {
		t.Fatalf("zero count after down = %d, %v", opened, err)
	}
	for _, table := range []string{"import_batches", "import_batch_rows", "import_batch_records"} {
		var name *string
		if err := f.Pool.QueryRow(context.Background(), "SELECT to_regclass($1)::text", table).Scan(&name); err != nil || name != nil {
			t.Fatalf("%s still exists: %v %v", table, name, err)
		}
	}
	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	_, err := tx.Exec(context.Background(), `INSERT INTO card_ledger (tenant_id,store_id,card_id,kind,principal_before,principal_delta,principal_after,bonus_before,bonus_delta,bonus_after,business_date,actor_id) VALUES ($1,$2,$3,'import',0,0,0,0,0,0,'2020-01-01',$4)`, f.Res.TenantID, f.Res.StoreID, cardID, f.Res.OwnerID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != "card_ledger_kind_check" {
		t.Fatalf("import kind after down = %v", err)
	}
	dbtest.Rollback(t, tx)
	tx = dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	_, err = tx.Exec(context.Background(), `INSERT INTO audit_log(tenant_id,store_id,actor_id,action,target_kind,target_id) VALUES($1,$2,$3,'test','import_batch',$4)`, f.Res.TenantID, f.Res.StoreID, f.Res.OwnerID, cardID)
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != "audit_log_target_kind_check" {
		t.Fatalf("import_batch audit after down = %v", err)
	}
	dbtest.Rollback(t, tx)
}
