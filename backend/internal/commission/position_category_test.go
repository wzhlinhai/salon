package commission_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func businessDayAt(t *testing.T, s *optest.Shop, now time.Time) time.Time {
	t.Helper()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	defer dbtest.Rollback(t, tx)
	day, err := settings.BusinessDate(context.Background(), dbgen.New(tx), s.Res.StoreID, now)
	if err != nil {
		t.Fatal(err)
	}
	return day
}

func createPosition(t *testing.T, s *optest.Shop, name string) uuid.UUID {
	t.Helper()
	return uuid.MustParse(s.Call(t, s.Owner, "create_position", map[string]any{"name": name}, 200).Body["id"].(string))
}

func createCategory(t *testing.T, s *optest.Shop, name string, ratio int) uuid.UUID {
	t.Helper()
	return uuid.MustParse(s.Call(t, s.Owner, "create_item_category", map[string]any{"name": name, "performance_ratio": ratio}, 200).Body["id"].(string))
}

func createStaffAt(t *testing.T, s *optest.Shop, name, number string, position uuid.UUID) uuid.UUID {
	t.Helper()
	return uuid.MustParse(s.Call(t, s.Owner, "create_staff", map[string]any{"name": name, "number": number, "position_id": position, "role_ids": []uuid.UUID{}}, 200).Body["id"].(string))
}

func createCategorizedItem(t *testing.T, s *optest.Shop, name string, category *uuid.UUID, positions []uuid.UUID, labor any) uuid.UUID {
	t.Helper()
	slots := make([]any, 0, len(positions))
	for _, position := range positions {
		slots = append(slots, map[string]any{"position_id": position, "required": false})
	}
	body := map[string]any{
		"name": name, "kind": "service", "price": "100", "discountable": true,
		"labor_fee_amount": labor, "labor_fee_percent": nil,
		"positions": slots, "level_prices": []any{}, "member_prices": []any{},
	}
	if category != nil {
		body["category_id"] = *category
	}
	return uuid.MustParse(s.Call(t, s.Owner, "create_item", body, 200).Body["id"].(string))
}

func itemAssignment(staff map[int16]uuid.UUID) []any {
	out := []any{}
	for slot := int16(1); slot <= 3; slot++ {
		if staff[slot] != uuid.Nil {
			out = append(out, map[string]any{"slot": slot, "staff_id": staff[slot]})
		}
	}
	return out
}

func checkoutLine(t *testing.T, s *optest.Shop, item uuid.UUID, staff []any, payment map[string]any) (uuid.UUID, uuid.UUID) {
	return checkoutLineFor(t, s, nil, item, staff, payment)
}

func checkoutLineFor(t *testing.T, s *optest.Shop, customer *uuid.UUID, item uuid.UUID, staff []any, payment map[string]any) (uuid.UUID, uuid.UUID) {
	t.Helper()
	body := map[string]any{
		"gender": "female", "designated": false,
		"items": []any{map[string]any{"item_id": item, "staff": staff}},
	}
	if customer != nil {
		body["customer_id"] = *customer
	}
	order := s.Call(t, s.Owner, "open_order", body, 200).Body
	line := uuid.MustParse(order["items"].([]any)[0].(map[string]any)["id"].(string))
	if payment["method"] == "count" {
		payment["order_item_id"] = line
	}
	checked := s.Call(t, s.Owner, "checkout_orders", map[string]any{"orders": []any{map[string]any{"order_id": order["id"], "payments": []any{payment}}}}, 200).Body
	return uuid.MustParse(checked["items"].([]any)[0].(map[string]any)["id"].(string)), line
}

func commissionsFor(t *testing.T, s *optest.Shop, ch optest.Channel, day time.Time) []map[string]any {
	t.Helper()
	r := ch.Call(t, s.MustCaller(s.Owner), "list_commissions", map[string]any{"date_from": day.Format(time.DateOnly), "date_to": day.Format(time.DateOnly), "limit": 200})
	if r.Status != 200 {
		t.Fatal(ch.Name, r)
	}
	entries := []map[string]any{}
	for _, raw := range r.Body["entries"].([]any) {
		entries = append(entries, raw.(map[string]any))
	}
	return entries
}

