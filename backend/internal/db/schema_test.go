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

// TestBusinessForeignKeysIncludeTenant 检查业务外键按列位置将 tenant_id 引用到 tenant_id。
func TestBusinessForeignKeysIncludeTenant(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPool(t)

	exceptions := map[string]string{
		"card_count_ledger_card_id_item_id_fkey": "card_count_ledger 和 card_items 都通过 (tenant_id, card_id) → cards 和 (tenant_id, item_id) → items 将两列绑定到本租户",
	}
	rows, err := pool.Query(ctx, `
		SELECT src.relname, k.conname, dst.relname,
		       string_agg(sa.attname, ', ' ORDER BY cols.position),
		       string_agg(da.attname, ', ' ORDER BY cols.position),
		       bool_or(sa.attname = 'tenant_id' AND da.attname = 'tenant_id')
		FROM pg_constraint k
		JOIN pg_class src ON src.oid = k.conrelid
		JOIN pg_namespace sn ON sn.oid = src.relnamespace
		JOIN pg_class dst ON dst.oid = k.confrelid
		JOIN pg_namespace dn ON dn.oid = dst.relnamespace
		CROSS JOIN LATERAL unnest(k.conkey, k.confkey) WITH ORDINALITY AS cols(srcnum, dstnum, position)
		JOIN pg_attribute sa ON sa.attrelid = src.oid AND sa.attnum = cols.srcnum
		JOIN pg_attribute da ON da.attrelid = dst.oid AND da.attnum = cols.dstnum
		WHERE k.contype = 'f' AND sn.nspname = 'public' AND dn.nspname = 'public'
		GROUP BY src.relname, k.oid, k.conname, dst.relname
		ORDER BY src.relname, k.conname`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	seen := 0
	for rows.Next() {
		var table, constraint, target, columns, targetColumns string
		var tenantPaired bool
		if err := rows.Scan(&table, &constraint, &target, &columns, &targetColumns, &tenantPaired); err != nil {
			t.Fatal(err)
		}
		reason, exception := exceptions[constraint]
		exception = exception && table == "card_count_ledger"
		if structuralTables[table] || (structuralTables[target] && target != "stores" && !exception) {
			continue
		}
		seen++
		t.Logf("%s: %s (%s) → %s (%s); tenant paired=%v", table, constraint, columns, target, targetColumns, tenantPaired)
		if exception {
			if target != "card_items" || columns != "card_id, item_id" || targetColumns != "card_id, item_id" {
				t.Errorf("%s: %s FOREIGN KEY (%s) REFERENCES %s (%s): shape does not match exception: %s", table, constraint, columns, target, targetColumns, reason)
			} else {
				delete(exceptions, constraint)
			}
			continue
		}
		if !tenantPaired {
			t.Errorf("%s: %s FOREIGN KEY (%s) REFERENCES %s (%s): tenant_id must reference tenant_id", table, constraint, columns, target, targetColumns)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if seen == 0 {
		t.Fatal("no business foreign keys found")
	}
	for constraint, reason := range exceptions {
		t.Errorf("unused foreign key exception %s: %s", constraint, reason)
	}
}
