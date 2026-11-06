package dataio_test

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

func TestDefaultManagerCannotImportOrExport(t *testing.T) {
	f := open(t)
	_, token := f.Hire("店长", "002", "manager", f.Role("店长"))
	if r := f.upload(token, defaultWorkbook(t), "import.xlsx"); r.Status != http.StatusForbidden || r.Body["detail"] != "permission denied" {
		t.Fatalf("upload = %d %v", r.Status, r.Body)
	}
	if code, _ := f.export(token); code != http.StatusForbidden {
		t.Fatalf("export = %d", code)
	}
}

func TestOwnerCanGrantImportExport(t *testing.T) {
	f := open(t)
	_, token := f.Hire("店长", "002", "manager", f.Role("店长"))
	roles := f.call("list_roles", map[string]any{})["roles"].([]any)
	var managerRole map[string]any
	for _, raw := range roles {
		role := raw.(map[string]any)
		if role["name"] == "店长" {
			managerRole = role
		}
	}
	if managerRole == nil {
		t.Fatal("missing manager role")
	}
	permissions := append([]any{}, managerRole["permissions"].([]any)...)
	permissions = append(permissions, map[string]any{"permission": "data.import"}, map[string]any{"permission": "data.export"})
	f.call("update_role", map[string]any{"role_id": managerRole["id"], "name": "店长", "permissions": permissions})
	if r := f.upload(token, defaultWorkbook(t), "import.xlsx"); r.Status != http.StatusOK {
		t.Fatalf("upload after grant = %d %v", r.Status, r.Body)
	}
	if code, _ := f.export(token); code != http.StatusOK {
		t.Fatalf("export after grant = %d", code)
	}
}

func TestImportOperationsRequireDataImport(t *testing.T) {
	f := open(t)
	batch, _ := f.previewDefault()
	_, token := f.Hire("店长", "002", "manager", f.Role("店长"))
	cases := []struct {
		op   string
		args map[string]any
	}{
		{"commit_import", map[string]any{"batch_id": batch}},
		{"undo_import", map[string]any{"batch_id": batch}},
		{"list_import_batches", map[string]any{}},
		{"get_import_batch", map[string]any{"batch_id": batch}},
	}
	for _, tc := range cases {
		r := f.Call(t, token, tc.op, tc.args, 0)
		if r.Status != http.StatusForbidden || r.Detail() != "permission denied" {
			t.Fatalf("%s = %d %q", tc.op, r.Status, r.Detail())
		}
	}
}

func TestImportOperationsAndAIChannel(t *testing.T) {
	f := open(t)
	caller := f.MustCaller(f.Owner)
	tools := map[string]bool{}
	for _, tool := range f.Registry.Tools(caller) {
		tools[tool.Name] = true
	}
	for _, name := range []string{"commit_import", "undo_import"} {
		if tools[name] {
			t.Fatalf("%s must not be an AI tool", name)
		}
	}
	for _, name := range []string{"list_import_batches", "get_import_batch"} {
		if !tools[name] {
			t.Fatalf("%s should be an AI read tool", name)
		}
	}
	batch, _ := f.previewDefault()
	if r := f.AI(t, caller, "commit_import", map[string]any{"batch_id": batch}); r.Status != http.StatusNotFound {
		t.Fatalf("AI commit = %d %v", r.Status, r.Err)
	}
	if r := f.AI(t, caller, "list_import_batches", map[string]any{}); r.Status != http.StatusOK {
		t.Fatalf("AI list = %d %v", r.Status, r.Err)
	}
	if r := f.AI(t, caller, "get_import_batch", map[string]any{"batch_id": batch}); r.Status != http.StatusOK {
		t.Fatalf("AI get = %d %v", r.Status, r.Err)
	}
}

type queryCounter struct{ all atomic.Int64 }

