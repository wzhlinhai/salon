package catalog_test

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
)

func (s *fixture) category(name string, ratio int, active bool) map[string]any {
	s.t.Helper()
	row := s.call("create_item_category", map[string]any{"name": name, "performance_ratio": ratio}, 200)
	if !active {
		row = s.call("update_item_category", map[string]any{"id": row["id"], "name": name, "performance_ratio": ratio, "active": false}, 200)
	}
	return row
}

func TestItemCategoryRoundTripAndAudit(t *testing.T) {
	s := open(t)
	category := s.category("烫染", 90, true)
	if category["performance_ratio"] != float64(90) || category["active"] != true {
		t.Fatal(category)
	}
	in := input("烫发")
	in["category_id"] = category["id"]
	item := s.call("create_item", in, 200)
	if item["category"].(map[string]any)["id"] != category["id"] || item["category"].(map[string]any)["name"] != "烫染" {
		t.Fatal(item)
	}
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "list_item_categories", map[string]any{})
		if r.Status != 200 || len(r.Body["items"].([]any)) != 1 || r.Body["items"].([]any)[0].(map[string]any)["name"] != "烫染" {
			t.Fatal(ch.Name, r)
		}
		r = ch.Call(t, s.MustCaller(s.Owner), "list_items", map[string]any{})
		if r.Status != 200 || r.Body["items"].([]any)[0].(map[string]any)["category"].(map[string]any)["name"] != "烫染" {
			t.Fatal(ch.Name, r)
		}
	}
	edited := s.call("update_item_category", map[string]any{"id": category["id"], "name": "烫染护理", "performance_ratio": 80, "active": false}, 200)
	if edited["name"] != "烫染护理" || edited["performance_ratio"] != float64(80) || edited["active"] != false {
		t.Fatal(edited)
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	rows, err := tx.Query(context.Background(), "SELECT action, target_kind, before, after FROM audit_log WHERE target_id=$1 ORDER BY at", category["id"])
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	actions := []string{}
	for rows.Next() {
		var action, kind string
		var before, after []byte
		if err := rows.Scan(&action, &kind, &before, &after); err != nil {
			t.Fatal(err)
		}
		if kind != "item_category" {
			t.Fatal(kind)
		}
		actions = append(actions, action)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	if !reflect.DeepEqual(actions, []string{"create_item_category", "update_item_category"}) {
		t.Fatal(actions)
	}
}

func TestItemCategoryRules(t *testing.T) {
	s := open(t)
	category := s.category("烫染", 90, true)
	if r := s.call("create_item_category", map[string]any{"name": "烫染", "performance_ratio": 100}, 409); r["detail"] != "类别名称重复" {
		t.Fatal(r)
	}
	if r := s.call("create_item_category", map[string]any{"name": "   ", "performance_ratio": 100}, 409); r["detail"] != "类别名称不能为空" {
		t.Fatal(r)
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	otherStore, err := dbgen.New(tx).CreateStore(context.Background(), dbgen.CreateStoreParams{TenantID: s.Res.TenantID, Name: "同租户别店"})
	if err != nil {
		t.Fatal(err)
	}
	var foreignID uuid.UUID
	if err := tx.QueryRow(context.Background(), "INSERT INTO item_categories(tenant_id,store_id,name) VALUES($1,$2,'外店类别') RETURNING id", s.Res.TenantID, otherStore.ID).Scan(&foreignID); err != nil {
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)
	in := input("烫发")
	in["category_id"] = foreignID
	if r := s.call("create_item", in, 404); r["detail"] != "not found" {
		t.Fatal(r)
	}
	if r := s.call("update_item_category", map[string]any{"id": foreignID, "name": "外店类别", "performance_ratio": 100, "active": true}, 404); r["detail"] != "not found" {
		t.Fatal(r)
	}
	in["category_id"] = category["id"]
	item := s.call("create_item", in, 200)
	in["category_id"] = foreignID
	if r := s.call("update_item", update(in, item["id"]), 404); r["detail"] != "not found" {
		t.Fatal(r)
	}
	in["category_id"] = category["id"]
	s.call("update_item_category", map[string]any{"id": category["id"], "name": "烫染", "performance_ratio": 90, "active": false}, 200)
	if r := s.call("create_item", in, 409); r["detail"] != "类别已停用" {
		t.Fatal(r)
	}
	kept := update(in, item["id"])
	kept["name"] = "烫发改名"
	kept["category_id"] = category["id"]
	after := s.call("update_item", kept, 200)
	if after["category"].(map[string]any)["id"] != category["id"] {
		t.Fatal(after)
	}
	delete(kept, "category_id")
	cleared := s.call("update_item", kept, 200)
	if cleared["category"] != nil {
		t.Fatal(cleared)
	}
}

func TestItemCategoryPermissionsAndDraft(t *testing.T) {
	s := open(t)
	_, token := s.Hire("发型师", "002", "stylist", s.Role("发型师"))
	args := map[string]any{"name": "提成类别", "performance_ratio": 90}
	if r := s.HTTPWithToken(t, token, "create_item_category", args); r.Status != 403 || r.Detail() != "permission denied" {
		t.Fatal(r)
	}
	before := s.call("list_item_categories", map[string]any{}, 200)
	r := s.AI(t, s.MustCaller(s.Owner), "create_item_category", args)
	optest.RequireDraftCreated(t, r, reflect.DeepEqual(before, s.call("list_item_categories", map[string]any{}, 200)))
	created := s.category("正常类别", 100, true)
	edited := map[string]any{"id": created["id"], "name": "正常类别改", "performance_ratio": 80, "active": true}
	if r := s.HTTPWithToken(t, token, "update_item_category", edited); r.Status != 403 || r.Detail() != "permission denied" {
		t.Fatal(r)
	}
	before = s.call("list_item_categories", map[string]any{}, 200)
	r = s.AI(t, s.MustCaller(s.Owner), "update_item_category", edited)
	optest.RequireDraftCreated(t, r, reflect.DeepEqual(before, s.call("list_item_categories", map[string]any{}, 200)))
}

type categoryQueryCounter struct{ count atomic.Int64 }

func (c *categoryQueryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.count.Add(1)
	return ctx
}

func (*categoryQueryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestListItemCategoriesQueryCount(t *testing.T) {
	s := open(t)
	category := s.category("批量类别", 90, true)
	in := input("批量项目一")
	in["category_id"] = category["id"]
	s.call("create_item", in, 200)
	counter := &categoryQueryCounter{}
	config := s.Pool.Config().Copy()
	config.ConnConfig.Tracer = counter
	pool, err := db.NewPoolWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	app := optest.New(t, pool)
	caller := s.MustCaller(s.Owner)
	counter.count.Store(0)
	if r := app.HTTP(t, &caller, "list_items", map[string]any{}); r.Status != 200 {
		t.Fatal(r)
	}
	one := counter.count.Load()
	for i := range 5 {
		next := input("批量项目" + string(rune('二'+i)))
		next["category_id"] = category["id"]
		s.call("create_item", next, 200)
	}
	counter.count.Store(0)
	r := app.HTTP(t, &caller, "list_items", map[string]any{})
	if r.Status != 200 || len(r.Body["items"].([]any)) != 6 {
		t.Fatal(r)
	}
	if many := counter.count.Load(); many != one {
		t.Fatalf("queries grew: %d -> %d", one, many)
	}
}

func TestItemCategoryMigrationDown(t *testing.T) {
	s := open(t)
	category := s.category("回滚类别", 90, true)
	in := input("回滚项目")
	in["category_id"] = category["id"]
	s.call("create_item", in, 200)
	dbtest.MigrateDownTo(t, s.Pool, 53)
	var n int
	if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM information_schema.columns WHERE table_name IN ('items','order_items','performance_entries') AND column_name IN ('category_id','category_ratio')").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM information_schema.tables WHERE table_name='item_categories'").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM audit_log WHERE target_kind='item_category'").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if err := db.MigrateUp(context.Background(), s.Pool); err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM information_schema.columns WHERE table_name IN ('items','order_items','performance_entries') AND column_name IN ('category_id','category_ratio')").Scan(&n); err != nil || n != 4 {
		t.Fatal(n, err)
	}
}

func TestItemCategoryDatabaseProtections(t *testing.T) {
	s := open(t)
	s.category("数据库类别", 90, true)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var grants, rls bool
	if err := tx.QueryRow(context.Background(), `SELECT has_table_privilege('salon_app','item_categories','SELECT') AND has_table_privilege('salon_app','item_categories','INSERT') AND has_table_privilege('salon_app','item_categories','UPDATE') AND NOT has_table_privilege('salon_app','item_categories','DELETE')`).Scan(&grants); err != nil || !grants {
		t.Fatal(grants, err)
	}
	if err := tx.QueryRow(context.Background(), `SELECT relrowsecurity FROM pg_class WHERE oid='item_categories'::regclass`).Scan(&rls); err != nil || !rls {
		t.Fatal(rls, err)
	}
	var ratio int16
	if err := tx.QueryRow(context.Background(), `INSERT INTO item_categories(tenant_id,store_id,name) VALUES($1,$2,'默认比例') RETURNING performance_ratio`, s.Res.TenantID, s.Res.StoreID).Scan(&ratio); err != nil || ratio != 100 {
		t.Fatal(ratio, err)
	}
	dbtest.Rollback(t, tx)
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, err := tx.Exec(context.Background(), `INSERT INTO item_categories(tenant_id,store_id,name) SELECT tenant_id,store_id,name FROM item_categories LIMIT 1`)
	dbtest.Rollback(t, tx)
	var pg interface{ SQLState() string }
	if !errors.As(err, &pg) || pg.SQLState() != "23505" {
		t.Fatal(err)
	}
}
