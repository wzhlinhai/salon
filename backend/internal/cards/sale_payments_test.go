package cards_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func TestSalePaymentsRecorded(t *testing.T) {
	s := open(t)
	card := s.call("open_card", map[string]any{"customer_id": s.customer, "card_type_id": s.cardType, "principal": "100.00", "bonus": "10.00", "sales_staff_ids": []any{s.Res.OwnerID}, "payments": []any{map[string]any{"method": "cash", "amount": "60.00"}, map[string]any{"method": "unionpay", "amount": "40.00"}}}, 200)
	s.call("recharge_card", map[string]any{"card_id": card["id"], "principal": "50.00", "bonus": "0.00", "sales_staff_ids": []any{s.Res.OwnerID}, "payments": []any{map[string]any{"method": "wechat", "amount": "50.00"}}}, 200)
	to := s.call("create_card_type", typeInput("升级目标"), 200)
	s.call("upgrade_card", map[string]any{"card_id": card["id"], "to_type_id": to["id"], "principal": "25.00", "bonus": "0.00", "sales_staff_ids": []any{s.Res.OwnerID}, "payments": []any{map[string]any{"method": "alipay", "amount": "25.00"}}}, 200)

	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	ledgerIDs := map[uuid.UUID]bool{}
	le, err := tx.Query(context.Background(), `SELECT id FROM card_ledger WHERE card_id=$1`, card["id"])
	if err != nil {
		t.Fatal(err)
	}
	for le.Next() {
		var id uuid.UUID
		if err := le.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ledgerIDs[id] = true
	}
	le.Close()
	rows, err := tx.Query(context.Background(), `SELECT method,amount::text,business_date::text,card_ledger_id,red_packet_id FROM sale_payments WHERE store_id=$1 ORDER BY id`, s.Res.StoreID)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for rows.Next() {
		var method, amount, day string
		var ledger, packet *uuid.UUID
		if err := rows.Scan(&method, &amount, &day, &ledger, &packet); err != nil {
			t.Fatal(err)
		}
		if ledger == nil || !ledgerIDs[*ledger] || packet != nil {
			t.Fatalf("sale payment source = %v/%v, want one of this card's ledgers", ledger, packet)
		}
		got = append(got, method+"/"+amount+"/"+day)
	}
	rows.Close()
	dbtest.Rollback(t, tx)
	day := settings.Date(s.Now()).Format("2006-01-02")
	want := []string{"cash/60.00/" + day, "unionpay/40.00/" + day, "wechat/50.00/" + day, "alipay/25.00/" + day}
	if len(got) != len(want) {
		t.Fatal(got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatal(got, want)
		}
	}
}

func TestSalePaymentRules(t *testing.T) {
	s := open(t)
	base := func(principal string, payments []any) map[string]any {
		return map[string]any{"customer_id": s.customer, "card_type_id": s.cardType, "principal": principal, "bonus": "0.00", "sales_staff_ids": []any{s.Res.OwnerID}, "payments": payments}
	}
	before := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "sale_payments", "cards", "card_ledger", "performance_entries", "audit_log")
	for _, tc := range []struct {
		name string
		in   map[string]any
	}{
		{"sum mismatch", base("100.00", []any{map[string]any{"method": "cash", "amount": "90.00"}})},
		{"money sale without payment", base("100.00", []any{})},
		{"zero with payment", base("0.00", []any{map[string]any{"method": "cash", "amount": "1.00"}})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := s.HTTPWithToken(t, s.Owner, "open_card", tc.in)
			if r.Status != 409 || r.Detail() != "收款合计必须等于 "+tc.in["principal"].(string) {
				t.Fatal(r)
			}
			if dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "sale_payments", "cards", "card_ledger", "performance_entries", "audit_log") != before {
				t.Fatal("rejection wrote data")
			}
		})
	}
	for _, tc := range []struct {
		name string
		in   map[string]any
		text string
	}{
		{"unknown method", base("10.00", []any{map[string]any{"method": "groupbuy", "amount": "10.00"}}), "validation failed"},
		{"zero amount", base("10.00", []any{map[string]any{"method": "cash", "amount": "0.00"}}), "invalid arguments: 收款金额必须大于零"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := s.HTTPWithToken(t, s.Owner, "open_card", tc.in)
			if r.Status != 422 || !strings.Contains(r.Detail(), tc.text) {
				t.Fatal(r)
			}
			if dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "sale_payments", "cards", "card_ledger", "performance_entries", "audit_log") != before {
				t.Fatal("rejection wrote data")
			}
		})
	}
}

func TestZeroMoneySaleTakesEmptyPayments(t *testing.T) {
	s := open(t)
	s.call("open_card", map[string]any{"customer_id": s.customer, "card_type_id": s.cardType, "principal": "0.00", "bonus": "200.00", "sales_staff_ids": []any{s.Res.OwnerID}, "payments": []any{}}, 200)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var n int
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM sale_payments").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	dbtest.Rollback(t, tx)
}

func TestCountCardPaymentsUseCountsValue(t *testing.T) {
	s := open(t)
	item := s.item("剪发")
	ct := s.call("create_card_type", countType("次数卡", "count", countItem(item)), 200)
	in := s.opening()
	in["card_type_id"], in["principal"], in["bonus"] = ct["id"], "0.00", "0.00"
	in["payments"] = []any{}
	r := s.HTTPWithToken(t, s.Owner, "open_card", in)
	if r.Status != 409 || r.Detail() != "收款合计必须等于 123.40" {
		t.Fatal(r)
	}
	in["payments"] = cashPayment("123.40")
	card := s.call("open_card", in, 200)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var ledger uuid.UUID
	var amount string
	if err := tx.QueryRow(context.Background(), `SELECT l.id, sp.amount::text FROM card_ledger l JOIN sale_payments sp ON sp.card_ledger_id=l.id WHERE l.card_id=$1 AND l.kind='open'`, card["id"]).Scan(&ledger, &amount); err != nil {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	if amount != "123.40" {
		t.Fatal(amount)
	}
}
