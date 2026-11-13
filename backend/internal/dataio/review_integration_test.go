package dataio_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
)

func singleCustomerWorkbook(t *testing.T, phone string) []byte {
	t.Helper()
	return workbook(t,
		[][]string{{"来源顾客ID", "姓名", "手机号(可空)", "性别(男/女/空)"}, {"C1", "新顾客", phone, ""}},
		[][]string{{"来源卡号", "来源顾客ID", "卡种", "本金", "赠送金", "到期日(YYYY-MM-DD,可空)", "积分(整数,可空)"}},
		[][]string{{"来源卡号", "来源顾客ID", "卡种", "项目", "剩余购买次数", "剩余赠送次数", "到期日(可空)"}},
		[][]string{{"顾客数", "储值卡数", "本金合计", "赠送金合计", "次卡套餐卡数"}, {"1", "0", "0", "0", "0"}},
	)
}

func holdCustomerPhone(t *testing.T, f *fixture, phone string) interface {
	Commit(context.Context) error
	Rollback(context.Context) error
} {
	t.Helper()
	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	if _, err := tx.Exec(context.Background(), `SELECT pg_advisory_xact_lock(hashtextextended(current_setting('app.tenant_id') || $1::text, 0))`, phone); err != nil {
		t.Fatal(err)
	}
	return tx
}

func TestImportTakesPhoneLock(t *testing.T) {
	f := open(t)
	batch, _ := f.previewDefault()
	tx := holdCustomerPhone(t, f, "13800131234")
	done := make(chan optest.Result, 1)
	go func() { done <- f.HTTPWithToken(t, f.Owner, "commit_import", map[string]any{"batch_id": batch}) }()
	optest.AssertWaiting(t, f.Pool, done, 1)
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	optest.WaitResult(t, done, http.StatusOK)
}

func TestImportRejectsCustomerLinkedAfterPreview(t *testing.T) {
	f := open(t)
	const phone = "13700135555"
	r := f.upload(f.Owner, singleCustomerWorkbook(t, phone), "changed-phone.xlsx")
	if r.Status != http.StatusOK {
		t.Fatalf("preview = %d %v", r.Status, r.Body)
	}
	batch := r.Body["id"].(string)
	held := holdCustomerPhone(t, f, phone)
	done := make(chan optest.Result, 1)
	go func() { done <- f.HTTPWithToken(t, f.Owner, "commit_import", map[string]any{"batch_id": batch}) }()
	optest.AssertWaiting(t, f.Pool, done, 1)

	inserted := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	dbtest.Exec(t, inserted, `INSERT INTO customers(tenant_id,store_id,name,phone,created_by) VALUES($1,$2,'并发顾客',$3,$4)`, f.Res.TenantID, f.Res.StoreID, phone, f.Res.OwnerID)
	dbtest.Commit(t, inserted)
	if err := held.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}

	result := optest.WaitResult(t, done, http.StatusOK)
	if got := rowError(t, rowsBySheet(t, result.Body, "顾客"), "C1"); got != "顾客手机号已变化，请重新预览导入" {
		t.Fatalf("row error = %q", got)
	}
	if n := f.count("SELECT count(*) FROM customers WHERE phone=$1", phone); n != 1 {
		t.Fatalf("customers with phone = %d, want 1", n)
	}
}

func TestImportRejectsLinkedCustomerNotPrelocked(t *testing.T) {
	f := open(t)
	batch, _ := f.previewDefault()
	linked := uuid.New()
	ctx := context.Background()

	setup := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	dbtest.Exec(t, setup, `UPDATE customers SET phone=$2 WHERE id=$1`, uuid.MustParse(f.nativeCustomer), "13800130000")
	dbtest.Exec(t, setup, `UPDATE import_batch_rows SET entity_id=$2 WHERE batch_id=$1 AND sheet='顾客' AND source_key='C1'`, uuid.MustParse(batch), linked)
	dbtest.Commit(t, setup)

	held := holdCustomerPhone(t, f, "13800131234")
	done := make(chan optest.Result, 1)
	go func() { done <- f.HTTPWithToken(t, f.Owner, "commit_import", map[string]any{"batch_id": batch}) }()
	optest.AssertWaiting(t, f.Pool, done, 1)

	inserted := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	dbtest.Exec(t, inserted, `INSERT INTO customers(id,tenant_id,store_id,name,phone,merged_into,created_by) VALUES($1,$2,$3,'后建顾客',$4,$5,$6)`, linked, f.Res.TenantID, f.Res.StoreID, "13800131234", uuid.MustParse(f.nativeCustomer), f.Res.OwnerID)
	dbtest.Commit(t, inserted)
	if err := held.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	result := optest.WaitResult(t, done, http.StatusOK)
	if got := rowError(t, rowsBySheet(t, result.Body, "顾客"), "C1"); got != "顾客手机号已变化，请重新预览导入" {
		t.Fatalf("row error = %q", got)
	}
}

