package cards_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/cards"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func saleAmounts(t *testing.T, s *fixture, card any, method, purchase string, ids []uuid.UUID, want []string) {
	t.Helper()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	rows, err := tx.Query(context.Background(), `SELECT e.staff_id,e.amount::text,e.paid_amount=e.amount AND e.ratio=100 AND e.kind='sale' AND e.business_date=l.business_date AND e.purchase=$3 AND e.order_id IS NULL AND e.order_item_id IS NULL AND e.checkout_id IS NULL AND e.settings_snapshot_id IS NULL AND e.red_packet_id IS NULL FROM performance_entries e JOIN card_ledger l ON l.id=e.card_ledger_id WHERE l.card_id=$1 AND e.method=$2 ORDER BY e.id`, card, method, purchase)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for rows.Next() {
		var staff uuid.UUID
		var amount string
		var valid bool
		if err := rows.Scan(&staff, &amount, &valid); err != nil {
			t.Fatal(err)
		}
		i := len(got)
		if !valid || i >= len(ids) || staff != ids[i] {
			t.Fatal(staff, amount, valid)
		}
		got = append(got, amount)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	dbtest.Rollback(t, tx)
	if !reflect.DeepEqual(got, want) {
		t.Fatal(method, got, want)
	}
}

func TestCardSaleSources(t *testing.T) {
	s := open(t)
	second, _ := s.Hire("乙", "002", "sale2")
	third, _ := s.Hire("丙", "003", "sale3")
	ids := []uuid.UUID{second, s.Res.OwnerID, third}
	in := s.opening()
	in["principal"], in["bonus"], in["sales_staff_ids"] = "1000", "200", ids[:2]
	card := s.call("open_card", in, 200)
	saleAmounts(t, s, card["id"], "card_open", "first", ids[:2], []string{"500.00", "500.00"})
	next := s.call("open_card", in, 200)
	saleAmounts(t, s, next["id"], "card_open", "repeat", ids[:2], []string{"500.00", "500.00"})
	to := s.call("create_card_type", typeInput("高级"), 200)
	s.call("create_card_upgrade_rule", rule(s.cardType, to["id"], "single_recharge", "500"), 200)
	recharge := s.recharge(card["id"])
	recharge["principal"], recharge["bonus"], recharge["sales_staff_ids"] = "500", "100", ids
	out := s.call("recharge_card", recharge, 200)
	if out["upgraded_to"] == nil {
		t.Fatal(out)
	}
	saleAmounts(t, s, card["id"], "card_recharge", "repeat", ids, []string{"166.68", "166.66", "166.66"})
	recharge["principal"] = "0"
	s.call("recharge_card", recharge, 200)
	saleAmounts(t, s, card["id"], "card_recharge", "repeat", ids, []string{"166.68", "166.66", "166.66"})
	saleAmounts(t, s, card["id"], "card_upgrade", "repeat", ids, []string{})
	u := upgrade(card["id"], s.cardType)
	u["principal"], u["bonus"], u["sales_staff_ids"] = "200", "50", ids[:2]
	s.call("upgrade_card", u, 200)
	saleAmounts(t, s, card["id"], "card_upgrade", "repeat", ids[:2], []string{"100.00", "100.00"})
	s.call("upgrade_card", upgrade(card["id"], to["id"]), 200)
	saleAmounts(t, s, card["id"], "card_upgrade", "repeat", ids[:2], []string{"100.00", "100.00"})
	assertSQL(t, s, `SELECT sum(paid_amount)=2700 AND count(*)=9 FROM performance_entries WHERE kind='sale'`)
}

