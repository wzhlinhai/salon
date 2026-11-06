package dataio_test

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/dataio"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func rewriteZip(t *testing.T, data []byte, replace map[string][]byte, extra string, extraSize int64) []byte {
	t.Helper()
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, file := range r.File {
		header := &zip.FileHeader{Name: file.Name, Method: file.Method}
		w, err := zw.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		content, ok := replace[file.Name]
		if ok {
			if _, err := w.Write(content); err != nil {
				t.Fatal(err)
			}
			continue
		}
		rc, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(w, rc); err != nil {
			t.Fatal(err)
		}
		if err := rc.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if extra != "" {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: extra, Method: zip.Deflate})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.CopyN(w, zeroReader{}, extraSize); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestWorkbookReadErrorsBecomeFileErrors(t *testing.T) {
	f := open(t)
	t.Run("corrupt xml", func(t *testing.T) {
		data := workbook(t, [][]string{{"来源顾客ID", "姓名", "手机号(可空)", "性别(男/女/空)"}},
			[][]string{{"来源卡号", "来源顾客ID", "卡种", "本金", "赠送金", "到期日(YYYY-MM-DD,可空)", "积分(整数,可空)"}},
			[][]string{{"来源卡号", "来源顾客ID", "卡种", "项目", "剩余购买次数", "剩余赠送次数", "到期日(可空)"}},
			[][]string{{"顾客数", "储值卡数", "本金合计", "赠送金合计", "次卡套餐卡数"}, {"0", "0", "0", "0", "0"}})
		broken := rewriteZip(t, data, map[string][]byte{"xl/sharedStrings.xml": []byte("<sst><si>")}, "", 0)
		r := f.upload(f.Owner, broken, "broken.xlsx")
		if r.Status != http.StatusUnprocessableEntity || !strings.Contains(r.Body["detail"].(string), "无法读取") {
			t.Fatalf("corrupt xml = %d %v", r.Status, r.Body)
		}
	})
	t.Run("unzip bomb", func(t *testing.T) {
		data := workbook(t, [][]string{{"来源顾客ID", "姓名", "手机号(可空)", "性别(男/女/空)"}},
			[][]string{{"来源卡号", "来源顾客ID", "卡种", "本金", "赠送金", "到期日(YYYY-MM-DD,可空)", "积分(整数,可空)"}},
			[][]string{{"来源卡号", "来源顾客ID", "卡种", "项目", "剩余购买次数", "剩余赠送次数", "到期日(可空)"}},
			[][]string{{"顾客数", "储值卡数", "本金合计", "赠送金合计", "次卡套餐卡数"}, {"0", "0", "0", "0", "0"}})
		bomb := rewriteZip(t, data, nil, "xl/media/bomb.bin", dataio.MaxUnzipBytes+1)
		r := f.upload(f.Owner, bomb, "bomb.xlsx")
		if r.Status != http.StatusUnprocessableEntity || !strings.Contains(r.Body["detail"].(string), "无法读取 Excel") {
			t.Fatalf("unzip bomb = %d %v", r.Status, r.Body)
		}
	})
}

func TestSummaryRowLimit(t *testing.T) {
	f := open(t)
	summary := make([][]string, dataio.MaxSummaryRows+2)
	summary[0] = []string{"顾客数", "储值卡数", "本金合计", "赠送金合计", "次卡套餐卡数"}
	for i := 1; i < len(summary); i++ {
		summary[i] = []string{"0", "0", "0", "0", "0"}
	}
	data := workbook(t, [][]string{{"来源顾客ID", "姓名", "手机号(可空)", "性别(男/女/空)"}},
		[][]string{{"来源卡号", "来源顾客ID", "卡种", "本金", "赠送金", "到期日(YYYY-MM-DD,可空)", "积分(整数,可空)"}},
		[][]string{{"来源卡号", "来源顾客ID", "卡种", "项目", "剩余购买次数", "剩余赠送次数", "到期日(可空)"}},
		summary)
	r := f.upload(f.Owner, data, "summary.xlsx")
	if r.Status != http.StatusUnprocessableEntity || !strings.Contains(r.Body["detail"].(string), "行上限") {
		t.Fatalf("summary rows = %d %v", r.Status, r.Body)
	}
}

