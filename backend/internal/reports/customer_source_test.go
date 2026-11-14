package reports_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/money"
)

func TestCustomerSourceReport(t *testing.T) {
	f := open(t)
	source := id(f.call("create_customer_source", map[string]any{"name": "渠道X"}, 200)["id"])
	first := f.customer("新客甲", "13800000041", &source)
	second := f.customer("新客乙", "13800000042", nil)
	unknown := f.customer("未识别", "", nil)
	imported := f.customer("导入客", "13800000043", &source)
	f.markImported(imported)

	f.now("2026-11-01", 12)
	f.checkout(f.order(first, []uuid.UUID{f.staffA}, true, "female", f.item), payment("cash", "100"))
	f.now("2026-11-02", 12)
	f.checkout(f.order(first, []uuid.UUID{f.staffA}, false, "male", f.item), payment("cash", "100"))
	f.checkout(f.order(second, []uuid.UUID{f.staffB}, true, "female", f.item), payment("cash", "100"))
	f.checkout(f.order(unknown, []uuid.UUID{f.staffB}, false, "female", f.item), payment("cash", "100"))
	f.checkout(f.order(imported, []uuid.UUID{f.staffB}, true, "female", f.item), payment("cash", "100"))

	report := f.call("get_customer_source_report", map[string]any{"from": "2026-11-01", "to": "2026-11-02"}, 200)
	rows := report["rows"].([]any)
	bySource := sourceRows(rows)
	if bySource["渠道X"]["new_customers"].(float64) != 1 || bySource["渠道X"]["returning"].(float64) != 1 || bySource["渠道X"]["revenue"].(string) != "200.00" {
		t.Fatal(bySource["渠道X"])
	}
	if bySource["未填来源"]["new_customers"].(float64) != 1 || bySource["未填来源"]["returning"].(float64) != 0 || bySource["未填来源"]["revenue"].(string) != "100.00" {
		t.Fatal(bySource["未填来源"])
	}
	if report["unidentified_orders"].(float64) != 1 {
		t.Fatalf("unidentified = %v", report["unidentified_orders"])
	}
	assertSourceTotals(t, rows, report["totals"].(map[string]any))
}

func TestCustomerSourceRowsOrderAndStoreFilter(t *testing.T) {
	f := open(t)
	f.now("2026-11-02", 12)
	f.call("create_customer_source", map[string]any{"name": "A空来源"}, 200)
	inactiveData := id(f.call("create_customer_source", map[string]any{"name": "B停用有数据"}, 200)["id"])
	inactiveEmpty := id(f.call("create_customer_source", map[string]any{"name": "C停用无数据"}, 200)["id"])
	customer := f.customer("停用来源客", "13800000044", &inactiveData)
	f.checkout(f.order(customer, []uuid.UUID{f.staffA}, true, "female", f.item), payment("cash", "100"))
	f.call("update_customer_source", map[string]any{"id": inactiveData, "name": "B停用有数据", "active": false}, 200)
	f.call("update_customer_source", map[string]any{"id": inactiveEmpty, "name": "C停用无数据", "active": false}, 200)

	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	var otherStore, foreignSource uuid.UUID
	if err := tx.QueryRow(context.Background(), `INSERT INTO stores (tenant_id,name) VALUES ($1,'二店') RETURNING id`, f.Res.TenantID).Scan(&otherStore); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(context.Background(), `INSERT INTO customer_sources (tenant_id,store_id,name) VALUES ($1,$2,'外店来源') RETURNING id`, f.Res.TenantID, otherStore).Scan(&foreignSource); err != nil {
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)
	foreignCustomer := f.customer("外店来源客", "13800000045", nil)
	tx = dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	dbtest.Exec(t, tx, `UPDATE customers SET source_id=$1 WHERE id=$2`, foreignSource, foreignCustomer)
	dbtest.Commit(t, tx)
	f.checkout(f.order(foreignCustomer, []uuid.UUID{f.staffB}, false, "male", f.item), payment("cash", "100"))

	report := f.call("get_customer_source_report", map[string]any{"from": "2026-11-02", "to": "2026-11-02"}, 200)
	rows := report["rows"].([]any)
	bySource := sourceRows(rows)
	if bySource["A空来源"] == nil || bySource["A空来源"]["new_customers"].(float64) != 0 || bySource["A空来源"]["revenue"].(string) != "0.00" {
		t.Fatalf("active empty source = %#v", bySource["A空来源"])
	}
	if bySource["B停用有数据"] == nil || bySource["B停用有数据"]["new_customers"].(float64) != 1 {
		t.Fatalf("inactive source with data = %#v", bySource["B停用有数据"])
	}
	if bySource["C停用无数据"] != nil {
		t.Fatalf("inactive empty source appeared: %#v", bySource["C停用无数据"])
	}
	if bySource["未填来源"] == nil || bySource["未填来源"]["new_customers"].(float64) != 1 {
		t.Fatalf("未填来源 should include the foreign-source customer: %#v", bySource["未填来源"])
	}
	if bySource["外店来源"] != nil {
		t.Fatal("foreign store source leaked")
	}
	names := []string{}
	for _, raw := range rows {
		row := raw.(map[string]any)
		if row["source_name"] == nil {
			names = append(names, "未填来源")
			continue
		}
		names = append(names, row["source_name"].(string))
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] == "未填来源" || (names[i] != "未填来源" && names[i-1] > names[i]) {
			t.Fatalf("order = %v", names)
		}
	}
	if names[len(names)-1] != "未填来源" {
		t.Fatalf("未填来源 is not last: %v", names)
	}
	assertSourceTotals(t, rows, report["totals"].(map[string]any))
}

