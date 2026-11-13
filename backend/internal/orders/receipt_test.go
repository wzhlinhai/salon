package orders_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/groupbuy"
	"github.com/wzhlinhai/salon/backend/internal/groupbuy/fakeprovider"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/orders"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

func (s *fixture) namedPaperOrder(customer uuid.UUID, date string, line orders.OrderItemInput) map[string]any {
	s.t.Helper()
	in := s.opening(line)
	in.CustomerID = &customer
	in.PaperDate = &date
	return s.call("open_order", in, 200)
}

func TestGetReceipt(t *testing.T) {
	s := open(t)
	customer := id(s.call("create_customer", map[string]any{"name": "张小明", "phone": "13900000009"}, 200)["customer"].(map[string]any)["id"])
	line := s.line(1, 2, 3)
	cardType := s.paymentType("小票卡", nil)
	card := s.call("open_card", map[string]any{"customer_id": customer, "card_type_id": cardType["id"], "principal": "1000", "bonus": "0", "sales_staff_ids": []uuid.UUID{s.Res.OwnerID}, "payments": testPayments(cardSaleAmount(cardType, "1000"))}, 200)
	countType := s.call("create_card_type", map[string]any{"name": "剪发卡" + uuid.NewString(), "kind": "count", "validity_kind": "none", "items": []any{map[string]any{"item_id": line.ItemID, "paid_count": 2, "gift_count": 1, "unit_price": "100"}}}, 200)
	countCard := s.call("open_card", map[string]any{"customer_id": customer, "card_type_id": countType["id"], "principal": "0", "bonus": "0", "sales_staff_ids": []uuid.UUID{s.Res.OwnerID}, "payments": testPayments(cardSaleAmount(countType, "0"))}, 200)
	cardOrder := s.namedPaperOrder(customer, s.date(-1), line)
	countOrder := s.namedPaperOrder(customer, s.date(-1), line)
	after := s.call("checkout_orders", checkoutInput(
		checkoutOrder(cardOrder, cardPayment(card, "150")),
		checkoutOrder(countOrder, countPayment(countCard, firstLine(countOrder), "150")),
	), 200)
	checkoutID := id(lines(after)[0].(map[string]any)["checkout_id"])
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "get_receipt", orders.GetReceiptInput{CheckoutID: checkoutID})
		if r.Status != 200 {
			t.Fatal(ch.Name, r)
		}
		if r.Body["customer_name"] != "张**" || r.Body["backfill"] != true || r.Body["business_date"] != s.date(-1) {
			t.Fatalf("%s receipt = %v", ch.Name, r.Body)
		}
		raw, _ := json.Marshal(r.Body)
		if strings.Contains(string(raw), "13900000009") || strings.Contains(string(raw), "张小明") {
			t.Fatalf("%s leaked customer identity: %s", ch.Name, raw)
		}
		cards := r.Body["cards"].([]any)
		if len(cards) != 2 {
			t.Fatal(ch.Name, cards)
		}
		found := map[string]map[string]any{}
		for _, rawCard := range cards {
			row := rawCard.(map[string]any)
			found[row["card_id"].(string)] = row
		}
		if got := found[card["id"].(string)]; got["principal"] != "850.00" || got["bonus"] != "0.00" {
			t.Fatalf("%s card balance = %v", ch.Name, got)
		}
		counts := found[countCard["id"].(string)]["counts"].([]any)
		if len(counts) != 1 {
			t.Fatal(ch.Name, counts)
		}
		count := counts[0].(map[string]any)
		if count["paid"] != float64(1) || count["gift"] != float64(1) || count["name"] != "剪发" {
			t.Fatalf("%s count balance = %v", ch.Name, count)
		}
		order := r.Body["orders"].([]any)[0].(map[string]any)
		item := order["items"].([]any)[0].(map[string]any)
		if item["item_name"] != "剪发" || item["net"] != "150.00" || len(item["staff_names"].([]any)) != 1 {
			t.Fatalf("%s item = %v", ch.Name, item)
		}
		// 充值之后重打小票，仍显示结账时的卡余额。
		s.call("recharge_card", map[string]any{"card_id": card["id"], "principal": "50", "bonus": "0", "sales_staff_ids": []uuid.UUID{s.Res.OwnerID}, "payments": []any{map[string]any{"method": "cash", "amount": "50"}}}, 200)
		again := ch.Call(t, s.MustCaller(s.Owner), "get_receipt", orders.GetReceiptInput{CheckoutID: checkoutID})
		if again.Status != 200 {
			t.Fatal(ch.Name, again)
		}
		for _, rawCard := range again.Body["cards"].([]any) {
			row := rawCard.(map[string]any)
			if row["card_id"] == card["id"] && row["principal"] != "850.00" {
				t.Fatalf("%s reprint balance changed: %v", ch.Name, row)
			}
		}
	}
}

