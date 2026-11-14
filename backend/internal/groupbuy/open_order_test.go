package groupbuy_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/groupbuy"
	"github.com/wzhlinhai/salon/backend/internal/groupbuy/fakeprovider"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/orders"
)

func (e *orderEnv) openGroupbuy(redemption, deal, item, staff uuid.UUID) groupbuy.OpenGroupbuyOrderInput {
	return groupbuy.OpenGroupbuyOrderInput{
		CustomerID:   &e.customer,
		RedemptionID: redemption,
		DealID:       deal,
		Gender:       "female",
		Designated:   true,
		Items:        []orders.OrderItemInput{{ItemID: item, Staff: []orders.OrderItemStaffInput{{Slot: 1, StaffID: staff}}}},
	}
}

func TestOpenGroupbuyOrderHTTP(t *testing.T) {
	e := newOrderEnv(t)
	item := e.item("团购剪发", "298.00", 100)
	deal := e.deal("douyin", "剪发券", "99.00", item)
	c := e.coupon("open-groupbuy-http")
	before := e.linkSnapshot()
	r := e.HTTP(t, &e.caller, "open_groupbuy_order", e.openGroupbuy(c.id, deal, item, e.staff))
	if r.Status != 200 || before == e.linkSnapshot() {
		t.Fatalf("open = %d %v", r.Status, r.Body)
	}
	order := r.Body["order"].(map[string]any)
	redemption := r.Body["redemption"].(map[string]any)
	groupbuy := order["groupbuy"].([]any)
	if redemption["order_id"] != order["id"] || redemption["linked_at"] == nil || len(order["items"].([]any)) != 1 || len(groupbuy) != 1 || groupbuy[0].(map[string]any)["deal_id"] != deal.String() {
		t.Fatal(r.Body)
	}
}

func TestOpenGroupbuyOrderAIDraftAndConfirm(t *testing.T) {
	e := newOrderEnv(t)
	item := e.item("团购剪发", "298.00", 100)
	deal := e.deal("douyin", "剪发券", "99.00", item)
	c := e.coupon("open-groupbuy-ai")
	before := e.linkSnapshot()
	optest.RequireDraftCreated(t, e.AI(t, e.caller, "open_groupbuy_order", e.openGroupbuy(c.id, deal, item, e.staff)), before == e.linkSnapshot())
	draft := e.Call(t, e.Owner, "list_drafts", map[string]any{}, 200).Body["drafts"].([]any)[0].(map[string]any)
	e.Call(t, e.Owner, "confirm_draft", map[string]any{"id": draft["id"]}, 200)
	list := e.Call(t, e.Owner, "list_open_orders", map[string]any{}, 200).Body["items"].([]any)
	if len(list) != 1 {
		t.Fatal(list)
	}
	got := e.redemption(c)
	if got["order_id"] != list[0].(map[string]any)["id"] || got["linked_at"] == nil {
		t.Fatal(got)
	}
}

