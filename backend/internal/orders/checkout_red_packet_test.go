package orders_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/orders"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func (s *fixture) redPacket(customer any, face string) map[string]any {
	s.t.Helper()
	return s.call("issue_red_packet", map[string]any{"customer_id": customer, "kind": "gift", "face_value": face, "expires_on": "2999-01-01"}, 200)
}
func packetPayment(p map[string]any, amount string) orders.PaymentInput {
	v := payment("red_packet", amount)
	v.RedPacketID = new(id(p["id"]))
	return v
}
func (s *fixture) packetSnapshot() string {
	return dbtest.Snapshot(s.t, s.Pool, s.Res.TenantID, "checkouts", "order_payments", "orders", "order_items", "card_ledger", "red_packets", "red_packet_sales", "audit_log")
}
func (s *fixture) packetRefused(c ops.Caller, in orders.CheckoutOrdersInput, status int, detail string) {
	s.t.Helper()
	before := s.packetSnapshot()
	r := s.HTTP(s.t, &c, "checkout_orders", in)
	if r.Status != status || detail != "" && r.Body["detail"] != detail || s.packetSnapshot() != before {
		s.t.Fatal(r)
	}
}
func TestCheckoutRedPackets(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	p := s.redPacket(s.customer, "80")
	o := s.customerOrder(line)
	result := s.call("checkout_orders", checkoutInput(checkoutOrder(o, packetPayment(p, "30"), payment("cash", "70"))), 200)
	got := lines(result)[0].(map[string]any)
	ps := got["payments"].([]any)
	if len(ps) != 2 || ps[0].(map[string]any)["red_packet_id"] != p["id"] || ps[0].(map[string]any)["amount"] != "30.00" || ps[1].(map[string]any)["red_packet_id"] != nil {
		t.Fatal(result)
	}
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "list_customer_red_packets", map[string]any{"customer_id": s.customer})
		if r.Status != 200 {
			t.Fatal(r)
		}
		item := r.Body["items"].([]any)[0].(map[string]any)
		if item["status"] != "used" || item["order_id"] != o["id"] || item["used_amount"] != "30.00" || item["face_value"] != "80.00" {
			t.Fatal(item)
		}
		c := s.MustCaller(s.Owner)
		delete(c.Permissions, perm.CustomerView)
		delete(c.Permissions, perm.CustomerFullPhone)
		r = ch.Call(t, c, "get_order", orders.GetOrderInput{ID: id(o["id"])})
		if r.Status != 200 || r.Body["payments"].([]any)[0].(map[string]any)["red_packet_id"] != p["id"] {
			t.Fatal(r)
		}
	}
	before := s.packetSnapshot()
	r := s.HTTPWithToken(t, s.Owner, "revoke_red_packet", map[string]any{"red_packet_id": p["id"], "reason": "退回"})
	if r.Status != 409 || r.Body["detail"] != "红包已用，不能撤回" || s.packetSnapshot() != before {
		t.Fatal(r)
	}
	a, b := s.redPacket(s.customer, "50"), s.redPacket(s.customer, "60")
	o = s.customerOrder(line)
	result = s.call("checkout_orders", checkoutInput(checkoutOrder(o, packetPayment(a, "50"), packetPayment(b, "50"))), 200)
	if len(lines(result)[0].(map[string]any)["payments"].([]any)) != 2 {
		t.Fatal(result)
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var used, payments int
	if err := tx.QueryRow(context.Background(), "SELECT (SELECT count(*) FROM red_packets WHERE order_payment_id IS NOT NULL),(SELECT count(*) FROM order_payments)").Scan(&used, &payments); err != nil || used != 3 || payments != 4 {
		t.Fatal(used, payments, err)
	}
	dbtest.Rollback(t, tx)
}
func TestCheckoutRedPacketRules(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	a, b := s.customerOrder(line), s.customerOrder(line)
	c := s.MustCaller(s.Owner)
	p := s.redPacket(s.customer, "99")
	s.packetRefused(c, checkoutInput(checkoutOrder(a, packetPayment(p, "100"))), 409, "红包抵扣不能超过面额")
	s.packetRefused(c, checkoutInput(checkoutOrder(a, packetPayment(p, "0"), payment("cash", "100"))), 409, "支付金额须大于零")
	s.packetRefused(c, checkoutInput(checkoutOrder(a, packetPayment(p, "50"), packetPayment(p, "50"))), 409, "同一个红包只能用一次")
	s.packetRefused(c, checkoutInput(checkoutOrder(a, packetPayment(p, "99"), payment("cash", "1")), checkoutOrder(b, packetPayment(p, "99"), payment("cash", "1"))), 409, "同一个红包只能用一次")
	missing := payment("red_packet", "100")
	s.packetRefused(c, checkoutInput(checkoutOrder(a, missing)), 409, "红包支付要选红包")
	wrong := packetPayment(p, "100")
	wrong.Method = "cash"
	s.packetRefused(c, checkoutInput(checkoutOrder(a, wrong)), 409, "只有红包支付要选红包")
	unknown := map[string]any{"id": uuid.NewString()}
	s.packetRefused(c, checkoutInput(checkoutOrder(a, packetPayment(unknown, "100"))), 404, "")
	hidden := s.MustCaller(s.Owner)
	hidden.Permissions[perm.CustomerView] = ops.Grant{Scope: ops.ScopeSelf}
	s.packetRefused(hidden, checkoutInput(checkoutOrder(a, packetPayment(p, "99"), payment("cash", "1"))), 404, "")
	// 即使单据快照允许使用别人的卡，红包仍只允许本人使用。
	s.call("change_setting", map[string]any{"key": "others_card_payment", "value": "allowed", "effective_from": settings.Date(time.Now()).Format(time.DateOnly)}, 200)
	other := s.call("create_customer", map[string]any{}, 200)["customer"].(map[string]any)
	foreign := s.redPacket(other["id"], "100")
	allowed := s.customerOrder(line)
	s.packetRefused(c, checkoutInput(checkoutOrder(allowed, packetPayment(foreign, "100"))), 409, "只能用这位顾客自己的红包")
	s.call("revoke_red_packet", map[string]any{"red_packet_id": p["id"], "reason": "撤回"}, 200)
	s.packetRefused(c, checkoutInput(checkoutOrder(a, packetPayment(p, "99"), payment("cash", "1"))), 409, "红包已撤回")
	used := s.redPacket(s.customer, "100")
	s.call("checkout_orders", checkoutInput(checkoutOrder(a, packetPayment(used, "100"))), 200)
	s.packetRefused(c, checkoutInput(checkoutOrder(b, packetPayment(used, "100"))), 409, "红包已用")
	// 同租户其他门店的红包也不允许使用。
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var packetID uuid.UUID
	st, err := dbgen.New(tx).CreateStore(context.Background(), dbgen.CreateStoreParams{TenantID: s.Res.TenantID, Name: "红包别店"})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(context.Background(), `INSERT INTO red_packets(tenant_id,store_id,customer_id,kind,face_value,expires_on,business_date,issued_by) VALUES($1,$2,$3,'gift',100,'2999-01-01','2020-01-01',$4) RETURNING id`, s.Res.TenantID, st.ID, s.customer, s.Res.OwnerID).Scan(&packetID); err != nil {
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)
	s.packetRefused(c, checkoutInput(checkoutOrder(b, packetPayment(map[string]any{"id": packetID.String()}, "100"))), 404, "")
	before := s.packetSnapshot()
	r := s.AI(t, c, "checkout_orders", checkoutInput(checkoutOrder(b, packetPayment(used, "100"))))
	if r.Status != 409 || !errors.Is(r.Err, ops.ErrDraftRequired) || s.packetSnapshot() != before {
		t.Fatal(r)
	}
}
func TestCheckoutRedPacketBusinessDate(t *testing.T) {
	s := open(t)
	day := settings.Date(time.Now()).AddDate(0, 0, 2)
	s.call("change_setting", map[string]any{"key": "business_day_end", "value": "02:00", "effective_from": day.Format(time.DateOnly)}, 200)
	at := time.Date(day.Year(), day.Month(), day.Day()+1, 1, 30, 0, 0, settings.Location)
	line := s.checkoutLine()
	for _, offset := range []int{-1, 0} {
		o := s.customerOrder(line)
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		var packet uuid.UUID
		if err := tx.QueryRow(context.Background(), `INSERT INTO red_packets(tenant_id,store_id,customer_id,kind,face_value,expires_on,business_date,issued_by) VALUES($1,$2,$3,'gift',100,$4,$4,$5) RETURNING id`, s.Res.TenantID, s.Res.StoreID, s.customer, day.AddDate(0, 0, offset), s.Res.OwnerID).Scan(&packet); err != nil {
			t.Fatal(err)
		}
		dbtest.Commit(t, tx)
		before := s.packetSnapshot()
		err := db.InTenant(context.Background(), s.Pool, s.Res.TenantID, func(q *dbgen.Queries) error {
			_, err := orders.CheckoutOrdersAtForTest(context.Background(), ops.Env{Caller: s.MustCaller(s.Owner), Q: q}, new(checkoutInput(checkoutOrder(o, packetPayment(map[string]any{"id": packet.String()}, "100")))), at)
			return err
		})
		if offset == 0 && err != nil || offset == -1 && (ops.StatusOf(err) != 409 || err.Error() != "红包已过期" || before != s.packetSnapshot()) {
			t.Fatal(err)
		}
	}
}
func TestCheckoutWaitsForRedPacket(t *testing.T) {
	s := open(t)
	o := s.customerOrder(s.checkoutLine())
	p := s.redPacket(s.customer, "100")
	c := s.MustCaller(s.Owner)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "SELECT id FROM red_packets WHERE id=$1 FOR NO KEY UPDATE", p["id"])
	dbtest.Exec(t, tx, "UPDATE red_packets SET revoked_by=$1,revoked_at=clock_timestamp(),revoke_reason='撤回' WHERE id=$2", s.Res.OwnerID, p["id"])
	expected := dbtest.SnapshotTx(t, tx, "checkouts", "order_payments", "orders", "order_items", "card_ledger", "red_packets", "red_packet_sales", "audit_log")
	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTP(t, &c, "checkout_orders", checkoutInput(checkoutOrder(o, packetPayment(p, "100"))))
	}()
	optest.AssertWaiting(t, done)
	dbtest.Commit(t, tx)
	r := optest.WaitResult(t, done, 409)
	if r.Body["detail"] != "红包已撤回" || s.packetSnapshot() != expected {
		t.Fatal(r)
	}
}