func TestGetReceiptStoreAndPermission(t *testing.T) {
	s := open(t)
	o := s.customerOrder(s.checkoutLine())
	after := s.call("checkout_orders", checkoutInput(checkoutOrder(o, payment("cash", "100"))), 200)
	checkoutID := id(lines(after)[0].(map[string]any)["checkout_id"])
	other := s.otherStore()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var otherCheckout uuid.UUID
	if err := tx.QueryRow(context.Background(), "INSERT INTO checkouts (tenant_id,store_id,business_date,checked_out_by) VALUES ($1,$2,CURRENT_DATE,$3) RETURNING id", s.Res.TenantID, other, s.Res.OwnerID).Scan(&otherCheckout); err != nil {
		t.Fatal(err)
	}
	// 本店结账单 ID 上挂一张别店的单，检查 get_receipt 内部的单据门店过滤。
	dbtest.Exec(t, tx, `INSERT INTO orders (tenant_id,store_id,customer_id,gender,designated,status,settings_snapshot_id,business_date,opened_by,checkout_id,backfill)
SELECT tenant_id,$1,customer_id,gender,designated,'paid',settings_snapshot_id,business_date,opened_by,$2,backfill FROM orders WHERE id=$3`, other, checkoutID, id(o["id"]))
	dbtest.Commit(t, tx)
	second, err := staff.Bootstrap(context.Background(), s.Pool, staff.BootstrapInput{TenantName: "另租户", StoreName: "另店", OwnerName: "别老板", OwnerNumber: "001", OwnerAccount: "other-boss", OwnerPassword: "other-password"})
	if err != nil {
		t.Fatal(err)
	}
	tx = dbtest.AppTx(t, s.Pool, second.TenantID)
	var otherTenantCheckout uuid.UUID
	if err := tx.QueryRow(context.Background(), "INSERT INTO checkouts (tenant_id,store_id,business_date,checked_out_by) VALUES ($1,$2,CURRENT_DATE,$3) RETURNING id", second.TenantID, second.StoreID, second.OwnerID).Scan(&otherTenantCheckout); err != nil {
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)
	for _, ch := range s.Channels() {
		for _, id := range []uuid.UUID{otherCheckout, otherTenantCheckout} {
			r := ch.Call(t, s.MustCaller(s.Owner), "get_receipt", orders.GetReceiptInput{CheckoutID: id})
			if r.Status != 404 {
				t.Fatalf("%s other store/tenant receipt = %d %v", ch.Name, r.Status, r.Body)
			}
		}
		r := ch.Call(t, s.MustCaller(s.Owner), "get_receipt", orders.GetReceiptInput{CheckoutID: checkoutID})
		if r.Status != 200 || len(r.Body["orders"].([]any)) != 1 {
			t.Fatalf("%s nested store filter failed: %d %v", ch.Name, r.Status, r.Body)
		}
		caller := s.MustCaller(s.Owner)
		delete(caller.Permissions, perm.PaymentCollect)
		r = ch.Call(t, caller, "get_receipt", orders.GetReceiptInput{CheckoutID: checkoutID})
		if r.Status != 403 || r.Detail() != "permission denied" {
			t.Fatalf("%s receipt without permission = %d %v", ch.Name, r.Status, r.Body)
		}
	}
}

