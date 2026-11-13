package groupbuy_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/groupbuy"
	"github.com/wzhlinhai/salon/backend/internal/groupbuy/fakeprovider"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/orders"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

type orderEnv struct {
	*optest.Shop
	t        *testing.T
	fake     *fakeprovider.Fake
	caller   ops.Caller
	position uuid.UUID
	staff    uuid.UUID
	customer uuid.UUID
}

type coupon struct {
	key string
	id  uuid.UUID
}

func newOrderEnv(t *testing.T) *orderEnv {
	t.Helper()
	s, f, c := setup(t)
	e := &orderEnv{Shop: s, t: t, fake: f, caller: c}
	e.position = guid(s.Call(t, s.Owner, "create_position", map[string]any{"name": "大工"}, 200).Body["id"])
	e.staff, _ = s.Hire("员工", "002", "staff")
	e.customer = guid(s.Call(t, s.Owner, "create_customer", map[string]any{"name": "顾客", "phone": "13800000001"}, 200).Body["customer"].(map[string]any)["id"])
	return e
}

func guid(v any) uuid.UUID { return uuid.MustParse(v.(string)) }

func amount(s string) money.Amount {
	var a money.Amount
	if err := a.Scan(s); err != nil {
		panic(err)
	}
	return a
}

func plus(t *testing.T, a, b money.Amount) money.Amount {
	t.Helper()
	out, err := a.Add(b)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func timeAt(year int, month time.Month, day, hour, minute int) time.Time {
	return time.Date(year, month, day, hour, minute, 0, 0, settings.Location)
}

func (e *orderEnv) item(name, price string, shares ...int16) uuid.UUID {
	e.t.Helper()
	positions := []any{}
	for _, share := range shares {
		positions = append(positions, map[string]any{"position_id": e.position, "required": false, "share_percent": share})
	}
	return guid(e.Call(e.t, e.Owner, "create_item", map[string]any{"name": name, "price": price, "discountable": true, "labor_fee_amount": nil, "labor_fee_percent": nil, "positions": positions, "level_prices": []any{}}, 200).Body["id"])
}

func (e *orderEnv) order(item uuid.UUID, staff ...uuid.UUID) uuid.UUID {
	e.t.Helper()
	assigned := []orders.OrderItemStaffInput{}
	for i, id := range staff {
		assigned = append(assigned, orders.OrderItemStaffInput{Slot: int16(i + 1), StaffID: id})
	}
	return guid(e.Call(e.t, e.Owner, "open_order", orders.OpenOrderInput{CustomerID: &e.customer, Gender: "female", Designated: true, Items: []orders.OrderItemInput{{ItemID: item, Staff: assigned}}}, 200).Body["id"])
}

func (e *orderEnv) deal(platform, name, purchase string, item uuid.UUID) uuid.UUID {
	e.t.Helper()
	return guid(e.Call(e.t, e.Owner, "create_groupbuy_deal", groupbuy.CreateDealInput{Platform: platform, Name: name, PurchasePrice: amount(purchase), ItemID: item}, 200).Body["id"])
}

func (e *orderEnv) coupon(key string) coupon {
	e.t.Helper()
	e.Call(e.t, e.Owner, "redeem_coupon", groupbuy.CouponRequest{Platform: "douyin", CouponCode: key, Key: key}, 200)
	body := e.Call(e.t, e.Owner, "get_redemption", groupbuy.LookupInput{Key: key}, 200).Body
	return coupon{key: key, id: guid(body["id"])}
}

func (e *orderEnv) redemption(c coupon) map[string]any {
	e.t.Helper()
	return e.Call(e.t, e.Owner, "get_redemption", groupbuy.LookupInput{Key: c.key}, 200).Body
}

func (e *orderEnv) link(redemption, deal, order uuid.UUID) optest.Result {
	e.t.Helper()
	return e.Call(e.t, e.Owner, "link_coupon_order", groupbuy.LinkCouponOrderInput{RedemptionID: redemption, DealID: deal, OrderID: order}, 0)
}

func cash(s string) orders.PaymentInput {
	return orders.PaymentInput{Method: "cash", Amount: amount(s)}
}

func (e *orderEnv) checkout(order uuid.UUID, payments ...orders.PaymentInput) map[string]any {
	e.t.Helper()
	if payments == nil {
		payments = []orders.PaymentInput{}
	}
	body := e.Call(e.t, e.Owner, "checkout_orders", orders.CheckoutOrdersInput{Orders: []orders.CheckoutOrderInput{{OrderID: order, Payments: payments}}}, 200).Body
	return body["items"].([]any)[0].(map[string]any)
}

func (e *orderEnv) entries() []any {
	e.t.Helper()
	day := settings.Date(e.Now()).Format(settings.DateLayout)
	return e.Call(e.t, e.Owner, "list_performance", map[string]any{"date_from": day, "date_to": day, "limit": 200}, 200).Body["entries"].([]any)
}

func (e *orderEnv) collections(order uuid.UUID) []any {
	e.t.Helper()
	return e.Call(e.t, e.Owner, "list_collections", map[string]any{"order_id": order}, 200).Body["items"].([]any)
}

func rejectionDetail(t *testing.T, r optest.Result, status int, detail string) {
	t.Helper()
	if r.Status != status || !strings.Contains(r.Detail(), detail) {
		t.Fatalf("want %d %s: %+v", status, detail, r)
	}
}

func (e *orderEnv) linkSnapshot() string {
	e.t.Helper()
	return dbtest.Snapshot(e.t, e.Pool, e.Res.TenantID, "coupon_redemptions", "orders", "order_items", "order_item_positions", "audit_log")
}

func (e *orderEnv) assertLinkRefused(caller ops.Caller, in groupbuy.LinkCouponOrderInput, status int, detail string) {
	e.t.Helper()
	before := e.linkSnapshot()
	r := e.HTTP(e.t, &caller, "link_coupon_order", in)
	if r.Status != status || !strings.Contains(r.Detail(), detail) || before != e.linkSnapshot() {
		e.t.Fatalf("want %d %s and no data change: %+v", status, detail, r)
	}
}

func (e *orderEnv) assertUnlinkRefused(caller ops.Caller, in groupbuy.UnlinkCouponOrderInput, status int, detail string) {
	e.t.Helper()
	before := e.linkSnapshot()
	r := e.HTTP(e.t, &caller, "unlink_coupon_order", in)
	if r.Status != status || !strings.Contains(r.Detail(), detail) || before != e.linkSnapshot() {
		e.t.Fatalf("want %d %s and no data change: %+v", status, detail, r)
	}
}

func TestGroupbuyOwnerExample(t *testing.T) {
	e := newOrderEnv(t)
	dealItem := e.item("染发", "298.00")
	deal := e.deal("douyin", "染发团购", "99.00", dealItem)
	c := e.coupon("owner-example")
	performed := e.item("烫发", "398.00", 70, 30)
	order := e.order(performed, e.Res.OwnerID, e.staff)
	if r := e.link(c.id, deal, order); r.Status != 200 {
		t.Fatal(r)
	}
	preview := e.Call(t, e.Owner, "preview_checkout", orders.PreviewCheckoutInput{OrderID: order}, 200).Body
	previewPayments := preview["payments"].([]any)
	if len(previewPayments) != 1 || previewPayments[0].(map[string]any)["method"] != "groupbuy" || previewPayments[0].(map[string]any)["amount"] != "298.00" || preview["net"] != "398.00" {
		t.Fatal(preview)
	}
	paid := e.checkout(order, cash("100.00"))
	if e.redemption(c)["order_payment_id"] == nil {
		t.Fatal("redemption current payment not recorded")
	}
	ps := paid["payments"].([]any)
	if len(ps) != 2 || ps[0].(map[string]any)["method"] != "groupbuy" || ps[0].(map[string]any)["amount"] != "298.00" || ps[0].(map[string]any)["coupon_redemption_id"] != c.id.String() || ps[1].(map[string]any)["method"] != "cash" || ps[1].(map[string]any)["amount"] != "100.00" {
		t.Fatal(ps)
	}
	gb := paid["groupbuy"].([]any)
	if len(gb) != 1 {
		t.Fatal(gb)
	}
	view := gb[0].(map[string]any)
	if view["purchase_price"] != "99.00" || view["store_price"] != "298.00" || view["amount"] != "298.00" || view["deal_name"] != "染发团购" || view["platform"] != "douyin" {
		t.Fatal(view)
	}
	collections := e.collections(order)
	if len(collections) != 1 || collections[0].(map[string]any)["amount"] != "100.00" || collections[0].(map[string]any)["channel"] != "cash" {
		t.Fatal(collections)
	}
	entries := e.entries()
	type slotMethod struct {
		method string
		slot   int
	}
	sum := map[string]money.Amount{}
	slots := map[slotMethod]money.Amount{}
	ratios := map[string]any{}
	for _, raw := range entries {
		entry := raw.(map[string]any)
		method := entry["method"].(string)
		sum[method] = plus(t, sum[method], amount(entry["amount"].(string)))
		key := slotMethod{method: method, slot: int(entry["slot"].(float64))}
		slots[key] = plus(t, slots[key], amount(entry["amount"].(string)))
		ratios[method] = entry["ratio"]
	}
	if len(entries) != 4 || sum["groupbuy"].String() != "298.00" || sum["cash"].String() != "100.00" || ratios["groupbuy"] != float64(100) || ratios["cash"] != float64(100) {
		t.Fatal(entries)
	}
	for key, want := range map[slotMethod]string{
		{method: "groupbuy", slot: 1}: "208.60", {method: "groupbuy", slot: 2}: "89.40",
		{method: "cash", slot: 1}: "70.00", {method: "cash", slot: 2}: "30.00",
	} {
		if slots[key].String() != want {
			t.Fatalf("%+v = %s, want %s", key, slots[key], want)
		}
	}
}

func TestGroupbuyNotCashPerformance(t *testing.T) {
	e := newOrderEnv(t)
	deal := e.deal("douyin", "非现金业绩券", "99.00", e.item("非现金项目", "298.00"))
	c := e.coupon("not-cash")
	order := e.order(e.item("履约", "398.00", 70, 30), e.Res.OwnerID, e.staff)
	if r := e.link(c.id, deal, order); r.Status != 200 {
		t.Fatal(r)
	}
	day := settings.Date(e.Now()).Format(settings.DateLayout)
	e.Call(t, e.Owner, "save_commission_scheme", map[string]any{"effective_from": day, "scheme": map[string]any{"rules": []any{map[string]any{"source": "item", "base": "cash_performance", "by_position": false, "by_designated": false, "by_member": false, "cells": []any{map[string]any{"percent": "10.00"}}}}, "overrides": []any{}, "excluded_staff": []any{}}}, 200)
	e.checkout(order, cash("100.00"))
	body := e.Call(t, e.Owner, "list_commissions", map[string]any{"date_from": day, "date_to": day}, 200).Body
	if body["total"] != "10.00" {
		t.Fatal(body)
	}
}

func TestGroupbuyReductionOrder(t *testing.T) {
	e := newOrderEnv(t)
	first := e.coupon("reduce-first")
	second := e.coupon("reduce-second")
	order := e.order(e.item("减少顺序履约", "300.00"))
	if r := e.link(first.id, e.deal("douyin", "大券", "99.00", e.item("大券项目", "298.00")), order); r.Status != 200 {
		t.Fatal(r)
	}
	e.Advance(time.Second)
	if r := e.link(second.id, e.deal("douyin", "小券", "50.00", e.item("小券项目", "150.00")), order); r.Status != 200 {
		t.Fatal(r)
	}
	paid := e.checkout(order)
	ps := paid["payments"].([]any)
	if len(ps) != 2 || ps[0].(map[string]any)["amount"] != "298.00" || ps[1].(map[string]any)["amount"] != "2.00" {
		t.Fatal(ps)
	}
}

func TestGroupbuyReducedToNet(t *testing.T) {
	e := newOrderEnv(t)
	dealItem := e.item("染发", "298.00")
	deal := e.deal("douyin", "便宜履约", "99.00", dealItem)
	c := e.coupon("cheaper")
	performed := e.item("烫发", "200.00")
	order := e.order(performed)
	if r := e.link(c.id, deal, order); r.Status != 200 {
		t.Fatal(r)
	}
	paid := e.checkout(order)
	ps := paid["payments"].([]any)
	if len(ps) != 1 || ps[0].(map[string]any)["method"] != "groupbuy" || ps[0].(map[string]any)["amount"] != "200.00" {
		t.Fatal(ps)
	}
	if view := paid["groupbuy"].([]any)[0].(map[string]any); view["store_price"] != "298.00" || view["amount"] != "200.00" {
		t.Fatal(view)
	}
	if len(e.collections(order)) != 0 {
		t.Fatal("groupbuy produced a collection")
	}
	entries := e.entries()
	if len(entries) != 1 || entries[0].(map[string]any)["method"] != "groupbuy" || entries[0].(map[string]any)["amount"] != "200.00" {
		t.Fatal(entries)
	}
}

func TestGroupbuyTwoCoupons(t *testing.T) {
	e := newOrderEnv(t)
	item := e.item("组合履约", "500.00")
	first := e.coupon("two-a")
	second := e.coupon("two-b")
	order := e.order(item)
	if r := e.link(first.id, e.deal("douyin", "券一", "99.00", e.item("券一项目", "298.00")), order); r.Status != 200 {
		t.Fatal(r)
	}
	if r := e.link(second.id, e.deal("douyin", "券二", "50.00", e.item("券二项目", "150.00")), order); r.Status != 200 {
		t.Fatal(r)
	}
	paid := e.checkout(order, cash("52.00"))
	ps := paid["payments"].([]any)
	if len(ps) != 3 || ps[0].(map[string]any)["amount"] != "298.00" || ps[1].(map[string]any)["amount"] != "150.00" || ps[2].(map[string]any)["method"] != "cash" || ps[2].(map[string]any)["amount"] != "52.00" {
		t.Fatal(ps)
	}
	if len(paid["groupbuy"].([]any)) != 2 {
		t.Fatal(paid)
	}
}

func TestGroupbuyLinkRules(t *testing.T) {
	t.Run("wrong store", func(t *testing.T) {
		e := newOrderEnv(t)
		c := e.coupon("wrong-store")
		deal := e.deal("douyin", "外店券", "99.00", e.item("外店项目", "298.00"))
		order := e.order(e.item("履约", "100.00"))
		other := e.caller
		tx := dbtest.AppTx(t, e.Pool, e.Res.TenantID)
		store, err := dbgen.New(tx).CreateStore(context.Background(), dbgen.CreateStoreParams{TenantID: e.Res.TenantID, Name: "另一门店"})
		dbtest.Commit(t, tx)
		if err != nil {
			t.Fatal(err)
		}
		other.StoreID = store.ID
		e.assertLinkRefused(other, groupbuy.LinkCouponOrderInput{RedemptionID: c.id, DealID: deal, OrderID: order}, 404, "not found")
	})
	t.Run("not redeemed", func(t *testing.T) {
		e := newOrderEnv(t)
		e.fake.Replies["redeem"] = []fakeprovider.Reply{{Err: groupbuy.ErrUnknown}}
		key := "not-redeemed"
		rejectionDetail(t, e.HTTP(t, &e.caller, "redeem_coupon", groupbuy.CouponRequest{Platform: "douyin", CouponCode: key, Key: key}), 409, "结果未知")
		c := coupon{key: key, id: guid(e.Call(t, e.Owner, "get_redemption", groupbuy.LookupInput{Key: key}, 200).Body["id"])}
		deal := e.deal("douyin", "未核销券", "99.00", e.item("未核销项目", "298.00"))
		order := e.order(e.item("履约", "100.00"))
		e.assertLinkRefused(e.caller, groupbuy.LinkCouponOrderInput{RedemptionID: c.id, DealID: deal, OrderID: order}, 409, "只有已核销的团购券可以关联订单")
	})
	t.Run("already linked", func(t *testing.T) {
		e := newOrderEnv(t)
		c := e.coupon("already-linked")
		deal := e.deal("douyin", "已关联券", "99.00", e.item("已关联项目", "298.00"))
		first, second := e.order(e.item("履约一", "100.00")), e.order(e.item("履约二", "100.00"))
		if r := e.link(c.id, deal, first); r.Status != 200 {
			t.Fatal(r)
		}
		e.assertLinkRefused(e.caller, groupbuy.LinkCouponOrderInput{RedemptionID: c.id, DealID: deal, OrderID: second}, 409, "团购券已关联其他单")
	})
	t.Run("order paid", func(t *testing.T) {
		e := newOrderEnv(t)
		c := e.coupon("order-paid")
		deal := e.deal("douyin", "已结账券", "99.00", e.item("已结账项目", "298.00"))
		order := e.order(e.item("履约", "100.00"))
		e.checkout(order, cash("100.00"))
		e.assertLinkRefused(e.caller, groupbuy.LinkCouponOrderInput{RedemptionID: c.id, DealID: deal, OrderID: order}, 409, "单已结账")
	})
	t.Run("deal inactive", func(t *testing.T) {
		e := newOrderEnv(t)
		c := e.coupon("deal-inactive")
		dealItem := e.item("停用项目", "298.00")
		deal := e.deal("douyin", "停用券", "99.00", dealItem)
		e.Call(t, e.Owner, "update_groupbuy_deal", groupbuy.UpdateDealInput{ID: deal, Name: "停用券", PurchasePrice: amount("99.00"), ItemID: dealItem, Active: false}, 200)
		order := e.order(e.item("履约", "100.00"))
		e.assertLinkRefused(e.caller, groupbuy.LinkCouponOrderInput{RedemptionID: c.id, DealID: deal, OrderID: order}, 409, "团购商品已停用")
	})
	t.Run("other platform", func(t *testing.T) {
		e := newOrderEnv(t)
		c := e.coupon("other-platform")
		deal := e.deal("meituan", "平台不一致", "99.00", e.item("平台项目", "298.00"))
		order := e.order(e.item("履约", "100.00"))
		e.assertLinkRefused(e.caller, groupbuy.LinkCouponOrderInput{RedemptionID: c.id, DealID: deal, OrderID: order}, 409, "团购平台不一致")
	})
}

func TestGroupbuyUnlinkOnlyOpen(t *testing.T) {
	e := newOrderEnv(t)
	c := e.coupon("unlink-open")
	deal := e.deal("douyin", "取消关联", "99.00", e.item("关联项目", "298.00"))
	order := e.order(e.item("履约", "398.00"))
	if r := e.link(c.id, deal, order); r.Status != 200 {
		t.Fatal(r)
	}
	if r := e.Call(t, e.Owner, "unlink_coupon_order", groupbuy.UnlinkCouponOrderInput{RedemptionID: c.id}, 200); r.Body["order_id"] != nil || r.Body["linked_at"] != nil || r.Body["order_payment_id"] != nil || r.Body["deal_id"] != deal.String() || r.Body["store_price"] != "298.00" {
		t.Fatal(r.Body)
	}
	if r := e.link(c.id, deal, order); r.Status != 200 {
		t.Fatal(r)
	}
	e.checkout(order, cash("100.00"))
	e.assertUnlinkRefused(e.caller, groupbuy.UnlinkCouponOrderInput{RedemptionID: c.id}, 409, "单已结账")
}

func TestGroupbuyLinkAudit(t *testing.T) {
	e := newOrderEnv(t)
	c := e.coupon("link-audit")
	deal := e.deal("douyin", "审计券", "99.00", e.item("审计项目", "298.00"))
	order := e.order(e.item("履约", "398.00"))
	if r := e.link(c.id, deal, order); r.Status != 200 {
		t.Fatal(r)
	}
	assertAction := func(action string) {
		t.Helper()
		audits := e.Call(t, e.Owner, "list_audit_log", map[string]any{"target_id": c.id}, 200).Body["entries"].([]any)
		found := false
		for _, raw := range audits {
			entry := raw.(map[string]any)
			if entry["action"] == action && entry["target_kind"] == "coupon_redemption" {
				found = true
			}
		}
		if !found {
			t.Fatal(action, audits)
		}
	}
	assertAction("link_coupon_order")
	orderAudits := e.Call(t, e.Owner, "list_audit_log", map[string]any{"target_id": order}, 200).Body["entries"].([]any)
	orderFound := false
	for _, raw := range orderAudits {
		if raw.(map[string]any)["action"] == "link_coupon_order" {
			orderFound = true
		}
	}
	if !orderFound {
		t.Fatal(orderAudits)
	}
	e.Call(t, e.Owner, "unlink_coupon_order", groupbuy.UnlinkCouponOrderInput{RedemptionID: c.id}, 200)
	assertAction("unlink_coupon_order")
}

func TestGroupbuyLinkWaitsForOrder(t *testing.T) {
	e := newOrderEnv(t)
	c := e.coupon("link-lock")
	deal := e.deal("douyin", "锁券", "99.00", e.item("锁项目", "298.00"))
	order := e.order(e.item("履约", "398.00"))
	held := dbtest.AppTx(t, e.Pool, e.Res.TenantID)
	dbtest.Exec(t, held, "SELECT id FROM orders WHERE id=$1 FOR NO KEY UPDATE", order)
	done := make(chan optest.Result, 1)
	go func() {
		done <- e.HTTPWithToken(t, e.Owner, "link_coupon_order", groupbuy.LinkCouponOrderInput{RedemptionID: c.id, DealID: deal, OrderID: order})
	}()
	optest.AssertWaiting(t, e.Pool, done, 1)
	dbtest.Commit(t, held)
	optest.WaitResult(t, done, 200)
}

func TestGroupbuyRevokeRejectedWhileLinked(t *testing.T) {
	e := newOrderEnv(t)
	c := e.coupon("revoke-linked")
	deal := e.deal("douyin", "撤销关联", "99.00", e.item("撤销项目", "298.00"))
	order := e.order(e.item("履约", "398.00"))
	if r := e.link(c.id, deal, order); r.Status != 200 {
		t.Fatal(r)
	}
	rejectionDetail(t, e.HTTP(t, &e.caller, "revoke_coupon", groupbuy.LookupInput{Key: c.key}), 409, "团购券已关联订单，请先取消关联或撤单")
	if e.fake.Count("revoke") != 0 {
		t.Fatal("rejected revoke called provider")
	}
	e.checkout(order, cash("100.00"))
	before := dbtest.Snapshot(t, e.Pool, e.Res.TenantID, "coupon_redemptions", "orders", "audit_log")
	count := e.fake.Count("revoke")
	rejectionDetail(t, e.HTTP(t, &e.caller, "revoke_coupon", groupbuy.LookupInput{Key: c.key}), 409, "团购券已关联订单，请先取消关联或撤单")
	if count != e.fake.Count("revoke") || before != dbtest.Snapshot(t, e.Pool, e.Res.TenantID, "coupon_redemptions", "orders", "audit_log") {
		t.Fatal("paid linked revoke called provider or changed data")
	}
}

func TestGroupbuyVoidReleases(t *testing.T) {
	e := newOrderEnv(t)
	c := e.coupon("void-release")
	deal := e.deal("douyin", "撤单释放", "99.00", e.item("撤单项目", "298.00"))
	order := e.order(e.item("履约", "398.00", 70, 30), e.Res.OwnerID, e.staff)
	if r := e.link(c.id, deal, order); r.Status != 200 {
		t.Fatal(r)
	}
	e.checkout(order, cash("100.00"))
	e.Call(t, e.Owner, "void_order", orders.VoidOrderInput{OrderID: order, Reason: "测试撤单"}, 200)
	view := e.redemption(c)
	if view["order_id"] != nil || view["linked_at"] != nil || view["order_payment_id"] != nil || view["deal_id"] != deal.String() || view["store_price"] != "298.00" || view["purchase_price"] != "99.00" {
		t.Fatal(view)
	}
	totals := e.Call(t, e.Owner, "list_performance", map[string]any{"date_from": settings.Date(e.Now()).Format(settings.DateLayout), "date_to": settings.Date(e.Now()).Format(settings.DateLayout)}, 200).Body["totals"].([]any)
	if totals[0].(map[string]any)["amount"] != "0.00" {
		t.Fatal(totals)
	}
	audits := e.Call(t, e.Owner, "list_audit_log", map[string]any{"target_id": c.id}, 200).Body["entries"].([]any)
	found := false
	for _, raw := range audits {
		entry := raw.(map[string]any)
		if entry["action"] == "void_order" && entry["target_kind"] == "coupon_redemption" && entry["after"].(map[string]any)["order_id"] == nil {
			found = true
		}
	}
	if !found {
		t.Fatal(audits)
	}
	month := e.Call(t, e.Owner, "get_groupbuy_month", groupbuy.GroupbuyMonthInput{Platform: "douyin", Month: settings.Date(e.Now()).Format("2006-01")}, 200).Body
	if month["unlinked"].(map[string]any)["count"] != float64(1) || month["unlinked"].(map[string]any)["purchase_total"] != "99.00" || month["purchase_total"] != "99.00" {
		t.Fatal(month)
	}
	reopened := e.order(e.item("再次履约", "100.00"))
	if r := e.link(c.id, deal, reopened); r.Status != 200 {
		t.Fatal(r)
	}
}

func TestGroupbuyZeroReducedCoupon(t *testing.T) {
	e := newOrderEnv(t)
	first := e.coupon("zero-first")
	second := e.coupon("zero-second")
	order := e.order(e.item("零减少履约", "298.00"))
	if r := e.link(first.id, e.deal("douyin", "零大券", "99.00", e.item("零大券项目", "298.00")), order); r.Status != 200 {
		t.Fatal(r)
	}
	e.Advance(time.Second)
	if r := e.link(second.id, e.deal("douyin", "零小券", "50.00", e.item("零小券项目", "150.00")), order); r.Status != 200 {
		t.Fatal(r)
	}
	paid := e.checkout(order)
	ps := paid["payments"].([]any)
	if len(ps) != 1 || ps[0].(map[string]any)["amount"] != "298.00" {
		t.Fatal(ps)
	}
	gb := paid["groupbuy"].([]any)
	if len(gb) != 2 || gb[0].(map[string]any)["amount"] != "298.00" || gb[1].(map[string]any)["amount"] != "0.00" {
		t.Fatal(gb)
	}
}

func TestGroupbuyMergeMovesLink(t *testing.T) {
	e := newOrderEnv(t)
	c := e.coupon("merge-link")
	deal := e.deal("douyin", "合并券", "99.00", e.item("合并项目", "298.00"))
	keep := guid(e.Call(t, e.Owner, "open_order", orders.OpenOrderInput{CustomerID: &e.customer, Gender: "female", Designated: true, Items: []orders.OrderItemInput{}}, 200).Body["id"])
	merged := e.order(e.item("合并履约", "398.00"))
	if r := e.link(c.id, deal, merged); r.Status != 200 {
		t.Fatal(r)
	}
	e.Call(t, e.Owner, "merge_orders", orders.MergeOrdersInput{OrderID: keep, MergedOrderIDs: []uuid.UUID{merged}}, 200)
	if e.redemption(c)["order_id"] != keep.String() {
		t.Fatal(e.redemption(c))
	}
	audits := e.Call(t, e.Owner, "list_audit_log", map[string]any{"target_id": c.id}, 200).Body["entries"].([]any)
	foundMerge := false
	for _, raw := range audits {
		entry := raw.(map[string]any)
		before, beforeOK := entry["before"].(map[string]any)
		after, afterOK := entry["after"].(map[string]any)
		if beforeOK && afterOK && entry["action"] == "merge_orders" && entry["target_kind"] == "coupon_redemption" && before["order_id"] == merged.String() && after["order_id"] == keep.String() {
			foundMerge = true
		}
	}
	if !foundMerge {
		t.Fatal(audits)
	}
	paid := e.checkout(keep, cash("100.00"))
	ps := paid["payments"].([]any)
	if len(ps) != 2 || ps[0].(map[string]any)["method"] != "groupbuy" || ps[0].(map[string]any)["amount"] != "298.00" || ps[1].(map[string]any)["amount"] != "100.00" {
		t.Fatal(ps)
	}
}

func TestGroupbuyMonthWindowMarch(t *testing.T) {
	e := newOrderEnv(t)
	early := timeAt(2026, 2, 20, 12, 0)
	e.SetNow(early)
	e.fake.Now = early
	e.Call(t, e.Owner, "change_setting", map[string]any{"key": settings.KeyBusinessDayEnd, "value": "02:00", "effective_from": settings.Date(early).AddDate(0, 0, 1).Format(settings.DateLayout)}, 200)
	marchEnd := timeAt(2026, 3, 31, 23, 30)
	e.SetNow(marchEnd)
	e.fake.Now = marchEnd
	e.coupon("march-end")
	marchEarly := timeAt(2026, 3, 1, 1, 30)
	e.SetNow(marchEarly)
	e.fake.Now = marchEarly
	e.coupon("march-early")
	march := e.Call(t, e.Owner, "get_groupbuy_month", groupbuy.GroupbuyMonthInput{Platform: "douyin", Month: "2026-03"}, 200).Body
	february := e.Call(t, e.Owner, "get_groupbuy_month", groupbuy.GroupbuyMonthInput{Platform: "douyin", Month: "2026-02"}, 200).Body
	if march["redeemed_count"] != float64(1) || march["unlinked_no_deal"] != float64(1) || february["redeemed_count"] != float64(1) || february["unlinked_no_deal"] != float64(1) {
		t.Fatal(march, february)
	}
}

func TestGroupbuyUnlinkPermissions(t *testing.T) {
	e := newOrderEnv(t)
	c := e.coupon("unlink-permission")
	deal := e.deal("douyin", "权限券", "99.00", e.item("权限项目", "298.00"))
	order := e.order(e.item("履约", "100.00"))
	if r := e.link(c.id, deal, order); r.Status != 200 {
		t.Fatal(r)
	}
	noOrder := e.MustCaller(e.Owner)
	delete(noOrder.Permissions, "order.create")
	e.assertUnlinkRefused(noOrder, groupbuy.UnlinkCouponOrderInput{RedemptionID: c.id}, 403, "permission denied")
	noRedeem := e.MustCaller(e.Owner)
	delete(noRedeem.Permissions, "groupbuy.redeem")
	e.assertUnlinkRefused(noRedeem, groupbuy.UnlinkCouponOrderInput{RedemptionID: c.id}, 403, "permission denied")
	other := e.order(e.item("另一履约", "100.00"))
	e.assertLinkRefused(noRedeem, groupbuy.LinkCouponOrderInput{RedemptionID: c.id, DealID: deal, OrderID: other}, 403, "permission denied")
}

func TestGroupbuyRevokeRaceOpen(t *testing.T) {
	e := newOrderEnv(t)
	c := e.coupon("revoke-race-open")
	deal := e.deal("douyin", "竞态券", "99.00", e.item("竞态项目", "298.00"))
	order := e.order(e.item("履约", "100.00"))
	var linkErr error
	e.fake.Before = func(action string, _ groupbuy.CouponRequest) {
		if action == "revoke" && linkErr == nil {
			if r := e.link(c.id, deal, order); r.Status != 200 {
				linkErr = r.Err
				if linkErr == nil {
					linkErr = &detailError{detail: r.Detail()}
				}
			}
		}
	}
	r := e.HTTP(t, &e.caller, "revoke_coupon", groupbuy.LookupInput{Key: c.key})
	if linkErr != nil || r.Status != 200 {
		t.Fatal(linkErr, r)
	}
	view := e.redemption(c)
	if view["status"] != "revoked" || view["order_id"] != nil || view["linked_at"] != nil || view["order_payment_id"] != nil || view["deal_id"] != deal.String() || view["store_price"] != "298.00" {
		t.Fatal(view)
	}
}

func TestGroupbuyRevokeRacePaid(t *testing.T) {
	e := newOrderEnv(t)
	c := e.coupon("revoke-race-paid")
	deal := e.deal("douyin", "已用竞态券", "99.00", e.item("已用竞态项目", "298.00"))
	order := e.order(e.item("履约", "100.00"))
	var setupErr error
	e.fake.Before = func(action string, _ groupbuy.CouponRequest) {
		if action == "revoke" && setupErr == nil {
			if r := e.link(c.id, deal, order); r.Status != 200 {
				setupErr = &detailError{detail: "link " + r.Detail()}
				return
			}
			if r := e.checkout(order); r == nil {
				setupErr = &detailError{detail: "checkout failed"}
			}
		}
	}
	r := e.HTTP(t, &e.caller, "revoke_coupon", groupbuy.LookupInput{Key: c.key})
	if setupErr != nil || r.Status != 409 || r.Detail() != "团购券已被平台撤销；该券已用于已结账的订单，请撤单或走售后处理" {
		t.Fatal(setupErr, r)
	}
	view := e.redemption(c)
	if view["status"] != "revoked" || view["order_id"] != order.String() || view["order_payment_id"] == nil || view["deal_id"] != deal.String() {
		t.Fatal(view)
	}
}

type detailError struct{ detail string }

func (e *detailError) Error() string { return e.detail }

func TestGroupbuyDealAndSettlementAudit(t *testing.T) {
	e := newOrderEnv(t)
	item := e.item("审计项目", "298.00")
	deal := e.deal("douyin", "审计商品", "99.00", item)
	createAudit := e.Call(t, e.Owner, "list_audit_log", map[string]any{"target_id": deal}, 200).Body["entries"].([]any)
	if len(createAudit) != 1 || createAudit[0].(map[string]any)["action"] != "create_groupbuy_deal" || createAudit[0].(map[string]any)["target_kind"] != "groupbuy_deal" {
		t.Fatal(createAudit)
	}
	e.Call(t, e.Owner, "update_groupbuy_deal", groupbuy.UpdateDealInput{ID: deal, Name: "审计商品二", PurchasePrice: amount("120.00"), ItemID: item, Active: true}, 200)
	updateAudit := e.Call(t, e.Owner, "list_audit_log", map[string]any{"target_id": deal}, 200).Body["entries"].([]any)
	found := false
	for _, raw := range updateAudit {
		entry := raw.(map[string]any)
		if entry["action"] == "update_groupbuy_deal" && entry["before"].(map[string]any)["purchase_price"] == "99.00" && entry["after"].(map[string]any)["purchase_price"] == "120.00" {
			found = true
		}
	}
	if !found {
		t.Fatal(updateAudit)
	}
	settlement := e.Call(t, e.Owner, "record_groupbuy_settlement", groupbuy.RecordSettlementInput{Platform: "douyin", Month: "2026-11", Amount: amount("200.00")}, 200).Body
	settlementID := guid(settlement["id"])
	settlementAudit := e.Call(t, e.Owner, "list_audit_log", map[string]any{"target_id": settlementID}, 200).Body["entries"].([]any)
	if len(settlementAudit) != 1 || settlementAudit[0].(map[string]any)["action"] != "record_groupbuy_settlement" || settlementAudit[0].(map[string]any)["target_kind"] != "groupbuy_settlement" {
		t.Fatal(settlementAudit)
	}
}

func TestGroupbuyAIDrafts(t *testing.T) {
	e := newOrderEnv(t)
	item := e.item("AI草稿项目", "298.00")
	deal := e.deal("douyin", "AI草稿券", "99.00", item)
	c := e.coupon("ai-draft")
	order := e.order(e.item("履约", "100.00"))
	if r := e.link(c.id, deal, order); r.Status != 200 {
		t.Fatal(r)
	}
	before := dbtest.Snapshot(t, e.Pool, e.Res.TenantID, "groupbuy_deals", "groupbuy_settlements", "coupon_redemptions", "orders", "audit_log")
	optest.RequireDraftCreated(t, e.AI(t, e.caller, "create_groupbuy_deal", groupbuy.CreateDealInput{Platform: "douyin", Name: "AI新券", PurchasePrice: amount("1"), ItemID: item}), before == dbtest.Snapshot(t, e.Pool, e.Res.TenantID, "groupbuy_deals", "groupbuy_settlements", "coupon_redemptions", "orders", "audit_log"))
	optest.RequireDraftCreated(t, e.AI(t, e.caller, "update_groupbuy_deal", groupbuy.UpdateDealInput{ID: deal, Name: "AI改名", PurchasePrice: amount("120.00"), ItemID: item, Active: true}), before == dbtest.Snapshot(t, e.Pool, e.Res.TenantID, "groupbuy_deals", "groupbuy_settlements", "coupon_redemptions", "orders", "audit_log"))
	optest.RequireDraftCreated(t, e.AI(t, e.caller, "unlink_coupon_order", groupbuy.UnlinkCouponOrderInput{RedemptionID: c.id}), before == dbtest.Snapshot(t, e.Pool, e.Res.TenantID, "groupbuy_deals", "groupbuy_settlements", "coupon_redemptions", "orders", "audit_log"))
	optest.RequireDraftCreated(t, e.AI(t, e.caller, "record_groupbuy_settlement", groupbuy.RecordSettlementInput{Platform: "douyin", Month: "2026-11", Amount: amount("1")}), before == dbtest.Snapshot(t, e.Pool, e.Res.TenantID, "groupbuy_deals", "groupbuy_settlements", "coupon_redemptions", "orders", "audit_log"))
}

func TestGroupbuyConcurrentLink(t *testing.T) {
	e := newOrderEnv(t)
	c := e.coupon("concurrent-link")
	deal := e.deal("douyin", "并发券", "99.00", e.item("并发项目", "298.00"))
	first, second := e.order(e.item("并发履约一", "100.00")), e.order(e.item("并发履约二", "100.00"))
	start := make(chan struct{})
	done := make(chan int, 2)
	for _, order := range []uuid.UUID{first, second} {
		go func(order uuid.UUID) {
			<-start
			r := e.HTTPWithToken(t, e.Owner, "link_coupon_order", groupbuy.LinkCouponOrderInput{RedemptionID: c.id, DealID: deal, OrderID: order})
			done <- r.Status
		}(order)
	}
	close(start)
	statuses := []int{<-done, <-done}
	if (statuses[0] != 200 || statuses[1] != 409) && (statuses[0] != 409 || statuses[1] != 200) {
		t.Fatal(statuses)
	}
}

func TestGroupbuyVoidRelink(t *testing.T) {
	e := newOrderEnv(t)
	c := e.coupon("void-relink")
	deal := e.deal("douyin", "重联券", "99.00", e.item("重联项目", "298.00"))
	order := e.order(e.item("履约", "398.00"))
	if r := e.link(c.id, deal, order); r.Status != 200 {
		t.Fatal(r)
	}
	e.checkout(order, cash("100.00"))
	e.Call(t, e.Owner, "void_order", orders.VoidOrderInput{OrderID: order, Reason: "重联测试"}, 200)
	next := e.order(e.item("再次履约", "398.00"))
	if r := e.link(c.id, deal, next); r.Status != 200 {
		t.Fatal(r)
	}
	paid := e.checkout(next, cash("100.00"))
	tx := dbtest.AppTx(t, e.Pool, e.Res.TenantID)
	var payments int
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM order_payments WHERE coupon_redemption_id=$1 AND method='groupbuy'", c.id).Scan(&payments); err != nil {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	if payments != 2 || paid["status"] != "paid" || e.redemption(c)["order_payment_id"] == nil {
		t.Fatal(payments, paid, e.redemption(c))
	}
}
func TestGroupbuyPerformanceRatio(t *testing.T) {
	e := newOrderEnv(t)
	e.Call(t, e.Owner, "change_setting", map[string]any{"key": settings.KeyPerformanceRatioGroupbuy, "value": 40, "effective_from": settings.Date(e.Now()).Format(settings.DateLayout)}, 200)
	deal := e.deal("douyin", "比例券", "99.00", e.item("比例项目", "298.00"))
	c := e.coupon("ratio")
	order := e.order(e.item("履约", "398.00"))
	if r := e.link(c.id, deal, order); r.Status != 200 {
		t.Fatal(r)
	}
	e.checkout(order, cash("100.00"))
	groupbuy, cashTotal := money.Amount{}, money.Amount{}
	for _, raw := range e.entries() {
		entry := raw.(map[string]any)
		if entry["method"] == "groupbuy" {
			groupbuy = plus(t, groupbuy, amount(entry["amount"].(string)))
			if entry["ratio"] != float64(40) {
				t.Fatal(entry)
			}
		}
		if entry["method"] == "cash" {
			cashTotal = plus(t, cashTotal, amount(entry["amount"].(string)))
		}
	}
	if groupbuy.String() != "119.20" || cashTotal.String() != "100.00" {
		t.Fatal(groupbuy, cashTotal)
	}
}

