package groupbuy_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/groupbuy"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/orders"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

func TestDealCatalogue(t *testing.T) {
	e := newOrderEnv(t)
	item := e.item("团购项目", "298.00")
	deal := e.deal("douyin", "团购商品", "99.00", item)
	list := e.Call(t, e.Owner, "list_groupbuy_deals", groupbuy.ListDealsInput{}, 200)
	items := list.Body["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != deal.String() || items[0].(map[string]any)["item_name"] != "团购项目" || items[0].(map[string]any)["purchase_price"] != "99.00" || items[0].(map[string]any)["active"] != true {
		t.Fatal(list.Body)
	}
	updated := e.Call(t, e.Owner, "update_groupbuy_deal", groupbuy.UpdateDealInput{ID: deal, Name: "改名团购", PurchasePrice: amount("120.00"), ItemID: item, Active: false}, 200)
	if updated.Body["name"] != "改名团购" || updated.Body["purchase_price"] != "120.00" || updated.Body["active"] != false || updated.Body["platform"] != "douyin" {
		t.Fatal(updated.Body)
	}
	duplicate := e.Call(t, e.Owner, "create_groupbuy_deal", groupbuy.CreateDealInput{Platform: "douyin", Name: "改名团购", PurchasePrice: amount("1"), ItemID: item}, 0)
	rejectionDetail(t, duplicate, 409, "团购商品名称已存在")
	blank := e.Call(t, e.Owner, "create_groupbuy_deal", groupbuy.CreateDealInput{Platform: "douyin", Name: "   ", PurchasePrice: amount("1"), ItemID: item}, 0)
	rejectionDetail(t, blank, 409, "团购平台和商品名称不能为空")
	blankUpdate := e.Call(t, e.Owner, "update_groupbuy_deal", groupbuy.UpdateDealInput{ID: deal, Name: "   ", PurchasePrice: amount("120.00"), ItemID: item, Active: true}, 0)
	rejectionDetail(t, blankUpdate, 409, "团购商品名称不能为空")
}

func TestDealChangesDoNotAlterLinkedSnapshots(t *testing.T) {
	e := newOrderEnv(t)
	item := e.item("原项目", "298.00")
	deal := e.deal("douyin", "快照券", "99.00", item)
	c := e.coupon("snapshot")
	order := e.order(e.item("履约", "398.00"))
	if r := e.link(c.id, deal, order); r.Status != 200 {
		t.Fatal(r)
	}
	e.Call(t, e.Owner, "update_groupbuy_deal", groupbuy.UpdateDealInput{ID: deal, Name: "快照券", PurchasePrice: amount("120.00"), ItemID: item, Active: true}, 200)
	view := e.redemption(c)
	if view["purchase_price"] != "99.00" || view["store_price"] != "298.00" {
		t.Fatal(view)
	}
}

func TestGroupbuyOrderUsesRedemptionPlatform(t *testing.T) {
	e := newOrderEnv(t)
	deal := e.deal("douyin", "平台快照券", "99.00", e.item("平台快照项目", "298.00"))
	c := e.coupon("order-platform")
	order := e.order(e.item("履约", "398.00"))
	if r := e.link(c.id, deal, order); r.Status != 200 {
		t.Fatal(r)
	}
	if _, err := e.Pool.Exec(context.Background(), "UPDATE groupbuy_deals SET platform='meituan' WHERE id=$1", deal); err != nil {
		t.Fatal(err)
	}
	deals := e.Call(t, e.Owner, "list_groupbuy_deals", groupbuy.ListDealsInput{}, 200).Body["items"].([]any)
	if deals[0].(map[string]any)["platform"] != "meituan" {
		t.Fatal(deals)
	}
	view := e.Call(t, e.Owner, "get_order", orders.GetOrderInput{ID: order}, 200).Body
	if view["groupbuy"].([]any)[0].(map[string]any)["platform"] != "douyin" {
		t.Fatal(view)
	}
}

