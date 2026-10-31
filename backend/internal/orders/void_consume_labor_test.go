package orders_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/orders"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func TestVoidConsumeLabor(t *testing.T) {
	s := open(t)
	line := s.laborLine("8.03", nil)
	c := s.countCard(line, "count", 1, 0)
	o := s.customerOrder(line)
	s.paid(o, countPayment(c, firstLine(o), "100"))
	original := withoutSale(s.performanceList()["entries"].([]any))
	day := settings.Date(time.Now()).AddDate(0, 0, 1)
	if err := db.InTenant(context.Background(), s.Pool, s.Res.TenantID, func(q *dbgen.Queries) error {
		_, err := orders.VoidOrderAtForTest(context.Background(), ops.Env{Caller: s.MustCaller(s.Owner), Q: q}, new(voidInput(o)), day.Add(8*time.Hour))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "list_performance", map[string]any{"date_from": day.AddDate(0, 0, -1).Format(time.DateOnly), "date_to": day.Format(time.DateOnly)})
		if r.Status != 200 || len(withoutSale(r.Body["entries"].([]any))) != 8 {
			t.Fatal(r)
		}
		for _, v := range withoutSale(r.Body["totals"].([]any)) {
			total := v.(map[string]any)
			want := "0.00"
			if total["kind"] == "item" {
				want = "0"
			}
			if total["amount"] != want || total["paid_amount"] != want {
				t.Fatal(r)
			}
		}
		for _, v := range withoutSale(r.Body["entries"].([]any))[:4] {
			e := v.(map[string]any)
			if e["reversal"] != true || e["business_date"] != day.Format(time.DateOnly) {
				t.Fatal(e)
			}
			found := false
			for _, v := range original {
				orig := v.(map[string]any)
				if e["reverses_id"] != orig["id"] {
					continue
				}
				found = true
				for _, key := range []string{"kind", "order_id", "order_item_id", "slot", "position_id", "staff", "method", "paid_amount", "ratio", "amount", "settings_snapshot_id", "checkout_id"} {
					if !reflect.DeepEqual(e[key], orig[key]) {
						t.Fatal(key, e, orig)
					}
				}
			}
			if !found {
				t.Fatal(e)
			}
		}
	}

}
