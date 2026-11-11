package booking_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

func TestBookingTenantIsolationAndRLS(t *testing.T) {
	s := openBooking(t)
	ctx := context.Background()
	second, err := staff.Bootstrap(ctx, s.Pool, staff.BootstrapInput{
		TenantName: "别家", StoreName: "别家门店", OwnerName: "李老板", OwnerNumber: "001", OwnerAccount: "boss2", OwnerPassword: "password-boss2",
	})
	if err != nil {
		t.Fatal(err)
	}
	secondCaller := s.MustCaller(s.MustLogin("boss2", "password-boss2"))
	item := s.HTTP(t, &secondCaller, "create_item", map[string]any{"name": "别家剪发", "price": "100", "discountable": true, "duration_minutes": 30, "labor_fee_amount": nil, "labor_fee_percent": nil, "positions": []any{}, "level_prices": []any{}, "member_prices": []any{}})
	if item.Status != 200 {
		t.Fatal(item)
	}
	customer := s.HTTP(t, &secondCaller, "create_customer", map[string]any{"name": "别家顾客", "phone": "13900000001"})
	if customer.Status != 200 {
		t.Fatal(customer)
	}
	appointment := s.HTTP(t, &secondCaller, "create_appointment", map[string]any{
		"staff_id": second.OwnerID, "customer_id": customer.Body["customer"].(map[string]any)["id"], "items": []any{map[string]any{"item_id": item.Body["id"]}},
		"starts_at": s.at(0, "13:00"),
	})
	if appointment.Status != 200 {
		t.Fatal(appointment)
	}
	owner := s.MustCaller(s.Owner)
	if r := s.HTTP(t, &owner, "get_appointment", map[string]any{"id": appointment.Body["id"]}); r.Status != 404 || r.Detail() != "not found" {
		t.Fatal(r)
	}

	for _, tc := range []struct {
		name   string
		tenant uuid.UUID
		want   int
	}{{"own tenant", s.Res.TenantID, 0}, {"other tenant", second.TenantID, 1}} {
		tx := dbtest.AppTx(t, s.Pool, tc.tenant)
		var count int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM appointments").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != tc.want {
			t.Fatalf("%s sees %d appointments, want %d", tc.name, count, tc.want)
		}
		dbtest.Rollback(t, tx)
	}

	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, err = tx.Exec(ctx, `INSERT INTO appointments (tenant_id,store_id,staff_id,customer_id,kind,starts_at,ends_at,status,created_by) VALUES ($1,$2,$3,$4,'appointment',now(),now()+interval '1 hour','confirmed',$5)`, s.Res.TenantID, s.Res.StoreID, second.OwnerID, s.customer, s.Res.OwnerID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		t.Fatalf("cross-tenant staff insert = %v, want foreign key violation", err)
	}
	dbtest.Rollback(t, tx)
}

func TestBookingMigrationDown(t *testing.T) {
	s := openBooking(t)
	appointment := s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "13:00"), "ends_at": s.at(0, "14:00")})
	s.block(s.staff, s.at(0, "15:00"), s.at(0, "16:00"), 200)
	s.call("cancel_appointment", map[string]any{"id": appointment["id"], "reason": "顾客取消"}, 200)

	dbtest.MigrateDownTo(t, s.Pool, 45)
	for _, table := range []string{"appointments", "appointment_items", "appointment_events"} {
		var name *string
		if err := s.Pool.QueryRow(context.Background(), "SELECT to_regclass($1)::text", table).Scan(&name); err != nil || name != nil {
			t.Fatalf("%s after down = %v, %v", table, name, err)
		}
	}
	var columns int
	if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM information_schema.columns WHERE table_name='items' AND column_name IN ('duration_minutes','bookable')").Scan(&columns); err != nil || columns != 0 {
		t.Fatalf("item booking columns after down = %d, %v", columns, err)
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, err := tx.Exec(context.Background(), `INSERT INTO audit_log(tenant_id,store_id,actor_id,action,target_kind,target_id) VALUES($1,$2,$3,'test','appointment',$4)`, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, uuid.New())
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != "audit_log_target_kind_check" {
		t.Fatalf("appointment audit target after down = %v, want target kind check", err)
	}
	dbtest.Rollback(t, tx)
}

func TestAppointmentExclusionConstraint(t *testing.T) {
	s := openBooking(t)
	insert := func(start, end time.Time) error {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), `INSERT INTO appointments (tenant_id,store_id,staff_id,customer_id,kind,starts_at,ends_at,status,created_by) VALUES ($1,$2,$3,$4,'appointment',$5,$6,'confirmed',$7)`, s.Res.TenantID, s.Res.StoreID, s.staff, s.customer, start, end, s.Res.OwnerID)
		if err != nil {
			dbtest.Rollback(t, tx)
			return err
		}
		dbtest.Commit(t, tx)
		return nil
	}
	if err := insert(s.at(0, "13:00"), s.at(0, "14:00")); err != nil {
		t.Fatal(err)
	}
	err := insert(s.at(0, "13:30"), s.at(0, "14:30"))
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23P01" {
		t.Fatalf("overlapping insert = %v, want exclusion violation", err)
	}
}

func TestBookingAppendOnlyGrants(t *testing.T) {
	s := openBooking(t)
	appointment := s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "13:00"), "items": []any{map[string]any{"item_id": s.item}}})
	s.block(s.staff, s.at(0, "15:00"), s.at(0, "16:00"), 200)
	for _, stmt := range []string{
		"UPDATE appointment_events SET note='x'",
		"DELETE FROM appointment_events",
		"UPDATE appointment_items SET position=2",
		"DELETE FROM appointment_items",
		"DELETE FROM appointments",
	} {
		t.Run(stmt, func(t *testing.T) {
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			_, err := tx.Exec(context.Background(), stmt)
			if err == nil || !strings.Contains(err.Error(), "permission denied") {
				t.Fatalf("err = %v, want permission denied", err)
			}
			dbtest.Rollback(t, tx)
		})
	}
	_ = appointment
}
