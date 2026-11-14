package reports_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/money"
)

func TestCardReportSalesConsumptionRefundAndImport(t *testing.T) {
	f := open(t)
	customer := f.customer("顾客甲", "13800000031", nil)
	f.now("2026-11-02", 12)
	card := f.openCard(customer, f.stored, "200.00", "20.00", payment("cash", "200"))
	f.call("recharge_card", map[string]any{"card_id": card["id"], "principal": "100.00", "bonus": "10.00", "sales_staff_ids": []any{f.Res.OwnerID}, "payments": []any{payment("cash", "100")}}, 200)
	f.checkout(f.order(customer, []uuid.UUID{f.staffA}, true, "female", f.item), f.cardPayment(id(card["id"]), "50"), payment("cash", "50"))
	bonusCard := f.openCard(customer, f.stored, "0", "100.00")
	f.checkout(f.order(customer, []uuid.UUID{f.staffB}, false, "male", f.item), f.cardPayment(id(bonusCard["id"]), "100"))
	refunded := f.openCard(customer, f.stored, "100.00", "0", payment("cash", "100"))
	quote := f.call("preview_card_refund", map[string]any{"card_id": refunded["id"]}, 200)
	f.call("refund_card", map[string]any{"card_id": refunded["id"], "method": "cash", "amount": quote["amount"]}, 200)

	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	var imported uuid.UUID
	if err := tx.QueryRow(context.Background(), `INSERT INTO cards (tenant_id,store_id,customer_id,card_type_id,opened_at,created_by) VALUES ($1,$2,$3,$4,clock_timestamp(),$5) RETURNING id`, f.Res.TenantID, f.Res.StoreID, customer, f.stored, f.Res.OwnerID).Scan(&imported); err != nil {
		t.Fatal(err)
	}
	dbtest.Exec(t, tx, `INSERT INTO card_ledger (tenant_id,store_id,card_id,kind,principal_before,principal_delta,principal_after,bonus_before,bonus_delta,bonus_after,business_date,actor_id) VALUES ($1,$2,$3,'import',0,500,500,0,0,0,'2026-11-02',$4)`, f.Res.TenantID, f.Res.StoreID, imported, f.Res.OwnerID)
	var future uuid.UUID
	if err := tx.QueryRow(context.Background(), `INSERT INTO cards (tenant_id,store_id,customer_id,card_type_id,opened_at,created_by) VALUES ($1,$2,$3,$4,clock_timestamp(),$5) RETURNING id`, f.Res.TenantID, f.Res.StoreID, customer, f.stored, f.Res.OwnerID).Scan(&future); err != nil {
		t.Fatal(err)
	}
	dbtest.Exec(t, tx, `INSERT INTO card_ledger (tenant_id,store_id,card_id,kind,principal_before,principal_delta,principal_after,bonus_before,bonus_delta,bonus_after,business_date,actor_id) VALUES ($1,$2,$3,'import',0,999,999,0,0,0,'2026-11-04',$4)`, f.Res.TenantID, f.Res.StoreID, future, f.Res.OwnerID)
	dbtest.Commit(t, tx)

	report := f.call("get_card_report", map[string]any{"from": "2026-11-02", "to": "2026-11-02"}, 200)
	rows := report["rows"].([]any)
	if len(rows) != 1 {
		t.Fatal(rows)
	}
	row := rows[0].(map[string]any)
	if row["card_type"].(map[string]any)["name"] != "储值卡" || row["open_count"].(float64) != 3 || row["open_principal"].(string) != "300.00" || row["open_bonus"].(string) != "120.00" {
		t.Fatal(row)
	}
	if row["recharge_principal"].(string) != "100.00" || row["recharge_bonus"].(string) != "10.00" || row["refund_count"].(float64) != 1 || row["refund_amount"].(string) != "100.00" {
		t.Fatal(row)
	}
	if row["ending_balance"].(string) != "780.00" || row["ending_counts"].(float64) != 0 {
		t.Fatal(row)
	}
	if row["consumed_principal"].(string) != "50.00" || row["consumed_bonus"].(string) != "100.00" {
		t.Fatal(row)
	}
	business := f.call("get_business_report", map[string]any{"from": "2026-11-02", "to": "2026-11-02", "granularity": "day"}, 200)
	businessRow := business["rows"].([]any)[0].(map[string]any)
	if row["consumed_principal"] != businessRow["card_principal"] || row["consumed_bonus"] != businessRow["card_bonus"] {
		t.Fatalf("consumption %v/%v, business %v/%v", row["consumed_principal"], row["consumed_bonus"], businessRow["card_principal"], businessRow["card_bonus"])
	}
	assertCardTotals(t, rows, report["totals"].(map[string]any))
}