func TestCardSaleCounts(t *testing.T) {
	for _, kind := range []string{"count", "package"} {
		t.Run(kind, func(t *testing.T) {
			s := open(t)
			item := s.item("剪发")
			itemIn := map[string]any{"item_id": item, "paid_count": 10, "gift_count": 2, "unit_price": "50"}
			typeIn := countType("次数销售", kind, itemIn)
			ct := s.call("create_card_type", typeIn, 200)
			in := s.opening()
			in["card_type_id"], in["principal"], in["bonus"] = ct["id"], "0", "0"
			want := "500.00"
			if kind == "package" {
				in["principal"], in["bonus"] = "300", "200"
				want = "800.00"
			}
			card := s.call("open_card", in, 200)
			saleAmounts(t, s, card["id"], "card_open", "first", []uuid.UUID{s.Res.OwnerID}, []string{want})
			itemIn["unit_price"] = "60"
			typeIn["id"], typeIn["active"] = ct["id"], true
			s.call("update_card_type", typeIn, 200)
			saleAmounts(t, s, card["id"], "card_open", "first", []uuid.UUID{s.Res.OwnerID}, []string{want})
			next := s.call("open_card", in, 200)
			want = "600.00"
			if kind == "package" {
				want = "900.00"
			}
			saleAmounts(t, s, next["id"], "card_open", "repeat", []uuid.UUID{s.Res.OwnerID}, []string{want})
		})
	}
}

func TestCardSaleEarlierCards(t *testing.T) {
	for _, mode := range []string{"refunded", "merged"} {
		t.Run(mode, func(t *testing.T) {
			s := open(t)
			card := s.call("open_card", s.opening(), 200)
			if mode == "refunded" {
				q := s.call("preview_card_refund", map[string]any{"card_id": card["id"]}, 200)
				s.call("refund_card", map[string]any{"card_id": card["id"], "amount": q["amount"], "method": "cash"}, 200)
			} else {
				keep := s.call("create_customer", map[string]any{}, 200)["customer"].(map[string]any)["id"].(string)
				s.call("merge_customers", map[string]any{"keep_id": keep, "merged_id": s.customer}, 200)
				s.customer = keep
			}
			next := s.call("open_card", s.opening(), 200)
			saleAmounts(t, s, next["id"], "card_open", "repeat", []uuid.UUID{s.Res.OwnerID}, []string{"1234.56"})
		})
	}
}

func TestCardSaleOnlySources(t *testing.T) {
	s := open(t)
	a := s.call("open_card", s.opening(), 200)
	b := s.call("open_card", s.opening(), 200)
	before := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "performance_entries")
	s.call("adjust_card", map[string]any{"card_id": a["id"], "principal": "20", "bonus": "5", "reason": "更正"}, 200)
	s.call("transfer_card_balance", transfer(a["id"], b["id"]), 200)
	q := s.call("preview_card_refund", map[string]any{"card_id": a["id"]}, 200)
	s.call("refund_card", map[string]any{"card_id": a["id"], "amount": q["amount"], "method": "cash"}, 200)
	if dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "performance_entries") != before {
		t.Fatal("non-sale changed performance")
	}
}

func TestCardSaleBusinessDate(t *testing.T) {
	s := open(t)
	caller := s.MustCaller(s.Owner)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, `INSERT INTO setting_changes(tenant_id,store_id,key,value,is_default,old_value,old_is_default,effective_from,changed_by) VALUES($1,$2,'business_day_end','"02:00"',false,'"00:00"',true,'2020-01-01',$3)`, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID)
	var principal money.Amount
	if err := principal.Scan("100"); err != nil {
		t.Fatal(err)
	}
	out, err := cards.OpenCardAtForTest(context.Background(), ops.Env{Q: dbgen.New(tx), Caller: caller}, &cards.OpenCardInput{CustomerID: uuid.MustParse(s.customer), CardTypeID: uuid.MustParse(s.cardType), TopUpInput: cards.TopUpInput{Principal: principal, SalesStaffIDs: []uuid.UUID{s.Res.OwnerID}}}, time.Date(2030, 1, 2, 1, 30, 0, 0, settings.Location))
	if err != nil {
		t.Fatal(err)
	}
	var ok bool
	if err := tx.QueryRow(context.Background(), `SELECT count(*)=1 AND bool_and(e.business_date=l.business_date AND e.business_date='2030-01-01') FROM performance_entries e JOIN card_ledger l ON l.id=e.card_ledger_id WHERE l.card_id=$1`, out.ID).Scan(&ok); err != nil || !ok {
		t.Fatal(ok, err)
	}
	dbtest.Rollback(t, tx)
}

