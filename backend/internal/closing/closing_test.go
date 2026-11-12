package closing_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/groupbuy/fakeprovider"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

type fixture struct {
	*optest.Shop
	t        *testing.T
	item     uuid.UUID
	customer uuid.UUID
	cardType uuid.UUID
}

func open(t *testing.T) *fixture {
	t.Helper()
	s := optest.OpenShop(t)
	f := &fixture{Shop: s, t: t}
	r := s.Call(t, s.Owner, "create_item", map[string]any{"name": "剪发", "price": "100.00", "discountable": true, "labor_fee_amount": nil, "labor_fee_percent": nil, "positions": []any{}, "level_prices": []any{}}, 200)
	f.item = uuid.MustParse(r.Body["id"].(string))
	r = s.Call(t, s.Owner, "create_customer", map[string]any{"name": "顾客", "phone": "13800138000"}, 200)
	f.customer = uuid.MustParse(r.Body["customer"].(map[string]any)["id"].(string))
	r = s.Call(t, s.Owner, "create_card_type", map[string]any{"name": "储值", "kind": "stored_value", "validity_kind": "none"}, 200)
	f.cardType = uuid.MustParse(r.Body["id"].(string))
	return f
}

func at(day string, hour int) time.Time {
	d, err := time.ParseInLocation(settings.DateLayout, day, settings.Location)
	if err != nil {
		panic(err)
	}
	return time.Date(d.Year(), d.Month(), d.Day(), hour, 0, 0, 0, settings.Location)
}

func (f *fixture) now(day string, hour int) {
	f.t.Helper()
	f.SetNow(at(day, hour))
}

func (f *fixture) order(day string, customer uuid.UUID, price string) uuid.UUID {
	f.t.Helper()
	f.now(day, 12)
	r := f.Call(f.t, f.Owner, "open_order", map[string]any{"customer_id": customer, "gender": "female", "designated": false, "items": []any{map[string]any{"item_id": f.item, "staff": []any{}}}}, 200)
	id := uuid.MustParse(r.Body["id"].(string))
	f.Call(f.t, f.Owner, "reprice_order_item", map[string]any{"order_id": id, "order_item_id": r.Body["items"].([]any)[0].(map[string]any)["id"], "price": price, "reason": "测试金额"}, 200)
	return id
}

func (f *fixture) call(op string, in any) map[string]any {
	f.t.Helper()
	return f.Call(f.t, f.Owner, op, in, 200).Body
}

func (f *fixture) checkout(order uuid.UUID, payments ...map[string]any) map[string]any {
	f.t.Helper()
	return f.Call(f.t, f.Owner, "checkout_orders", map[string]any{"orders": []any{map[string]any{"order_id": order, "payments": payments}}}, 200).Body
}

func payment(method, amount string) map[string]any {
	return map[string]any{"method": method, "amount": amount}
}

func (f *fixture) openCard(principal, bonus string, payments ...map[string]any) map[string]any {
	f.t.Helper()
	list := any(payments)
	if len(payments) == 0 {
		list = []any{}
	}
	return f.Call(f.t, f.Owner, "open_card", map[string]any{"customer_id": f.customer, "card_type_id": f.cardType, "principal": principal, "bonus": bonus, "sales_staff_ids": []any{f.Res.OwnerID}, "payments": list}, 200).Body
}

func amount(t *testing.T, lines []any, method string) string {
	t.Helper()
	for _, raw := range lines {
		line := raw.(map[string]any)
		if line["method"] == method {
			return line["amount"].(string)
		}
	}
	t.Fatalf("method %s not found in %v", method, lines)
	return ""
}

func figure(t *testing.T, body map[string]any, key, method string) string {
	t.Helper()
	return amount(t, body[key].([]any), method)
}

func difference(t *testing.T, lines []any, method string) string {
	t.Helper()
	for _, raw := range lines {
		line := raw.(map[string]any)
		if line["method"] == method {
			return line["difference"].(string)
		}
	}
	t.Fatalf("method %s not found in %v", method, lines)
	return ""
}