func TestGetReceiptDataScope(t *testing.T) {
	s := open(t)
	customer := id(s.call("create_customer", map[string]any{"name": "张小明", "phone": "13900000009"}, 200)["customer"].(map[string]any)["id"])
	line := s.line(1, 2, 3)
	cardType := s.paymentType("可见性卡", nil)
	card := s.call("open_card", map[string]any{"customer_id": customer, "card_type_id": cardType["id"], "principal": "1000", "bonus": "0", "sales_staff_ids": []uuid.UUID{s.Res.OwnerID}, "payments": testPayments(cardSaleAmount(cardType, "1000"))}, 200)
	order := s.namedPaperOrder(customer, s.date(-1), line)
	after := s.call("checkout_orders", checkoutInput(checkoutOrder(order, cardPayment(card, "150"))), 200)
	checkoutID := id(lines(after)[0].(map[string]any)["checkout_id"])
	caller := s.MustCaller(s.Owner)
	delete(caller.Permissions, perm.CustomerView)
	for _, ch := range s.Channels() {
		r := ch.Call(t, caller, "get_receipt", orders.GetReceiptInput{CheckoutID: checkoutID})
		if r.Status != 200 {
			t.Fatal(ch.Name, r)
		}
		if r.Body["customer_name"] != nil || len(r.Body["cards"].([]any)) != 0 {
			t.Fatalf("%s leaked customer data: %v", ch.Name, r.Body)
		}
		if len(r.Body["orders"].([]any)) != 1 || len(r.Body["orders"].([]any)[0].(map[string]any)["items"].([]any)) != 1 {
			t.Fatalf("%s should keep the rest of the receipt: %v", ch.Name, r.Body)
		}
	}
}

func TestGetReceiptGroupbuyAndTodaySettings(t *testing.T) {
	s := open(t)
	s.Groupbuy.Register("meituan", fakeprovider.New("meituan", s.Now()))
	deal := s.call("create_groupbuy_deal", map[string]any{"platform": "meituan", "name": "染色券", "purchase_price": "80.00", "item_id": s.item}, 200)
	s.call("redeem_coupon", groupbuy.CouponRequest{Platform: "meituan", CouponCode: "coupon-1", Key: "coupon-1"}, 200)
	redemption := s.call("get_redemption", map[string]any{"key": "coupon-1"}, 200)
	order := s.paperOrder(s.date(-1), s.line(1, 2, 3))
	s.call("link_coupon_order", map[string]any{"redemption_id": redemption["id"], "deal_id": deal["id"], "order_id": order["id"]}, 200)
	after := s.call("checkout_orders", checkoutInput(checkoutOrder(order, payment("cash", "50"))), 200)
	checkoutID := id(lines(after)[0].(map[string]any)["checkout_id"])
	s.call("change_setting", map[string]any{"key": settings.KeyReceiptHeader, "value": "今天抬头", "effective_from": s.date(0)}, 200)
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "get_receipt", orders.GetReceiptInput{CheckoutID: checkoutID})
		if r.Status != 200 || r.Body["header"] != "今天抬头" || r.Body["backfill"] != true {
			t.Fatalf("%s receipt = %v", ch.Name, r.Body)
		}
		found := false
		for _, raw := range r.Body["orders"].([]any)[0].(map[string]any)["payments"].([]any) {
			payment := raw.(map[string]any)
			if payment["method"] == "groupbuy" {
				found = payment["amount"] == "100.00" && payment["store_price"] == "100.00"
			}
		}
		if !found {
			t.Fatalf("%s groupbuy payment = %v", ch.Name, r.Body)
		}
	}
}

type receiptQueryCounter struct{ all atomic.Int64 }

func (c *receiptQueryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.all.Add(1)
	return ctx
}
func (*receiptQueryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestReceiptQueryCount(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	one := s.customerOrder(line)
	oneAfter := s.call("checkout_orders", checkoutInput(checkoutOrder(one, payment("cash", "100"))), 200)
	manyInput := []orders.CheckoutOrderInput{}
	for range 9 {
		o := s.customerOrder(line)
		manyInput = append(manyInput, checkoutOrder(o, payment("cash", "100")))
	}
	r := s.call("checkout_orders", checkoutInput(manyInput...), 200)
	manyCheckout := id(lines(r)[0].(map[string]any)["checkout_id"])
	counter := &receiptQueryCounter{}
	config := s.Pool.Config().Copy()
	config.ConnConfig.Tracer = counter
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	app := optest.New(t, pool)
	caller := s.MustCaller(s.Owner)
	for _, ch := range app.Channels() {
		counts := []int64{}
		for _, checkoutID := range []uuid.UUID{id(lines(oneAfter)[0].(map[string]any)["checkout_id"]), manyCheckout} {
			counter.all.Store(0)
			result := ch.Call(t, caller, "get_receipt", orders.GetReceiptInput{CheckoutID: checkoutID})
			if result.Status != 200 {
				t.Fatal(ch.Name, result)
			}
			counts = append(counts, counter.all.Load())
		}
		if counts[0] != counts[1] {
			t.Fatalf("%s query count grew with orders: %v", ch.Name, counts)
		}
	}
}