func eachCommissionChannel(t *testing.T, s *optest.Shop, day time.Time, check func(t *testing.T, entries []map[string]any)) {
	t.Helper()
	for _, ch := range s.Channels() {
		t.Run(ch.Name, func(t *testing.T) {
			check(t, commissionsFor(t, s, ch, day))
		})
	}
}

func commissionForLine(t *testing.T, entries []map[string]any, line uuid.UUID, staff *uuid.UUID) map[string]any {
	t.Helper()
	for _, entry := range entries {
		origin := entry["explanation"].(map[string]any)["origin"].(map[string]any)
		if origin["order_item_id"] == line.String() && (staff == nil || entry["staff"].(map[string]any)["id"] == staff.String()) {
			return entry
		}
	}
	t.Fatalf("no commission for line %s staff %v: %v", line, staff, entries)
	return nil
}

func updateItemCategory(t *testing.T, s *optest.Shop, item uuid.UUID, name string, category uuid.UUID, positions []uuid.UUID) {
	t.Helper()
	slots := make([]any, 0, len(positions))
	for _, position := range positions {
		slots = append(slots, map[string]any{"position_id": position, "required": false})
	}
	s.Call(t, s.Owner, "update_item", map[string]any{
		"id": item, "name": name, "kind": "service", "price": "100", "discountable": true,
		"labor_fee_amount": nil, "labor_fee_percent": nil, "active": true,
		"positions": slots, "level_prices": []any{}, "member_prices": []any{}, "category_id": category,
	}, 200)
}

func TestCommissionPositionCategoryMatches(t *testing.T) {
	s := optest.OpenShop(t)
	day := today(t, s)
	assistant := createPosition(t, s, "助理")
	main := createPosition(t, s, "大工")
	hair := createCategory(t, s, "剪发类", 100)
	care := createCategory(t, s, "护理类", 100)
	hairItem := createCategorizedItem(t, s, "洗剪吹", &hair, []uuid.UUID{assistant, main}, nil)
	careItem := createCategorizedItem(t, s, "护理项目", &care, []uuid.UUID{assistant}, nil)
	plain := createCategorizedItem(t, s, "未归类项目", nil, []uuid.UUID{assistant}, nil)
	scheme := emptyScheme()
	r := rule("item")
	r["cells"] = []any{map[string]any{"percent": "5"}}
	scheme["rules"] = []any{r}
	scheme["overrides"] = []any{
		map[string]any{"source": "item", "position_id": assistant, "category_id": hair, "fixed_amount": "3"},
		map[string]any{"source": "item", "position_id": assistant, "category_id": care, "fixed_amount": "5"},
		map[string]any{"source": "item", "position_id": main, "category_id": hair, "percent": "10"},
	}
	saved := save(t, s, day, scheme)
	assistantStaff := createStaffAt(t, s, "新助理", "101", assistant)
	mainStaff := createStaffAt(t, s, "新大工", "102", main)
	_, hairLine := checkoutLine(t, s, hairItem, itemAssignment(map[int16]uuid.UUID{1: assistantStaff, 2: mainStaff}), map[string]any{"method": "cash", "amount": "100"})
	_, careLine := checkoutLine(t, s, careItem, itemAssignment(map[int16]uuid.UUID{1: assistantStaff}), map[string]any{"method": "cash", "amount": "100"})
	_, plainLine := checkoutLine(t, s, plain, itemAssignment(map[int16]uuid.UUID{1: assistantStaff}), map[string]any{"method": "cash", "amount": "100"})
	eachCommissionChannel(t, s, day, func(t *testing.T, entries []map[string]any) {
		assistantCommission := commissionForLine(t, entries, hairLine, &assistantStaff)
		mainCommission := commissionForLine(t, entries, hairLine, &mainStaff)
		careCommission := commissionForLine(t, entries, careLine, &assistantStaff)
		plainCommission := commissionForLine(t, entries, plainLine, &assistantStaff)
		if assistantCommission["amount"] != "3.00" || assistantCommission["explanation"].(map[string]any)["match"].(map[string]any)["kind"] != "override_position_category" {
			t.Fatal(assistantCommission)
		}
		if careCommission["amount"] != "5.00" || careCommission["explanation"].(map[string]any)["match"].(map[string]any)["kind"] != "override_position_category" {
			t.Fatal(careCommission)
		}
		if mainCommission["amount"] != "5.00" {
			t.Fatal(mainCommission)
		}
		if plainCommission["amount"] != "5.00" || plainCommission["explanation"].(map[string]any)["match"].(map[string]any)["kind"] != "cell" {
			t.Fatal(plainCommission)
		}
		origin := assistantCommission["explanation"].(map[string]any)["origin"].(map[string]any)
		if origin["category"].(map[string]any)["name"] != "剪发类" || origin["position"].(map[string]any)["name"] != "助理" {
			t.Fatal(origin)
		}
		careOrigin := careCommission["explanation"].(map[string]any)["origin"].(map[string]any)
		if careOrigin["category"].(map[string]any)["name"] != "护理类" {
			t.Fatal(careOrigin)
		}
	})
	for _, ch := range s.Channels() {
		got := ch.Call(t, s.MustCaller(s.Owner), "get_commission_scheme", map[string]any{"version": int(saved.Body["version"].(float64))})
		if got.Status != 200 {
			t.Fatal(got)
		}
		overrides := got.Body["scheme"].(map[string]any)["overrides"].([]any)
		first := overrides[0].(map[string]any)
		if first["position"].(map[string]any)["name"] != "助理" || first["category"].(map[string]any)["name"] != "剪发类" {
			t.Fatal(got)
		}
	}
}