func (c *queryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.all.Add(1)
	return ctx
}
func (*queryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestImportReadQueryCount(t *testing.T) {
	f := open(t)
	counter := &queryCounter{}
	cfg := f.Pool.Config().Copy()
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
	smallData := workbook(t,
		[][]string{{"来源顾客ID", "姓名", "手机号(可空)", "性别(男/女/空)"}, {"C1", "", "", ""}},
		[][]string{{"来源卡号", "来源顾客ID", "卡种", "本金", "赠送金", "到期日(YYYY-MM-DD,可空)", "积分(整数,可空)"}},
		[][]string{{"来源卡号", "来源顾客ID", "卡种", "项目", "剩余购买次数", "剩余赠送次数", "到期日(可空)"}},
		[][]string{{"顾客数", "储值卡数", "本金合计", "赠送金合计", "次卡套餐卡数"}, {"1", "0", "0", "0", "0"}},
	)
	small := f.upload(f.Owner, smallData, "small.xlsx")
	if small.Status != http.StatusOK {
		t.Fatal(small)
	}
	customers := make([][]string, 51)
	customers[0] = []string{"来源顾客ID", "姓名", "手机号(可空)", "性别(男/女/空)"}
	for i := 1; i < len(customers); i++ {
		customers[i] = []string{fmt.Sprintf("C%d", i), "", "", ""}
	}
	largeData := workbook(t, customers,
		[][]string{{"来源卡号", "来源顾客ID", "卡种", "本金", "赠送金", "到期日(YYYY-MM-DD,可空)", "积分(整数,可空)"}},
		[][]string{{"来源卡号", "来源顾客ID", "卡种", "项目", "剩余购买次数", "剩余赠送次数", "到期日(可空)"}},
		[][]string{{"顾客数", "储值卡数", "本金合计", "赠送金合计", "次卡套餐卡数"}, {"50", "0", "0", "0", "0"}},
	)
	large := f.upload(f.Owner, largeData, "large.xlsx")
	if large.Status != http.StatusOK {
		t.Fatal(large)
	}
	smallID := small.Body["id"].(string)
	largeID := large.Body["id"].(string)
	countSmall := 0
	countLarge := 0
	counter.all.Store(0)
	if r := app.HTTPWithToken(t, f.Owner, "get_import_batch", map[string]any{"batch_id": smallID}); r.Status != http.StatusOK {
		t.Fatal(r.Status, r.Body)
	}
	countSmall = int(counter.all.Load())
	counter.all.Store(0)
	if r := app.HTTPWithToken(t, f.Owner, "get_import_batch", map[string]any{"batch_id": largeID}); r.Status != http.StatusOK {
		t.Fatal(r.Status, r.Body)
	}
	countLarge = int(counter.all.Load())
	if countSmall != countLarge {
		t.Fatalf("get query count: 1 row=%d, 50 rows=%d", countSmall, countLarge)
	}
	// list 的查询数不随批次数增长。
	counter.all.Store(0)
	app.HTTPWithToken(t, f.Owner, "list_import_batches", map[string]any{})
	first := counter.all.Load()
	for range 10 {
		f.upload(f.Owner, smallData, "more.xlsx")
	}
	counter.all.Store(0)
	app.HTTPWithToken(t, f.Owner, "list_import_batches", map[string]any{})
	second := counter.all.Load()
	if first != second {
		t.Fatalf("list query count: 2 batches=%d, 12 batches=%d", first, second)
	}
}

func TestImportExportMasksPhoneWithoutFullPhonePermission(t *testing.T) {
	f := open(t)
	role := f.call("create_role", map[string]any{"name": "数据", "permissions": []any{
		map[string]any{"permission": "data.import"}, map[string]any{"permission": "data.export"},
	}})
	_, token := f.Hire("数据员", "003", "data", uuid.MustParse(role["id"].(string)))
	r := f.upload(token, defaultWorkbook(t), "mask.xlsx")
	if r.Status != http.StatusOK {
		t.Fatal(r)
	}
	for _, row := range rowsBySheet(t, r.Body, "顾客") {
		if row["source_key"] == "C1" && row["phone"] != "138****1234" {
			t.Fatalf("import preview phone = %v", row["phone"])
		}
	}
	file := exportFile(t, f, token)
	rows, err := file.GetRows("顾客")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows[1:] {
		if row[0] == f.nativeCustomer && row[2] != "138****1234" {
			t.Fatalf("export phone = %v", row[2])
		}
	}
}

func TestUndoIsStoreIsolated(t *testing.T) {
	f := open(t)
	batch, _ := f.previewDefault()
	f.commit(batch)
	if _, err := staff.Bootstrap(context.Background(), f.Pool, staff.BootstrapInput{TenantName: "别店", StoreName: "别店", OwnerName: "别老板", OwnerNumber: "001", OwnerAccount: "other", OwnerPassword: "other-password"}); err != nil {
		t.Fatal(err)
	}
	code, token := f.Login(t, "other", "other-password")
	if code != http.StatusOK {
		t.Fatal(code)
	}
	r := f.Call(t, token, "undo_import", map[string]any{"batch_id": batch}, 0)
	if r.Status != http.StatusNotFound {
		t.Fatalf("cross-store undo = %d %v", r.Status, r.Body)
	}
}