func preview(t *testing.T, f *fixture, day string) map[string]any {
	t.Helper()
	return f.Call(t, f.Owner, "preview_day_close", map[string]any{"date": day}, 200).Body
}

func TestPreviewDayCloseSystemFigures(t *testing.T) {
	f := open(t)
	f.now("2026-09-30", 12)
	refundCard := f.openCard("30.00", "0.00", payment("cash", "30"))
	f.now("2026-10-01", 12)
	order := f.order("2026-10-01", f.customer, "100.00")
	f.checkout(order, payment("cash", "30"), payment("unionpay", "20"), payment("wechat", "30"), payment("alipay", "20"))

	f.openCard("100.00", "10.00", payment("cash", "60"), payment("unionpay", "40"))
	f.Call(t, f.Owner, "recharge_card", map[string]any{"card_id": f.openCard("0.00", "0.00")["id"], "principal": "50.00", "bonus": "0.00", "sales_staff_ids": []any{f.Res.OwnerID}, "payments": []any{payment("wechat", "50")}}, 200)
	f.Call(t, f.Owner, "issue_red_packet", map[string]any{"customer_id": f.customer, "kind": "sold", "face_value": "20.00", "price": "20.00", "expires_on": "2027-01-01", "sales_staff_ids": []any{f.Res.OwnerID}, "payments": []any{payment("alipay", "20")}}, 200)

	// 退前一天开的只收现金的卡，当天现场现金应扣掉退款。
	quote := f.Call(t, f.Owner, "preview_card_refund", map[string]any{"card_id": refundCard["id"]}, 200).Body
	f.Call(t, f.Owner, "refund_card", map[string]any{"card_id": refundCard["id"], "amount": quote["amount"], "method": "cash"}, 200)

	// 挂账 40 元后当天还 40 元现金。
	debtOrder := f.order("2026-10-01", f.customer, "40.00")
	f.checkout(debtOrder, payment("debt", "40"))
	debts := f.Call(t, f.Owner, "list_customer_debts", map[string]any{"customer_id": f.customer}, 200).Body
	debtID := debts["items"].([]any)[0].(map[string]any)["id"]
	f.Call(t, f.Owner, "repay_debt", map[string]any{"debt_id": debtID, "amount": "40.00", "method": "cash"}, 200)

	body := preview(t, f, "2026-10-01")
	if got := figure(t, body, "on_site", "cash"); got != "100.00" {
		t.Fatalf("cash = %s", got)
	}
	if got := figure(t, body, "on_site", "unionpay"); got != "60.00" {
		t.Fatalf("unionpay = %s", got)
	}
	if got := figure(t, body, "on_site", "wechat"); got != "80.00" {
		t.Fatalf("wechat = %s", got)
	}
	if got := figure(t, body, "on_site", "alipay"); got != "40.00" {
		t.Fatalf("alipay = %s", got)
	}
	if got := figure(t, body, "non_site", "debt"); got != "40.00" {
		t.Fatalf("debt = %s", got)
	}
	if figure(t, body, "non_site", "card_principal") != "0.00" || figure(t, body, "non_site", "card_bonus") != "0.00" {
		t.Fatalf("card lines = %v", body["non_site"])
	}
	if body["unrecorded"].(map[string]any)["amount"] != "0.00" || body["unrecorded"].(map[string]any)["count"] != float64(0) {
		t.Fatalf("unrecorded = %v", body["unrecorded"])
	}
	if body["guest_visits"] != "2.0000" || body["checkout_count"] != float64(2) || body["void_count"] != float64(0) {
		t.Fatalf("summary = %v", body)
	}
	channels := map[string]map[string]any{}
	for _, raw := range body["channels"].([]any) {
		channel := raw.(map[string]any)
		channels[channel["channel"].(string)] = channel["amounts"].(map[string]any)
	}
	if channels["cash"]["succeeded"] != "30.00" || channels["unionpay_register"]["succeeded"] != "20.00" {
		t.Fatalf("channels = %v", body["channels"])
	}
}

