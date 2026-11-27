package reports_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/reports"
)

type balanceShop struct {
	*fixture
	month, expired, counted, plain, pkg map[string]any
}

// balanceCards 开出各种状态的卡：快到期的储值卡、已过期的次卡、没到期日的次卡、升级过的储值卡、套餐卡，
// 另有余额用完的卡、已退的卡和别的门店的卡。今天是 2026-11-02。
func balanceCards(t *testing.T) *balanceShop {
	f := open(t)
	customer := f.customer("顾客甲", "13800000091", nil)
	other := f.customer("顾客乙", "13800000092", nil)
	monthly := id(f.call("create_card_type", map[string]any{"name": "月卡", "kind": "stored_value", "validity_kind": "days_after_open", "validity_days": 30}, 200)["id"])
	short := id(f.call("create_card_type", map[string]any{"name": "短期次卡", "kind": "count", "validity_kind": "days_after_open", "validity_days": 20, "items": []any{map[string]any{"item_id": f.item, "paid_count": 10, "gift_count": 0, "unit_price": "30.00"}}}, 200)["id"])
	pkgType := id(f.call("create_card_type", map[string]any{"name": "套餐", "kind": "package", "validity_kind": "none", "items": []any{map[string]any{"item_id": f.item, "paid_count": 5, "gift_count": 2, "unit_price": "40.00"}}}, 200)["id"])
	vip := id(f.call("create_card_type", map[string]any{"name": "高级卡", "kind": "stored_value", "validity_kind": "none"}, 200)["id"])
	s := &balanceShop{fixture: f}

	f.now("2026-10-10", 12)
	s.month = f.openCard(customer, monthly, "100.00", "20.00", payment("cash", "100"))
	s.expired = f.openCard(other, short, "0", "0", payment("cash", "300"))
	f.now("2026-11-02", 12)
	s.counted = f.openCard(customer, f.count, "0", "0", payment("cash", "300"))
	order := f.order(customer, []uuid.UUID{f.staffA}, true, "female", f.item)
	f.checkout(order, f.countPayment(id(s.counted["id"]), id(f.line(order, 0)["id"]), "100"))
	f.now("2026-11-02", 13)
	s.plain = f.openCard(other, f.stored, "50.00", "0", payment("cash", "50"))
	f.call("upgrade_card", map[string]any{"card_id": s.plain["id"], "to_type_id": vip, "principal": "10.00", "bonus": "0", "sales_staff_ids": []any{f.Res.OwnerID}, "payments": []any{payment("cash", "10.00")}}, 200)
	f.now("2026-11-02", 14)
	s.pkg = f.openCard(customer, pkgType, "0", "0", payment("cash", "200"))
	empty := f.openCard(customer, f.stored, "100.00", "0", payment("cash", "100"))
	f.checkout(f.order(customer, []uuid.UUID{f.staffB}, false, "male", f.item), f.cardPayment(id(empty["id"]), "100"))
	refunded := f.openCard(other, f.stored, "50.00", "0", payment("cash", "50"))
	quote := f.call("preview_card_refund", map[string]any{"card_id": refunded["id"]}, 200)
	f.call("refund_card", map[string]any{"card_id": refunded["id"], "method": "cash", "amount": quote["amount"]}, 200)
	f.foreignStoreCard("999.00", "2026-11-01")
	return s
}

func cardIDs(t *testing.T, report map[string]any) []string {
	t.Helper()
	out := []string{}
	for _, raw := range report["rows"].([]any) {
		out = append(out, raw.(map[string]any)["card_id"].(string))
	}
	return out
}