func TestCheckoutRedPacketsUUIDOrder(t *testing.T) {
	s := open(t)
	low, high := s.redPacket(s.customer, "100"), s.redPacket(s.customer, "100")
	if low["id"].(string) > high["id"].(string) {
		low, high = high, low
	}
	o := s.customerOrder(s.checkoutLine())
	c := s.MustCaller(s.Owner)
	held := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, held, "SELECT id FROM red_packets WHERE id=$1 FOR NO KEY UPDATE", low["id"])
	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTP(t, &c, "checkout_orders", checkoutInput(checkoutOrder(o, packetPayment(high, "50"), packetPayment(low, "50"))))
	}()
	dbtest.WaitForLockWait(t, s.Pool)
	probe := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, err := probe.Exec(context.Background(), "SELECT id FROM red_packets WHERE id=$1 FOR NO KEY UPDATE NOWAIT", high["id"])
	dbtest.Rollback(t, probe)
	dbtest.Commit(t, held)
	optest.WaitResult(t, done, 200)
	if err != nil {
		t.Fatal("结账未按 UUID 锁红包", err)
	}
}

func TestCheckoutRedPacketRollbackAcrossOrders(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	a, b := s.customerOrder(line), s.customerOrder(line)
	first, second := s.redPacket(s.customer, "100"), s.redPacket(s.customer, "99")
	s.packetRefused(s.MustCaller(s.Owner), checkoutInput(checkoutOrder(a, packetPayment(first, "100")), checkoutOrder(b, packetPayment(second, "100"))), 409, "红包抵扣不能超过面额")
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "list_customer_red_packets", map[string]any{"customer_id": s.customer})
		if r.Status != 200 {
			t.Fatal(r)
		}
		for _, raw := range r.Body["items"].([]any) {
			if raw.(map[string]any)["status"] != "available" {
				t.Fatal(r)
			}
		}
	}
}