func TestDealPermissions(t *testing.T) {
	e := newOrderEnv(t)
	item := e.item("权限项目", "298.00")
	front, token := e.Hire("前台", "003", "front", e.Res.Roles["前台"])
	if token == "" || front == uuid.Nil {
		t.Fatal("hire failed")
	}
	frontCaller := e.MustCaller(token)
	rejectionDetail(t, e.HTTP(t, &frontCaller, "create_groupbuy_deal", groupbuy.CreateDealInput{Platform: "douyin", Name: "无权限", PurchasePrice: amount("1"), ItemID: item}), 403, "permission denied")
	if r := e.HTTP(t, &frontCaller, "list_groupbuy_deals", groupbuy.ListDealsInput{}); r.Status != 200 {
		t.Fatal(r)
	}
	redeemOnly := e.MustCaller(e.Owner)
	delete(redeemOnly.Permissions, "order.create")
	deal := e.deal("douyin", "双权限券", "99.00", item)
	c := e.coupon("dual-permission")
	order := e.order(e.item("履约", "100.00"))
	rejectionDetail(t, e.HTTP(t, &redeemOnly, "link_coupon_order", groupbuy.LinkCouponOrderInput{RedemptionID: c.id, DealID: deal, OrderID: order}), 403, "permission denied")
}

func TestGroupbuyAIAndChannels(t *testing.T) {
	e := newOrderEnv(t)
	item := e.item("AI项目", "298.00")
	deal := e.deal("douyin", "AI券", "99.00", item)
	c := e.coupon("ai-link")
	order := e.order(e.item("履约", "100.00"))
	before := dbtest.Snapshot(t, e.Pool, e.Res.TenantID, "coupon_redemptions", "orders", "audit_log")
	optest.RequireDraftCreated(t, e.AI(t, e.caller, "link_coupon_order", groupbuy.LinkCouponOrderInput{RedemptionID: c.id, DealID: deal, OrderID: order}), before == dbtest.Snapshot(t, e.Pool, e.Res.TenantID, "coupon_redemptions", "orders", "audit_log"))
	for _, ch := range e.Channels() {
		if r := ch.Call(t, e.caller, "list_groupbuy_deals", groupbuy.ListDealsInput{}); r.Status != 200 || len(r.Body["items"].([]any)) != 1 {
			t.Fatal(ch.Name, r)
		}
		if r := ch.Call(t, e.caller, "get_groupbuy_month", groupbuy.GroupbuyMonthInput{Platform: "douyin", Month: settings.Date(e.Now()).Format("2006-01")}); r.Status != 200 {
			t.Fatal(ch.Name, r)
		}
	}
}

func TestGroupbuySettlementPermissions(t *testing.T) {
	e := newOrderEnv(t)
	reader := e.MustCaller(e.Owner)
	delete(reader.Permissions, perm.ReportView)
	rejectionDetail(t, e.HTTP(t, &reader, "get_groupbuy_month", groupbuy.GroupbuyMonthInput{Platform: "douyin", Month: "2026-11"}), 403, "permission denied")
	writer := e.MustCaller(e.Owner)
	delete(writer.Permissions, perm.ClosingDaily)
	rejectionDetail(t, e.HTTP(t, &writer, "record_groupbuy_settlement", groupbuy.RecordSettlementInput{Platform: "douyin", Month: "2026-11", Amount: amount("1")}), 403, "permission denied")
	rejectionDetail(t, e.Call(t, e.Owner, "get_groupbuy_month", groupbuy.GroupbuyMonthInput{Platform: "   ", Month: "2026-11"}, 0), 409, "团购平台不能为空")
}

func TestGroupbuyUnlinkErrors(t *testing.T) {
	e := newOrderEnv(t)
	unlinked := e.coupon("unlink-none")
	rejectionDetail(t, e.Call(t, e.Owner, "unlink_coupon_order", groupbuy.UnlinkCouponOrderInput{RedemptionID: unlinked.id}, 0), 409, "团购券没有关联订单")
	revoked := e.coupon("unlink-revoked")
	e.Call(t, e.Owner, "revoke_coupon", groupbuy.LookupInput{Key: revoked.key}, 200)
	rejectionDetail(t, e.Call(t, e.Owner, "unlink_coupon_order", groupbuy.UnlinkCouponOrderInput{RedemptionID: revoked.id}, 0), 409, "只有已核销的团购券可以取消关联")
}

type groupbuyQueryCounter struct{ all atomic.Int64 }

