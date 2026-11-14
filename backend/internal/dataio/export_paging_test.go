package dataio_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/dataio"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
)

func exportSheets(t *testing.T, f *fixture, pageSize int32) map[string][][]string {
	t.Helper()
	restore := dataio.SetExportPageSizeForTest(pageSize)
	defer restore()
	file := exportFile(t, f, f.Owner)
	out := map[string][][]string{}
	for _, sheet := range file.GetSheetList() {
		rows, err := file.GetRows(sheet)
		if err != nil {
			t.Fatal(err)
		}
		out[sheet] = rows
	}
	return out
}

func maxRowsByQuery(t *testing.T, pageSize int32, run func()) map[string]int {
	t.Helper()
	max := map[string]int{}
	restore := dataio.SetExportPageObserverForTest(func(name string, rows int) {
		if rows > max[name] {
			max[name] = rows
		}
	})
	defer restore()
	run()
	for name, rows := range max {
		if rows > int(pageSize) {
			t.Fatalf("%s returned %d rows in one query, page size %d", name, rows, pageSize)
		}
	}
	return max
}

func TestExportPagingKeepsContentAndBoundsQueries(t *testing.T) {
	f := open(t)
	batch, _ := f.previewDefault()
	f.commit(batch)
	card := f.call("open_card", map[string]any{"customer_id": f.nativeCustomer, "card_type_id": f.storedType, "principal": "5.00", "bonus": "0", "sales_staff_ids": []any{f.Res.OwnerID}, "payments": []any{map[string]any{"method": "cash", "amount": "5.00"}}})

	addOrders := func(n int) {
		for range n {
			order := f.call("open_order", map[string]any{"gender": "female", "designated": false, "items": []any{map[string]any{"item_id": f.item, "staff": []any{}}}})
			f.call("checkout_orders", map[string]any{"orders": []any{map[string]any{"order_id": order["id"], "payments": []any{map[string]any{"method": "cash", "amount": "100.00"}}}}})
		}
	}
	addOrders(3)
	insertExportTies(t, f, uuid.MustParse(card["id"].(string)))

	small := exportSheets(t, f, 1)
	large := exportSheets(t, f, 1000)
	if !reflect.DeepEqual(small, large) {
		t.Fatal("paged export differs from the single-page reference")
	}
	max := maxRowsByQuery(t, 1, func() {
		_ = exportSheets(t, f, 1)
	})
	for _, name := range []string{"customers", "card_ledger", "orders", "order_items", "order_payments"} {
		if max[name] == 0 {
			t.Fatalf("query %s was not exercised: %v", name, max)
		}
	}
	assertColumn(t, small["顾客"], 0, queryIDs(t, f, "SELECT id FROM customers WHERE store_id=$1 ORDER BY created_at, id"))
	assertColumn(t, small["卡流水"], 0, queryIDs(t, f, "SELECT id FROM card_ledger WHERE store_id=$1 ORDER BY at, id"))
	assertColumn(t, small["单据"], 0, dedupe(queryIDs(t, f, "SELECT id FROM orders WHERE store_id=$1 ORDER BY opened_at, id")))
	assertColumn(t, small["支付"], 0, queryIDs(t, f, "SELECT id FROM order_payments WHERE store_id=$1 ORDER BY id"))

	addOrders(3)
	small = exportSheets(t, f, 1)
	large = exportSheets(t, f, 1000)
	if !reflect.DeepEqual(small, large) {
		t.Fatal("paged export differs from the single-page reference after doubling rows")
	}
	maxRowsByQuery(t, 1, func() {
		_ = exportSheets(t, f, 1)
	})
	if got := len(small["单据"]); got != 9 {
		t.Fatalf("单据 rows = %d, want 9 (header + 8 orders)", got)
	}
}

