package dbtest

import (
	"context"
	"errors"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/wzhlinhai/salon/backend/internal/db"
)

func TestNewPoolsAreIndependent(t *testing.T) {
	first, second := NewPool(t), NewPool(t)
	NewTenant(t, first, "独立数据库")
	var count int
	if err := second.QueryRow(context.Background(), "SELECT count(*) FROM tenants").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("second database has %d tenants, want 0", count)
	}
}

func TestNewPoolIsFullyMigrated(t *testing.T) {
	pool := NewPool(t)
	migrations, err := goose.CollectMigrations("../db/migrations", 0, goose.MaxVersion)
	if err != nil {
		t.Fatal(err)
	}
	version, err := db.MigrationVersion(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if latest := migrations[len(migrations)-1].Version; version != latest {
		t.Fatalf("migration version = %d, want %d", version, latest)
	}
}

func TestNewPoolDoesNotChangeTemplate(t *testing.T) {
	first := NewPool(t)
	NewTenant(t, first, "不写入模板")
	second := NewPool(t)
	var count int
	if err := second.QueryRow(context.Background(), "SELECT count(*) FROM tenants").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("fresh clone has %d tenants, want 0", count)
	}
}

func TestRunPostgres(t *testing.T) {
	reaperErr := errors.New("create container: reaper: new reaper: No such container")
	otherErr := errors.New("pull postgres: unavailable")
	for _, tc := range []struct {
		name  string
		first error
		next  error
		calls int
		want  error
	}{
		{name: "success", calls: 1},
		{name: "reaper recovers", first: reaperErr, calls: 2},
		{name: "reaper fails twice", first: reaperErr, next: reaperErr, calls: 2, want: reaperErr},
		{name: "retry returns other error", first: reaperErr, next: otherErr, calls: 2, want: otherErr},
		{name: "other error is not retried", first: otherErr, calls: 1, want: otherErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			container := &postgres.PostgresContainer{}
			calls := 0
			got, err := runPostgres(func() (*postgres.PostgresContainer, error) {
				calls++
				failure := tc.first
				if calls > 1 {
					failure = tc.next
				}
				if failure != nil {
					return nil, failure
				}
				return container, nil
			})
			if calls != tc.calls || !errors.Is(err, tc.want) {
				t.Fatalf("calls=%d, err=%v; want calls=%d, err=%v", calls, err, tc.calls, tc.want)
			}
			if tc.want == nil && got != container {
				t.Fatal("successful run did not return its container")
			}
		})
	}
}
