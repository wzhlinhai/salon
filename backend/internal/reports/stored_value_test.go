package reports_test

import (
	"reflect"
	"regexp"
	"testing"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/reports"
)

// read 在 HTTP 和 AI 两条路径上调用读操作，要求结果相同并返回。
func (f *fixture) read(op string, args map[string]any) map[string]any {
	f.t.Helper()
	caller := f.MustCaller(f.Owner)
	var first map[string]any
	for _, ch := range f.Channels() {
		r := ch.Call(f.t, caller, op, args)
		if r.Status != 200 {
			f.t.Fatal(ch.Name, op, r)
		}
		if first == nil {
			first = r.Body
		} else if !reflect.DeepEqual(first, r.Body) {
			f.t.Fatalf("%s differs between channels: %v vs %v", op, first, r.Body)
		}
	}
	return first
}

// foreignStoreCard 在同租户的另一家门店里放一张有余额的卡。
func (f *fixture) foreignStoreCard(principal, day string) {
	f.t.Helper()
	tx := dbtest.AppTx(f.t, f.Pool, f.Res.TenantID)
	var store, customer, typ, card uuid.UUID
	if err := tx.QueryRow(f.t.Context(), `INSERT INTO stores (tenant_id,name) VALUES ($1,'外店'||gen_random_uuid()) RETURNING id`, f.Res.TenantID).Scan(&store); err != nil {
		f.t.Fatal(err)
	}
	if err := tx.QueryRow(f.t.Context(), `INSERT INTO customers (tenant_id,store_id,phone,created_by) VALUES ($1,$2,'138'||lpad((random()*99999999)::int::text,8,'0'),$3) RETURNING id`, f.Res.TenantID, store, f.Res.OwnerID).Scan(&customer); err != nil {
		f.t.Fatal(err)
	}
	if err := tx.QueryRow(f.t.Context(), `INSERT INTO card_types (tenant_id,store_id,name,kind,validity_kind) VALUES ($1,$2,'外店储值卡','stored_value','none') RETURNING id`, f.Res.TenantID, store).Scan(&typ); err != nil {
		f.t.Fatal(err)
	}
	if err := tx.QueryRow(f.t.Context(), `INSERT INTO cards (tenant_id,store_id,customer_id,card_type_id,opened_at,expires_on,created_by) VALUES ($1,$2,$3,$4,clock_timestamp(),'2026-11-03',$5) RETURNING id`, f.Res.TenantID, store, customer, typ, f.Res.OwnerID).Scan(&card); err != nil {
		f.t.Fatal(err)
	}
	dbtest.Exec(f.t, tx, `INSERT INTO card_ledger (tenant_id,store_id,card_id,kind,principal_before,principal_delta,principal_after,bonus_before,bonus_delta,bonus_after,business_date,actor_id) VALUES ($1,$2,$3,'open',0,$4,$4,0,0,0,$6,$5)`, f.Res.TenantID, store, card, principal, f.Res.OwnerID, day)
	dbtest.Commit(f.t, tx)
}

var storedValueColumns = []string{"added", "upgrade", "consumed", "refunded", "adjusted"}