func TestPreviewVoidCountsOnVoidDay(t *testing.T) {
	f := open(t)
	order := f.order("2026-10-01", f.customer, "50.00")
	f.checkout(order, payment("cash", "50"))
	f.now("2026-10-02", 12)
	f.Call(t, f.Owner, "void_order", map[string]any{"order_id": order, "reason": "录错"}, 200)

	first := preview(t, f, "2026-10-01")
	if figure(t, first, "on_site", "cash") != "50.00" || first["void_count"] != float64(0) {
		t.Fatal(first)
	}
	second := preview(t, f, "2026-10-02")
	if figure(t, second, "on_site", "cash") != "-50.00" || second["void_count"] != float64(1) || second["checkout_count"] != float64(0) {
		t.Fatal(second)
	}
}

func TestPreviewBusinessDayEndAcrossMidnight(t *testing.T) {
	f := open(t)
	f.now("2029-12-31", 12)
	f.Call(t, f.Owner, "change_setting", map[string]any{"key": "business_day_end", "value": "02:00", "effective_from": "2030-01-01"}, 200)
	f.now("2030-01-02", 1)
	late := f.order("2030-01-01", f.customer, "20.00")
	f.now("2030-01-02", 1)
	f.checkout(late, payment("cash", "20"))
	if got := figure(t, preview(t, f, "2030-01-01"), "on_site", "cash"); got != "20.00" {
		t.Fatalf("first day cash = %s", got)
	}
	if got := figure(t, preview(t, f, "2030-01-02"), "on_site", "cash"); got != "0.00" {
		t.Fatalf("second day cash = %s", got)
	}
}

func TestPreviewUnrecorded(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	card := f.openCard("0.00", "0.00")
	// 同一天已有收款方式的销售不能被算作未记方式。
	f.openCard("5.00", "0.00", payment("cash", "5"))
	f.Call(t, f.Owner, "issue_red_packet", map[string]any{"customer_id": f.customer, "kind": "sold", "face_value": "10.00", "price": "10.00", "expires_on": "2027-01-01", "sales_staff_ids": []any{f.Res.OwnerID}, "payments": []any{payment("cash", "10")}}, 200)
	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	dbtest.Exec(t, tx, `INSERT INTO card_ledger (tenant_id,store_id,card_id,kind,principal_before,principal_delta,principal_after,bonus_before,bonus_delta,bonus_after,business_date,actor_id) VALUES ($1,$2,$3,'recharge',0,100,100,0,0,0,'2026-10-01',$4)`, f.Res.TenantID, f.Res.StoreID, card["id"], f.Res.OwnerID)
	dbtest.Exec(t, tx, `INSERT INTO red_packets (tenant_id,store_id,customer_id,kind,face_value,price,expires_on,business_date,issued_by) VALUES ($1,$2,$3,'sold',20,20,'2027-01-01','2026-10-01',$4)`, f.Res.TenantID, f.Res.StoreID, f.customer, f.Res.OwnerID)
	dbtest.Commit(t, tx)

	body := preview(t, f, "2026-10-01")
	if body["unrecorded"].(map[string]any)["amount"] != "120.00" || body["unrecorded"].(map[string]any)["count"] != float64(2) {
		t.Fatal(body["unrecorded"])
	}
}

