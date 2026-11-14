package groupbuy_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/groupbuy"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

func TestListRedemptionsFiltersAndChannels(t *testing.T) {
	e := newOrderEnv(t)
	day := "2026-11-01"
	deal := e.deal("douyin", "列表券", "99.00", e.item("列表项目", "298.00"))
	order := e.order(e.item("履约", "100.00"))
	unlinked := e.coupon("list-unlinked")
	linked := e.coupon("list-linked")
	if r := e.link(linked.id, deal, order); r.Status != 200 {
		t.Fatal(r)
	}
	tx := dbtest.AppTx(t, e.Pool, e.Res.TenantID)
	store, err := dbgen.New(tx).CreateStore(context.Background(), dbgen.CreateStoreParams{TenantID: e.Res.TenantID, Name: "另一门店"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), `INSERT INTO coupon_redemptions (tenant_id,store_id,platform,coupon_code,idempotency_key,status,redeemed_by,created_at,redeemed_at)
VALUES ($1,$2,'douyin','other-store','other-store','redeemed',$3,$4,$4)`, e.Res.TenantID, store.ID, e.Res.OwnerID, e.Now()); err != nil {
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)
	other, err := staff.Bootstrap(context.Background(), e.Pool, staff.BootstrapInput{TenantName: "另有租户", StoreName: "另一门店", OwnerName: "别老板", OwnerNumber: "001", OwnerAccount: "list-other", OwnerPassword: "list-other-password"})
	if err != nil {
		t.Fatal(err)
	}
	tx = dbtest.AppTx(t, e.Pool, other.TenantID)
	if _, err := tx.Exec(context.Background(), `INSERT INTO coupon_redemptions (tenant_id,store_id,platform,coupon_code,idempotency_key,status,redeemed_by,created_at,redeemed_at)
VALUES ($1,$2,'douyin','other-tenant','other-tenant','redeemed',$3,$4,$4)`, other.TenantID, other.StoreID, other.OwnerID, e.Now()); err != nil {
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)
	tx = dbtest.AppTx(t, e.Pool, e.Res.TenantID)
	if _, err := tx.Exec(context.Background(), `INSERT INTO coupon_redemptions (tenant_id,store_id,platform,coupon_code,idempotency_key,status,redeemed_by,created_at,redeemed_at)
VALUES ($1,$2,'douyin','old-day','old-day','redeemed',$3,'2026-10-31T12:00:00Z','2026-10-31T12:00:00Z')`, e.Res.TenantID, e.Res.StoreID, e.Res.OwnerID); err != nil {
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)
	for _, ch := range e.Channels() {
		all := ch.Call(t, e.caller, "list_redemptions", groupbuy.ListRedemptionsInput{From: day, To: day})
		if all.Status != 200 || len(all.Body["items"].([]any)) != 2 {
			t.Fatalf("%s all = %d %v", ch.Name, all.Status, all.Body)
		}
		for _, raw := range all.Body["items"].([]any) {
			if raw.(map[string]any)["coupon_code"] == "other-store" {
				t.Fatalf("%s other store redemption leaked: %v", ch.Name, raw)
			}
		}
		only := ch.Call(t, e.caller, "list_redemptions", groupbuy.ListRedemptionsInput{From: day, To: day, UnlinkedOnly: true})
		if only.Status != 200 || len(only.Body["items"].([]any)) != 1 || only.Body["items"].([]any)[0].(map[string]any)["id"] != unlinked.id.String() {
			t.Fatalf("%s unlinked = %d %v", ch.Name, only.Status, only.Body)
		}
		revoked := ch.Call(t, e.caller, "list_redemptions", groupbuy.ListRedemptionsInput{From: day, To: day, Status: "revoked"})
		if revoked.Status != 200 || len(revoked.Body["items"].([]any)) != 0 {
			t.Fatalf("%s revoked before = %d %v", ch.Name, revoked.Status, revoked.Body)
		}
	}
	if r := e.Call(t, e.Owner, "revoke_coupon", groupbuy.LookupInput{Key: unlinked.key}, 200); r.Body["status"] != "revoked" {
		t.Fatal(r)
	}
	for _, ch := range e.Channels() {
		revoked := ch.Call(t, e.caller, "list_redemptions", groupbuy.ListRedemptionsInput{From: day, To: day, Status: "revoked"})
		if revoked.Status != 200 || len(revoked.Body["items"].([]any)) != 1 || revoked.Body["items"].([]any)[0].(map[string]any)["id"] != unlinked.id.String() {
			t.Fatalf("%s revoked = %d %v", ch.Name, revoked.Status, revoked.Body)
		}
	}
	caller := e.MustCaller(e.Owner)
	delete(caller.Permissions, perm.GroupBuyRedeem)
	rejectionDetail(t, e.HTTP(t, &caller, "list_redemptions", groupbuy.ListRedemptionsInput{From: day, To: day}), 403, "permission denied")
}

func TestListRedemptionsRejectsBadRange(t *testing.T) {
	e := newOrderEnv(t)
	rejectionDetail(t, e.Call(t, e.Owner, "list_redemptions", groupbuy.ListRedemptionsInput{From: "2026-11-02", To: "2026-11-01"}, 0), 409, "开始营业日不能晚于结束营业日")
	rejectionDetail(t, e.Call(t, e.Owner, "list_redemptions", groupbuy.ListRedemptionsInput{From: "2026-01-01", To: "2026-04-04"}, 0), 409, "查询范围最多 92 天")
}

func TestListRedemptionsQueryCount(t *testing.T) {
	e := newOrderEnv(t)
	e.coupon("count-one")
	counter := &groupbuyQueryCounter{}
	app := countedApp(t, e, counter)
	counts := []int64{}
	for i := range 2 {
		if i == 1 {
			for range 4 {
				e.coupon(uuid.NewString())
			}
		}
		counter.all.Store(0)
		r := app.Call(t, e.Owner, "list_redemptions", groupbuy.ListRedemptionsInput{From: "2026-11-01", To: "2026-11-01"}, 200)
		if len(r.Body["items"].([]any)) != []int{1, 5}[i] {
			t.Fatal(r)
		}
		counts = append(counts, counter.all.Load())
	}
	if counts[0] != counts[1] {
		t.Fatal("redemption list query count grew", counts)
	}
}