func customerOnlyWorkbook(t *testing.T, rows [][]string) []byte {
	t.Helper()
	return workbook(t,
		append([][]string{{"来源顾客ID", "姓名", "手机号(可空)", "性别(男/女/空)"}}, rows...),
		[][]string{{"来源卡号", "来源顾客ID", "卡种", "本金", "赠送金", "到期日(YYYY-MM-DD,可空)", "积分(整数,可空)"}},
		[][]string{{"来源卡号", "来源顾客ID", "卡种", "项目", "剩余购买次数", "剩余赠送次数", "到期日(可空)"}},
		[][]string{{"顾客数", "储值卡数", "本金合计", "赠送金合计", "次卡套餐卡数"}, {fmt.Sprint(len(rows)), "0", "0", "0", "0"}})
}

func TestPhoneDedupeIsTenantWide(t *testing.T) {
	f := open(t)
	ctx := context.Background()
	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	var storeID, customerID uuid.UUID
	if err := tx.QueryRow(ctx, "INSERT INTO stores (tenant_id, name) VALUES ($1, '同租户二店') RETURNING id", f.Res.TenantID).Scan(&storeID); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, "INSERT INTO customers (tenant_id, store_id, name, phone, created_by) VALUES ($1, $2, '二店顾客', '13700135555', $3) RETURNING id", f.Res.TenantID, storeID, f.Res.OwnerID).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	r := f.upload(f.Owner, customerOnlyWorkbook(t, [][]string{{"C1", "同号", "13700135555", ""}}), "tenant.xlsx")
	if r.Status != http.StatusOK {
		t.Fatal(r)
	}
	rows := rowsBySheet(t, r.Body, "顾客")
	if rows[0]["action"] != "linked" || rows[0]["entity_id"] != customerID.String() {
		t.Fatalf("cross-store row = %v", rows[0])
	}
	f.commit(r.Body["id"].(string))
	if n := f.count("SELECT count(*) FROM customers WHERE phone='13700135555'"); n != 1 {
		t.Fatalf("same-tenant phone rows = %d", n)
	}
}

func TestPhoneDedupeRespectsCustomerView(t *testing.T) {
	f := open(t)
	otherID, _ := f.Hire("其他员工", "002", "other")
	f.call("assign_customer_responsible", map[string]any{"customer_ids": []any{f.nativeCustomer}, "staff_id": otherID})
	restricted := f.MustCaller(f.Owner)
	restricted.Permissions = map[ops.Permission]ops.Grant{perm.DataImport: {}, perm.CustomerView: {Scope: ops.ScopeSelf}}

	detail, err := dataio.Preview(context.Background(), f.Pool, restricted, time.Now(), "scope.xlsx", bytes.NewReader(customerOnlyWorkbook(t, [][]string{{"C1", "看不见", "13800131234", ""}})))
	if err != nil {
		t.Fatal(err)
	}
	if got := rowError(t, detailRows(detail), "C1"); got != "手机号已属于其他顾客，无权导入" {
		t.Fatalf("preview row error = %q", got)
	}

	batch, _ := f.previewDefault()
	f.call("assign_customer_responsible", map[string]any{"customer_ids": []any{f.nativeCustomer}, "staff_id": otherID})
	r := f.HTTP(t, &restricted, "commit_import", map[string]any{"batch_id": batch})
	if r.Status != http.StatusOK {
		t.Fatalf("commit = %d %v", r.Status, r.Body)
	}
	if got := rowError(t, rowsBySheet(t, r.Body, "顾客"), "C1"); got != "手机号已属于其他顾客，无权导入" {
		t.Fatalf("commit row error = %q", got)
	}
}

