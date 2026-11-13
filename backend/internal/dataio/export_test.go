package dataio_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/wzhlinhai/salon/backend/internal/dataio"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

func (f *fixture) export(token string) (int, []byte) {
	f.t.Helper()
	rec := f.exportResponse(token)
	return rec.Code, rec.Body.Bytes()
}

func (f *fixture) exportResponse(token string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodGet, dataio.ExportPath, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	f.Handler.ServeHTTP(rec, req)
	return rec
}

func exportFile(t *testing.T, f *fixture, token string) *excelize.File {
	t.Helper()
	code, data := f.export(token)
	if code != http.StatusOK {
		t.Fatalf("export = %d %s", code, data)
	}
	file, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

func TestExportAllReadable(t *testing.T) {
	f := open(t)
	batch, _ := f.previewDefault()
	f.commit(batch)
	// 原生卡与导入数据同时导出。
	f.call("open_card", map[string]any{"customer_id": f.nativeCustomer, "card_type_id": f.storedType, "principal": "5.00", "bonus": "0", "sales_staff_ids": []any{f.Res.OwnerID}, "payments": []any{map[string]any{"method": "cash", "amount": "5.00"}}})

	file := exportFile(t, f, f.Owner)
	wantSheets := []string{"顾客", "储值卡", "次卡套餐", "卡流水", "单据", "支付", "员工", "项目", "卡种"}
	if got := file.GetSheetList(); len(got) != len(wantSheets) {
		t.Fatalf("sheets = %v", got)
	} else {
		for i := range wantSheets {
			if got[i] != wantSheets[i] {
				t.Fatalf("sheets = %v", got)
			}
		}
	}
	headers := map[string][]string{
		"顾客":   {"来源顾客ID", "姓名", "手机号(可空)", "性别(男/女/空)"},
		"储值卡":  {"来源卡号", "来源顾客ID", "卡种", "本金", "赠送金", "到期日(YYYY-MM-DD,可空)", "积分(整数,可空)"},
		"次卡套餐": {"来源卡号", "来源顾客ID", "卡种", "项目", "剩余购买次数", "剩余赠送次数", "到期日(可空)"},
	}
	for sheet, want := range headers {
		rows, err := file.GetRows(sheet)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 0 {
			t.Fatalf("%s has no header", sheet)
		}
		for i := range want {
			if rows[0][i] != want[i] {
				t.Fatalf("%s header = %v", sheet, rows[0])
			}
		}
	}
	rows, _ := file.GetRows("顾客")
	if len(rows)-1 != f.count("SELECT count(*) FROM customers") {
		t.Fatalf("顾客 rows = %d, db = %d", len(rows)-1, f.count("SELECT count(*) FROM customers"))
	}
	rows, _ = file.GetRows("储值卡")
	if len(rows)-1 != f.count("SELECT count(*) FROM cards c JOIN card_types t ON t.id=c.card_type_id WHERE t.kind='stored_value'") {
		t.Fatalf("储值卡 rows = %d", len(rows)-1)
	}
	rows, _ = file.GetRows("次卡套餐")
	if len(rows)-1 != f.count("SELECT count(*) FROM card_items ci JOIN cards c ON c.id=ci.card_id JOIN card_types t ON t.id=c.card_type_id WHERE t.kind IN ('count','package')") {
		t.Fatalf("次卡套餐 rows = %d", len(rows)-1)
	}
	for sheet, query := range map[string]string{
		"卡流水": "SELECT count(*) FROM card_ledger",
		"支付":  "SELECT count(*) FROM order_payments",
		"员工":  "SELECT count(*) FROM staff",
		"项目":  "SELECT count(*) FROM items",
		"卡种":  "SELECT count(*) FROM card_types t LEFT JOIN card_type_items ci ON ci.card_type_id=t.id",
	} {
		rows, _ := file.GetRows(sheet)
		if len(rows)-1 != f.count(query) {
			t.Fatalf("%s rows = %d, db = %d", sheet, len(rows)-1, f.count(query))
		}
	}
	orders, _ := file.GetRows("单据")
	if len(orders)-1 != f.count("SELECT count(*) FROM orders o LEFT JOIN order_items i ON i.order_id=o.id") {
		t.Fatalf("单据 rows = %d", len(orders)-1)
	}
	if n := f.count("SELECT count(*) FROM audit_log WHERE target_kind='data_export'"); n != 1 {
		t.Fatalf("export audit = %d", n)
	}
}

func TestExportIsStoreIsolated(t *testing.T) {
	f := open(t)
	batch, _ := f.previewDefault()
	f.commit(batch)
	before := f.count("SELECT count(*) FROM customers")

	second, err := staff.Bootstrap(context.Background(), f.Pool, staff.BootstrapInput{TenantName: "二店", StoreName: "二店", OwnerName: "二老板", OwnerNumber: "001", OwnerAccount: "second", OwnerPassword: "second-password"})
	if err != nil {
		t.Fatal(err)
	}
	_ = second
	code, token := f.Login(t, "second", "second-password")
	if code != http.StatusOK {
		t.Fatal(code)
	}
	r := f.HTTPWithToken(t, token, "create_customer", map[string]any{"name": "二店顾客", "phone": "13600130000"})
	if r.Status != http.StatusOK {
		t.Fatal(r.Status, r.Body)
	}
	file := exportFile(t, f, f.Owner)
	rows, _ := file.GetRows("顾客")
	if len(rows)-1 != before {
		t.Fatalf("first store export rows = %d, want %d", len(rows)-1, before)
	}
	secondFile := exportFile(t, f, token)
	secondRows, _ := secondFile.GetRows("顾客")
	if len(secondRows)-1 != f.count("SELECT count(*) FROM customers WHERE tenant_id=$1", second.TenantID) {
		t.Fatalf("second store rows = %d", len(secondRows)-1)
	}
	if len(secondRows)-1 != 1 {
		t.Fatalf("second store exported %d customers", len(secondRows)-1)
	}
}

func TestExportOnlySendsAttachmentHeadersOnSuccess(t *testing.T) {
	f := open(t)
	ok := f.exportResponse(f.Owner)
	if ok.Code != http.StatusOK {
		t.Fatalf("export = %d %s", ok.Code, ok.Body.String())
	}
	if got := ok.Header().Get("Content-Type"); got != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
		t.Fatalf("success content type = %q", got)
	}
	if got := ok.Header().Get("Content-Disposition"); got != `attachment; filename="salon-export.xlsx"` {
		t.Fatalf("success disposition = %q", got)
	}

	_, manager := f.Hire("店长", "002", "manager", f.Role("店长"))
	forbidden := f.exportResponse(manager)
	var forbiddenBody map[string]string
	if err := json.Unmarshal(forbidden.Body.Bytes(), &forbiddenBody); err != nil {
		t.Fatal(err)
	}
	if forbidden.Code != http.StatusForbidden || forbiddenBody["detail"] != "permission denied" {
		t.Fatalf("forbidden export = %d %s", forbidden.Code, forbidden.Body.String())
	}
	if got := forbidden.Header().Get("Content-Disposition"); got != "" {
		t.Fatalf("403 disposition = %q", got)
	}
	if got := forbidden.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("403 content type = %q", got)
	}

	if _, err := f.Pool.Exec(context.Background(), "REVOKE SELECT ON customers FROM salon_app"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = f.Pool.Exec(context.Background(), "GRANT SELECT ON customers TO salon_app")
	})
	failed := f.exportResponse(f.Owner)
	if failed.Code != http.StatusInternalServerError || !strings.Contains(failed.Body.String(), "internal error") {
		t.Fatalf("failed export = %d %s", failed.Code, failed.Body.String())
	}
	if got := failed.Header().Get("Content-Disposition"); got != "" {
		t.Fatalf("500 disposition = %q", got)
	}
	if got := failed.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("500 content type = %q", got)
	}
}