func TestCloseVersionsAndDifferences(t *testing.T) {
	f := open(t)
	order := f.order("2026-10-01", f.customer, "100.00")
	f.checkout(order, payment("cash", "100"))
	f.now("2026-10-02", 12)
	first := f.Call(t, f.Owner, "close_day", map[string]any{"date": "2026-10-01", "cash_counted": "90.00", "unionpay_actual": "0.00", "wechat_actual": "0.00", "alipay_actual": "0.00"}, 200).Body
	if first["version"] != float64(1) || difference(t, first["differences"].([]any), "cash") != "-10.00" {
		t.Fatal(first)
	}
	second := f.Call(t, f.Owner, "close_day", map[string]any{"date": "2026-10-01", "cash_counted": "110.00", "unionpay_actual": "0.00", "wechat_actual": "0.00", "alipay_actual": "0.00"}, 200).Body
	if second["version"] != float64(2) || difference(t, second["differences"].([]any), "cash") != "10.00" {
		t.Fatal(second)
	}
	detail := f.Call(t, f.Owner, "get_day_close", map[string]any{"date": "2026-10-01"}, 200).Body
	if detail["current"].(map[string]any)["version"] != float64(2) || len(detail["versions"].([]any)) != 2 {
		t.Fatal(detail)
	}
	if difference(t, detail["versions"].([]any)[1].(map[string]any)["differences"].([]any), "cash") != "-10.00" {
		t.Fatal("old version changed")
	}
}

func TestCloseRejections(t *testing.T) {
	f := open(t)
	f.now("2026-10-02", 12)
	for _, day := range []string{"2026-10-02", "2026-10-03"} {
		r := f.HTTP(t, new(f.MustCaller(f.Owner)), "close_day", map[string]any{"date": day, "cash_counted": "0", "unionpay_actual": "0", "wechat_actual": "0", "alipay_actual": "0"})
		if r.Status != http.StatusConflict || r.Detail() != "只能日结今天以前的营业日" {
			t.Fatal(r)
		}
	}
	f.now("2026-10-01", 12)
	order := f.order("2026-10-01", f.customer, "10.00")
	f.checkout(order, payment("cash", "10"))
	f.now("2026-10-02", 12)
	f.Call(t, f.Owner, "close_day", map[string]any{"date": "2026-10-01", "cash_counted": "10", "unionpay_actual": "0", "wechat_actual": "0", "alipay_actual": "0"}, 200)
	f.Call(t, f.Owner, "seal_day", map[string]any{"date": "2026-10-01"}, 200)
	r := f.HTTP(t, new(f.MustCaller(f.Owner)), "close_day", map[string]any{"date": "2026-10-01", "cash_counted": "10", "unionpay_actual": "0", "wechat_actual": "0", "alipay_actual": "0"})
	if r.Status != http.StatusConflict || r.Detail() != "该营业日已封账（2026-10-01）" {
		t.Fatal(r)
	}
}

func TestPreviewCardSplitAndVoid(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	card := f.openCard("100.00", "0.00", payment("cash", "100"))
	order := f.order("2026-10-01", f.customer, "60.00")
	f.checkout(order, map[string]any{"method": "card", "amount": "60.00", "card_id": card["id"]})
	first := preview(t, f, "2026-10-01")
	if figure(t, first, "non_site", "card_principal") != "60.00" || figure(t, first, "non_site", "card_bonus") != "0.00" {
		t.Fatal(first)
	}
	f.now("2026-10-02", 12)
	f.Call(t, f.Owner, "void_order", map[string]any{"order_id": order, "reason": "撤卡支付"}, 200)
	second := preview(t, f, "2026-10-02")
	if figure(t, second, "non_site", "card_principal") != "-60.00" || figure(t, second, "on_site", "cash") != "0.00" {
		t.Fatal(second)
	}
}

func TestListDayCloses(t *testing.T) {
	f := open(t)
	order := f.order("2026-10-01", f.customer, "10.00")
	f.checkout(order, payment("cash", "10"))
	f.now("2026-10-03", 12)
	f.Call(t, f.Owner, "close_day", map[string]any{"date": "2026-10-01", "cash_counted": "10", "unionpay_actual": "0", "wechat_actual": "0", "alipay_actual": "0"}, 200)
	f.Call(t, f.Owner, "seal_day", map[string]any{"date": "2026-10-01"}, 200)
	body := f.Call(t, f.Owner, "list_day_closes", map[string]any{"date_from": "2026-10-01", "date_to": "2026-10-02"}, 200).Body
	items := body["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["business_date"] != "2026-10-01" || items[0].(map[string]any)["sealed"] != true || body["sealed_through"] != "2026-10-01" {
		t.Fatal(body)
	}
}

