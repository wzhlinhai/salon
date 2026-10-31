package orders_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
)

func (s *fixture) saleSource() {
	s.t.Helper()
	s.paymentCard(s.paymentType("销售来源", nil), "100", "20")
}

func TestSaleDatabaseChecks(t *testing.T) {
	s := open(t)
	s.saleSource()
	packet := s.call("issue_red_packet", map[string]any{"customer_id": s.customer, "kind": "sold", "face_value": "100", "price": "88", "expires_on": "2999-01-01", "sales_staff_ids": []uuid.UUID{s.Res.OwnerID}}, 200)["id"]
	s.paid(s.customerOrder(s.checkoutLine()), payment("cash", "100"))
	checks := []struct {
		name      string
		sale      bool
		overrides map[string]any
	}{
		{"kind", true, map[string]any{"kind": "other"}},
		{"method", true, map[string]any{"method": "other"}},
		{"sale_method", true, map[string]any{"method": "cash"}},
		{"sale_method", false, map[string]any{"method": "card_open"}},
		{"order_source", true, map[string]any{"order_id": uuid.NewString()}},
		{"order_source", true, map[string]any{"order_item_id": uuid.NewString()}},
		{"order_source", true, map[string]any{"checkout_id": uuid.NewString()}},
		{"order_source", true, map[string]any{"settings_snapshot_id": uuid.NewString()}},
		{"order_source", false, map[string]any{"order_id": nil}},
		{"order_source", false, map[string]any{"order_item_id": nil}},
		{"order_source", false, map[string]any{"checkout_id": nil}},
		{"order_source", false, map[string]any{"settings_snapshot_id": nil}},
		{"non_sale_slot", false, map[string]any{"staff_id": s.Res.OwnerID}},
		{"sale_staff", true, map[string]any{"staff_id": nil}},
		{"sale_staff", true, map[string]any{"slot": 1}},
		{"sale_staff", true, map[string]any{"position_id": uuid.NewString()}},
		{"sale_ratio", true, map[string]any{"ratio": 99, "amount": "99"}},
		{"sale_source", true, map[string]any{"red_packet_id": packet}},
		{"sale_source", true, map[string]any{"card_ledger_id": nil}},
		{"sale_source", true, map[string]any{"method": "red_packet_sold"}},
		{"sale_source", true, map[string]any{"card_ledger_id": nil, "red_packet_id": packet}},
		{"sale_source", false, map[string]any{"card_ledger_id": uuid.NewString()}},
		{"sale_source", false, map[string]any{"red_packet_id": packet}},
		{"purchase", true, map[string]any{"purchase": nil}},
		{"purchase", true, map[string]any{"purchase": "other"}},
		{"purchase", true, map[string]any{"method": "card_recharge", "purchase": "first"}},
		{"purchase", true, map[string]any{"method": "card_upgrade", "purchase": "first"}},
		{"purchase", true, map[string]any{"method": "red_packet_sold", "purchase": "first"}},
		{"purchase", false, map[string]any{"purchase": "repeat"}},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			// Isolate the selected CHECK and remove FKs so overlapping protections cannot mask it.
			dbtest.Exec(t, tx, "RESET ROLE")
			rows, err := tx.Query(context.Background(), `SELECT conname FROM pg_constraint WHERE conrelid='performance_entries'::regclass AND contype IN ('c','f') AND conname<>$1`, "performance_entries_"+tc.name+"_check")
			if err != nil {
				t.Fatal(err)
			}
			names := []string{}
			for rows.Next() {
				var name string
				if err := rows.Scan(&name); err != nil {
					t.Fatal(err)
				}
				names = append(names, name)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			for _, name := range names {
				dbtest.Exec(t, tx, "ALTER TABLE performance_entries DROP CONSTRAINT "+name)
			}
			dbtest.Exec(t, tx, "SET LOCAL ROLE salon_app")
			raw, err := json.Marshal(tc.overrides)
			if err != nil {
				t.Fatal(err)
			}
			_, err = tx.Exec(context.Background(), `INSERT INTO performance_entries SELECT (jsonb_populate_record(NULL::performance_entries,to_jsonb(e)||$1::jsonb||jsonb_build_object('id',uuidv7()))).* FROM performance_entries e WHERE (kind='sale')=$2 AND red_packet_id IS NULL LIMIT 1`, string(raw), tc.sale)
			dbtest.Rollback(t, tx)
			voidState(t, err, "23514")
		})
	}
}

func TestSaleCompositeForeignKeys(t *testing.T) {
	s := open(t)
	s.saleSource()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var ok bool
	if err := tx.QueryRow(context.Background(), `SELECT count(*)=2 AND bool_and(pg_get_constraintdef(oid) LIKE 'FOREIGN KEY (tenant_id, %REFERENCES %(tenant_id, id)%') FROM pg_constraint WHERE conrelid='performance_entries'::regclass AND conname IN ('performance_entries_tenant_id_card_ledger_id_fkey','performance_entries_tenant_id_red_packet_id_fkey')`).Scan(&ok); err != nil || !ok {
		t.Fatal(ok, err)
	}
	dbtest.Rollback(t, tx)
}

func TestSaleMigrationDown(t *testing.T) {
	s := open(t)
	s.saleSource()
	s.call("issue_red_packet", map[string]any{"customer_id": s.customer, "kind": "sold", "face_value": "100", "price": "88", "expires_on": "2999-01-01", "sales_staff_ids": []uuid.UUID{s.Res.OwnerID}}, 200)
	s.paid(s.customerOrder(s.checkoutLine()), payment("cash", "100"))
	dbtest.MigrateDownTo(t, s.Pool, 29)
	var rows, cols, nullable, checks int
	if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM performance_entries").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.QueryRow(context.Background(), `SELECT count(*) FROM information_schema.columns WHERE table_name='performance_entries' AND column_name IN ('card_ledger_id','red_packet_id','purchase')`).Scan(&cols); err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.QueryRow(context.Background(), `SELECT count(*) FROM information_schema.columns WHERE table_name='performance_entries' AND column_name IN ('order_id','order_item_id','checkout_id','settings_snapshot_id') AND is_nullable='YES'`).Scan(&nullable); err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.QueryRow(context.Background(), `SELECT count(*) FROM pg_constraint WHERE conrelid='performance_entries'::regclass AND conname IN ('performance_entries_check1','performance_entries_kind_check','performance_entries_method_check','performance_entries_item_method_check','performance_entries_consume_method_check','performance_entries_labor_method_check','performance_entries_labor_ratio_check')`).Scan(&checks); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || cols != 0 || nullable != 0 || checks != 7 {
		t.Fatal(rows, cols, nullable, checks)
	}
}

func TestCountCheckoutNoSale(t *testing.T) {
	s := open(t)
	line := s.laborLine("10", nil)
	card := s.countCard(line, "count", 2, 1)
	before := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "performance_entries")
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var sales int
	if err := tx.QueryRow(context.Background(), `SELECT count(*) FROM performance_entries WHERE kind='sale'`).Scan(&sales); err != nil || sales != 1 {
		t.Fatal(sales, err)
	}
	dbtest.Rollback(t, tx)
	o := s.customerOrder(line)
	s.paid(o, countPayment(card, firstLine(o), "100"))
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var after int
	if err := tx.QueryRow(context.Background(), `SELECT count(*) FROM performance_entries WHERE kind='sale'`).Scan(&after); err != nil || after != sales {
		t.Fatal(after, err)
	}
	dbtest.Rollback(t, tx)
	if dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "performance_entries") == before {
		t.Fatal("checkout did not write consume/labor")
	}
}
