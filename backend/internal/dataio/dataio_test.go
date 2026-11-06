package dataio_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/wzhlinhai/salon/backend/internal/dataio"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
)

type fixture struct {
	*optest.Shop
	t              *testing.T
	item           string
	storedType     string
	countType      string
	nativeCustomer string
}

func open(t *testing.T) *fixture {
	t.Helper()
	s := optest.OpenShop(t)
	f := &fixture{Shop: s, t: t}
	item := s.Call(t, s.Owner, "create_item", map[string]any{
		"name": "剪发", "price": "100.00", "discountable": true,
		"labor_fee_amount": nil, "labor_fee_percent": nil, "positions": []any{}, "level_prices": []any{},
	}, http.StatusOK).Body
	f.item = item["id"].(string)
	stored := s.Call(t, s.Owner, "create_card_type", map[string]any{"name": "储值卡A", "kind": "stored_value", "validity_kind": "none", "items": []any{}}, http.StatusOK).Body
	f.storedType = stored["id"].(string)
	count := s.Call(t, s.Owner, "create_card_type", map[string]any{
		"name": "剪发十次", "kind": "count", "validity_kind": "none",
		"items": []any{map[string]any{"item_id": f.item, "paid_count": 10, "gift_count": 0, "unit_price": "50"}},
	}, http.StatusOK).Body
	f.countType = count["id"].(string)
	customer := s.Call(t, s.Owner, "create_customer", map[string]any{"name": "已有顾客", "phone": "13800131234"}, http.StatusOK).Body
	f.nativeCustomer = customer["customer"].(map[string]any)["id"].(string)
	return f
}

func (f *fixture) call(op string, args any) map[string]any {
	f.t.Helper()
	return f.Call(f.t, f.Owner, op, args, http.StatusOK).Body
}

func workbook(t *testing.T, customers, stored, counts, summary [][]string) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()

	if err := f.SetSheetName("Sheet1", "顾客"); err != nil {
		t.Fatal(err)
	}
	for _, sheet := range []string{"储值卡", "次卡套餐", "汇总"} {
		if _, err := f.NewSheet(sheet); err != nil {
			t.Fatal(err)
		}
	}
	for sheet, rows := range map[string][][]string{"顾客": customers, "储值卡": stored, "次卡套餐": counts, "汇总": summary} {
		for i, row := range rows {
			values := make([]any, len(row))
			for j := range row {
				values[j] = row[j]
			}
			if err := f.SetSheetRow(sheet, fmt.Sprintf("A%d", i+1), &values); err != nil {
				t.Fatal(err)
			}
		}
	}
	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func defaultWorkbook(t *testing.T) []byte {
	return workbook(t,
		[][]string{
			{"来源顾客ID", "姓名", "手机号(可空)", "性别(男/女/空)"},
			{"C1", "链接顾客", "13800131234", "男"},
			{"C2", "无手机顾客", "", "女"},
			{"C3", "新手机顾客", "13700139999", ""},
		},
		[][]string{
			{"来源卡号", "来源顾客ID", "卡种", "本金", "赠送金", "到期日(YYYY-MM-DD,可空)", "积分(整数,可空)"},
			{"K1", "C1", "储值卡A", "1000.50", "200", "2029-12-31", "50"},
		},
		[][]string{
			{"来源卡号", "来源顾客ID", "卡种", "项目", "剩余购买次数", "剩余赠送次数", "到期日(可空)"},
			{"K2", "C2", "剪发十次", "剪发", "8", "2", "2030-01-31"},
		},
		[][]string{
			{"顾客数", "储值卡数", "本金合计", "赠送金合计", "次卡套餐卡数"},
			{"3", "1", "1000.50", "200", "1"},
		})
}

type upload struct {
	Status int
	Body   map[string]any
}

func (f *fixture) upload(token string, data []byte, name string) upload {
	f.t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		f.t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		f.t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, dataio.UploadPath, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	f.Handler.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			f.t.Fatalf("decode upload response %q: %v", rec.Body.String(), err)
		}
	}
	return upload{Status: rec.Code, Body: out}
}