func TestCommissionCategoryDuplicateKey(t *testing.T) {
	s := optest.OpenShop(t)
	day := today(t, s)
	position := createPosition(t, s, "助理")
	first := createCategory(t, s, "剪发类", 100)
	second := createCategory(t, s, "护理类", 100)
	scheme := emptyScheme()
	scheme["rules"] = []any{rule("item")}
	scheme["overrides"] = []any{
		map[string]any{"source": "item", "position_id": position, "category_id": first, "fixed_amount": "3"},
		map[string]any{"source": "item", "position_id": position, "category_id": second, "fixed_amount": "5"},
	}
	saved := save(t, s, day, scheme)
	if len(saved.Body["scheme"].(map[string]any)["overrides"].([]any)) != 2 {
		t.Fatal(saved)
	}
}

func TestCommissionCategorySnapshot(t *testing.T) {
	s := optest.OpenShop(t)
	day := today(t, s)
	assistant := createPosition(t, s, "助理")
	first := createCategory(t, s, "剪发", 100)
	second := createCategory(t, s, "护理", 100)
	item := createCategorizedItem(t, s, "快照项目", &first, []uuid.UUID{assistant}, nil)
	scheme := emptyScheme()
	r := rule("item")
	r["cells"] = []any{map[string]any{"percent": "5"}}
	scheme["rules"] = []any{r}
	scheme["overrides"] = []any{map[string]any{"source": "item", "position_id": assistant, "category_id": first, "fixed_amount": "3"}}
	save(t, s, day, scheme)
	staff := createStaffAt(t, s, "快照助理", "103", assistant)
	order := s.Call(t, s.Owner, "open_order", map[string]any{"gender": "female", "designated": false, "items": []any{map[string]any{"item_id": item, "staff": itemAssignment(map[int16]uuid.UUID{1: staff})}}}, 200).Body
	line := uuid.MustParse(order["items"].([]any)[0].(map[string]any)["id"].(string))
	updateItemCategory(t, s, item, "快照项目", second, []uuid.UUID{assistant})
	checked := s.Call(t, s.Owner, "checkout_orders", map[string]any{"orders": []any{map[string]any{"order_id": order["id"], "payments": []any{map[string]any{"method": "cash", "amount": "100"}}}}}, 200).Body
	orderID := uuid.MustParse(checked["items"].([]any)[0].(map[string]any)["id"].(string))
	entries := []map[string]any{}
	for _, ch := range s.Channels() {
		got := ch.Call(t, s.MustCaller(s.Owner), "list_commissions", map[string]any{"date_from": day.Format(time.DateOnly), "date_to": day.Format(time.DateOnly)})
		if got.Status != 200 {
			t.Fatal(got)
		}
		for _, raw := range got.Body["entries"].([]any) {
			entry := raw.(map[string]any)
			origin := entry["explanation"].(map[string]any)["origin"].(map[string]any)
			if origin["order_id"] == orderID.String() {
				entries = append(entries, entry)
			}
		}
	}
	if len(entries) == 0 {
		t.Fatal("missing commission")
	}
	for _, entry := range entries {
		if entry["amount"] != "3.00" || entry["explanation"].(map[string]any)["match"].(map[string]any)["kind"] != "override_position_category" {
			t.Fatal(line, entry)
		}
	}
}