func TestCardBalanceReportRows(t *testing.T) {
	s := balanceCards(t)
	report := s.read("get_card_balance_report", map[string]any{})
	got := cardIDs(t, report)
	want := []string{s.expired["id"].(string), s.month["id"].(string), s.counted["id"].(string), s.plain["id"].(string), s.pkg["id"].(string)}
	if len(got) != len(want) {
		t.Fatalf("rows = %v", report["rows"])
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d = %s, want %s (empty, refunded and foreign cards must be absent, no expiry last)", i, got[i], want[i])
		}
	}
	rows := report["rows"].([]any)
	row := func(i int) map[string]any { return rows[i].(map[string]any) }

	expired := row(0)
	if expired["expires_on"] != "2026-10-30" || expired["days_left"].(float64) != -3 || expired["opened_on"] != "2026-10-10" {
		t.Fatalf("expired = %v", expired)
	}
	if expired["card_type"].(map[string]any)["kind"] != "count" || expired["customer"].(map[string]any)["name"] != "顾客乙" {
		t.Fatalf("expired = %v", expired)
	}
	if expired["remaining_counts"].(float64) != 10 || expired["remaining_value"] != "300.00" || expired["principal"] != "0.00" {
		t.Fatalf("expired = %v", expired)
	}
	month := row(1)
	if month["expires_on"] != "2026-11-09" || month["days_left"].(float64) != 7 || month["principal"] != "100.00" || month["bonus"] != "20.00" {
		t.Fatalf("month = %v", month)
	}
	if month["remaining_value"] != "100.00" || month["remaining_counts"].(float64) != 0 || len(month["items"].([]any)) != 0 {
		t.Fatalf("month = %v", month)
	}
	counted := row(2)
	if counted["expires_on"] != nil || counted["days_left"] != nil || counted["remaining_counts"].(float64) != 9 || counted["remaining_value"] != "270.00" {
		t.Fatalf("counted = %v", counted)
	}
	item := counted["items"].([]any)[0].(map[string]any)
	if item["item_id"] != s.item.String() || item["item_name"] != "剪发" || item["paid_remaining"].(float64) != 9 || item["gift_remaining"].(float64) != 0 || item["unit_price"] != "30.00" {
		t.Fatalf("counted item = %v", item)
	}
	plain := row(3)
	if plain["card_type"].(map[string]any)["name"] != "高级卡" || plain["principal"] != "60.00" || plain["remaining_value"] != "60.00" {
		t.Fatalf("plain card should show the upgraded type: %v", plain)
	}
	pkg := row(4)
	if pkg["remaining_counts"].(float64) != 7 || pkg["remaining_value"] != "200.00" || pkg["card_type"].(map[string]any)["kind"] != "package" {
		t.Fatalf("pkg = %v", pkg)
	}
	pkgItem := pkg["items"].([]any)[0].(map[string]any)
	if pkgItem["paid_remaining"].(float64) != 5 || pkgItem["gift_remaining"].(float64) != 2 || pkgItem["unit_price"] != "40.00" {
		t.Fatalf("pkg item = %v", pkgItem)
	}

	totals := report["totals"].(map[string]any)
	if totals["cards"].(float64) != 5 || totals["remaining_counts"].(float64) != 26 || totals["remaining_value"] != "930.00" ||
		totals["principal"] != "160.00" || totals["bonus"] != "20.00" || totals["expiring_in_30_days"].(float64) != 1 || report["truncated"] != false {
		t.Fatalf("totals = %v truncated=%v", totals, report["truncated"])
	}
}

func TestCardBalanceReportFilters(t *testing.T) {
	s := balanceCards(t)
	for _, tc := range []struct {
		name  string
		args  map[string]any
		cards []string
		total float64
		in30  float64
	}{
		{"within ten days includes expired", map[string]any{"expiring_within_days": 10}, []string{"expired", "month"}, 2, 1},
		{"zero days is expired only", map[string]any{"expiring_within_days": 0}, []string{"expired"}, 1, 1},
		{"three days leaves month out", map[string]any{"expiring_within_days": 3}, []string{"expired"}, 1, 1},
		{"count kind", map[string]any{"kind": "count"}, []string{"expired", "counted"}, 2, 0},
		{"package kind", map[string]any{"kind": "package"}, []string{"pkg"}, 1, 0},
		{"stored value kind", map[string]any{"kind": "stored_value"}, []string{"month", "plain"}, 2, 1},
		{"kind and days", map[string]any{"kind": "stored_value", "expiring_within_days": 10}, []string{"month"}, 1, 1},
	} {
		report := s.read("get_card_balance_report", tc.args)
		ids := map[string]string{"expired": s.expired["id"].(string), "month": s.month["id"].(string), "counted": s.counted["id"].(string), "plain": s.plain["id"].(string), "pkg": s.pkg["id"].(string)}
		got := cardIDs(t, report)
		if len(got) != len(tc.cards) {
			t.Fatalf("%s: rows = %v", tc.name, report["rows"])
		}
		for i, name := range tc.cards {
			if got[i] != ids[name] {
				t.Fatalf("%s: row %d = %s, want %s", tc.name, i, got[i], name)
			}
		}
		totals := report["totals"].(map[string]any)
		if totals["cards"].(float64) != tc.total || totals["expiring_in_30_days"].(float64) != tc.in30 {
			t.Fatalf("%s: totals = %v", tc.name, totals)
		}
	}
}

func TestCardBalanceReportTruncation(t *testing.T) {
	s := balanceCards(t)
	restore := reports.SetCardBalanceRowLimit(2)
	defer restore()
	report := s.read("get_card_balance_report", map[string]any{})
	totals := report["totals"].(map[string]any)
	if len(report["rows"].([]any)) != 2 || report["truncated"] != true || totals["cards"].(float64) != 5 || totals["remaining_counts"].(float64) != 26 || totals["remaining_value"] != "930.00" {
		t.Fatalf("truncated = %v", report)
	}
	reports.SetCardBalanceRowLimit(5)
	report = s.read("get_card_balance_report", map[string]any{})
	if len(report["rows"].([]any)) != 5 || report["truncated"] != false {
		t.Fatalf("exactly at the limit = %v", report["truncated"])
	}
}

func TestCardBalanceReportNeedsCustomerView(t *testing.T) {
	f := open(t)
	for _, missing := range []string{"report.view", "customer.view"} {
		caller := f.MustCaller(f.Owner)
		for key := range caller.Permissions {
			if string(key) == missing {
				delete(caller.Permissions, key)
			}
		}
		for _, ch := range f.Channels() {
			got := ch.Call(t, caller, "get_card_balance_report", map[string]any{})
			if got.Status != 403 || got.Detail() != "permission denied" {
				t.Fatalf("%s without %s: %d %q", ch.Name, missing, got.Status, got.Detail())
			}
		}
	}
}