func TestStoredValueReportDaysAndInvariants(t *testing.T) {
	f := open(t)
	customer := f.customer("顾客甲", "13800000081", nil)
	typeB := id(f.call("create_card_type", map[string]any{"name": "高级卡", "kind": "stored_value", "validity_kind": "none"}, 200)["id"])

	f.now("2026-11-01", 12)
	cardA := f.openCard(customer, f.stored, "200.00", "20.00", payment("cash", "200"))
	f.now("2026-11-02", 12)
	f.call("recharge_card", map[string]any{"card_id": cardA["id"], "principal": "100.00", "bonus": "10.00", "sales_staff_ids": []any{f.Res.OwnerID}, "payments": []any{payment("cash", "100")}}, 200)
	cardB := f.openCard(customer, f.stored, "100.00", "0", payment("cash", "100"))
	spent := f.checkout(f.order(customer, []uuid.UUID{f.staffA}, true, "female", f.item), f.cardPayment(id(cardA["id"]), "50"), payment("cash", "50"))
	f.now("2026-11-03", 12)
	f.call("void_order", map[string]any{"order_id": spent["id"], "reason": "录入错误"}, 200)
	f.call("adjust_card", map[string]any{"card_id": cardA["id"], "principal": "310.00", "bonus": "35.00", "reason": "更正"}, 200)
	quote := f.call("preview_card_refund", map[string]any{"card_id": cardB["id"]}, 200)
	f.call("refund_card", map[string]any{"card_id": cardB["id"], "method": "cash", "amount": quote["amount"]}, 200)
	f.now("2026-11-04", 12)
	f.call("upgrade_card", map[string]any{"card_id": cardA["id"], "to_type_id": typeB, "principal": "10.00", "bonus": "5.00", "sales_staff_ids": []any{f.Res.OwnerID}, "payments": []any{payment("cash", "10.00")}}, 200)
	f.checkout(f.order(customer, []uuid.UUID{f.staffB}, false, "male", f.item), f.cardPayment(id(cardA["id"]), "30"), payment("cash", "70"))
	f.foreignStoreCard("999.00", "2026-11-03")
	f.foreignStoreCard("777.00", "2026-11-01")

	report := f.read("get_stored_value_report", map[string]any{"from": "2026-11-02", "to": "2026-11-05"})
	rows := report["rows"].([]any)
	if len(rows) != 4 {
		t.Fatalf("rows = %v", rows)
	}
	for i, date := range []string{"2026-11-02", "2026-11-03", "2026-11-04", "2026-11-05"} {
		if rows[i].(map[string]any)["business_date"] != date {
			t.Fatalf("row %d date = %v", i, rows[i])
		}
	}
	day := func(i int) map[string]any { return rows[i].(map[string]any) }
	want := func(i int, field, value string) {
		t.Helper()
		if day(i)[field] != value {
			t.Fatalf("day %d %s = %v, want %s: %v", i, field, day(i)[field], value, day(i))
		}
	}
	want(0, "opening_principal", "200.00")
	want(0, "opening_bonus", "20.00")
	want(0, "added_principal", "200.00")
	want(0, "added_bonus", "10.00")
	if day(0)["consumed_principal"] == "0.00" {
		t.Fatalf("day 0 consumption missing: %v", day(0))
	}
	want(1, "refunded_principal", "100.00")
	want(1, "refunded_bonus", "0.00")
	want(2, "upgrade_principal", "10.00")
	want(2, "upgrade_bonus", "5.00")
	want(2, "added_principal", "0.00")
	for _, field := range []string{"added_principal", "added_bonus", "upgrade_principal", "upgrade_bonus", "consumed_principal", "consumed_bonus", "refunded_principal", "refunded_bonus", "adjusted_principal", "adjusted_bonus"} {
		want(3, field, "0.00")
	}
	if day(1)["consumed_principal"] == "0.00" || day(1)["adjusted_principal"] == "0.00" {
		t.Fatalf("day 1 should have a void and an adjustment: %v", day(1))
	}
	if day(1)["consumed_principal"].(string)[0] != '-' {
		t.Fatalf("void must reduce consumption: %v", day(1))
	}
	want(3, "opening_principal", day(2)["closing_principal"].(string))

	for i := range rows {
		row := day(i)
		for _, part := range []string{"principal", "bonus"} {
			sum := changeOf(t, row["opening_"+part].(string))
			signed := money.SignedTotal{}.Add(sum)
			for _, column := range storedValueColumns {
				value := changeOf(t, row[column+"_"+part].(string))
				if column == "consumed" || column == "refunded" {
					value = value.Negate()
				}
				signed = signed.Add(value)
			}
			if signed.Format() != row["closing_"+part] {
				t.Fatalf("day %d %s: closing %v != opening plus flows %s", i, part, row["closing_"+part], signed.Format())
			}
			if i+1 < len(rows) && row["closing_"+part] != day(i + 1)["opening_"+part] {
				t.Fatalf("day %d %s closing %v != next opening %v", i, part, row["closing_"+part], day(i + 1)["opening_"+part])
			}
		}
	}

	totals := report["totals"].(map[string]any)
	if totals["opening_principal"] != day(0)["opening_principal"] || totals["opening_bonus"] != day(0)["opening_bonus"] ||
		totals["closing_principal"] != day(3)["closing_principal"] || totals["closing_bonus"] != day(3)["closing_bonus"] {
		t.Fatalf("totals boundaries = %v", totals)
	}
	for _, field := range []string{"added_principal", "added_bonus", "upgrade_principal", "upgrade_bonus", "consumed_principal", "consumed_bonus", "refunded_principal", "refunded_bonus", "adjusted_principal", "adjusted_bonus"} {
		sum := money.SignedTotal{}
		for i := range rows {
			sum = sum.Add(changeOf(t, day(i)[field].(string)))
		}
		if sum.Format() != totals[field] {
			t.Fatalf("totals %s = %v, want %s", field, totals[field], sum.Format())
		}
	}

	cardReport := f.read("get_card_report", map[string]any{"from": "2026-11-02", "to": "2026-11-05"})
	ending := changeOf(t, cardReport["totals"].(map[string]any)["ending_balance"].(string))
	closing := money.SignedTotal{}.Add(changeOf(t, totals["closing_principal"].(string))).Add(changeOf(t, totals["closing_bonus"].(string)))
	if closing.Format() != ending.String() {
		t.Fatalf("closing %s != card report ending balance %s", closing.Format(), ending)
	}
	empty := f.read("get_stored_value_report", map[string]any{"from": "2026-11-06", "to": "2026-11-06"})
	if len(empty["rows"].([]any)) != 1 || empty["rows"].([]any)[0].(map[string]any)["opening_principal"] != empty["rows"].([]any)[0].(map[string]any)["closing_principal"] {
		t.Fatalf("quiet day = %v", empty)
	}
}

func TestStoredValueColumnCoversEveryLedgerKind(t *testing.T) {
	f := open(t)
	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	var def string
	if err := tx.QueryRow(t.Context(), `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='card_ledger_kind_check' AND conrelid='card_ledger'::regclass`).Scan(&def); err != nil {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	kinds := regexp.MustCompile(`'([a-z_]+)'`).FindAllStringSubmatch(def, -1)
	if len(kinds) < 11 {
		t.Fatalf("constraint = %s", def)
	}
	// 列号：0 充入，1 升级，2 耗卡，3 退卡，4 调整。
	want := map[string]int{"open": 0, "recharge": 0, "upgrade": 1, "consume": 2, "void": 2, "aftersale_refund": 2, "refund": 3, "adjust": 4, "transfer_in": 4, "transfer_out": 4, "import": 4}
	for _, kind := range kinds {
		column, err := reports.StoredValueColumn(kind[1])
		expected, known := want[kind[1]]
		if !known {
			t.Fatalf("kind %s has no expected column in this test", kind[1])
		}
		if err != nil || column != expected {
			t.Fatalf("kind %s: column %d, want %d (%v)", kind[1], column, expected, err)
		}
	}
	if len(kinds) != len(want) {
		t.Fatalf("constraint has %d kinds, test knows %d", len(kinds), len(want))
	}
	if _, err := reports.StoredValueColumn("unknown_kind"); err == nil {
		t.Fatal("unknown kind must be an error")
	}
}