func (c *groupbuyQueryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.all.Add(1)
	return ctx
}
func (*groupbuyQueryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func countedApp(t *testing.T, e *orderEnv, counter *groupbuyQueryCounter) *optest.App {
	t.Helper()
	config := e.Pool.Config().Copy()
	config.ConnConfig.Tracer = counter
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	return optest.New(t, pool)
}

func TestListDealsQueryCount(t *testing.T) {
	e := newOrderEnv(t)
	item := e.item("查询项目", "298.00")
	e.deal("douyin", "一个", "1.00", item)
	counter := &groupbuyQueryCounter{}
	app := countedApp(t, e, counter)
	counts := []int64{}
	for i := range 2 {
		if i == 1 {
			e.deal("douyin", "两个", "2.00", item)
			e.deal("meituan", "三个", "3.00", item)
		}
		counter.all.Store(0)
		r := app.Call(t, e.Owner, "list_groupbuy_deals", groupbuy.ListDealsInput{}, 200)
		if len(r.Body["items"].([]any)) != []int{1, 3}[i] {
			t.Fatal(r)
		}
		counts = append(counts, counter.all.Load())
	}
	if counts[0] != counts[1] {
		t.Fatal("deal list query count grew", counts)
	}
}

func TestGetGroupbuyMonthQueryCount(t *testing.T) {
	e := newOrderEnv(t)
	deal := e.deal("douyin", "查询券", "99.00", e.item("查询券项目", "298.00"))
	order := e.order(e.item("履约", "100.00"))
	counter := &groupbuyQueryCounter{}
	app := countedApp(t, e, counter)
	counts := []int64{}
	for i := range 2 {
		if i == 1 {
			for range 4 {
				c := e.coupon(uuid.NewString())
				if r := e.link(c.id, deal, order); r.Status != 200 {
					t.Fatal(r)
				}
			}
		}
		counter.all.Store(0)
		r := app.Call(t, e.Owner, "get_groupbuy_month", groupbuy.GroupbuyMonthInput{Platform: "douyin", Month: settings.Date(e.Now()).Format("2006-01")}, 200)
		want := 0
		if i == 1 {
			want = 4
		}
		if r.Body["linked_open"].(map[string]any)["count"] != float64(want) {
			t.Fatal(r)
		}
		counts = append(counts, counter.all.Load())
	}
	if counts[0] != counts[1] {
		t.Fatal("month query count grew", counts)
	}
}

func TestGroupbuyTenantIsolationAndForeignKeys(t *testing.T) {
	e := newOrderEnv(t)
	item := e.item("本店项目", "298.00")
	_ = e.deal("douyin", "本店券", "99.00", item)
	order := e.order(e.item("履约", "100.00"))
	other, err := staff.Bootstrap(context.Background(), e.Pool, staff.BootstrapInput{TenantName: "另一租户", StoreName: "另一门店", OwnerName: "另一老板", OwnerNumber: "001", OwnerAccount: "other", OwnerPassword: "other-password"})
	if err != nil {
		t.Fatal(err)
	}
	var otherItem, otherDeal, otherRedemption uuid.UUID
	tx := dbtest.AppTx(t, e.Pool, other.TenantID)
	res, err := dbgen.New(tx).CreateItem(context.Background(), dbgen.CreateItemParams{TenantID: other.TenantID, StoreID: other.StoreID, Name: "外店项目", Price: amount("298.00"), Discountable: true, Kind: "service"})
	if err != nil {
		t.Fatal(err)
	}
	otherItem = res.ID
	resDeal, err := dbgen.New(tx).CreateGroupbuyDeal(context.Background(), dbgen.CreateGroupbuyDealParams{TenantID: other.TenantID, StoreID: other.StoreID, Platform: "douyin", Name: "外店券", PurchasePrice: amount("99.00"), ItemID: otherItem, CreatedBy: other.OwnerID})
	if err != nil {
		t.Fatal(err)
	}
	otherDeal = resDeal.ID
	if err := tx.QueryRow(context.Background(), "INSERT INTO coupon_redemptions (tenant_id,store_id,platform,coupon_code,idempotency_key,status,redeemed_by,deal_id,purchase_price,store_price,created_at) VALUES ($1,$2,'douyin','other-cross','other-cross','redeemed',$3,$4,99,298,clock_timestamp()) RETURNING id", other.TenantID, other.StoreID, other.OwnerID, otherDeal).Scan(&otherRedemption); err != nil {
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)

	tx = dbtest.AppTx(t, e.Pool, e.Res.TenantID)
	var visible int
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM groupbuy_deals WHERE id=$1", otherDeal).Scan(&visible); err != nil || visible != 0 {
		t.Fatal("other tenant deal visible", visible, err)
	}
	dbtest.Rollback(t, tx)
	checkout := guid(e.checkout(order, cash("100.00"))["checkout_id"])
	for _, tc := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO coupon_redemptions (tenant_id,store_id,platform,coupon_code,idempotency_key,status,redeemed_by,order_id,deal_id,purchase_price,store_price,linked_at,created_at) VALUES ($1,$2,'douyin','cross','cross','redeemed',$3,$4,$5,99,298,clock_timestamp(),clock_timestamp())", []any{e.Res.TenantID, e.Res.StoreID, e.Res.OwnerID, order, otherDeal}},
		{"INSERT INTO groupbuy_deals (tenant_id,store_id,platform,name,purchase_price,item_id,created_by) VALUES ($1,$2,'douyin','跨租户项目',1,$3,$4)", []any{e.Res.TenantID, e.Res.StoreID, otherItem, e.Res.OwnerID}},
		{"INSERT INTO groupbuy_settlements (tenant_id,store_id,platform,month,amount,entered_by) VALUES ($1,$2,'douyin','2026-11',1,$3)", []any{e.Res.TenantID, e.Res.StoreID, other.OwnerID}},
		{"INSERT INTO order_payments (tenant_id,store_id,checkout_id,order_id,method,amount,coupon_redemption_id) VALUES ($1,$2,$3,$4,'groupbuy',1,$5)", []any{e.Res.TenantID, e.Res.StoreID, checkout, order, otherRedemption}},
	} {
		tx := dbtest.AppTx(t, e.Pool, e.Res.TenantID)
		_, err := tx.Exec(context.Background(), tc.sql, tc.args...)
		assertPGError(t, err, "23503")
		dbtest.Rollback(t, tx)
	}
}

