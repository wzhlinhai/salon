package orders_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
)

func TestGuestVisitDatabase(t *testing.T) {
	s := open(t)
	o := s.paid(s.customerOrder(s.performanceLine(nil)), payment("cash", "100"))
	source := s.guestList()["entries"].([]any)[0].(map[string]any)["id"]
	for _, overrides := range []map[string]any{{"share": "0"}, {"share": "-0.0001"}, {"share": "1.0001"}, {"gender": "other"}, {"void_id": nil, "reverses_id": source}, {"void_id": uuid.NewString(), "reverses_id": nil}} {
		s.cloneRefused("guest_visits", source, overrides, "23514")
	}
	for _, column := range []string{"order_id", "checkout_id", "settings_snapshot_id", "staff_id", "store_id"} {
		s.cloneRefused("guest_visits", source, map[string]any{column: uuid.NewString()}, "23503")
	}
	after := s.void(o)
	voidID := after["void"].(map[string]any)["id"]
	s.cloneRefused("guest_visits", source, map[string]any{"void_id": voidID, "reverses_id": source}, "23505")
	s.cloneRefused("guest_visits", source, map[string]any{"void_id": uuid.NewString(), "reverses_id": uuid.NewString()}, "23503")
	for _, sql := range []string{"UPDATE guest_visits SET share=share", "DELETE FROM guest_visits"} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), sql)
		dbtest.Rollback(t, tx)
		voidState(t, err, "42501")
	}
}

func TestGuestVisitCompositeForeignKeys(t *testing.T) {
	s := open(t)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var ok bool
	if err := tx.QueryRow(context.Background(), `SELECT count(*)=7 AND bool_and(pg_get_constraintdef(oid) LIKE 'FOREIGN KEY (tenant_id, %REFERENCES %(tenant_id, id)%') FROM pg_constraint WHERE conrelid='guest_visits'::regclass AND contype='f'`).Scan(&ok); err != nil || !ok {
		t.Fatal(ok, err)
	}
	dbtest.Rollback(t, tx)
}

func TestGuestVisitMigrationDown(t *testing.T) {
	s := open(t)
	s.paid(s.customerOrder(s.performanceLine(nil)), payment("cash", "100"))
	dbtest.MigrateDownTo(t, s.Pool, 30)
	var table *string
	if err := s.Pool.QueryRow(context.Background(), "SELECT to_regclass('guest_visits')::text").Scan(&table); err != nil {
		t.Fatal(err)
	}
	if table != nil {
		t.Fatal(table)
	}
}