func (f *fixture) previewDefault() (string, map[string]any) {
	f.t.Helper()
	r := f.upload(f.Owner, defaultWorkbook(f.t), "import.xlsx")
	if r.Status != http.StatusOK {
		f.t.Fatalf("preview = %d %v", r.Status, r.Body)
	}
	return r.Body["id"].(string), r.Body
}

func (f *fixture) commit(batch string) map[string]any {
	f.t.Helper()
	return f.call("commit_import", map[string]any{"batch_id": batch})
}

func (f *fixture) count(query string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.Pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func rowsBySheet(t *testing.T, body map[string]any, sheet string) []map[string]any {
	t.Helper()
	out := []map[string]any{}
	for _, raw := range body["rows"].([]any) {
		row := raw.(map[string]any)
		if row["sheet"] == sheet {
			out = append(out, row)
		}
	}
	return out
}

func rowError(t *testing.T, rows []map[string]any, source string) string {
	t.Helper()
	for _, row := range rows {
		if row["source_key"] == source {
			if value, ok := row["error"].(string); ok {
				return value
			}
		}
	}
	t.Fatalf("no row %s in %v", source, rows)
	return ""
}

func TestPreviewLeavesBusinessDataUnchanged(t *testing.T) {
	f := open(t)
	before := dbtest.Snapshot(t, f.Pool, f.Res.TenantID, "customers", "cards", "card_ledger", "card_items", "card_count_ledger", "card_points_ledger")
	batch, body := f.previewDefault()
	if body["status"] != "previewed" {
		t.Fatal(body)
	}
	if body["declared"].(map[string]any)["customers"] != float64(3) || body["file"].(map[string]any)["stored_value_cards"] != float64(1) {
		t.Fatal(body)
	}
	if got := dbtest.Snapshot(t, f.Pool, f.Res.TenantID, "customers", "cards", "card_ledger", "card_items", "card_count_ledger", "card_points_ledger"); got != before {
		t.Fatal("preview changed business data")
	}
	if n := f.count("SELECT count(*) FROM import_batch_rows WHERE batch_id=$1", batch); n != 5 {
		t.Fatal(n)
	}
}

func TestCommitImport(t *testing.T) {
	f := open(t)
	batch, _ := f.previewDefault()
	out := f.commit(batch)
	if out["status"] != "committed" {
		t.Fatal(out)
	}
	written := out["written"].(map[string]any)
	if written["customers"] != float64(3) || written["stored_value_cards"] != float64(1) || written["count_package_cards"] != float64(1) || written["principal"] != "1000.50" || written["bonus"] != "200.00" {
		t.Fatal(written)
	}
	if n := f.count("SELECT count(*) FROM customers"); n != 3 {
		t.Fatalf("customers = %d, want native + two created", n)
	}
	if n := f.count("SELECT count(*) FROM customers WHERE phone='13800131234'"); n != 1 {
		t.Fatalf("phone duplicate: %d", n)
	}
	if n := f.count("SELECT count(*) FROM cards"); n != 2 {
		t.Fatal(n)
	}
	if n := f.count("SELECT count(*) FROM card_ledger WHERE kind='import'"); n != 2 {
		t.Fatal(n)
	}
	if n := f.count("SELECT count(*) FROM card_count_ledger WHERE kind='import'"); n != 1 {
		t.Fatal(n)
	}
	if n := f.count("SELECT count(*) FROM card_points_ledger WHERE kind='import'"); n != 1 {
		t.Fatal(n)
	}
	if n := f.count("SELECT count(*) FROM performance_entries"); n != 0 {
		t.Fatalf("import created %d performance entries", n)
	}
	for _, action := range []string{"preview_import", "commit_import"} {
		if n := f.count("SELECT count(*) FROM audit_log WHERE target_kind='import_batch' AND action=$1", action); n != 1 {
			t.Fatalf("%s audit = %d", action, n)
		}
	}
	detail := f.call("get_import_batch", map[string]any{"batch_id": batch})
	diff := detail["differences"].(map[string]any)["declared_minus_written"].(map[string]any)
	for _, key := range []string{"customers", "stored_value_cards", "principal", "bonus", "count_package_cards"} {
		if diff[key] != float64(0) && diff[key] != "0.00" {
			t.Fatalf("difference %s = %v", key, diff[key])
		}
	}
}

func TestPhoneDedupe(t *testing.T) {
	f := open(t)
	data := workbook(t,
		[][]string{
			{"来源顾客ID", "姓名", "手机号(可空)", "性别(男/女/空)"},
			{"A", "同号一", "13800131234", ""},
			{"B", "同号二", "13800131234", ""},
			{"C", "无手机一", "", ""},
			{"D", "无手机二", "", ""},
			{"E", "新号一", "13700138888", ""},
			{"F", "新号二", "13700138888", ""},
		},
		[][]string{{"来源卡号", "来源顾客ID", "卡种", "本金", "赠送金", "到期日(YYYY-MM-DD,可空)", "积分(整数,可空)"}},
		[][]string{{"来源卡号", "来源顾客ID", "卡种", "项目", "剩余购买次数", "剩余赠送次数", "到期日(可空)"}},
		[][]string{{"顾客数", "储值卡数", "本金合计", "赠送金合计", "次卡套餐卡数"}, {"6", "0", "0", "0", "0"}},
	)
	r := f.upload(f.Owner, data, "dedupe.xlsx")
	if r.Status != http.StatusOK {
		t.Fatal(r)
	}
	f.commit(r.Body["id"].(string))
	if n := f.count("SELECT count(*) FROM customers"); n != 4 {
		t.Fatalf("customers = %d, want native + new phone + two unidentified", n)
	}
	for _, phone := range []string{"13800131234", "13700138888"} {
		if n := f.count("SELECT count(*) FROM customers WHERE phone=$1", phone); n != 1 {
			t.Fatalf("phone %s rows = %d", phone, n)
		}
	}
}

func TestRowErrorsDoNotAbortCommit(t *testing.T) {
	f := open(t)
	data := workbook(t,
		[][]string{
			{"来源顾客ID", "姓名", "手机号(可空)", "性别(男/女/空)"},
			{"BAD", "坏手机", "123", ""},
			{"C1", "好顾客", "13900139999", "女"},
			{"C2", "坏性别", "13600130000", "未知"},
		},
		[][]string{
			{"来源卡号", "来源顾客ID", "卡种", "本金", "赠送金", "到期日(YYYY-MM-DD,可空)", "积分(整数,可空)"},
			{"K1", "BAD", "储值卡A", "100", "0", "", ""},
			{"K2", "C1", "不存在的卡种", "100", "0", "", ""},
			{"K3", "C1", "储值卡A", "100.123", "0", "", ""},
			{"K4", "C1", "储值卡A", "100", "0", "", ""},
			{"K5", "C1", "储值卡A", "100", "0", "2020-13-40", ""},
		},
		[][]string{{"来源卡号", "来源顾客ID", "卡种", "项目", "剩余购买次数", "剩余赠送次数", "到期日(可空)"}},
		[][]string{{"顾客数", "储值卡数", "本金合计", "赠送金合计", "次卡套餐卡数"}, {"2", "4", "400", "0", "0"}},
	)
	r := f.upload(f.Owner, data, "errors.xlsx")
	if r.Status != http.StatusOK {
		t.Fatal(r)
	}
	batch := r.Body["id"].(string)
	customers := rowsBySheet(t, r.Body, "顾客")
	if !strings.Contains(rowError(t, customers, "BAD"), "手机号格式不正确") {
		t.Fatal(customers)
	}
	if !strings.Contains(rowError(t, customers, "C2"), "性别只能填写男、女或空") {
		t.Fatal(customers)
	}
	stored := rowsBySheet(t, r.Body, "储值卡")
	if !strings.Contains(rowError(t, stored, "K1"), "来源顾客不存在") {
		t.Fatal(stored)
	}
	if rowError(t, stored, "K2") != "卡种未配置: 不存在的卡种" {
		t.Fatal(stored)
	}
	if !strings.Contains(rowError(t, stored, "K3"), "本金格式不正确") {
		t.Fatal(stored)
	}
	if !strings.Contains(rowError(t, stored, "K5"), "到期日格式不正确") {
		t.Fatal(stored)
	}
	f.commit(batch)
	if n := f.count("SELECT count(*) FROM cards"); n != 1 {
		t.Fatalf("cards = %d, want one valid row", n)
	}
	if n := f.count("SELECT count(*) FROM customers"); n != 2 {
		t.Fatal(n)
	}
}

func TestReuploadIsIdempotentForCards(t *testing.T) {
	f := open(t)
	batch, _ := f.previewDefault()
	f.commit(batch)
	cards := f.count("SELECT count(*) FROM cards")
	again := f.upload(f.Owner, defaultWorkbook(t), "again.xlsx")
	if again.Status != http.StatusOK {
		t.Fatal(again)
	}
	if got := rowError(t, rowsBySheet(t, again.Body, "储值卡"), "K1"); got != "已导入" {
		t.Fatalf("reupload error = %q", got)
	}
	f.commit(again.Body["id"].(string))
	if n := f.count("SELECT count(*) FROM cards"); n != cards {
		t.Fatal(n)
	}
}

func TestCountCardItemValidation(t *testing.T) {
	f := open(t)
	data := workbook(t,
		[][]string{
			{"来源顾客ID", "姓名", "手机号(可空)", "性别(男/女/空)"},
			{"C1", "顾客", "13800131234", ""},
		},
		[][]string{
			{"来源卡号", "来源顾客ID", "卡种", "本金", "赠送金", "到期日(YYYY-MM-DD,可空)", "积分(整数,可空)"},
			{"S1", "C1", "剪发十次", "10", "0", "", ""},
		},
		[][]string{
			{"来源卡号", "来源顾客ID", "卡种", "项目", "剩余购买次数", "剩余赠送次数", "到期日(可空)"},
			{"K1", "C1", "剪发十次", "烫发", "1", "0", ""},
			{"K2", "C1", "储值卡A", "剪发", "1", "0", ""},
		},
		[][]string{{"顾客数", "储值卡数", "本金合计", "赠送金合计", "次卡套餐卡数"}, {"1", "1", "10", "0", "2"}},
	)
	r := f.upload(f.Owner, data, "items.xlsx")
	if r.Status != http.StatusOK {
		t.Fatal(r)
	}
	if got := rowError(t, rowsBySheet(t, r.Body, "储值卡"), "S1"); got != "卡种未配置: 剪发十次" {
		t.Fatal(got)
	}
	counts := rowsBySheet(t, r.Body, "次卡套餐")
	if got := rowError(t, counts, "K1"); got != "同一来源卡号的其他项目有错误" {
		t.Fatal(got)
	}
	if got := rowError(t, counts, "K2"); got != "同一来源卡号的其他项目有错误" {
		t.Fatal(got)
	}
}

func TestCheckTotalsDifferences(t *testing.T) {
	f := open(t)
	data := workbook(t,
		[][]string{
			{"来源顾客ID", "姓名", "手机号(可空)", "性别(男/女/空)"},
			{"C1", "顾客", "13800131234", ""},
		},
		[][]string{
			{"来源卡号", "来源顾客ID", "卡种", "本金", "赠送金", "到期日(YYYY-MM-DD,可空)", "积分(整数,可空)"},
			{"K1", "C1", "储值卡A", "10", "0", "", ""},
		},
		[][]string{{"来源卡号", "来源顾客ID", "卡种", "项目", "剩余购买次数", "剩余赠送次数", "到期日(可空)"}},
		[][]string{{"顾客数", "储值卡数", "本金合计", "赠送金合计", "次卡套餐卡数"}, {"5", "1", "999", "5", "0"}},
	)
	r := f.upload(f.Owner, data, "diff.xlsx")
	if r.Status != http.StatusOK {
		t.Fatal(r)
	}
	diff := r.Body["differences"].(map[string]any)["declared_minus_file"].(map[string]any)
	if diff["customers"] != float64(4) || diff["principal"] != "989.00" || diff["bonus"] != "5.00" {
		t.Fatalf("declared-minus-file = %v", diff)
	}
}

func TestFileParseErrors(t *testing.T) {
	f := open(t)
	t.Run("missing sheet", func(t *testing.T) {
		data := workbook(t, [][]string{{"来源顾客ID", "姓名", "手机号(可空)", "性别(男/女/空)"}}, nil, nil, nil)
		// workbook always creates all sheets; remove one to simulate a converter bug.
		file, err := excelize.OpenReader(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if err := file.DeleteSheet("汇总"); err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if err := file.Write(&buf); err != nil {
			t.Fatal(err)
		}
		_ = file.Close()
		r := f.upload(f.Owner, buf.Bytes(), "missing.xlsx")
		if r.Status != http.StatusUnprocessableEntity || !strings.Contains(r.Body["detail"].(string), "缺少工作表") {
			t.Fatal(r)
		}
	})
	t.Run("wrong header", func(t *testing.T) {
		data := workbook(t, [][]string{{"错误", "姓名", "手机号(可空)", "性别(男/女/空)"}}, [][]string{{"来源卡号", "来源顾客ID", "卡种", "本金", "赠送金", "到期日(YYYY-MM-DD,可空)", "积分(整数,可空)"}}, [][]string{{"来源卡号", "来源顾客ID", "卡种", "项目", "剩余购买次数", "剩余赠送次数", "到期日(可空)"}}, [][]string{{"顾客数", "储值卡数", "本金合计", "赠送金合计", "次卡套餐卡数"}, {"0", "0", "0", "0", "0"}})
		r := f.upload(f.Owner, data, "header.xlsx")
		if r.Status != http.StatusUnprocessableEntity || !strings.Contains(r.Body["detail"].(string), "表头不正确") {
			t.Fatal(r)
		}
	})
	t.Run("not xlsx", func(t *testing.T) {
		r := f.upload(f.Owner, []byte("not excel"), "bad.xlsx")
		if r.Status != http.StatusUnprocessableEntity {
			t.Fatal(r)
		}
	})
	t.Run("oversize", func(t *testing.T) {
		r := f.upload(f.Owner, make([]byte, dataio.MaxUploadBytes+1), "big.xlsx")
		if r.Status != http.StatusUnprocessableEntity || !strings.Contains(r.Body["detail"].(string), "大小上限") {
			t.Fatal(r)
		}
	})
}

func TestMaxRowsPerSheet(t *testing.T) {
	f := open(t)
	customers := make([][]string, dataio.MaxRowsPerSheet+2)
	customers[0] = []string{"来源顾客ID", "姓名", "手机号(可空)", "性别(男/女/空)"}
	for i := 1; i < len(customers); i++ {
		customers[i] = []string{fmt.Sprintf("C%d", i), "", "", ""}
	}
	data := workbook(t, customers, [][]string{{"来源卡号", "来源顾客ID", "卡种", "本金", "赠送金", "到期日(YYYY-MM-DD,可空)", "积分(整数,可空)"}}, [][]string{{"来源卡号", "来源顾客ID", "卡种", "项目", "剩余购买次数", "剩余赠送次数", "到期日(可空)"}}, [][]string{{"顾客数", "储值卡数", "本金合计", "赠送金合计", "次卡套餐卡数"}, {"0", "0", "0", "0", "0"}})
	r := f.upload(f.Owner, data, "rows.xlsx")
	if r.Status != http.StatusUnprocessableEntity || !strings.Contains(r.Body["detail"].(string), "行上限") {
		t.Fatal(r)
	}
}