func TestCardSaleConcurrentFirst(t *testing.T) {
	s := open(t)
	caller := s.MustCaller(s.Owner)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var value money.Amount
	if err := value.Scan("100"); err != nil {
		t.Fatal(err)
	}
	_, err := cards.OpenCardAtForTest(context.Background(), ops.Env{Q: dbgen.New(tx), Caller: caller}, &cards.OpenCardInput{CustomerID: uuid.MustParse(s.customer), CardTypeID: uuid.MustParse(s.cardType), TopUpInput: cards.TopUpInput{Principal: value, SalesStaffIDs: []uuid.UUID{s.Res.OwnerID}}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan optest.Result, 1)
	go func() { done <- s.HTTP(t, &caller, "open_card", s.opening()) }()
	optest.AssertWaiting(t, done)
	dbtest.Commit(t, tx)
	optest.WaitResult(t, done, 200)
	assertSQL(t, s, `SELECT count(*)=2 AND count(*) FILTER(WHERE purchase='first')=1 AND count(*) FILTER(WHERE purchase='repeat')=1 FROM performance_entries WHERE kind='sale'`)
}

func TestCardSaleAI(t *testing.T) {
	s := open(t)
	card := s.call("open_card", s.opening(), 200)
	target := s.call("create_card_type", typeInput("升级"), 200)
	before := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "performance_entries", "cards", "card_ledger", "card_upgrades")
	for _, tc := range []struct {
		op string
		in any
	}{{"open_card", s.opening()}, {"recharge_card", s.recharge(card["id"])}, {"upgrade_card", upgrade(card["id"], target["id"])}} {
		r := s.AI(t, s.MustCaller(s.Owner), tc.op, tc.in)
		if r.Status != 409 || !errors.Is(r.Err, ops.ErrDraftRequired) {
			t.Fatal(r)
		}
		if dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "performance_entries", "cards", "card_ledger", "card_upgrades") != before {
			t.Fatal("AI wrote sale")
		}
	}
}

func TestOpenCardMissingStoreCustomer(t *testing.T) {
	s := open(t)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var other uuid.UUID
	if err := tx.QueryRow(context.Background(), "INSERT INTO stores(tenant_id,name) VALUES($1,'别店') RETURNING id", s.Res.TenantID).Scan(&other); err != nil {
		t.Fatal(err)
	}
	dbtest.Exec(t, tx, "RESET ROLE")
	dbtest.Exec(t, tx, "UPDATE customers SET store_id=$2 WHERE id=$1", s.customer, other)
	dbtest.Commit(t, tx)
	caller := s.MustCaller(s.Owner)
	for _, tc := range []struct {
		name string
		id   uuid.UUID
	}{{"unknown", uuid.New()}, {"other-store", uuid.MustParse(s.customer)}} {
		t.Run(tc.name, func(t *testing.T) {
			before := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "cards", "card_items", "card_ledger", "card_ledger_sales", "card_count_ledger", "card_points_ledger", "performance_entries", "audit_log")
			in := s.opening()
			in["customer_id"] = tc.id
			r := s.HTTP(t, &caller, "open_card", in)
			if r.Status != 409 || r.Detail() != "没有本店顾客" {
				t.Fatal(r)
			}
			if dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "cards", "card_items", "card_ledger", "card_ledger_sales", "card_count_ledger", "card_points_ledger", "performance_entries", "audit_log") != before {
				t.Fatal("rejected opening wrote data")
			}
		})
	}
}

