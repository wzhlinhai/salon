package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
)

func setPacificLocalTime(t *testing.T) {
	t.Helper()
	previous := time.Local
	losAngeles, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	time.Local = losAngeles
	t.Cleanup(func() { time.Local = previous })
}

func scanStoreTime(t *testing.T, query func(any) error) {
	t.Helper()
	var scanned time.Time
	if err := query(&scanned); err != nil {
		t.Fatal(err)
	}
	if got := scanned.Format(time.RFC3339); got != "2026-11-11T12:00:00+08:00" {
		t.Fatalf("scanned time = %s, want 2026-11-11T12:00:00+08:00", got)
	}
	if want := time.Date(2026, 11, 11, 4, 0, 0, 0, time.UTC); !scanned.Equal(want) {
		t.Fatalf("scanned instant = %s, want %s", scanned.UTC(), want)
	}
}

func TestPoolScansTimestamptzInStoreTimeZone(t *testing.T) {
	setPacificLocalTime(t)

	pool := dbtest.NewPool(t)
	ctx := context.Background()
	scanStoreTime(t, func(dst any) error {
		return pool.QueryRow(ctx, "SELECT '2026-11-11T12:00:00+08:00'::timestamptz").Scan(dst)
	})

	var timestamp pgtype.Timestamptz
	if err := pool.QueryRow(ctx, "SELECT '2026-11-11T12:00:00+08:00'::timestamptz").Scan(&timestamp); err != nil {
		t.Fatal(err)
	}
	if got := timestamp.Time.Format(time.RFC3339); got != "2026-11-11T12:00:00+08:00" {
		t.Fatalf("pgtype.Timestamptz = %s, want 2026-11-11T12:00:00+08:00", got)
	}
}

func TestNewPoolScansTimestamptzInStoreTimeZone(t *testing.T) {
	setPacificLocalTime(t)
	pool, err := db.NewPool(context.Background(), dbtest.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ctx := context.Background()
	scanStoreTime(t, func(dst any) error {
		return pool.QueryRow(ctx, "SELECT '2026-11-11T12:00:00+08:00'::timestamptz").Scan(dst)
	})
}

func TestConnectScansTimestamptzInStoreTimeZone(t *testing.T) {
	setPacificLocalTime(t)
	conn, err := db.Connect(context.Background(), dbtest.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	ctx := context.Background()
	scanStoreTime(t, func(dst any) error {
		return conn.QueryRow(ctx, "SELECT '2026-11-11T12:00:00+08:00'::timestamptz").Scan(dst)
	})
}
