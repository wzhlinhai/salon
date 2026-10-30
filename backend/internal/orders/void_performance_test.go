package orders_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/orders"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func pgDateForPerformance(day time.Time) pgtype.Date { return pgtype.Date{Time: day, Valid: true} }

func TestVoidPerformance(t *testing.T) {
	s := open(t)
	o := s.paid(s.customerOrder(s.performanceLine(70, 30)), payment("cash", "100"))
	original := s.performanceList()["entries"].([]any)
	day := settings.Date(time.Now()).AddDate(0, 0, 1)
	err := db.InTenant(context.Background(), s.Pool, s.Res.TenantID, func(q *dbgen.Queries) error {
		_, err := orders.VoidOrderAtForTest(context.Background(), ops.Env{Caller: s.MustCaller(s.Owner), Q: q}, new(voidInput(o)), day.Add(8*time.Hour))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	today := day.AddDate(0, 0, -1).Format(time.DateOnly)
	for _, ch := range s.Channels() {
		for _, tc := range []struct {
			to, total string
			n         int
		}{{today, "100.00", 2}, {day.Format(time.DateOnly), "0.00", 4}} {
			r := ch.Call(t, s.MustCaller(s.Owner), "list_performance", map[string]any{"date_from": today, "date_to": tc.to})
			if r.Status != 200 || len(r.Body["entries"].([]any)) != tc.n || r.Body["totals"].([]any)[0].(map[string]any)["amount"] != tc.total || r.Body["totals"].([]any)[0].(map[string]any)["paid_amount"] != tc.total {
				t.Fatal(r)
			}
			if tc.n == 4 {
				for _, v := range r.Body["entries"].([]any)[:2] {
					e := v.(map[string]any)
					if e["reversal"] != true || e["business_date"] != day.Format(time.DateOnly) {
						t.Fatal(e)
					}
					found := false
					for _, o := range original {
						orig := o.(map[string]any)
						if e["reverses_id"] != orig["id"] {
							continue
						}
						found = true
						for _, k := range []string{"order_id", "order_item_id", "slot", "position_id", "method", "paid_amount", "ratio", "amount", "settings_snapshot_id", "checkout_id"} {
							if e[k] != orig[k] {
								t.Fatal(k, e, orig)
							}
						}
						if e["staff"].(map[string]any)["id"] != orig["staff"].(map[string]any)["id"] {
							t.Fatal(e, orig)
						}
					}
					if !found {
						t.Fatal(e)
					}
				}
			}
		}
		negative := ch.Call(t, s.MustCaller(s.Owner), "list_performance", map[string]any{"date_from": day.Format(time.DateOnly), "date_to": day.Format(time.DateOnly)})
		if negative.Status != 200 || negative.Body["totals"].([]any)[0].(map[string]any)["amount"] != "-100.00" {
			t.Fatal(negative)
		}
	}
}
