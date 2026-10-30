package orders_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
)

func TestPerformanceDatabase(t *testing.T) {
	s := open(t)
	o := s.paid(s.customerOrder(s.performanceLine(nil)), payment("cash", "100"))
	entries := s.performanceList()["entries"].([]any)
	source := entries[0].(map[string]any)["id"]
	for _, override := range []map[string]any{
		{"kind": "other"}, {"method": "card"}, {"ratio": 101, "amount": "101"}, {"ratio": -1, "amount": "-1"},
		{"amount": "99.99"}, {"paid_amount": "0", "amount": "0"}, {"paid_amount": "-1", "amount": "-1"},
		{"slot": nil}, {"position_id": nil}, {"staff_id": nil},
		{"void_id": nil, "reverses_id": source},
		{"void_id": uuid.NewString(), "reverses_id": nil},
	} {
		s.cloneRefused("performance_entries", source, override, "23514")
	}
	for _, column := range []string{"order_id", "order_item_id", "checkout_id", "settings_snapshot_id", "position_id", "staff_id", "store_id"} {
		s.cloneRefused("performance_entries", source, map[string]any{column: uuid.NewString()}, "23503")
	}
	after := s.void(o)
	voidID := after["void"].(map[string]any)["id"]
	s.cloneRefused("performance_entries", source, map[string]any{"void_id": voidID, "reverses_id": source}, "23505")
	// Valid reversal pair with nonexistent references isolates both nullable FKs.
	s.cloneRefused("performance_entries", source, map[string]any{"void_id": uuid.NewString(), "reverses_id": uuid.NewString()}, "23503")
	for _, sql := range []string{"UPDATE performance_entries SET amount=amount", "DELETE FROM performance_entries"} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), sql)
		dbtest.Rollback(t, tx)
		voidState(t, err, "42501")
	}

	for _, value := range []int{-1, 101} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), `INSERT INTO order_item_positions(tenant_id,store_id,order_item_id,slot,position_id,required,staff_id,share_percent)
   SELECT tenant_id,store_id,order_item_id,100,position_id,required,staff_id,$1 FROM order_item_positions LIMIT 1`, value)
		dbtest.Rollback(t, tx)
		voidState(t, err, "23514")
	}
	// Copied order shares cannot be edited by salon_app.
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, err := tx.Exec(context.Background(), "UPDATE order_item_positions SET share_percent=100")
	dbtest.Rollback(t, tx)
	voidState(t, err, "42501")
}

func TestPerformanceMigrationDown(t *testing.T) {
	s := open(t)
	s.paid(s.customerOrder(s.performanceLine(100)), payment("cash", "100"))
	dbtest.MigrateDownTo(t, s.Pool, 27)
	var table *string
	var cols int
	if err := s.Pool.QueryRow(context.Background(), "SELECT to_regclass('performance_entries')::text").Scan(&table); err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM information_schema.columns WHERE table_name IN ('item_positions','order_item_positions') AND column_name='share_percent'").Scan(&cols); err != nil {
		t.Fatal(err)
	}
	if table != nil || cols != 0 {
		t.Fatal(table, cols)
	}
}

func TestPerformanceSchema(t *testing.T) {
	s := open(t)
	var fks int
	if err := s.Pool.QueryRow(context.Background(), `SELECT count(*) FROM pg_constraint WHERE conrelid='performance_entries'::regclass AND contype='f' AND cardinality(conkey)=2`).Scan(&fks); err != nil {
		t.Fatal(err)
	}
	if fks != 9 {
		t.Fatal("missing composite foreign keys", fks)
	}
	s.performanceLine(100)
	for _, v := range []int{-1, 101} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), "UPDATE item_positions SET share_percent=$1", v)
		dbtest.Rollback(t, tx)
		voidState(t, err, "23514")
	}
}