func TestCommissionNewVersionOnlyFromEffectiveDate(t *testing.T) {
	s := optest.OpenShop(t)
	day := today(t, s)
	assistant := createPosition(t, s, "助理")
	category := createCategory(t, s, "版本类别", 100)
	item := createCategorizedItem(t, s, "版本项目", &category, []uuid.UUID{assistant}, nil)
	staff := createStaffAt(t, s, "版本助理", "104", assistant)
	scheme := emptyScheme()
	scheme["rules"] = []any{rule("item")}
	scheme["overrides"] = []any{map[string]any{"source": "item", "position_id": assistant, "category_id": category, "fixed_amount": "3"}}
	save(t, s, day, scheme)
	_, line := checkoutLine(t, s, item, itemAssignment(map[int16]uuid.UUID{1: staff}), map[string]any{"method": "cash", "amount": "100"})
	next := scheme
	next["overrides"] = []any{map[string]any{"source": "item", "position_id": assistant, "category_id": category, "fixed_amount": "7"}}
	save(t, s, day.AddDate(0, 0, 1), next)
	var nextDay time.Time
	var nextLine uuid.UUID
	s.SetNow(day.AddDate(0, 0, 1).Add(12 * time.Hour))
	nextDay = businessDayAt(t, s, s.Now())
	_, nextLine = checkoutLine(t, s, item, itemAssignment(map[int16]uuid.UUID{1: staff}), map[string]any{"method": "cash", "amount": "100"})
	s.SetNow(time.Time{})
	eachCommissionChannel(t, s, day, func(t *testing.T, entries []map[string]any) {
		if entry := commissionForLine(t, entries, line, &staff); entry["amount"] != "3.00" {
			t.Fatal(entry)
		}
	})
	eachCommissionChannel(t, s, nextDay, func(t *testing.T, entries []map[string]any) {
		if entry := commissionForLine(t, entries, nextLine, &staff); entry["amount"] != "7.00" {
			t.Fatal(entry)
		}
	})
}

