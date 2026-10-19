package db_test

import (
	"context"
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
)

// 租户和门店本身是结构表，用 id 而不是 tenant_id/store_id 标识自己。
var structuralTables = map[string]bool{"tenants": true, "stores": true}

// TestEveryTableIsTenantIsolated 检查 public 下的每张表：
// 都启用了行级安全并有策略；业务表都有 tenant_id、store_id，
// 并用 (tenant_id, store_id) 复合外键引用 stores，保证门店属于同一租户。
func TestEveryTableIsTenantIsolated(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPool(t)

	rows, err := pool.Query(ctx, `
		SELECT c.relname,
		       c.relrowsecurity,
		       EXISTS (SELECT 1 FROM pg_policy p WHERE p.polrelid = c.oid),
		       EXISTS (SELECT 1 FROM pg_attribute a WHERE a.attrelid = c.oid AND a.attname = 'tenant_id' AND NOT a.attisdropped),
		       EXISTS (SELECT 1 FROM pg_attribute a WHERE a.attrelid = c.oid AND a.attname = 'store_id' AND NOT a.attisdropped),
		       EXISTS (
		           SELECT 1 FROM pg_constraint k
		           WHERE k.conrelid = c.oid AND k.contype = 'f'
		             AND k.confrelid = 'public.stores'::regclass
		             AND (SELECT array_agg(a.attname ORDER BY a.attname) FROM pg_attribute a
		                  WHERE a.attrelid = c.oid AND a.attnum = ANY (k.conkey)) = ARRAY['store_id', 'tenant_id']::name[]
		       )
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p') AND c.relname <> 'goose_db_version'
		ORDER BY c.relname`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	seen := 0
	for rows.Next() {
		var name string
		var rls, policy, tenantCol, storeCol, storeFK bool
		if err := rows.Scan(&name, &rls, &policy, &tenantCol, &storeCol, &storeFK); err != nil {
			t.Fatal(err)
		}
		seen++
		if !rls || !policy {
			t.Errorf("%s: row level security enabled=%v, has policy=%v; want both", name, rls, policy)
		}
		if structuralTables[name] {
			continue
		}
		if !tenantCol || !storeCol {
			t.Errorf("%s: has tenant_id=%v, store_id=%v; business tables need both", name, tenantCol, storeCol)
		}
		if !storeFK {
			t.Errorf("%s: missing FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id)", name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if seen == 0 {
		t.Fatal("no tables found")
	}
}