func TestCheckoutRedPacketPricing(t *testing.T) {
	s := open(t)
	card := s.paymentCard(s.paymentType("红包混合八折卡", "80"), "100", "0")
	packet := s.redPacket(s.customer, "100")
	o := s.customerOrder(s.checkoutLine())
	after := s.paid(o, packetPayment(packet, "20"), cardPayment(card, "60"))
	if after["net"] != "80.00" || after["discount"] != "20.00" || len(after["payments"].([]any)) != 2 {
		t.Fatal(after)
	}
}

func TestMergeRedPacketsLocksCardsFirst(t *testing.T) {
	s := open(t)
	card := s.paymentCard(s.paymentType("合并锁卡", nil), "100", "0")
	packet := s.redPacket(s.customer, "100")
	keep := s.call("create_customer", map[string]any{}, 200)["customer"].(map[string]any)
	c := s.MustCaller(s.Owner)
	held := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, held, "SELECT id FROM cards WHERE id=$1 FOR NO KEY UPDATE", card["id"])
	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTP(t, &c, "merge_customers", map[string]any{"keep_id": keep["id"], "merged_id": s.customer})
	}()
	dbtest.WaitForLockWait(t, s.Pool)
	probe := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, err := probe.Exec(context.Background(), "SELECT id FROM red_packets WHERE id=$1 FOR NO KEY UPDATE NOWAIT", packet["id"])
	dbtest.Rollback(t, probe)
	dbtest.Commit(t, held)
	optest.WaitResult(t, done, 200)
	if err != nil {
		t.Fatal("合并在卡之前锁了红包，与结账倒置锁序", err)
	}
}