func TestOpenGroupbuyOrderRequiresBothPermissions(t *testing.T) {
	e := newOrderEnv(t)
	item := e.item("团购剪发", "298.00", 100)
	deal := e.deal("douyin", "剪发券", "99.00", item)
	c := e.coupon("open-groupbuy-perm")
	in := e.openGroupbuy(c.id, deal, item, e.staff)
	orderOnly := e.MustCaller(e.Owner)
	delete(orderOnly.Permissions, "groupbuy.redeem")
	for _, tool := range e.Registry.ChatTools(orderOnly) {
		if tool.Name == "open_groupbuy_order" {
			t.Fatal("ChatTools offered open_groupbuy_order without groupbuy.redeem")
		}
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Registry.CallChatTool(context.Background(), orderOnly, "open_groupbuy_order", raw); !errors.Is(err, ops.ErrUnknownOperation) {
		t.Fatalf("CallChatTool err = %v, want ErrUnknownOperation", err)
	}
	rejectionDetail(t, e.AI(t, orderOnly, "open_groupbuy_order", in), 403, "permission denied")
	rejectionDetail(t, e.AI(t, orderOnly, "create_draft", map[string]any{"operation": "open_groupbuy_order", "params": in, "summary": "团购开单", "utterance": "团购开单"}), 403, "permission denied")
	rejectionDetail(t, e.HTTP(t, &orderOnly, "open_groupbuy_order", in), 403, "permission denied")
	draft := e.Call(t, e.Owner, "create_draft", map[string]any{"operation": "open_groupbuy_order", "params": in, "summary": "团购开单", "utterance": "团购开单"}, 200).Body
	rejectionDetail(t, e.HTTP(t, &orderOnly, "confirm_draft", map[string]any{"id": draft["id"]}), 403, "permission denied")
	noOrder := e.MustCaller(e.Owner)
	delete(noOrder.Permissions, "order.create")
	rejectionDetail(t, e.HTTP(t, &noOrder, "open_groupbuy_order", in), 403, "permission denied")
	role := e.Call(t, e.Owner, "create_role", map[string]any{"name": "只能开单", "permissions": []any{map[string]any{"permission": "order.create"}}}, 200).Body
	_, token := e.Hire("开单员", "012", "groupbuy-order-only", uuid.MustParse(role["id"].(string)))
	for _, ch := range e.Channels() {
		r := ch.Call(t, e.MustCaller(token), "get_draft", map[string]any{"id": draft["id"]})
		if r.Status != 404 {
			t.Fatalf("%s invisible draft = %v", ch.Name, r)
		}
	}
}

func TestOpenGroupbuyOrderPreviewValidation(t *testing.T) {
	t.Run("already linked", func(t *testing.T) {
		e := newOrderEnv(t)
		item := e.item("团购剪发", "298.00", 100)
		deal := e.deal("douyin", "剪发券", "99.00", item)
		c := e.coupon("preview-linked")
		existing := e.order(item, e.staff)
		if r := e.link(c.id, deal, existing); r.Status != 200 {
			t.Fatal(r)
		}
		draft := e.Call(t, e.Owner, "create_draft", map[string]any{"operation": "open_groupbuy_order", "params": e.openGroupbuy(c.id, deal, item, e.staff), "summary": "团购开单", "utterance": "团购开单"}, 200).Body
		for _, ch := range e.Channels() {
			r := ch.Call(t, e.MustCaller(e.Owner), "get_draft", map[string]any{"id": draft["id"]})
			if r.Status != 200 || r.Body["preview_error"] != "团购券已关联其他单" {
				t.Fatalf("%s: %v", ch.Name, r)
			}
		}
	})
	t.Run("inactive deal", func(t *testing.T) {
		e := newOrderEnv(t)
		item := e.item("团购剪发", "298.00", 100)
		deal := e.deal("douyin", "剪发券", "99.00", item)
		c := e.coupon("preview-inactive")
		e.Call(t, e.Owner, "update_groupbuy_deal", groupbuy.UpdateDealInput{ID: deal, Name: "剪发券", PurchasePrice: amount("99.00"), ItemID: item, Active: false}, 200)
		draft := e.Call(t, e.Owner, "create_draft", map[string]any{"operation": "open_groupbuy_order", "params": e.openGroupbuy(c.id, deal, item, e.staff), "summary": "团购开单", "utterance": "团购开单"}, 200).Body
		for _, ch := range e.Channels() {
			r := ch.Call(t, e.MustCaller(e.Owner), "get_draft", map[string]any{"id": draft["id"]})
			if r.Status != 200 || r.Body["preview_error"] != "团购商品已停用" {
				t.Fatalf("%s: %v", ch.Name, r)
			}
		}
	})
	t.Run("item mismatch", func(t *testing.T) {
		e := newOrderEnv(t)
		item := e.item("团购剪发", "298.00", 100)
		other := e.item("别的项目", "398.00", 100)
		deal := e.deal("douyin", "剪发券", "99.00", item)
		c := e.coupon("preview-mismatch")
		draft := e.Call(t, e.Owner, "create_draft", map[string]any{"operation": "open_groupbuy_order", "params": e.openGroupbuy(c.id, deal, other, e.staff), "summary": "团购开单", "utterance": "团购开单"}, 200).Body
		for _, ch := range e.Channels() {
			r := ch.Call(t, e.MustCaller(e.Owner), "get_draft", map[string]any{"id": draft["id"]})
			if r.Status != 200 || r.Body["preview_error"] != "开单项目必须包含团购商品对应的项目" {
				t.Fatalf("%s: %v", ch.Name, r)
			}
		}
	})
}

func TestOpenGroupbuyOrderItemMismatch(t *testing.T) {
	e := newOrderEnv(t)
	dealItem := e.item("团购剪发", "298.00", 100)
	other := e.item("别的项目", "398.00", 100)
	deal := e.deal("douyin", "剪发券", "99.00", dealItem)
	c := e.coupon("open-groupbuy-mismatch")
	before := e.linkSnapshot()
	in := e.openGroupbuy(c.id, deal, other, e.staff)
	rejectionDetail(t, e.HTTP(t, &e.caller, "open_groupbuy_order", in), 409, "开单项目必须包含团购商品对应的项目")
	if before != e.linkSnapshot() {
		t.Fatal("mismatch wrote data")
	}
}

func TestOpenGroupbuyOrderAlreadyLinkedRollsBackOrder(t *testing.T) {
	e := newOrderEnv(t)
	dealItem := e.item("团购剪发", "298.00", 100)
	deal := e.deal("douyin", "剪发券", "99.00", dealItem)
	c := e.coupon("open-groupbuy-linked")
	existing := e.order(dealItem, e.staff)
	if r := e.link(c.id, deal, existing); r.Status != 200 {
		t.Fatal(r)
	}
	before := e.linkSnapshot()
	rejectionDetail(t, e.HTTP(t, &e.caller, "open_groupbuy_order", e.openGroupbuy(c.id, deal, dealItem, e.staff)), 409, "团购券已关联其他单")
	if before != e.linkSnapshot() {
		t.Fatal("failed link left the newly opened order behind")
	}
}

func TestOpenGroupbuyOrderLinkFailureRollsBackOrder(t *testing.T) {
	e := newOrderEnv(t)
	e.Groupbuy.Register("meituan", fakeprovider.New("meituan", instant))
	item := e.item("团购剪发", "298.00", 100)
	deal := e.deal("douyin", "抖音剪发券", "99.00", item)
	key := "open-groupbuy-platform-mismatch"
	e.Call(t, e.Owner, "redeem_coupon", groupbuy.CouponRequest{Platform: "meituan", CouponCode: key, Key: key}, 200)
	redemption := e.Call(t, e.Owner, "get_redemption", groupbuy.LookupInput{Key: key}, 200).Body
	before := e.linkSnapshot()
	rejectionDetail(t, e.HTTP(t, &e.caller, "open_groupbuy_order", e.openGroupbuy(guid(redemption["id"]), deal, item, e.staff)), 409, "团购平台不一致")
	if before != e.linkSnapshot() {
		t.Fatal("link failure left the newly opened order behind")
	}
}

func TestOpenGroupbuyOrderAINeverVerifiesOrRedeems(t *testing.T) {
	e := newOrderEnv(t)
	e.item("团购剪发", "298.00", 100)
	c := e.coupon("open-groupbuy-ainone")
	for _, name := range []string{"verify_coupon", "redeem_coupon", "revoke_coupon"} {
		r := e.AI(t, e.caller, name, map[string]any{"platform": "douyin", "coupon_code": c.key, "key": c.key})
		if r.Status != 404 {
			t.Fatalf("%s = %d %v", name, r.Status, r.Body)
		}
	}
}