func TestCustomerSourceMergesAndImport(t *testing.T) {
	f := open(t)
	chainSource := id(f.call("create_customer_source", map[string]any{"name": "合并导入来源"}, 200)["id"])
	imported := f.customer("导入客", "13800000046", nil)
	middle := f.customer("中间客", "13800000047", nil)
	survivor := f.customer("保留客", "13800000048", &chainSource)
	f.markImported(imported)
	f.merge(middle, imported)
	f.merge(survivor, middle)
	f.now("2026-11-02", 12)
	f.checkout(f.order(survivor, []uuid.UUID{f.staffA}, true, "female", f.item), payment("cash", "100"))
	report := f.call("get_customer_source_report", map[string]any{"from": "2026-11-02", "to": "2026-11-02"}, 200)
	if row := sourceRows(report["rows"].([]any))["合并导入来源"]; row == nil || row["new_customers"].(float64) != 0 {
		t.Fatalf("transitive import leak: %#v", row)
	}

	historySource := id(f.call("create_customer_source", map[string]any{"name": "合并历史来源"}, 200)["id"])
	history := f.customer("历史客", "13800000049", &historySource)
	kept := f.customer("保留历史客", "13800000050", &historySource)
	f.now("2026-11-01", 12)
	historyPaid := f.checkout(f.order(history, []uuid.UUID{f.staffA}, true, "female", f.item), payment("cash", "100"))
	f.now("2026-11-02", 12)
	f.checkout(f.order(kept, []uuid.UUID{f.staffB}, false, "male", f.item), payment("cash", "100"))
	f.merge(kept, history)
	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	dbtest.Exec(t, tx, `UPDATE orders SET customer_id=$1 WHERE id=$2`, history, id(historyPaid["id"]))
	dbtest.Commit(t, tx)
	report = f.call("get_customer_source_report", map[string]any{"from": "2026-11-02", "to": "2026-11-02"}, 200)
	if row := sourceRows(report["rows"].([]any))["合并历史来源"]; row == nil || row["new_customers"].(float64) != 0 {
		t.Fatalf("merged first visit not used: %#v", row)
	}
	report = f.call("get_customer_source_report", map[string]any{"from": "2026-11-01", "to": "2026-11-02"}, 200)
	if row := sourceRows(report["rows"].([]any))["合并历史来源"]; row == nil || row["new_customers"].(float64) != 1 {
		t.Fatalf("merged-away customer counted: %#v", row)
	}
}

func TestCustomerSourceRevenueNetsAftersaleRefund(t *testing.T) {
	f := open(t)
	source := id(f.call("create_customer_source", map[string]any{"name": "退款来源"}, 200)["id"])
	customer := f.customer("新客", "13800000051", &source)
	f.now("2026-11-02", 12)
	paid := f.checkout(f.order(customer, []uuid.UUID{f.staffA}, true, "female", f.item), payment("cash", "100"))
	aftersale := f.call("create_aftersale", map[string]any{"order_id": paid["id"], "type": "partial_refund", "reason": "退款", "refund_lines": []any{payment("cash", "30.00")}}, 200)
	f.call("approve_aftersale", map[string]any{"id": aftersale["id"]}, 200)
	report := f.call("get_customer_source_report", map[string]any{"from": "2026-11-02", "to": "2026-11-02"}, 200)
	row := sourceRows(report["rows"].([]any))["退款来源"]
	if row == nil || row["new_customers"].(float64) != 1 || row["revenue"].(string) != "70.00" {
		t.Fatal(row)
	}
}

func sourceRows(rows []any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, raw := range rows {
		row := raw.(map[string]any)
		name := "未填来源"
		if row["source_name"] != nil {
			name = row["source_name"].(string)
		}
		out[name] = row
	}
	return out
}

func assertSourceTotals(t *testing.T, rows []any, totals map[string]any) {
	t.Helper()
	var newCustomers, returning float64
	revenue := money.SignedTotal{}
	for _, raw := range rows {
		row := raw.(map[string]any)
		newCustomers += row["new_customers"].(float64)
		returning += row["returning"].(float64)
		revenue = revenue.Add(changeOf(t, row["revenue"].(string)))
	}
	if totals["new_customers"].(float64) != newCustomers || totals["returning"].(float64) != returning || totals["revenue"] != revenue.Format() {
		t.Fatalf("totals = %v, want %v/%v/%v", totals, newCustomers, returning, revenue.Format())
	}
}
