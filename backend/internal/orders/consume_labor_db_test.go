package orders_test

import (
	"context"
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
)

func TestConsumeLaborDatabase(t *testing.T) {
	s := open(t)
	line := s.laborLine("10", nil)
	o := s.paid(s.customerOrder(line), payment("cash", "100"))
	entries := s.performanceList()["entries"].([]any)
	source := entries[0].(map[string]any)["id"]
	for _, override := range []map[string]any{
		{"kind": "consume", "method": "cash"}, {"kind": "item", "method": "count_paid"},
		{"kind": "labor", "method": "count_gift"}, {"kind": "consume", "method": "labor"},
		{"kind": "labor", "method": "labor", "ratio": 99, "paid_amount": "100", "amount": "99"},
		{"kind": "other", "method": "cash"}, {"kind": "item", "method": "other"},
	} {
		s.cloneRefused("performance_entries", source, override, "23514")
	}
	for _, override := range []map[string]any{
		{"labor_fee_amount": "-1", "labor_fee_percent": nil},
		{"labor_fee_amount": nil, "labor_fee_percent": "-0.01"},
		{"labor_fee_amount": nil, "labor_fee_percent": "100.01"},
		{"labor_fee_amount": "1", "labor_fee_percent": "1"},
	} {
		s.cloneRefused("order_items", firstLine(o)["id"], override, "23514")
	}
	for _, column := range []string{"labor_fee_amount", "labor_fee_percent"} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), "UPDATE order_items SET "+column+"=NULL")
		dbtest.Rollback(t, tx)
		voidState(t, err, "42501")
	}
}

func TestConsumeLaborMigrationDown(t *testing.T) {
	s := open(t)
	line := s.laborLine("10", nil)
	c := s.countCard(line, "count", 1, 0)
	o := s.customerOrder(line)
	s.paid(o, countPayment(c, firstLine(o), "100"))
	s.void(o)
	// Keep item originals and reversals as well as the new kinds while downgrading.
	o = s.paid(s.customerOrder(line), payment("cash", "100"))
	s.void(o)
	dbtest.MigrateDownTo(t, s.Pool, 28)
	var rows, cols int
	if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM performance_entries").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM information_schema.columns WHERE table_name='order_items' AND column_name IN ('labor_fee_amount','labor_fee_percent')").Scan(&cols); err != nil {
		t.Fatal(err)
	}
	if rows != 4 || cols != 0 {
		t.Fatal(rows, cols)
	}
	for _, override := range []map[string]any{{"kind": "consume", "method": "count_paid"}, {"kind": "labor", "method": "labor"}} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		var source string
		if err := tx.QueryRow(context.Background(), "SELECT id::text FROM performance_entries WHERE reverses_id IS NULL LIMIT 1").Scan(&source); err != nil {
			t.Fatal(err)
		}
		dbtest.Rollback(t, tx)
		s.cloneRefused("performance_entries", source, override, "23514")
	}
}

func TestConsumeLaborPairingChecks(t *testing.T) {
	s := open(t)
	s.paid(s.customerOrder(s.checkoutLine()), payment("cash", "100"))
	// Pairing equivalences overlap. Isolate each constraint in a rolled-back schema
	// transaction, so another CHECK cannot hide a missing protection.
	checks := []struct{ name, override string }{
		{"performance_entries_kind_check", `{"kind":"other"}`},
		{"performance_entries_method_check", `{"method":"other"}`},
		{"performance_entries_item_method_check", `{"kind":"item","method":"count_paid"}`},
		{"performance_entries_consume_method_check", `{"kind":"consume","method":"cash"}`},
		{"performance_entries_labor_method_check", `{"kind":"labor","method":"cash"}`},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			dbtest.Exec(t, tx, "RESET ROLE")
			for _, other := range checks {
				if other.name != tc.name {
					dbtest.Exec(t, tx, "ALTER TABLE performance_entries DROP CONSTRAINT IF EXISTS "+other.name)
				}
			}
			dbtest.Exec(t, tx, "SET LOCAL ROLE salon_app")
			_, err := tx.Exec(context.Background(), `INSERT INTO performance_entries SELECT (jsonb_populate_record(NULL::performance_entries,to_jsonb(e)||$1::jsonb||jsonb_build_object('id',uuidv7()))).* FROM performance_entries e LIMIT 1`, tc.override)
			dbtest.Rollback(t, tx)
			voidState(t, err, "23514")
		})
	}
}