func TestClosingReadChannels(t *testing.T) {
	f := open(t)
	order := f.order("2026-10-01", f.customer, "10.00")
	f.checkout(order, payment("cash", "10"))
	f.now("2026-10-02", 12)
	f.call("close_day", map[string]any{"date": "2026-10-01", "cash_counted": "10", "unionpay_actual": "0", "wechat_actual": "0", "alipay_actual": "0"})
	caller := f.MustCaller(f.Owner)
	for _, ch := range f.Channels() {
		for _, tc := range []struct {
			op   string
			args map[string]any
		}{
			{"preview_day_close", map[string]any{"date": "2026-10-01"}},
			{"get_day_close", map[string]any{"date": "2026-10-01"}},
			{"list_day_closes", map[string]any{"date_from": "2026-10-01", "date_to": "2026-10-02"}},
		} {
			if r := ch.Call(t, caller, tc.op, tc.args); r.Status != http.StatusOK {
				t.Fatal(ch.Name, tc.op, r)
			}
		}
	}
}

func TestClosingAIDrafts(t *testing.T) {
	f := open(t)
	f.now("2026-10-02", 12)
	caller := f.MustCaller(f.Owner)
	before := dbtest.Snapshot(t, f.Pool, f.Res.TenantID, "day_closes", "closing_events", "store_sealings")
	for _, tc := range []struct {
		op   string
		args map[string]any
	}{
		{"close_day", map[string]any{"date": "2026-10-01", "cash_counted": "0", "unionpay_actual": "0", "wechat_actual": "0", "alipay_actual": "0"}},
		{"seal_day", map[string]any{"date": "2026-10-01"}},
		{"unseal_day", map[string]any{"reason": "AI 草稿"}},
	} {
		r := f.AI(t, caller, tc.op, tc.args)
		optest.RequireDraftCreated(t, r, dbtest.Snapshot(t, f.Pool, f.Res.TenantID, "day_closes", "closing_events", "store_sealings") == before)
	}
}

func TestPreviewNonSiteLinesAndVoid(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	bonusCard := f.openCard("0.00", "100.00")
	bonusOrder := f.order("2026-10-01", f.customer, "50.00")
	f.checkout(bonusOrder, map[string]any{"method": "card", "amount": "50.00", "card_id": bonusCard["id"]})

	item := f.Call(t, f.Owner, "create_item", map[string]any{"name": "扣次项目", "price": "30.00", "discountable": true, "labor_fee_amount": nil, "labor_fee_percent": nil, "positions": []any{}, "level_prices": []any{}}, 200).Body["id"]
	countType := f.Call(t, f.Owner, "create_card_type", map[string]any{"name": "扣次卡", "kind": "count", "validity_kind": "none", "items": []any{map[string]any{"item_id": item, "paid_count": 1, "gift_count": 0, "unit_price": "30.00"}}}, 200).Body
	countCard := f.Call(t, f.Owner, "open_card", map[string]any{"customer_id": f.customer, "card_type_id": countType["id"], "principal": "0.00", "bonus": "0.00", "sales_staff_ids": []any{f.Res.OwnerID}, "payments": []any{payment("cash", "30.00")}}, 200).Body
	created := f.Call(t, f.Owner, "open_order", map[string]any{"customer_id": f.customer, "gender": "female", "designated": false, "items": []any{map[string]any{"item_id": item, "staff": []any{}}}}, 200)
	countOrder := uuid.MustParse(created.Body["id"].(string))
	countItem := created.Body["items"].([]any)[0].(map[string]any)["id"]
	f.Call(t, f.Owner, "reprice_order_item", map[string]any{"order_id": countOrder, "order_item_id": countItem, "price": "30.00", "reason": "测试金额"}, 200)
	f.checkout(countOrder, map[string]any{"method": "count", "amount": "30.00", "card_id": countCard["id"], "order_item_id": countItem})

	packet := f.Call(t, f.Owner, "issue_red_packet", map[string]any{"customer_id": f.customer, "kind": "gift", "face_value": "30.00", "expires_on": "2027-01-01", "payments": []any{}}, 200).Body
	packetOrder := f.order("2026-10-01", f.customer, "30.00")
	f.checkout(packetOrder, map[string]any{"method": "red_packet", "amount": "30.00", "red_packet_id": packet["id"]})

	body := preview(t, f, "2026-10-01")
	if figure(t, body, "non_site", "card_bonus") != "50.00" || figure(t, body, "non_site", "count") != "30.00" || figure(t, body, "non_site", "red_packet") != "30.00" {
		t.Fatal(body["non_site"])
	}
	f.now("2026-10-02", 12)
	for _, order := range []uuid.UUID{bonusOrder, countOrder, packetOrder} {
		f.Call(t, f.Owner, "void_order", map[string]any{"order_id": order, "reason": "测试冲回"}, 200)
	}
	after := preview(t, f, "2026-10-02")
	if figure(t, after, "non_site", "card_bonus") != "-50.00" || figure(t, after, "non_site", "count") != "-30.00" || figure(t, after, "non_site", "red_packet") != "-30.00" {
		t.Fatal(after["non_site"])
	}
}