func TestGroupbuyMonthBoundary(t *testing.T) {
	e := newOrderEnv(t)
	early := instant.AddDate(0, 0, -2)
	e.SetNow(early)
	e.fake.Now = early
	e.Call(t, e.Owner, "change_setting", map[string]any{"key": settings.KeyBusinessDayEnd, "value": "02:00", "effective_from": settings.Date(early).AddDate(0, 0, 1).Format(settings.DateLayout)}, 200)
	at := timeAt(2026, 11, 1, 1, 30)
	e.SetNow(at)
	e.fake.Now = at
	deal := e.deal("douyin", "跨月券", "99.00", e.item("跨月项目", "298.00"))
	c := e.coupon("month-boundary")
	order := e.order(e.item("履约", "398.00"))
	if r := e.link(c.id, deal, order); r.Status != 200 {
		t.Fatal(r)
	}
	e.checkout(order, cash("100.00"))
	october := e.Call(t, e.Owner, "get_groupbuy_month", groupbuy.GroupbuyMonthInput{Platform: "douyin", Month: "2026-10"}, 200).Body
	november := e.Call(t, e.Owner, "get_groupbuy_month", groupbuy.GroupbuyMonthInput{Platform: "douyin", Month: "2026-11"}, 200).Body
	if october["linked_paid"].(map[string]any)["count"] != float64(1) || october["linked_paid"].(map[string]any)["purchase_total"] != "99.00" || november["redeemed_count"] != float64(0) {
		t.Fatal(october, november)
	}
}