func TestImportRejectsCustomerCreatedAfterPhoneLock(t *testing.T) {
	f := open(t)
	const phone = "13700136666"
	r := f.upload(f.Owner, singleCustomerWorkbook(t, phone), "late-customer.xlsx")
	if r.Status != http.StatusOK {
		t.Fatalf("preview = %d %v", r.Status, r.Body)
	}
	batch := r.Body["id"].(string)
	pending := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	dbtest.Exec(t, pending, `INSERT INTO customers(tenant_id,store_id,name,phone,created_by) VALUES($1,$2,'未提交顾客',$3,$4)`, f.Res.TenantID, f.Res.StoreID, phone, f.Res.OwnerID)

	done := make(chan optest.Result, 1)
	go func() { done <- f.HTTPWithToken(t, f.Owner, "commit_import", map[string]any{"batch_id": batch}) }()
	optest.AssertWaiting(t, f.Pool, done, 1)
	dbtest.Commit(t, pending)

	result := optest.WaitResult(t, done, http.StatusOK)
	if got := rowError(t, rowsBySheet(t, result.Body, "顾客"), "C1"); got != "顾客手机号已变化，请重新预览导入" {
		t.Fatalf("row error = %q", got)
	}
}

func TestImportDedupesStoredSourceKey(t *testing.T) {
	f := open(t)
	data := workbook(t,
		[][]string{{"来源顾客ID", "姓名", "手机号(可空)", "性别(男/女/空)"}, {"C1", "顾客", "", ""}},
		[][]string{
			{"来源卡号", "来源顾客ID", "卡种", "本金", "赠送金", "到期日(YYYY-MM-DD,可空)", "积分(整数,可空)"},
			{"K1", "C1", "储值卡A", "10", "0", "", ""},
			{"K1", "C1", "储值卡A", "20", "0", "", ""},
		},
		[][]string{{"来源卡号", "来源顾客ID", "卡种", "项目", "剩余购买次数", "剩余赠送次数", "到期日(可空)"}},
		[][]string{{"顾客数", "储值卡数", "本金合计", "赠送金合计", "次卡套餐卡数"}, {"1", "1", "30", "0", "0"}},
	)
	r := f.upload(f.Owner, data, "duplicate-stored.xlsx")
	if r.Status != http.StatusOK {
		t.Fatalf("preview = %d %v", r.Status, r.Body)
	}
	batch := r.Body["id"].(string)
	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	dbtest.Exec(t, tx, `UPDATE import_batch_rows SET error=NULL, action='created', card_type_id=$2 WHERE batch_id=$1 AND sheet='储值卡' AND row_number=3`, uuid.MustParse(batch), uuid.MustParse(f.storedType))
	dbtest.Commit(t, tx)

	result := f.Call(t, f.Owner, "commit_import", map[string]any{"batch_id": batch}, http.StatusOK)
	if n := f.count("SELECT count(*) FROM cards"); n != 1 {
		t.Fatalf("cards = %d, want 1", n)
	}
	duplicate := false
	for _, row := range rowsBySheet(t, result.Body, "储值卡") {
		if row["source_key"] == "K1" && row["error"] == "已导入" {
			duplicate = true
		}
	}
	if !duplicate {
		t.Fatalf("rows = %v", rowsBySheet(t, result.Body, "储值卡"))
	}
}

func TestImportRejectsChangedPhoneOfLinkedCustomer(t *testing.T) {
	f := open(t)
	data := workbook(t,
		[][]string{{"来源顾客ID", "姓名", "手机号(可空)", "性别(男/女/空)"}, {"C1", "链接顾客", "13800131234", ""}},
		[][]string{{"来源卡号", "来源顾客ID", "卡种", "本金", "赠送金", "到期日(YYYY-MM-DD,可空)", "积分(整数,可空)"}, {"K1", "C1", "储值卡A", "10", "0", "", ""}},
		[][]string{{"来源卡号", "来源顾客ID", "卡种", "项目", "剩余购买次数", "剩余赠送次数", "到期日(可空)"}},
		[][]string{{"顾客数", "储值卡数", "本金合计", "赠送金合计", "次卡套餐卡数"}, {"1", "1", "10", "0", "0"}},
	)
	r := f.upload(f.Owner, data, "changed-phone-linked.xlsx")
	if r.Status != http.StatusOK {
		t.Fatalf("preview = %d %v", r.Status, r.Body)
	}
	batch := r.Body["id"].(string)
	setup := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	dbtest.Exec(t, setup, `UPDATE customers SET phone=$2 WHERE id=$1`, uuid.MustParse(f.nativeCustomer), "13800130000")
	dbtest.Commit(t, setup)

	result := f.HTTPWithToken(t, f.Owner, "commit_import", map[string]any{"batch_id": batch})
	if result.Status != http.StatusOK {
		t.Fatalf("commit = %d %v", result.Status, result.Body)
	}
	if got := rowError(t, rowsBySheet(t, result.Body, "顾客"), "C1"); got != "顾客手机号已变化，请重新预览导入" {
		t.Fatalf("row error = %q", got)
	}
	if n := f.count("SELECT count(*) FROM cards"); n != 0 {
		t.Fatalf("cards = %d, want 0", n)
	}
}