func detailRows(detail *dataio.ImportBatchDetail) []map[string]any {
	out := make([]map[string]any, 0, len(detail.Rows))
	for _, row := range detail.Rows {
		item := map[string]any{"source_key": row.SourceKey}
		if row.Error != nil {
			item["error"] = *row.Error
		}
		out = append(out, item)
	}
	return out
}

func TestCommitRejectsMergedCustomerCard(t *testing.T) {
	f := open(t)
	keep := f.call("create_customer", map[string]any{"name": "保留顾客", "phone": "13900130001"})["customer"].(map[string]any)["id"].(string)
	data := workbook(t,
		[][]string{{"来源顾客ID", "姓名", "手机号(可空)", "性别(男/女/空)"}, {"C1", "待合并", "13800131234", ""}},
		[][]string{{"来源卡号", "来源顾客ID", "卡种", "本金", "赠送金", "到期日(YYYY-MM-DD,可空)", "积分(整数,可空)"}, {"K1", "C1", "储值卡A", "10", "0", "", ""}},
		[][]string{{"来源卡号", "来源顾客ID", "卡种", "项目", "剩余购买次数", "剩余赠送次数", "到期日(可空)"}},
		[][]string{{"顾客数", "储值卡数", "本金合计", "赠送金合计", "次卡套餐卡数"}, {"1", "1", "10", "0", "0"}})
	r := f.upload(f.Owner, data, "merged.xlsx")
	if r.Status != http.StatusOK {
		t.Fatal(r)
	}
	cardsBefore := f.count("SELECT count(*) FROM cards")
	f.call("merge_customers", map[string]any{"keep_id": keep, "merged_id": f.nativeCustomer})
	out := f.commit(r.Body["id"].(string))
	if got := rowError(t, rowsBySheet(t, out, "顾客"), "C1"); got != "该顾客已合并到另一位顾客" {
		t.Fatalf("customer row = %q", got)
	}
	if got := rowError(t, rowsBySheet(t, out, "储值卡"), "K1"); got != "来源顾客不存在或未导入" {
		t.Fatalf("card row = %q", got)
	}
	if got := f.count("SELECT count(*) FROM cards"); got != cardsBefore {
		t.Fatalf("merged import created %d cards", got-cardsBefore)
	}
}

