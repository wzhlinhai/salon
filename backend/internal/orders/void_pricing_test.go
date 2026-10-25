package orders_test

import (
	"reflect"
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/orders"
)

func TestVoidPreservesPricing(t *testing.T) {
	s := open(t)
	ct := s.paymentType("八折会员卡", "80")
	card := s.paymentCard(ct, "200", "50")
	in := s.itemInput("撤单会员价项目", 0)
	in["member_prices"] = []any{map[string]any{"card_type_id": ct["id"], "price": "70"}}
	item := s.call("create_item", in, 200)
	member := orders.OrderItemInput{ItemID: id(item["id"]), Staff: []orders.OrderItemStaffInput{}}
	paid := s.paid(s.customerOrder(member, s.checkoutLine()), cardPayment(card, "150"))
	items := paid["items"].([]any)
	if items[0].(map[string]any)["price_source"] != "member" || items[0].(map[string]any)["price"] != "70.00" || items[1].(map[string]any)["discount"] != "20.00" || paid["net"] != "150.00" {
		t.Fatal(paid)
	}
	original := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "order_items", "order_item_positions", "order_payments", "checkouts")
	columns := s.orderColumns(paid)
	consumes := s.consumeEntries(card)
	after := s.void(paid)
	got := s.call("get_order", orders.GetOrderInput{ID: id(paid["id"])}, 200)
	entries := s.consumeEntries(card)
	if got["status"] != "voided" || !reflect.DeepEqual(got, after) || !reflect.DeepEqual(after["items"], paid["items"]) || !reflect.DeepEqual(after["payments"], paid["payments"]) || columns != s.orderColumns(paid) || original != dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "order_items", "order_item_positions", "order_payments", "checkouts") || !reflect.DeepEqual(entries[1:], consumes) {
		t.Fatal("void changed original pricing, order, payments or consumes", got, entries)
	}
}