func insertExportTies(t *testing.T, f *fixture, cardID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	for i, id := range []string{"00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000002", "00000000-0000-0000-0000-000000000003"} {
		if _, err := f.Pool.Exec(ctx, "INSERT INTO customers (id,tenant_id,store_id,name,created_by,created_at) VALUES ($1,$2,$3,$4,$5,'2025-12-31T12:00:00Z')", uuid.MustParse(id), f.Res.TenantID, f.Res.StoreID, "并列顾客"+string(rune('A'+i)), f.Res.OwnerID); err != nil {
			t.Fatal(err)
		}
	}
	for i, id := range []string{"00000000-0000-0000-0000-000000000011", "00000000-0000-0000-0000-000000000012"} {
		if _, err := f.Pool.Exec(ctx, "INSERT INTO card_ledger (id,tenant_id,store_id,card_id,kind,principal_before,principal_delta,principal_after,bonus_before,bonus_delta,bonus_after,business_date,at,actor_id) VALUES ($1,$2,$3,$4,'adjust',0,$5,$5,0,0,0,'2025-12-31','2025-12-31T12:00:00Z',$6)", uuid.MustParse(id), f.Res.TenantID, f.Res.StoreID, cardID, i+1, f.Res.OwnerID); err != nil {
			t.Fatal(err)
		}
	}
	var snapshot uuid.UUID
	if err := f.Pool.QueryRow(ctx, "SELECT settings_snapshot_id FROM orders WHERE store_id=$1 LIMIT 1", f.Res.StoreID).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"00000000-0000-0000-0000-000000000021", "00000000-0000-0000-0000-000000000022"} {
		if _, err := f.Pool.Exec(ctx, "INSERT INTO orders (tenant_id,store_id,id,customer_id,gender,designated,settings_snapshot_id,business_date,opened_by,opened_at) VALUES ($1,$2,$3,$4,'female',false,$5,current_date,$6,'2025-12-31T12:00:00Z')", f.Res.TenantID, f.Res.StoreID, uuid.MustParse(id), f.nativeCustomer, snapshot, f.Res.OwnerID); err != nil {
			t.Fatal(err)
		}
	}
}

func queryIDs(t *testing.T, f *fixture, query string) []string {
	t.Helper()
	rows, err := f.Pool.Query(context.Background(), query, f.Res.StoreID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, id.String())
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func assertColumn(t *testing.T, rows [][]string, col int, want []string) {
	t.Helper()
	got := []string{}
	for _, row := range rows[1:] {
		got = append(got, row[col])
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("column %d = %v, want %v", col, got, want)
	}
}

func dedupe(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if len(out) == 0 || out[len(out)-1] != value {
			out = append(out, value)
		}
	}
	return out
}

func TestExportKeysetIndexes(t *testing.T) {
	f := open(t)
	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	if _, err := tx.Exec(context.Background(), "SET LOCAL enable_seqscan = off"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), "SET LOCAL enable_sort = off"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ query, index string }{
		{"SELECT id FROM customers WHERE store_id=$1 ORDER BY created_at, id LIMIT 10", "customers_export"},
		{"SELECT id FROM cards WHERE store_id=$1 ORDER BY opened_at, id LIMIT 10", "cards_export"},
		{"SELECT id FROM card_ledger WHERE store_id=$1 ORDER BY at, id LIMIT 10", "card_ledger_export"},
		{"SELECT id FROM orders WHERE store_id=$1 ORDER BY opened_at, id LIMIT 10", "orders_export"},
		{"SELECT id FROM order_payments WHERE store_id=$1 ORDER BY id LIMIT 10", "order_payments_export"},
		{"SELECT card_id, item_id FROM card_items WHERE store_id=$1 ORDER BY card_id, item_id LIMIT 10", "card_items_export"},
	} {
		rows, err := tx.Query(context.Background(), "EXPLAIN "+tc.query, f.Res.StoreID)
		if err != nil {
			t.Fatal(tc.index, err)
		}
		lines := []string{}
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, line)
		}
		rows.Close()
		if !strings.Contains(strings.Join(lines, "\n"), tc.index) {
			t.Fatalf("%s plan does not use %s:\n%s", tc.query, tc.index, strings.Join(lines, "\n"))
		}
	}
	dbtest.Rollback(t, tx)
}