func TestImportConflictFallbackWithoutCustomer(t *testing.T) {
	f := open(t)
	const phone = "13700138888"
	r := f.upload(f.Owner, singleCustomerWorkbook(t, phone), "fallback.xlsx")
	if r.Status != http.StatusOK {
		t.Fatalf("preview = %d %v", r.Status, r.Body)
	}
	batch := r.Body["id"].(string)
	ctx := context.Background()
	if _, err := f.Pool.Exec(ctx, `CREATE OR REPLACE FUNCTION dataio_skip_customer() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.phone = '13700138888' THEN
        RETURN NULL;
    END IF;
    RETURN NEW;
END $$;`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Pool.Exec(ctx, `CREATE TRIGGER dataio_skip_customer BEFORE INSERT ON customers FOR EACH ROW EXECUTE FUNCTION dataio_skip_customer()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = f.Pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS dataio_skip_customer ON customers`)
		_, _ = f.Pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS dataio_skip_customer()`)
	})

	result := f.HTTPWithToken(t, f.Owner, "commit_import", map[string]any{"batch_id": batch})
	if result.Status != http.StatusOK {
		t.Fatalf("commit = %d %v", result.Status, result.Body)
	}
	if got := rowError(t, rowsBySheet(t, result.Body, "顾客"), "C1"); got != "顾客手机号已变化，请重新预览导入" {
		t.Fatalf("row error = %q", got)
	}
	if n := f.count("SELECT count(*) FROM import_batches WHERE status='committed'"); n != 1 {
		t.Fatalf("committed batches = %d, want 1", n)
	}
}

func TestImportCommitKeepsSpecificGroupError(t *testing.T) {
	f := open(t)
	data := workbook(t,
		[][]string{{"来源顾客ID", "姓名", "手机号(可空)", "性别(男/女/空)"}, {"C1", "顾客", "13700139998", ""}},
		[][]string{{"来源卡号", "来源顾客ID", "卡种", "本金", "赠送金", "到期日(YYYY-MM-DD,可空)", "积分(整数,可空)"}},
		[][]string{
			{"来源卡号", "来源顾客ID", "卡种", "项目", "剩余购买次数", "剩余赠送次数", "到期日(可空)"},
			{"K1", "C1", "剪发十次", "剪发", "1", "0", ""},
			{"K1", "C1", "剪发十次", "烫发", "bad", "0", ""},
		},
		[][]string{{"顾客数", "储值卡数", "本金合计", "赠送金合计", "次卡套餐卡数"}, {"1", "0", "0", "0", "1"}},
	)
	r := f.upload(f.Owner, data, "specific-group.xlsx")
	if r.Status != http.StatusOK {
		t.Fatalf("preview = %d %v", r.Status, r.Body)
	}
	batch := r.Body["id"].(string)
	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	dbtest.Exec(t, tx, `UPDATE import_batch_rows SET error=NULL WHERE batch_id=$1 AND sheet='次卡套餐' AND row_number=2`, uuid.MustParse(batch))
	dbtest.Commit(t, tx)

	result := f.Call(t, f.Owner, "commit_import", map[string]any{"batch_id": batch}, http.StatusOK)
	for _, row := range rowsBySheet(t, result.Body, "次卡套餐") {
		if row["item_name"] == "烫发" && row["error"] != "剩余购买次数格式不正确" {
			t.Fatalf("specific row = %v", row)
		}
		if row["item_name"] == "剪发" && row["error"] != "同一来源卡号的其他项目有错误" {
			t.Fatalf("sibling row = %v", row)
		}
	}
	if n := f.count("SELECT count(*) FROM cards"); n != 0 {
		t.Fatalf("cards = %d, want 0", n)
	}
}

func TestImportCustomerLocksByUUIDOrder(t *testing.T) {
	f := open(t)
	second := f.call("create_customer", map[string]any{"name": "第二顾客", "phone": "13800132222"})["customer"].(map[string]any)["id"].(string)
	data := workbook(t,
		[][]string{
			{"来源顾客ID", "姓名", "手机号(可空)", "性别(男/女/空)"},
			{"C1", "第一位", "13800131234", ""},
			{"C2", "第二位", "13800132222", ""},
		},
		[][]string{{"来源卡号", "来源顾客ID", "卡种", "本金", "赠送金", "到期日(YYYY-MM-DD,可空)", "积分(整数,可空)"}},
		[][]string{{"来源卡号", "来源顾客ID", "卡种", "项目", "剩余购买次数", "剩余赠送次数", "到期日(可空)"}},
		[][]string{{"顾客数", "储值卡数", "本金合计", "赠送金合计", "次卡套餐卡数"}, {"2", "0", "0", "0", "0"}},
	)
	r := f.upload(f.Owner, data, "lock-order.xlsx")
	if r.Status != http.StatusOK {
		t.Fatalf("preview = %d %v", r.Status, r.Body)
	}
	batch := r.Body["id"].(string)
	low, high := uuid.MustParse(f.nativeCustomer), uuid.MustParse(second)
	if low.String() > high.String() {
		low, high = high, low
	}
	ctx := context.Background()
	held := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	if _, err := dbgen.New(held).LockCustomer(ctx, high); err != nil {
		t.Fatal(err)
	}
	done := make(chan optest.Result, 1)
	go func() { done <- f.HTTPWithToken(t, f.Owner, "commit_import", map[string]any{"batch_id": batch}) }()
	optest.AssertWaiting(t, f.Pool, done, 1)

	probe := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	_, err := probe.Exec(ctx, `SELECT id FROM customers WHERE id=$1 FOR NO KEY UPDATE NOWAIT`, low)
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "55P03" {
		t.Fatalf("low customer must be locked before waiting for high: got %v, want PostgreSQL 55P03", err)
	}
	dbtest.Rollback(t, probe)
	dbtest.Commit(t, held)
	optest.WaitResult(t, done, http.StatusOK)
}

func TestImportInvalidSourceKeyConstraint(t *testing.T) {
	f := open(t)
	batch, _ := f.previewDefault()
	ctx := context.Background()

	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	_, err := tx.Exec(ctx, `INSERT INTO import_batch_rows(tenant_id,store_id,batch_id,sheet,row_number,kind,source_key) VALUES($1,$2,$3,'顾客',99,'customer','')`, f.Res.TenantID, f.Res.StoreID, uuid.MustParse(batch))
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "23514" || pe.ConstraintName != "import_batch_rows_source_key_check" {
		t.Fatalf("empty key without error = %v", err)
	}
	dbtest.Rollback(t, tx)

	tx = dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	dbtest.Exec(t, tx, `INSERT INTO import_batch_rows(tenant_id,store_id,batch_id,sheet,row_number,kind,source_key,error) VALUES($1,$2,$3,'顾客',99,'customer','','来源顾客ID不能为空')`, f.Res.TenantID, f.Res.StoreID, uuid.MustParse(batch))
	dbtest.Rollback(t, tx)
}

func TestImportInvalidKeysDownGuard(t *testing.T) {
	f := open(t)
	batch, _ := f.previewDefault()
	ctx := context.Background()
	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	dbtest.Exec(t, tx, `INSERT INTO import_batch_rows(tenant_id,store_id,batch_id,sheet,row_number,kind,source_key,error) VALUES($1,$2,$3,'顾客',99,'customer','','来源顾客ID不能为空')`, f.Res.TenantID, f.Res.StoreID, uuid.MustParse(batch))
	dbtest.Commit(t, tx)

	err := db.MigrateDown(ctx, f.Pool)
	if err == nil || !strings.Contains(err.Error(), "无法恢复来源编号约束：存在空编号的导入错误行") {
		t.Fatalf("down with empty keys = %v", err)
	}
	if v, err := db.MigrationVersion(ctx, f.Pool); err != nil || v != 48 {
		t.Fatalf("version after refused down = %d, %v", v, err)
	}

	tx = dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	dbtest.Exec(t, tx, `UPDATE import_batch_rows SET source_key='fixed' WHERE batch_id=$1 AND row_number=99`, uuid.MustParse(batch))
	dbtest.Commit(t, tx)
	dbtest.MigrateDownTo(t, f.Pool, 47)

	tx = dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	_, err = tx.Exec(ctx, `INSERT INTO import_batch_rows(tenant_id,store_id,batch_id,sheet,row_number,kind,source_key,error) VALUES($1,$2,$3,'顾客',100,'customer','','任意错误')`, f.Res.TenantID, f.Res.StoreID, uuid.MustParse(batch))
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "23514" || pe.ConstraintName != "import_batch_rows_source_key_check" {
		t.Fatalf("old constraint after down = %v", err)
	}
	dbtest.Rollback(t, tx)
}