func assertCardTotals(t *testing.T, rows []any, totals map[string]any) {
	t.Helper()
	for _, field := range []string{"open_principal", "open_bonus", "recharge_principal", "recharge_bonus", "upgrade", "consumed_principal", "consumed_bonus", "count_amount", "refund_amount", "ending_balance"} {
		sum := money.SignedTotal{}
		for _, raw := range rows {
			sum = sum.Add(changeOf(t, raw.(map[string]any)[field].(string)))
		}
		if sum.Format() != totals[field] {
			t.Fatalf("%s total = %v, want %v", field, totals[field], sum.Format())
		}
	}
	for _, field := range []string{"open_count", "count_used", "refund_count", "ending_counts"} {
		var sum float64
		for _, raw := range rows {
			sum += raw.(map[string]any)[field].(float64)
		}
		if totals[field].(float64) != sum {
			t.Fatalf("%s total = %v, want %v", field, totals[field], sum)
		}
	}
}

func TestCardReportAttributesUpgradesByTime(t *testing.T) {
	f := open(t)
	customer := f.customer("顾客甲", "13800000032", nil)
	typeA := f.stored
	typeB := id(f.call("create_card_type", map[string]any{"name": "高级卡", "kind": "stored_value", "validity_kind": "none"}, 200)["id"])
	f.now("2026-11-02", 12)
	card := f.openCard(customer, typeA, "100.00", "0", payment("cash", "100"))
	f.now("2026-11-03", 12)
	f.call("upgrade_card", map[string]any{"card_id": card["id"], "to_type_id": typeB, "principal": "10.00", "bonus": "0", "sales_staff_ids": []any{f.Res.OwnerID}, "payments": []any{payment("cash", "10.00")}}, 200)
	f.now("2026-11-04", 12)
	f.call("recharge_card", map[string]any{"card_id": card["id"], "principal": "50.00", "bonus": "0", "sales_staff_ids": []any{f.Res.OwnerID}, "payments": []any{payment("cash", "50.00")}}, 200)

	before := f.call("get_card_report", map[string]any{"from": "2026-11-02", "to": "2026-11-02"}, 200)
	rowA := cardRowByName(before["rows"].([]any), "储值卡")
	if rowA == nil || rowA["open_count"].(float64) != 1 || rowA["open_principal"].(string) != "100.00" || rowA["ending_balance"].(string) != "100.00" {
		t.Fatalf("before upgrade = %#v", before["rows"])
	}
	after := f.call("get_card_report", map[string]any{"from": "2026-11-04", "to": "2026-11-04"}, 200)
	if cardRowByName(after["rows"].([]any), "储值卡") != nil {
		t.Fatalf("old type leaked after upgrade: %#v", after["rows"])
	}
	rowB := cardRowByName(after["rows"].([]any), "高级卡")
	if rowB == nil || rowB["recharge_principal"].(string) != "50.00" || rowB["ending_balance"].(string) != "160.00" {
		t.Fatalf("after upgrade = %#v", after["rows"])
	}
	all := f.call("get_card_report", map[string]any{"from": "2026-11-02", "to": "2026-11-04"}, 200)
	totals := all["totals"].(map[string]any)
	if totals["open_principal"].(string) != "100.00" || totals["recharge_principal"].(string) != "50.00" || totals["upgrade"].(string) != "10.00" || totals["ending_balance"].(string) != "160.00" {
		t.Fatalf("totals = %#v", totals)
	}
	assertCardTotals(t, all["rows"].([]any), totals)
}

func cardRowByName(rows []any, name string) map[string]any {
	for _, raw := range rows {
		row := raw.(map[string]any)
		if row["card_type"].(map[string]any)["name"] == name {
			return row
		}
	}
	return nil
}