func TestCountPackageGroupErrorsAreConsistent(t *testing.T) {
	f := open(t)
	secondItem := f.call("create_item", map[string]any{
		"name": "烫发", "price": "80.00", "discountable": true,
		"labor_fee_amount": nil, "labor_fee_percent": nil, "positions": []any{}, "level_prices": []any{},
	})["id"].(string)
	cardType := f.call("create_card_type", map[string]any{
		"name": "两项次卡", "kind": "count", "validity_kind": "none",
		"items": []any{
			map[string]any{"item_id": f.item, "paid_count": 10, "gift_count": 0, "unit_price": "50"},
			map[string]any{"item_id": secondItem, "paid_count": 10, "gift_count": 0, "unit_price": "40"},
		},
	})["id"].(string)

	t.Run("preview marks every item in the group", func(t *testing.T) {
		data := workbook(t,
			[][]string{{"来源顾客ID", "姓名", "手机号(可空)", "性别(男/女/空)"}, {"C1", "顾客", "13700136666", ""}},
			[][]string{{"来源卡号", "来源顾客ID", "卡种", "本金", "赠送金", "到期日(YYYY-MM-DD,可空)", "积分(整数,可空)"}},
			[][]string{{"来源卡号", "来源顾客ID", "卡种", "项目", "剩余购买次数", "剩余赠送次数", "到期日(可空)"},
				{"K1", "C1", "两项次卡", "剪发", "1", "0", ""},
				{"K1", "C1", "两项次卡", "烫发", "bad", "0", ""}},
			[][]string{{"顾客数", "储值卡数", "本金合计", "赠送金合计", "次卡套餐卡数"}, {"1", "0", "0", "0", "1"}})
		r := f.upload(f.Owner, data, "group.xlsx")
		if r.Status != http.StatusOK {
			t.Fatal(r)
		}
		counts := rowsBySheet(t, r.Body, "次卡套餐")
		for _, row := range counts {
			if row["error"] != "同一来源卡号的其他项目有错误" {
				t.Fatalf("group row = %v", row)
			}
		}
	})

	t.Run("commit marks every item after secondary validation fails", func(t *testing.T) {
		data := workbook(t,
			[][]string{{"来源顾客ID", "姓名", "手机号(可空)", "性别(男/女/空)"}, {"C1", "顾客", "13700137777", ""}},
			[][]string{{"来源卡号", "来源顾客ID", "卡种", "本金", "赠送金", "到期日(YYYY-MM-DD,可空)", "积分(整数,可空)"}},
			[][]string{{"来源卡号", "来源顾客ID", "卡种", "项目", "剩余购买次数", "剩余赠送次数", "到期日(可空)"},
				{"K1", "C1", "两项次卡", "剪发", "1", "0", ""},
				{"K1", "C1", "两项次卡", "烫发", "1", "0", ""}},
			[][]string{{"顾客数", "储值卡数", "本金合计", "赠送金合计", "次卡套餐卡数"}, {"1", "0", "0", "0", "1"}})
		r := f.upload(f.Owner, data, "secondary.xlsx")
		if r.Status != http.StatusOK {
			t.Fatal(r)
		}
		f.call("update_card_type", map[string]any{
			"id": cardType, "name": "两项次卡", "kind": "count", "validity_kind": "none", "active": true,
			"items": []any{map[string]any{"item_id": f.item, "paid_count": 10, "gift_count": 0, "unit_price": "50"}},
		})
		out := f.commit(r.Body["id"].(string))
		for _, row := range rowsBySheet(t, out, "次卡套餐") {
			if row["error"] != "同一来源卡号的其他项目有错误" {
				t.Fatalf("secondary group row = %v", row)
			}
			if row["action"] != nil || row["entity_id"] != nil {
				t.Fatalf("secondary group row has action/entity = %v", row)
			}
		}
		if n := f.count("SELECT count(*) FROM cards WHERE card_type_id=$1", cardType); n != 0 {
			t.Fatalf("secondary validation created %d cards", n)
		}
	})
}

func TestAuditContainsOnlyBatchSummary(t *testing.T) {
	f := open(t)
	batch, _ := f.previewDefault()
	f.commit(batch)
	f.call("undo_import", map[string]any{"batch_id": batch})
	for _, action := range []string{"preview_import", "commit_import", "undo_import"} {
		var after, before string
		var size int
		if err := f.Pool.QueryRow(context.Background(), `SELECT COALESCE(after::text,''), COALESCE(before::text,''),
			COALESCE(octet_length(after::text),0)+COALESCE(octet_length(before::text),0)
			FROM audit_log WHERE target_kind='import_batch' AND action=$1`, action).Scan(&after, &before, &size); err != nil {
			t.Fatal(err)
		}
		for _, text := range []string{after, before} {
			if strings.Contains(text, `"rows"`) || strings.Contains(text, "source_key") || strings.Contains(text, `"K1"`) || strings.Contains(text, `"C1"`) {
				t.Fatalf("%s audit contains row details: %s", action, text)
			}
		}
		if !strings.Contains(after, `"status"`) || !strings.Contains(after, `"differences"`) {
			t.Fatalf("%s audit lacks summary: %s", action, after)
		}
		if size > 4096 {
			t.Fatalf("%s audit size = %d", action, size)
		}
	}
}

func TestUndoRejectsNullStore(t *testing.T) {
	f := open(t)
	batch, _ := f.previewDefault()
	f.commit(batch)
	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	var refs []string
	err := tx.QueryRow(context.Background(), "SELECT undo_import_batch($1::uuid, NULL::uuid)", uuid.MustParse(batch)).Scan(&refs)
	if err == nil || !strings.Contains(err.Error(), "导入批次不存在") {
		t.Fatalf("null store undo = %v", err)
	}
}
