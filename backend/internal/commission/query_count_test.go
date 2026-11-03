package commission_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wzhlinhai/salon/backend/internal/optest"
)

func TestCommissionReadQueryCount(t *testing.T) {
	s := optest.OpenShop(t)
	day := today(t, s)
	scheme := emptyScheme()
	rules := []any{}
	for _, source := range []string{"item", "sale"} {
		r := rule(source)
		r["cells"] = []any{map[string]any{"percent": "10"}}
		rules = append(rules, r)
	}
	scheme["rules"] = rules
	save(t, s, day, scheme)
	checkout := func(staffID uuid.UUID, n int) any {
		t.Helper()
		positions := []any{}
		assignments := []any{}
		for slot := 1; slot <= 2; slot++ {
			position := s.Call(t, s.Owner, "create_position", map[string]any{"name": fmt.Sprintf("岗位%d-%d", n, slot)}, 200).Body["id"]
			positions = append(positions, map[string]any{"position_id": position, "required": false, "share_percent": 50})
			assignments = append(assignments, map[string]any{"slot": slot, "staff_id": staffID})
		}
		item := s.Call(t, s.Owner, "create_item", map[string]any{"name": fmt.Sprintf("项目%d", n), "kind": "service", "price": "100", "discountable": true, "labor_fee_amount": nil, "labor_fee_percent": nil, "positions": positions, "level_prices": []any{}, "member_prices": []any{}}, 200).Body["id"]
		order := s.Call(t, s.Owner, "open_order", map[string]any{"gender": "female", "designated": false, "items": []any{map[string]any{"item_id": item, "staff": assignments}, map[string]any{"item_id": item, "staff": assignments}}}, 200).Body
		s.Call(t, s.Owner, "checkout_orders", map[string]any{"orders": []any{map[string]any{"order_id": order["id"], "payments": []any{map[string]any{"method": "cash", "amount": "200"}}}}}, 200)
		return order["id"]
	}
	checkout(s.Res.OwnerID, 1)
	counter := &queryCounter{}
	config := s.Pool.Config().Copy()
	config.ConnConfig.Tracer = counter
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(t.Context()); err != nil {
		t.Fatal(err)
	}
	app := optest.New(t, pool)
	caller := s.MustCaller(s.Owner)
	counts := map[string]int64{}
	sizes := map[string]int{}
	for _, large := range []bool{false, true} {
		if large {
			for n := 2; n <= 4; n++ {
				staffID, _ := s.Hire(fmt.Sprintf("员工%d", n), fmt.Sprintf("%03d", n), fmt.Sprintf("worker%d", n))
				orderID := checkout(staffID, n)
				if n == 2 {
					s.Call(t, s.Owner, "void_order", map[string]any{"order_id": orderID, "reason": "查询次数冲回"}, 200)
				}
			}
			customer := s.Call(t, s.Owner, "create_customer", map[string]any{"name": "销售顾客", "gender": "female"}, 200).Body["customer"].(map[string]any)["id"]
			s.Call(t, s.Owner, "issue_red_packet", map[string]any{"customer_id": customer, "kind": "sold", "face_value": "100", "price": "100", "expires_on": day.AddDate(1, 0, 0).Format(time.DateOnly), "sales_staff_ids": []uuid.UUID{s.Res.OwnerID}}, 200)
		}
		for _, ch := range app.Channels() {
			counter.n.Store(0)
			result := ch.Call(t, caller, "list_commissions", map[string]any{"date_from": day.Format(time.DateOnly), "date_to": day.Format(time.DateOnly), "limit": 200})
			count := counter.n.Load()
			if result.Status != 200 {
				t.Fatal(ch.Name, result)
			}
			rows := result.Body["entries"].([]any)
			if !large {
				if len(rows) != 4 {
					t.Fatal(ch.Name, result)
				}
				counts[ch.Name], sizes[ch.Name] = count, len(rows)
			} else {
				if len(rows) <= sizes[ch.Name] {
					t.Fatal(ch.Name, "commissions did not grow", result)
				}
				if count != counts[ch.Name] {
					t.Fatalf("%s queries grew: %d -> %d", ch.Name, counts[ch.Name], count)
				}
				var reversals, sales int
				for _, row := range rows {
					entry := row.(map[string]any)
					if entry["reversal"] == true {
						reversals++
					}
					if entry["source"] == "sale" {
						sales++
					}
				}
				if reversals != 4 || sales != 1 {
					t.Fatal(ch.Name, "missing reversal or sale commissions", result)
				}
			}
			t.Logf("%s large=%t commissions=%d queries=%d", ch.Name, large, len(rows), count)
		}
	}
}
