package booking_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

func TestSelfBookingActorConstraints(t *testing.T) {
	s := openBooking(t)
	insertAppointment := func(start string, createdBy, createdByCustomer *uuid.UUID) error {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), `INSERT INTO appointments (tenant_id,store_id,staff_id,customer_id,kind,starts_at,ends_at,status,created_by,created_by_customer)
 VALUES ($1,$2,$3,$4,'appointment',$5,$6,'confirmed',$7,$8)`,
			s.Res.TenantID, s.Res.StoreID, s.staff, s.customer, s.at(2, start), s.at(2, start).Add(30*time.Minute), createdBy, createdByCustomer)
		dbtest.Rollback(t, tx)
		return err
	}
	owner, customer := s.Res.OwnerID, s.customer
	for _, tc := range []struct {
		name     string
		start    string
		staff    *uuid.UUID
		customer *uuid.UUID
		want     string
	}{
		{"both appointment actors", "10:00", &owner, &customer, "appointments_actor_check"},
		{"no appointment actor", "10:30", nil, nil, "appointments_actor_check"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var pgErr *pgconn.PgError
			if err := insertAppointment(tc.start, tc.staff, tc.customer); !errors.As(err, &pgErr) || pgErr.ConstraintName != tc.want {
				t.Fatalf("err = %v, want constraint %s", err, tc.want)
			}
		})
	}

	appointment := s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(2, "12:00"), "ends_at": s.at(2, "13:00")})
	insertEvent := func(staffActor, customerActor *uuid.UUID) error {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), `INSERT INTO appointment_events (tenant_id,store_id,appointment_id,kind,to_starts_at,to_ends_at,to_staff_id,actor_staff_id,actor_customer_id)
 VALUES ($1,$2,$3,'create',$4,$5,$6,$7,$8)`,
			s.Res.TenantID, s.Res.StoreID, appointment["id"], s.at(2, "12:00"), s.at(2, "13:00"), s.staff, staffActor, customerActor)
		dbtest.Rollback(t, tx)
		return err
	}
	for _, tc := range []struct {
		name     string
		staff    *uuid.UUID
		customer *uuid.UUID
	}{
		{"both event actors", &owner, &customer},
		{"no event actor", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var pgErr *pgconn.PgError
			if err := insertEvent(tc.staff, tc.customer); !errors.As(err, &pgErr) || pgErr.ConstraintName != "appointment_events_actor_check" {
				t.Fatalf("err = %v, want event actor check", err)
			}
		})
	}

	other, err := staff.Bootstrap(context.Background(), s.Pool, staff.BootstrapInput{
		TenantName: "演员别租户", StoreName: "演员别店", OwnerName: "别老板", OwnerNumber: "001",
		OwnerAccount: "actor-other-boss", OwnerPassword: "owner-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	var foreignCustomer uuid.UUID
	if err := s.Pool.QueryRow(context.Background(), `INSERT INTO customers (tenant_id,store_id,phone,created_by) VALUES ($1,$2,'13900139999',$3) RETURNING id`,
		other.TenantID, other.StoreID, other.OwnerID).Scan(&foreignCustomer); err != nil {
		t.Fatal(err)
	}
	var pgErr *pgconn.PgError
	if err := insertAppointment("11:00", nil, &foreignCustomer); !errors.As(err, &pgErr) || pgErr.ConstraintName != "appointments_created_by_customer_fkey" {
		t.Fatalf("cross-tenant customer actor err = %v, want tenant FK", err)
	}
	if err := insertEvent(nil, &foreignCustomer); !errors.As(err, &pgErr) || pgErr.ConstraintName != "appointment_events_actor_customer_fkey" {
		t.Fatalf("cross-tenant event actor err = %v, want tenant FK", err)
	}
}

func TestSelfBookingMigrationDownRefusesCustomerActor(t *testing.T) {
	t.Run("appointment", func(t *testing.T) {
		s := openBooking(t)
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		dbtest.Exec(t, tx, `INSERT INTO appointments (tenant_id,store_id,staff_id,customer_id,kind,starts_at,ends_at,status,created_by_customer)
 VALUES ($1,$2,$3,$4,'appointment',$5,$6,'pending',$4)`,
			s.Res.TenantID, s.Res.StoreID, s.staff, s.customer, s.at(3, "10:00"), s.at(3, "10:30"))
		dbtest.Commit(t, tx)
		dbtest.MigrateDownTo(t, s.Pool, 53)
		err := db.MigrateDown(context.Background(), s.Pool)
		if err == nil || !strings.Contains(err.Error(), "无法回滚 00053") {
			t.Fatalf("down error = %v", err)
		}
	})
	t.Run("event", func(t *testing.T) {
		s := openBooking(t)
		appointment := s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(3, "12:00"), "ends_at": s.at(3, "13:00")})
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		dbtest.Exec(t, tx, `INSERT INTO appointment_events (tenant_id,store_id,appointment_id,kind,to_starts_at,to_ends_at,to_staff_id,actor_customer_id)
 VALUES ($1,$2,$3,'create',$4,$5,$6,$7)`,
			s.Res.TenantID, s.Res.StoreID, appointment["id"], s.at(3, "12:00"), s.at(3, "13:00"), s.staff, s.customer)
		dbtest.Commit(t, tx)
		dbtest.MigrateDownTo(t, s.Pool, 53)
		err := db.MigrateDown(context.Background(), s.Pool)
		if err == nil || !strings.Contains(err.Error(), "无法回滚 00053") {
			t.Fatalf("down error = %v", err)
		}
	})
}
