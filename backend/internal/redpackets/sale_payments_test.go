package redpackets_test

import (
	"context"
	"strings"
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
)

func TestRedPacketSalePayments(t *testing.T) {
	s := open(t)
	in := s.input("sold")
	in["price"] = "80"
	in["payments"] = []any{map[string]any{"method": "wechat", "amount": "50.00"}, map[string]any{"method": "alipay", "amount": "30.00"}}
	p := s.call("issue_red_packet", in, 200)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var n int
	if err := tx.QueryRow(context.Background(), `SELECT count(*) FROM sale_payments WHERE red_packet_id=$1 AND card_ledger_id IS NULL AND method IN ('wechat','alipay') AND business_date=$2`, p["id"], p["business_date"]).Scan(&n); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	dbtest.Rollback(t, tx)
}

func TestRedPacketPaymentRules(t *testing.T) {
	s := open(t)
	for _, tc := range []struct {
		name string
		in   map[string]any
		want int
		text string
	}{
		{"sold mismatch", func() map[string]any {
			in := s.input("sold")
			in["payments"] = []any{map[string]any{"method": "cash", "amount": "70"}}
			return in
		}(), 409, "收款合计必须等于 80.00"},
		{"gift with payment", map[string]any{"customer_id": s.customer, "kind": "gift", "face_value": "10", "expires_on": "2999-01-01", "payments": []any{map[string]any{"method": "cash", "amount": "1"}}}, 409, "收款合计必须等于 0.00"},
		{"unknown method", func() map[string]any {
			in := s.input("sold")
			in["payments"] = []any{map[string]any{"method": "groupbuy", "amount": "80"}}
			return in
		}(), 422, "validation failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := s.snapshot()
			r := s.HTTPWithToken(t, s.Owner, "issue_red_packet", tc.in)
			if r.Status != tc.want || !strings.Contains(r.Detail(), tc.text) || s.snapshot() != before {
				t.Fatal(r)
			}
		})
	}
}
