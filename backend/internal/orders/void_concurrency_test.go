package orders_test

import (
	"context"
	"testing"
	"time"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/orders"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func TestVoidWaitsForCard(t *testing.T) {
	s := open(t)
	c := s.paymentCard(s.paymentType("卡", nil), "80", "100")
	o := s.paid(s.customerOrder(s.checkoutLine()), cardPayment(c, "100"))
	held := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, held, "SELECT id FROM cards WHERE id=$1 FOR NO KEY UPDATE", c["id"])
	// 充值先取得卡锁并追加流水，撤单必须接续该余额。
	dbtest.Exec(t, held, `INSERT INTO card_ledger(tenant_id,store_id,card_id,kind,principal_before,principal_delta,principal_after,bonus_before,bonus_delta,bonus_after,business_date,actor_id) VALUES($1,$2,$3,'recharge',0,25,25,80,10,90,$4,$5)`, s.Res.TenantID, s.Res.StoreID, c["id"], settings.Date(time.Now()), s.Res.OwnerID)
	done := make(chan optest.Result, 1)
	go func() { done <- s.HTTPWithToken(t, s.Owner, "void_order", voidInput(o)) }()
	optest.AssertWaiting(t, done)
	dbtest.Commit(t, held)
	optest.WaitResult(t, done, 200)
	l := s.consumeEntries(c)[0].(map[string]any)
	if l["principal_before"] != "25.00" || l["bonus_before"] != "90.00" || l["principal_after"] != "105.00" || l["bonus_after"] != "110.00" {
		t.Fatal(l)
	}
}
func TestVoidWaitsForRepayment(t *testing.T) {
	s := open(t)
	o := s.paid(s.customerOrder(s.checkoutLine()), payment("debt", "100"))
	d := o["payments"].([]any)[0].(map[string]any)["debt_id"]
	held := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, held, "SELECT id FROM debts WHERE id=$1 FOR NO KEY UPDATE", d)
	dbtest.Exec(t, held, `INSERT INTO debt_repayments(tenant_id,store_id,debt_id,amount,method,business_date,repaid_by) VALUES($1,$2,$3,1,'cash',$4,$5)`, s.Res.TenantID, s.Res.StoreID, d, settings.Date(time.Now()), s.Res.OwnerID)
	expected := dbtest.SnapshotTx(t, held, "orders", "order_voids", "debts", "debt_repayments", "audit_log")
	done := make(chan optest.Result, 1)
	go func() { done <- s.HTTPWithToken(t, s.Owner, "void_order", voidInput(o)) }()
	optest.AssertWaiting(t, done)
	dbtest.Commit(t, held)
	r := optest.WaitResult(t, done, 409)
	if r.Body["detail"] != "这张单的挂账已有还款，不能撤单" || expected != dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "orders", "order_voids", "debts", "debt_repayments", "audit_log") {
		t.Fatal(r)
	}
}
func TestRepaymentWaitsForVoid(t *testing.T) {
	s := open(t)
	o := s.paid(s.customerOrder(s.checkoutLine()), payment("debt", "100"))
	d := o["payments"].([]any)[0].(map[string]any)["debt_id"]
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	q := dbgen.New(tx)
	c := s.MustCaller(s.Owner)
	if _, err := orders.VoidOrderAtForTest(context.Background(), ops.Env{Caller: c, Q: q}, new(voidInput(o)), time.Now()); err != nil {
		t.Fatal(err)
	}
	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "repay_debt", map[string]any{"debt_id": d, "amount": "1", "method": "cash"})
	}()
	optest.AssertWaiting(t, done)
	dbtest.Commit(t, tx)
	r := optest.WaitResult(t, done, 409)
	if r.Body["detail"] != "这笔欠款已撤单" {
		t.Fatal(r)
	}
}
func TestCheckoutWaitsForVoidRedPacket(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	p := s.redPacket(s.customer, "100")
	o := s.paid(s.customerOrder(line), packetPayment(p, "100"))
	next, other := s.customerOrder(line), s.customerOrder(line)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	q := dbgen.New(tx)
	c := s.MustCaller(s.Owner)
	if _, err := orders.VoidOrderAtForTest(context.Background(), ops.Env{Caller: c, Q: q}, new(voidInput(o)), time.Now()); err != nil {
		t.Fatal(err)
	}
	done := make(chan optest.Result, 2)
	for _, n := range []map[string]any{next, other} {
		go func() {
			done <- s.HTTPWithToken(t, s.Owner, "checkout_orders", checkoutInput(checkoutOrder(n, packetPayment(p, "100"))))
		}()
	}
	optest.AssertWaiting(t, done)
	dbtest.Commit(t, tx)
	statuses := map[int]int{}
	for range 2 {
		select {
		case r := <-done:
			statuses[r.Status]++
			if r.Status == 409 && r.Detail() != "红包已用" {
				t.Fatal(r)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("checkout timed out")
		}
	}
	if statuses[200] != 1 || statuses[409] != 1 {
		t.Fatal(statuses)
	}
}

func (s *fixture) checkoutAtForVoid(o map[string]any, at time.Time, ps ...orders.PaymentInput) {
	s.t.Helper()
	tx := dbtest.AppTx(s.t, s.Pool, s.Res.TenantID)
	_, err := orders.CheckoutOrdersAtForTest(context.Background(), ops.Env{Caller: s.MustCaller(s.Owner), Q: dbgen.New(tx)}, new(checkoutInput(checkoutOrder(o, ps...))), at)
	if err != nil {
		s.t.Fatal(err)
	}
	dbtest.Commit(s.t, tx)
}

func TestVoidLockOrder(t *testing.T) {
	for _, stage := range []string{"order-debt", "debt-card", "card-packet", "card-uuid", "debt-uuid", "packet-uuid"} {
		t.Run(stage, func(t *testing.T) {
			s := open(t)
			line := s.checkoutLine()
			ct := s.paymentType("锁序卡", nil)
			a, b := s.paymentCard(ct, "100", "0"), s.paymentCard(ct, "100", "0")
			low, high := a, b
			if low["id"].(string) > high["id"].(string) {
				low, high = high, low
			}
			pl, ph := s.redPacket(s.customer, "100"), s.redPacket(s.customer, "100")
			if pl["id"].(string) > ph["id"].(string) {
				pl, ph = ph, pl
			}
			o := s.paid(s.customerOrder(line), payment("debt", "10"), payment("debt", "10"), cardPayment(high, "20"), cardPayment(low, "20"), packetPayment(ph, "20"), packetPayment(pl, "20"))
			ds := o["payments"].([]any)
			dl, dh := ds[0].(map[string]any)["debt_id"], ds[1].(map[string]any)["debt_id"]
			heldTable, heldID, probeTable, probeID := "orders", o["id"], "debts", dl
			switch stage {
			case "debt-card":
				heldTable, heldID, probeTable, probeID = "debts", dl, "cards", low["id"]
			case "card-packet":
				heldTable, heldID, probeTable, probeID = "cards", low["id"], "red_packets", pl["id"]
			case "card-uuid":
				heldTable, heldID, probeTable, probeID = "cards", low["id"], "cards", high["id"]
			case "debt-uuid":
				heldTable, heldID, probeTable, probeID = "debts", dl, "debts", dh
			case "packet-uuid":
				heldTable, heldID, probeTable, probeID = "red_packets", pl["id"], "red_packets", ph["id"]
			}
			held := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			dbtest.Exec(t, held, "SELECT id FROM "+heldTable+" WHERE id=$1 FOR NO KEY UPDATE", heldID)
			done := make(chan optest.Result, 1)
			go func() { done <- s.HTTPWithToken(t, s.Owner, "void_order", voidInput(o)) }()
			dbtest.WaitForLockWait(t, s.Pool)
			probe := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			_, err := probe.Exec(context.Background(), "SELECT id FROM "+probeTable+" WHERE id=$1 FOR NO KEY UPDATE NOWAIT", probeID)
			dbtest.Rollback(t, probe)
			dbtest.Commit(t, held)
			optest.WaitResult(t, done, 200)
			if err != nil {
				t.Fatal("void lock order", stage, err)
			}
		})
	}
}

func TestVoidWaitsForCheckout(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	c := s.paymentCard(s.paymentType("结账等待卡", nil), "200", "50")
	a := s.paid(s.customerOrder(line), cardPayment(c, "100"))
	b := s.customerOrder(line)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, err := orders.CheckoutOrdersAtForTest(context.Background(), ops.Env{Caller: s.MustCaller(s.Owner), Q: dbgen.New(tx)}, new(checkoutInput(checkoutOrder(b, cardPayment(c, "30"), payment("cash", "70")))), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan optest.Result, 1)
	go func() { done <- s.HTTPWithToken(t, s.Owner, "void_order", voidInput(a)) }()
	optest.AssertWaiting(t, done)
	dbtest.Commit(t, tx)
	optest.WaitResult(t, done, 200)
	l := s.consumeEntries(c)[0].(map[string]any)
	if l["principal_after"] != "170.00" || l["bonus_after"] != "50.00" {
		t.Fatal(l)
	}
}