func TestCommissionConsumeAndLaborPositionCategory(t *testing.T) {
	s := optest.OpenShop(t)
	day := today(t, s)
	assistant := createPosition(t, s, "助理")
	category := createCategory(t, s, "耗卡类别", 100)
	item := createCategorizedItem(t, s, "耗卡项目", &category, []uuid.UUID{assistant}, "10")
	staff := createStaffAt(t, s, "耗卡助理", "105", assistant)
	scheme := emptyScheme()
	rules := []any{}
	for _, source := range []string{"item", "consume", "labor"} {
		r := rule(source)
		r["cells"] = []any{map[string]any{"percent": "0"}}
		rules = append(rules, r)
	}
	scheme["rules"] = rules
	scheme["overrides"] = []any{
		map[string]any{"source": "item", "position_id": assistant, "category_id": category, "fixed_amount": "3"},
		map[string]any{"source": "consume", "position_id": assistant, "category_id": category, "fixed_amount": "4"},
		map[string]any{"source": "labor", "position_id": assistant, "category_id": category, "fixed_amount": "5"},
	}
	save(t, s, day, scheme)
	customer := uuid.MustParse(s.Call(t, s.Owner, "create_customer", map[string]any{"name": "耗卡顾客", "gender": "female"}, 200).Body["customer"].(map[string]any)["id"].(string))
	cardType := uuid.MustParse(s.Call(t, s.Owner, "create_card_type", map[string]any{
		"name": "耗卡次卡", "kind": "count", "validity_kind": "none",
		"items": []any{map[string]any{"item_id": item, "paid_count": 1, "gift_count": 0, "unit_price": "100"}},
	}, 200).Body["id"].(string))
	card := uuid.MustParse(s.Call(t, s.Owner, "open_card", map[string]any{"customer_id": customer, "card_type_id": cardType, "principal": "0", "bonus": "0", "sales_staff_ids": []uuid.UUID{s.Res.OwnerID}, "payments": []any{map[string]any{"method": "cash", "amount": "100"}}}, 200).Body["id"].(string))
	_, line := checkoutLineFor(t, s, &customer, item, itemAssignment(map[int16]uuid.UUID{1: staff}), map[string]any{"method": "count", "amount": "100", "card_id": card})
	eachCommissionChannel(t, s, day, func(t *testing.T, entries []map[string]any) {
		amounts := map[string]string{}
		for _, entry := range entries {
			origin := entry["explanation"].(map[string]any)["origin"].(map[string]any)
			if origin["order_item_id"] != line.String() {
				continue
			}
			amounts[entry["source"].(string)] = entry["amount"].(string)
			if entry["explanation"].(map[string]any)["match"].(map[string]any)["kind"] != "override_position_category" {
				t.Fatal(entry)
			}
		}
		if amounts["consume"] != "4.00" || amounts["labor"] != "5.00" {
			t.Fatal(amounts, entries)
		}
	})
}

func TestCommissionSettledExplanationWithoutCategory(t *testing.T) {
	s := optest.OpenShop(t)
	now := time.Now()
	month := time.Date(now.Year(), now.Month()-2, 1, 0, 0, 0, 0, time.UTC)
	if _, err := s.Pool.Exec(t.Context(), "UPDATE stores SET created_at=$1", month); err != nil {
		t.Fatal(err)
	}
	s.SetNow(time.Date(month.Year(), month.Month(), 15, 12, 0, 0, 0, now.Location()))
	day := businessDayAt(t, s, s.Now())
	assistant := createPosition(t, s, "助理")
	category := createCategory(t, s, "旧解释类别", 100)
	item := createCategorizedItem(t, s, "旧解释项目", &category, []uuid.UUID{assistant}, nil)
	staff := createStaffAt(t, s, "旧解释助理", "106", assistant)
	scheme := emptyScheme()
	scheme["rules"] = []any{rule("item")}
	scheme["overrides"] = []any{map[string]any{"source": "item", "position_id": assistant, "category_id": category, "fixed_amount": "3"}}
	save(t, s, day, scheme)
	_, line := checkoutLine(t, s, item, itemAssignment(map[int16]uuid.UUID{1: staff}), map[string]any{"method": "cash", "amount": "100"})
	var commissionID uuid.UUID
	eachCommissionChannel(t, s, day, func(t *testing.T, entries []map[string]any) {
		commissionID = uuid.MustParse(commissionForLine(t, entries, line, &staff)["id"].(string))
	})
	s.SetNow(now)
	s.Call(t, s.Owner, "lock_payroll_month", map[string]any{"month": day.Format("2006-01")}, 200)
	eachCommissionChannel(t, s, day, func(t *testing.T, entries []map[string]any) {
		got := commissionForLine(t, entries, line, &staff)
		origin := got["explanation"].(map[string]any)["origin"].(map[string]any)
		if got["amount"] != "3.00" || origin["category"].(map[string]any)["name"] != "旧解释类别" {
			t.Fatal(got)
		}
	})
	if _, err := s.Pool.Exec(t.Context(), "UPDATE settled_commissions SET explanation = explanation #- '{origin,category}' WHERE store_id=$1 AND id=$2", s.Res.StoreID, commissionID); err != nil {
		t.Fatal(err)
	}
	s.SetNow(time.Time{})
	eachCommissionChannel(t, s, day, func(t *testing.T, entries []map[string]any) {
		got := commissionForLine(t, entries, line, &staff)
		origin := got["explanation"].(map[string]any)["origin"].(map[string]any)
		if got["amount"] != "3.00" || origin["category"] != nil {
			t.Fatal(got)
		}
	})
}