func TestCardSaleCustomerStore(t *testing.T) {
	s := open(t)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var other uuid.UUID
	if err := tx.QueryRow(context.Background(), "INSERT INTO stores(tenant_id,name) VALUES($1,'别店') RETURNING id", s.Res.TenantID).Scan(&other); err != nil {
		t.Fatal(err)
	}
	dbtest.Exec(t, tx, "RESET ROLE")
	dbtest.Exec(t, tx, "UPDATE customers SET store_id=$2 WHERE id=$1", s.customer, other)
	dbtest.Commit(t, tx)
	caller := s.MustCaller(s.Owner)
	locked := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, locked, "SELECT 1 FROM customers WHERE id=$1 FOR NO KEY UPDATE", s.customer)
	done := make(chan optest.Result, 1)
	go func() { done <- s.HTTP(t, &caller, "open_card", s.opening()) }()
	r := optest.WaitResult(t, done, 409)
	if r.Status != 409 || r.Detail() != "没有本店顾客" {
		t.Fatal(r)
	}
	assertSQL(t, s, `SELECT NOT EXISTS(SELECT 1 FROM cards) AND NOT EXISTS(SELECT 1 FROM card_ledger) AND NOT EXISTS(SELECT 1 FROM performance_entries)`)
	dbtest.Rollback(t, locked)
}

func TestCardSaleZeroOpening(t *testing.T) {
	s := open(t)
	in := s.opening()
	in["principal"], in["bonus"] = "0", "200"
	s.call("open_card", in, 200)
	assertSQL(t, s, `SELECT NOT EXISTS(SELECT 1 FROM performance_entries)`)
	card := s.call("open_card", s.opening(), 200)
	saleAmounts(t, s, card["id"], "card_open", "repeat", []uuid.UUID{s.Res.OwnerID}, []string{"1234.56"})
}

func TestCardSaleMultipleCounts(t *testing.T) {
	s := open(t)
	first, second := s.item("剪发"), s.item("护理")
	ct := s.call("create_card_type", countType("多项目套餐", "package",
		map[string]any{"item_id": first, "paid_count": 10, "gift_count": 2, "unit_price": "50"},
		map[string]any{"item_id": second, "paid_count": 2, "gift_count": 3, "unit_price": "40"}), 200)
	in := s.opening()
	in["card_type_id"], in["principal"], in["bonus"] = ct["id"], "300", "200"
	card := s.call("open_card", in, 200)
	saleAmounts(t, s, card["id"], "card_open", "first", []uuid.UUID{s.Res.OwnerID}, []string{"880.00"})
}

func TestCardSaleLargeCounts(t *testing.T) {
	for _, n := range []int{2, 1} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			s := open(t)
			second, _ := s.Hire("乙", "002", "large-sale")
			ids := []uuid.UUID{s.Res.OwnerID, second}
			ct := s.call("create_card_type", countType("大额次卡", "count", map[string]any{"item_id": s.item("剪发"), "paid_count": 9999, "gift_count": 0, "unit_price": "20000.00"}), 200)
			in := s.opening()
			in["card_type_id"], in["principal"], in["bonus"], in["sales_staff_ids"] = ct["id"], "0", "0", ids[:n]
			if n == 2 {
				card := s.call("open_card", in, 200)
				saleAmounts(t, s, card["id"], "card_open", "first", ids, []string{"99990000.00", "99990000.00"})
				return
			}
			before := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "cards", "card_items", "card_ledger", "card_ledger_sales", "card_count_ledger", "performance_entries", "audit_log")
			caller := s.MustCaller(s.Owner)
			r := s.HTTP(t, &caller, "open_card", in)
			if r.Status != 409 || r.Detail() != "销售业绩金额超过上限" {
				t.Fatal(r)
			}
			if dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "cards", "card_items", "card_ledger", "card_ledger_sales", "card_count_ledger", "performance_entries", "audit_log") != before {
				t.Fatal("rejected opening wrote data")
			}
		})
	}
}