func TestGroupbuyMonthCountsAndSettlement(t *testing.T) {
	e := newOrderEnv(t)
	item := e.item("月对账履约", "100.00")
	deal := e.deal("douyin", "月对账券", "99.00", e.item("月对账项目", "298.00"))
	paidCoupon := e.coupon("month-paid")
	openCoupon := e.coupon("month-open")
	unlinked := e.coupon("month-unlinked")
	revoked := e.coupon("month-revoked")
	paidOrder := e.order(item)
	openOrder := e.order(item)
	for _, pair := range []struct {
		coupon coupon
		order  uuid.UUID
	}{{paidCoupon, paidOrder}, {openCoupon, openOrder}} {
		if r := e.link(pair.coupon.id, deal, pair.order); r.Status != 200 {
			t.Fatal(r)
		}
	}
	e.checkout(paidOrder)
	e.Call(t, e.Owner, "revoke_coupon", groupbuy.LookupInput{Key: revoked.key}, 200)
	if unlinked.id == uuid.Nil {
		t.Fatal(unlinked)
	}
	e.Call(t, e.Owner, "record_groupbuy_settlement", groupbuy.RecordSettlementInput{Platform: "douyin", Month: "2026-11", Amount: amount("200.00"), Note: "第一次"}, 200)
	e.Call(t, e.Owner, "record_groupbuy_settlement", groupbuy.RecordSettlementInput{Platform: "douyin", Month: "2026-11", Amount: amount("210.00"), Note: "第二次"}, 200)
	body := e.Call(t, e.Owner, "get_groupbuy_month", groupbuy.GroupbuyMonthInput{Platform: "douyin", Month: "2026-11"}, 200).Body
	if body["redeemed_count"] != float64(3) || body["purchase_total"] != "198.00" || body["linked_paid"].(map[string]any)["count"] != float64(1) || body["linked_paid"].(map[string]any)["purchase_total"] != "99.00" || body["linked_open"].(map[string]any)["count"] != float64(1) || body["linked_open"].(map[string]any)["purchase_total"] != "99.00" || body["unlinked"].(map[string]any)["count"] != float64(0) || body["unlinked"].(map[string]any)["purchase_total"] != "0.00" || body["unlinked_no_deal"] != float64(1) {
		t.Fatal(body)
	}
	settlement := body["settlement"].(map[string]any)
	history := body["history"].([]any)
	if settlement["amount"] != "210.00" || settlement["note"] != "第二次" || settlement["entered_by_name"] != "王老板" || body["difference"] != "12.00" || len(history) != 2 || history[0].(map[string]any)["note"] != "第二次" || history[1].(map[string]any)["note"] != "第一次" {
		t.Fatal(body)
	}
}