func TestCommissionFullSplitThreeStaffPositionCategory(t *testing.T) {
	s := optest.OpenShop(t)
	day := today(t, s)
	s.Call(t, s.Owner, "change_setting", map[string]any{"key": "performance_split", "value": "full", "effective_from": day.Format(time.DateOnly)}, 200)
	main := createPosition(t, s, "大工")
	middle := createPosition(t, s, "中工")
	assistant := createPosition(t, s, "助理")
	category := createCategory(t, s, "三人类别", 100)
	item := createCategorizedItem(t, s, "三人项目", &category, []uuid.UUID{main, middle, assistant}, nil)
	scheme := emptyScheme()
	scheme["rules"] = []any{rule("item")}
	scheme["overrides"] = []any{
		map[string]any{"source": "item", "position_id": main, "category_id": category, "fixed_amount": "3"},
		map[string]any{"source": "item", "position_id": middle, "category_id": category, "fixed_amount": "4"},
		map[string]any{"source": "item", "position_id": assistant, "category_id": category, "fixed_amount": "5"},
	}
	save(t, s, day, scheme)
	staff := map[uuid.UUID]uuid.UUID{
		main:      createStaffAt(t, s, "三人处理发师", "107", main),
		middle:    createStaffAt(t, s, "三人中工", "108", middle),
		assistant: createStaffAt(t, s, "三人助理", "109", assistant),
	}
	_, line := checkoutLine(t, s, item, itemAssignment(map[int16]uuid.UUID{1: staff[main], 2: staff[middle], 3: staff[assistant]}), map[string]any{"method": "cash", "amount": "100"})
	eachCommissionChannel(t, s, day, func(t *testing.T, entries []map[string]any) {
		count := 0
		for _, entry := range entries {
			origin := entry["explanation"].(map[string]any)["origin"].(map[string]any)
			if origin["order_item_id"] == line.String() {
				count++
			}
		}
		if count != 3 {
			t.Fatal("full split did not produce three units", entries)
		}
		for wantStaff, want := range map[uuid.UUID]string{staff[main]: "3.00", staff[middle]: "4.00", staff[assistant]: "5.00"} {
			entry := commissionForLine(t, entries, line, &wantStaff)
			if entry["amount"] != want || entry["explanation"].(map[string]any)["match"].(map[string]any)["kind"] != "override_position_category" {
				t.Fatal(entry)
			}
		}
	})
}

func TestCommissionPositionCategoryReversal(t *testing.T) {
	s := optest.OpenShop(t)
	day := today(t, s)
	assistant := createPosition(t, s, "助理")
	category := createCategory(t, s, "冲回类别", 100)
	item := createCategorizedItem(t, s, "冲回项目", &category, []uuid.UUID{assistant}, nil)
	staff := createStaffAt(t, s, "冲回助理", "110", assistant)
	scheme := emptyScheme()
	scheme["rules"] = []any{rule("item")}
	scheme["overrides"] = []any{map[string]any{"source": "item", "position_id": assistant, "category_id": category, "fixed_amount": "3"}}
	save(t, s, day, scheme)
	orderID, line := checkoutLine(t, s, item, itemAssignment(map[int16]uuid.UUID{1: staff}), map[string]any{"method": "cash", "amount": "100"})
	s.Call(t, s.Owner, "void_order", map[string]any{"order_id": orderID, "reason": "冲回提成测试"}, 200)
	eachCommissionChannel(t, s, day, func(t *testing.T, entries []map[string]any) {
		for _, entry := range entries {
			origin := entry["explanation"].(map[string]any)["origin"].(map[string]any)
			if origin["order_item_id"] == line.String() && entry["reversal"] == true {
				if entry["amount"] != "-3.00" || entry["explanation"].(map[string]any)["match"].(map[string]any)["kind"] != "override_position_category" {
					t.Fatal(entry)
				}
				return
			}
		}
		t.Fatal("missing reversal", entries)
	})
}