func TestAppointmentOnlyDayNeedsNoClose(t *testing.T) {
	f := open(t)
	start := at("2026-10-01", 10)
	if _, err := f.Pool.Exec(t.Context(), `INSERT INTO appointments(tenant_id,store_id,staff_id,kind,starts_at,ends_at,status,created_by) VALUES($1,$2,$3,'block',$4,$5,'confirmed',$3)`, f.Res.TenantID, f.Res.StoreID, f.Res.OwnerID, start, start.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	f.now("2026-10-02", 12)
	f.Call(t, f.Owner, "seal_day", map[string]any{"date": "2026-10-01"}, 200)
}

func TestCouponRedemptionOnlyDayRequiresClose(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	f.Groupbuy.Register("douyin", fakeprovider.New("douyin", at("2026-10-01", 12)))
	f.Call(t, f.Owner, "redeem_coupon", map[string]any{"platform": "douyin", "coupon_code": "only-coupon", "key": "only-coupon"}, 200)
	f.now("2026-10-02", 12)
	r := f.Call(t, f.Owner, "seal_day", map[string]any{"date": "2026-10-01"}, 0)
	if r.Status != 409 || r.Detail() != "请先日结 2026-10-01" {
		t.Fatal(r)
	}
}

func TestPreviewGroupbuyNonSiteLineAndVoid(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	f.Groupbuy.Register("douyin", fakeprovider.New("douyin", at("2026-10-01", 12)))
	dealItem := uuid.MustParse(f.Call(t, f.Owner, "create_item", map[string]any{"name": "团购染发", "price": "298.00", "discountable": true, "labor_fee_amount": nil, "labor_fee_percent": nil, "positions": []any{}, "level_prices": []any{}}, 200).Body["id"].(string))
	deal := f.Call(t, f.Owner, "create_groupbuy_deal", map[string]any{"platform": "douyin", "name": "染发券", "purchase_price": "99.00", "item_id": dealItem}, 200).Body["id"]
	order := f.order("2026-10-01", f.customer, "398.00")
	f.Call(t, f.Owner, "redeem_coupon", map[string]any{"platform": "douyin", "coupon_code": "gb-1", "key": "gb-1"}, 200)
	redemption := f.Call(t, f.Owner, "get_redemption", map[string]any{"key": "gb-1"}, 200).Body["id"]
	f.Call(t, f.Owner, "link_coupon_order", map[string]any{"redemption_id": redemption, "deal_id": deal, "order_id": order}, 200)
	f.checkout(order, payment("cash", "100"))

	body := preview(t, f, "2026-10-01")
	if figure(t, body, "on_site", "cash") != "100.00" || figure(t, body, "non_site", "groupbuy") != "298.00" {
		t.Fatal(body)
	}
	if hasMethod(body["on_site"].([]any), "groupbuy") {
		t.Fatal("groupbuy must not be on-site")
	}
	f.now("2026-10-02", 12)
	closed := f.call("close_day", map[string]any{"date": "2026-10-01", "cash_counted": "100.00", "unionpay_actual": "0", "wechat_actual": "0", "alipay_actual": "0"})
	if hasMethod(closed["differences"].([]any), "groupbuy") {
		t.Fatal("groupbuy must not have an arrival difference")
	}
	if difference(t, closed["differences"].([]any), "cash") != "0.00" {
		t.Fatal(closed["differences"])
	}

	f.Call(t, f.Owner, "void_order", map[string]any{"order_id": order, "reason": "团购撤单"}, 200)
	after := preview(t, f, "2026-10-02")
	if figure(t, after, "on_site", "cash") != "-100.00" || figure(t, after, "non_site", "groupbuy") != "-298.00" {
		t.Fatal(after)
	}
}

func hasMethod(lines []any, method string) bool {
	for _, raw := range lines {
		if raw.(map[string]any)["method"] == method {
			return true
		}
	}
	return false
}

func TestPreviewUnrecordedCountCard(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	item := f.Call(t, f.Owner, "create_item", map[string]any{"name": "未记次卡项目", "price": "30.00", "discountable": true, "labor_fee_amount": nil, "labor_fee_percent": nil, "positions": []any{}, "level_prices": []any{}}, 200).Body["id"]
	ct := f.Call(t, f.Owner, "create_card_type", map[string]any{"name": "未记次卡", "kind": "count", "validity_kind": "none", "items": []any{map[string]any{"item_id": item, "paid_count": 10, "gift_count": 0, "unit_price": "50.00"}}}, 200).Body
	card := f.Call(t, f.Owner, "open_card", map[string]any{"customer_id": f.customer, "card_type_id": ct["id"], "principal": "0.00", "bonus": "0.00", "sales_staff_ids": []any{f.Res.OwnerID}, "payments": []any{payment("cash", "500.00")}}, 200).Body
	if _, err := f.Pool.Exec(t.Context(), `DELETE FROM sale_payments WHERE card_ledger_id IN (SELECT id FROM card_ledger WHERE card_id=$1)`, card["id"]); err != nil {
		t.Fatal(err)
	}
	body := preview(t, f, "2026-10-01")
	if body["unrecorded"].(map[string]any)["amount"] != "500.00" || body["unrecorded"].(map[string]any)["count"] != float64(1) {
		t.Fatal(body["unrecorded"])
	}
}

func TestPreviewAmountOverflowFails(t *testing.T) {
	f := open(t)
	f.now("2026-10-01", 12)
	card := f.openCard("1.00", "0.00", payment("cash", "1"))
	tx := dbtest.AppTx(t, f.Pool, f.Res.TenantID)
	var ledger uuid.UUID
	if err := tx.QueryRow(t.Context(), `SELECT id FROM card_ledger WHERE card_id=$1 ORDER BY id LIMIT 1`, card["id"]).Scan(&ledger); err != nil {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	for range 2 {
		if _, err := f.Pool.Exec(t.Context(), `INSERT INTO sale_payments(tenant_id,store_id,card_ledger_id,method,amount,business_date,created_by) VALUES($1,$2,$3,'cash',99999999.99,'2026-10-01',$4)`, f.Res.TenantID, f.Res.StoreID, ledger, f.Res.OwnerID); err != nil {
			t.Fatal(err)
		}
	}
	r := f.HTTP(t, new(f.MustCaller(f.Owner)), "preview_day_close", map[string]any{"date": "2026-10-01"})
	if r.Status != http.StatusInternalServerError || r.Detail() != "internal error" {
		t.Fatal(r)
	}
}
