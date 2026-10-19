package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
)

type fixture struct {
	pool             *pgxpool.Pool
	tenantA, tenantB uuid.UUID
	storeA, storeB   uuid.UUID
}

// newFixture 建两个租户，各有一家门店。
func newFixture(t *testing.T) fixture {
	t.Helper()
	f := fixture{pool: dbtest.NewPool(t), tenantA: uuid.New(), tenantB: uuid.New()}
	f.storeA = createTenantWithStore(t, f.pool, f.tenantA, "A")
	f.storeB = createTenantWithStore(t, f.pool, f.tenantB, "B")
	return f
}

func createTenantWithStore(t *testing.T, pool *pgxpool.Pool, tenantID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	var storeID uuid.UUID
	err := db.InTenant(context.Background(), pool, tenantID, func(q *dbgen.Queries) error {
		ctx := context.Background()
		if _, err := q.CreateTenant(ctx, dbgen.CreateTenantParams{ID: tenantID, Name: "租户" + name}); err != nil {
			return err
		}
		s, err := q.CreateStore(ctx, dbgen.CreateStoreParams{TenantID: tenantID, Name: "门店" + name})
		storeID = s.ID
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return storeID
}

func inTenant(t *testing.T, f fixture, tenantID uuid.UUID, fn func(ctx context.Context, q *dbgen.Queries) error) error {
	t.Helper()
	ctx := context.Background()
	return db.InTenant(ctx, f.pool, tenantID, func(q *dbgen.Queries) error { return fn(ctx, q) })
}

func isRLSViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42501"
}

func TestTenantSeesOnlyOwnRows(t *testing.T) {
	f := newFixture(t)
	err := inTenant(t, f, f.tenantA, func(ctx context.Context, q *dbgen.Queries) error {
		stores, err := q.ListStores(ctx)
		if err != nil {
			return err
		}
		if len(stores) != 1 || stores[0].ID != f.storeA {
			t.Errorf("tenant A lists %v, want only store %s", stores, f.storeA)
		}
		if _, err := q.GetTenant(ctx, f.tenantA); err != nil {
			t.Errorf("tenant A cannot read itself: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCrossTenantReadIsRejected(t *testing.T) {
	f := newFixture(t)
	err := inTenant(t, f, f.tenantA, func(ctx context.Context, q *dbgen.Queries) error {
		if _, err := q.GetStore(ctx, f.storeB); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("read tenant B store: err = %v, want no rows", err)
		}
		if _, err := q.GetTenant(ctx, f.tenantB); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("read tenant B: err = %v, want no rows", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCrossTenantInsertIsRejected(t *testing.T) {
	f := newFixture(t)
	err := inTenant(t, f, f.tenantA, func(ctx context.Context, q *dbgen.Queries) error {
		_, err := q.CreateStore(ctx, dbgen.CreateStoreParams{TenantID: f.tenantB, Name: "越权门店"})
		return err
	})
	if !isRLSViolation(err) {
		t.Fatalf("insert store into tenant B: err = %v, want row-level security violation", err)
	}

	err = inTenant(t, f, f.tenantA, func(ctx context.Context, q *dbgen.Queries) error {
		_, err := q.CreateTenant(ctx, dbgen.CreateTenantParams{ID: uuid.New(), Name: "越权租户"})
		return err
	})
	if !isRLSViolation(err) {
		t.Fatalf("insert other tenant: err = %v, want row-level security violation", err)
	}
}

func TestCrossTenantUpdateAndDeleteAffectNothing(t *testing.T) {
	f := newFixture(t)
	err := inTenant(t, f, f.tenantA, func(ctx context.Context, q *dbgen.Queries) error {
		n, err := q.RenameStore(ctx, dbgen.RenameStoreParams{ID: f.storeB, Name: "被改名"})
		if err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("update tenant B store affected %d rows, want 0", n)
		}
		n, err = q.DeleteStore(ctx, f.storeB)
		if err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("delete tenant B store affected %d rows, want 0", n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = inTenant(t, f, f.tenantB, func(ctx context.Context, q *dbgen.Queries) error {
		s, err := q.GetStore(ctx, f.storeB)
		if err != nil {
			return err
		}
		if s.Name != "门店B" {
			t.Errorf("tenant B store name = %q, want unchanged", s.Name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// asAppRole 以 salon_app 角色在事务中执行 fn；tenant 为空时不设置租户上下文。
// 用于测试 sqlc 查询之外的写法。
func asAppRole(t *testing.T, pool *pgxpool.Pool, tenant string, fn func(ctx context.Context, tx pgx.Tx) error) error {
	t.Helper()
	ctx := context.Background()
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE salon_app"); err != nil {
			return err
		}
		if tenant != "" {
			if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenant); err != nil {
				return err
			}
		}
		return fn(ctx, tx)
	})
}

func TestMovingRowToOtherTenantIsRejected(t *testing.T) {
	f := newFixture(t)
	err := asAppRole(t, f.pool, f.tenantA.String(), func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "UPDATE stores SET tenant_id = $1 WHERE id = $2", f.tenantB, f.storeA)
		return err
	})
	if !isRLSViolation(err) {
		t.Fatalf("move store to tenant B: err = %v, want row-level security violation", err)
	}
}

func TestNoTenantContextSeesNothing(t *testing.T) {
	f := newFixture(t)
	err := asAppRole(t, f.pool, "", func(ctx context.Context, tx pgx.Tx) error {
		stores, err := dbgen.New(tx).ListStores(ctx)
		if err != nil {
			return err
		}
		if len(stores) != 0 {
			t.Errorf("without tenant context listed %d stores, want 0", len(stores))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestInTenantRequiresTenant(t *testing.T) {
	pool := dbtest.NewPool(t)
	err := db.InTenant(context.Background(), pool, uuid.Nil, func(*dbgen.Queries) error { return nil })
	if !errors.Is(err, db.ErrNoTenant) {
		t.Fatalf("err = %v, want ErrNoTenant", err)
	}
}

func TestTenantContextDoesNotLeakAcrossTransactions(t *testing.T) {
	f := newFixture(t)
	// 连接池只有一条连接时，下一个事务必定复用同一条连接。
	cfg := f.pool.Config()
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	f.pool = pool

	if err := inTenant(t, f, f.tenantA, func(ctx context.Context, q *dbgen.Queries) error {
		_, err := q.ListStores(ctx)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	var role, tenant string
	err = pool.QueryRow(ctx, "SELECT current_user, coalesce(current_setting('app.tenant_id', true), '')").Scan(&role, &tenant)
	if err != nil {
		t.Fatal(err)
	}
	if role == "salon_app" || tenant != "" {
		t.Fatalf("after transaction: role = %q, app.tenant_id = %q; want both reset", role, tenant)
	}
}