func assertPGError(t *testing.T, err error, code string) {
	t.Helper()
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != code {
		t.Fatalf("want SQLSTATE %s, got %v", code, err)
	}
}

func TestGroupbuyDatabaseConstraints(t *testing.T) {
	e := newOrderEnv(t)
	c := e.coupon("db-constraints")
	deal := e.deal("douyin", "约束券", "99.00", e.item("约束券项目", "298.00"))
	order := e.order(e.item("约束履约", "100.00"))
	checkout := guid(e.checkout(order, cash("100.00"))["checkout_id"])
	tx := dbtest.AppTx(t, e.Pool, e.Res.TenantID)
	var payment uuid.UUID
	if err := tx.QueryRow(context.Background(), "SELECT id FROM order_payments WHERE order_id=$1 AND method='cash' LIMIT 1", order).Scan(&payment); err != nil {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	for _, tc := range []struct {
		name string
		sql  string
		args []any
		code string
	}{
		{"groupbuy without coupon", "INSERT INTO order_payments (tenant_id,store_id,checkout_id,order_id,method,amount) VALUES ($1,$2,$3,$4,'groupbuy',1)", []any{e.Res.TenantID, e.Res.StoreID, checkout, order}, "23514"},
		{"cash with coupon", "INSERT INTO order_payments (tenant_id,store_id,checkout_id,order_id,method,amount,coupon_redemption_id) VALUES ($1,$2,$3,$4,'cash',1,$5)", []any{e.Res.TenantID, e.Res.StoreID, checkout, order, c.id}, "23514"},
		{"link without deal", "UPDATE coupon_redemptions SET order_id=$1 WHERE id=$2", []any{order, c.id}, "23514"},
		{"linked at without order", "UPDATE coupon_redemptions SET deal_id=$1,purchase_price=99,store_price=298,linked_at=clock_timestamp() WHERE id=$2", []any{deal, c.id}, "23514"},
		{"payment without order", "UPDATE coupon_redemptions SET deal_id=$1,purchase_price=99,store_price=298,order_payment_id=$2 WHERE id=$3", []any{deal, payment, c.id}, "23514"},
		{"bad month", "INSERT INTO groupbuy_settlements (tenant_id,store_id,platform,month,amount,entered_by) VALUES ($1,$2,'douyin','2026-13',1,$3)", []any{e.Res.TenantID, e.Res.StoreID, e.Res.OwnerID}, "23514"},
		{"negative amount", "INSERT INTO groupbuy_settlements (tenant_id,store_id,platform,month,amount,entered_by) VALUES ($1,$2,'douyin','2026-11',-1,$3)", []any{e.Res.TenantID, e.Res.StoreID, e.Res.OwnerID}, "23514"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := dbtest.AppTx(t, e.Pool, e.Res.TenantID)
			_, err := tx.Exec(context.Background(), tc.sql, tc.args...)
			assertPGError(t, err, tc.code)
			dbtest.Rollback(t, tx)
		})
	}
	t.Run("deal snapshot without order allowed", func(t *testing.T) {
		other := e.coupon("db-snapshot-allowed")
		tx := dbtest.AppTx(t, e.Pool, e.Res.TenantID)
		_, err := tx.Exec(context.Background(), "UPDATE coupon_redemptions SET deal_id=$1,purchase_price=99,store_price=298 WHERE id=$2", deal, other.id)
		if err != nil {
			t.Fatal(err)
		}
		dbtest.Rollback(t, tx)
	})
	t.Run("current payment unique", func(t *testing.T) {
		first, second := e.coupon("db-unique-a"), e.coupon("db-unique-b")
		update := "UPDATE coupon_redemptions SET deal_id=$1,purchase_price=99,store_price=298,order_id=$2,linked_at=clock_timestamp(),order_payment_id=$3 WHERE id=$4"
		tx := dbtest.AppTx(t, e.Pool, e.Res.TenantID)
		if _, err := tx.Exec(context.Background(), update, deal, order, payment, first.id); err != nil {
			t.Fatal(err)
		}
		_, err := tx.Exec(context.Background(), update, deal, order, payment, second.id)
		assertPGError(t, err, "23505")
		dbtest.Rollback(t, tx)
	})
}

func TestGroupbuyOrderMigrationDown(t *testing.T) {
	e := newOrderEnv(t)
	deal := e.deal("douyin", "迁移券", "99.00", e.item("迁移项目", "298.00"))
	c := e.coupon("down")
	order := e.order(e.item("迁移履约", "398.00"))
	if r := e.link(c.id, deal, order); r.Status != 200 {
		t.Fatal(r)
	}
	e.checkout(order, cash("100.00"))
	dbtest.MigrateDownTo(t, e.Pool, 44)
	var gone bool
	if err := e.Pool.QueryRow(context.Background(), `SELECT to_regclass('groupbuy_deals') IS NULL AND to_regclass('groupbuy_settlements') IS NULL AND NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='coupon_redemptions' AND column_name IN ('deal_id','purchase_price','store_price','linked_at','order_payment_id')) AND NOT EXISTS (SELECT 1 FROM order_payments WHERE method='groupbuy') AND NOT EXISTS (SELECT 1 FROM performance_entries WHERE method='groupbuy')`).Scan(&gone); err != nil || !gone {
		t.Fatal(gone, err)
	}
	var auditKinds int
	if err := e.Pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE target_kind IN ('groupbuy_deal','groupbuy_settlement')`).Scan(&auditKinds); err != nil || auditKinds != 0 {
		t.Fatal("groupbuy audit kinds not removed", auditKinds, err)
	}
	var grants bool
	if err := e.Pool.QueryRow(context.Background(), `SELECT has_column_privilege('salon_app','coupon_redemptions','status','UPDATE') AND NOT has_column_privilege('salon_app','coupon_redemptions','order_id','UPDATE')`).Scan(&grants); err != nil || !grants {
		t.Fatal("column grants not restored", grants, err)
	}
	tx := dbtest.AppTx(t, e.Pool, e.Res.TenantID)
	_, err := tx.Exec(context.Background(), `INSERT INTO audit_log(tenant_id,store_id,actor_id,action,target_kind,target_id) VALUES($1,$2,$3,'test','groupbuy_deal',$4)`, e.Res.TenantID, e.Res.StoreID, e.Res.OwnerID, uuid.New())
	assertPGError(t, err, "23514")
	dbtest.Rollback(t, tx)
	tx = dbtest.AppTx(t, e.Pool, e.Res.TenantID)
	_, err = tx.Exec(context.Background(), "INSERT INTO order_payments (tenant_id,store_id,checkout_id,order_id,method,amount) SELECT tenant_id,store_id,checkout_id,order_id,'groupbuy',1 FROM order_payments LIMIT 1")
	assertPGError(t, err, "23514")
	dbtest.Rollback(t, tx)
}
