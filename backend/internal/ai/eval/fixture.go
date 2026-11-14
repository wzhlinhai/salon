package eval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/groupbuy"
	"github.com/wzhlinhai/salon/backend/internal/groupbuy/fakeprovider"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// Fixture 是评测用的固定门店：所有数据都通过业务操作写入，引用在 seed 后解析成 ID。
type Fixture struct {
	Shop        *optest.Shop
	Now         time.Time
	Refs        map[string]string
	Callers     map[string]ops.Caller
	Fingerprint string
}

var fixtureNow = time.Date(2026, 11, 10, 12, 0, 0, 0, settings.Location)

// Seed 新建一个隔离的门店并写入评测数据。
func Seed(t testing.TB) *Fixture {
	t.Helper()
	s := optest.OpenShop(t)
	s.SetNow(fixtureNow)
	s.Groupbuy.Register("meituan", fakeprovider.New("meituan", fixtureNow))
	f := &Fixture{Shop: s, Now: fixtureNow, Refs: map[string]string{}, Callers: map[string]ops.Caller{}}

	chief := callID(t, s, s.Owner, "create_level", map[string]any{"name": "首席"})
	designer := callID(t, s, s.Owner, "create_level", map[string]any{"name": "设计师"})
	big := callID(t, s, s.Owner, "create_position", map[string]any{"name": "大工"})
	assistantPosition := callID(t, s, s.Owner, "create_position", map[string]any{"name": "助理"})

	managerID, managerToken := s.Hire("王店长", "002", "eval-manager", s.Role("店长"))
	stylistID, stylistToken := s.Hire("阿峰", "003", "eval-stylist", s.Role("发型师"))
	assistantID, assistantToken := s.Hire("小李", "004", "eval-assistant", s.Role("助理"))
	otherAssistantID, _ := s.Hire("小陈", "005", "eval-assistant-2", s.Role("助理"))
	s.Call(t, s.Owner, "update_staff", map[string]any{"staff_id": managerID, "name": "王店长", "number": "002", "level_id": chief, "position_id": nil}, 200)
	s.Call(t, s.Owner, "update_staff", map[string]any{"staff_id": stylistID, "name": "阿峰", "number": "003", "level_id": designer, "position_id": big}, 200)
	s.Call(t, s.Owner, "update_staff", map[string]any{"staff_id": assistantID, "name": "小李", "number": "004", "level_id": nil, "position_id": assistantPosition}, 200)
	s.Call(t, s.Owner, "update_staff", map[string]any{"staff_id": otherAssistantID, "name": "小陈", "number": "005", "level_id": nil, "position_id": assistantPosition}, 200)

	storedType := callID(t, s, s.Owner, "create_card_type", map[string]any{"name": "储值卡", "kind": "stored_value", "validity_kind": "none"})
	wash := createItem(t, s, "洗剪吹", "68.00", 45, true,
		[]any{map[string]any{"position_id": big, "required": true}, map[string]any{"position_id": assistantPosition, "required": false}},
		[]any{map[string]any{"level_id": chief, "price": "118.00"}, map[string]any{"level_id": designer, "price": "88.00"}},
		[]any{map[string]any{"card_type_id": storedType, "price": "58.00"}})
	dye := createItem(t, s, "染发", "398.00", 120, true,
		[]any{map[string]any{"position_id": big, "required": true}},
		[]any{map[string]any{"level_id": designer, "price": "428.00"}},
		[]any{map[string]any{"card_type_id": storedType, "price": "368.00"}})
	care := createItem(t, s, "护理", "128.00", 60, true,
		[]any{map[string]any{"position_id": big, "required": true}, map[string]any{"position_id": assistantPosition, "required": false}},
		nil,
		[]any{map[string]any{"card_type_id": storedType, "price": "108.00"}})
	cut := createItem(t, s, "剪发", "298.00", 45, true,
		[]any{map[string]any{"position_id": big, "required": true}},
		[]any{map[string]any{"level_id": designer, "price": "328.00"}},
		[]any{map[string]any{"card_type_id": storedType, "price": "268.00"}})
	countType := callID(t, s, s.Owner, "create_card_type", map[string]any{
		"name": "次卡", "kind": "count", "validity_kind": "none",
		"items": []any{map[string]any{"item_id": wash, "paid_count": 10, "gift_count": 0, "unit_price": "68.00"}},
	})

	wang := customerID(t, s, "王姐", "13800000001", "female")
	zhang := customerID(t, s, "张先生", "13800000002", "male")
	li := customerID(t, s, "李先生", "13800000003", "male")

	wangStored := openCard(t, s, wang, storedType, "500.00", "500.00", stylistID)
	wangCount := openCard(t, s, wang, countType, "0", "680.00", stylistID)
	zhangStored := openCard(t, s, zhang, storedType, "200.00", "200.00", managerID)

	s.SetNow(fixtureNow.AddDate(0, 0, -1))
	historyCare := openOrder(t, s, wang, "female", false, care, []any{map[string]any{"slot": 1, "staff_id": assistantID}})
	checkoutCash(t, s, historyCare, "128.00")
	s.SetNow(fixtureNow)

	s.Call(t, s.Owner, "save_commission_scheme", map[string]any{
		"effective_from": fixtureNow.Format(settings.DateLayout),
		"scheme": map[string]any{
			"rules": []any{map[string]any{
				"source": "item", "base": "paid", "by_position": false, "by_designated": false, "by_member": false,
				"cells": []any{map[string]any{"percent": "10"}},
			}},
			"overrides": []any{}, "excluded_staff": []any{},
		},
	}, 200)
	todayOrder := openOrder(t, s, wang, "female", true, care, []any{map[string]any{"slot": 1, "staff_id": stylistID}})
	checkoutCash(t, s, todayOrder, "128.00")

	wangOpen, wangOpenItem := openOrderWithItem(t, s, wang, "female", true, wash, []any{
		map[string]any{"slot": 1, "staff_id": stylistID},
		map[string]any{"slot": 2, "staff_id": otherAssistantID},
	})
	zhangOpen, _ := openOrderWithItem(t, s, zhang, "male", false, dye, []any{map[string]any{"slot": 1, "staff_id": stylistID}})

	tomorrow := fixtureNow.AddDate(0, 0, 1)
	appointment := callID(t, s, s.Owner, "create_appointment", map[string]any{
		"customer_id": wang, "staff_id": stylistID, "starts_at": tomorrow.Format(time.RFC3339),
		"items": []any{map[string]any{"item_id": wash}},
	})

	deal := callID(t, s, s.Owner, "create_groupbuy_deal", map[string]any{"platform": "meituan", "name": "美团剪发券", "purchase_price": "99.00", "item_id": cut})
	key := "eval-meituan-1"
	s.Call(t, s.Owner, "redeem_coupon", groupbuy.CouponRequest{Platform: "meituan", CouponCode: "MT20261110001", Key: key}, 200)
	redemption := callID(t, s, s.Owner, "get_redemption", map[string]any{"key": key})

	f.Refs = map[string]string{
		"customer:王姐":       wang,
		"customer:张先生":      zhang,
		"customer:李先生":      li,
		"staff:王店长":         managerID.String(),
		"staff:阿峰":          stylistID.String(),
		"staff:小李":          assistantID.String(),
		"staff:小陈":          otherAssistantID.String(),
		"item:洗剪吹":          wash,
		"item:染发":           dye,
		"item:护理":           care,
		"item:剪发":           cut,
		"cardtype:储值卡":      storedType,
		"cardtype:次卡":       countType,
		"card:王姐储值卡":        wangStored,
		"card:王姐次卡":         wangCount,
		"card:张先生储值卡":       zhangStored,
		"order:王姐挂单":        wangOpen,
		"order:张先生挂单":       zhangOpen,
		"order:历史单":         historyCare,
		"order:今日已结":        todayOrder,
		"orderitem:王姐挂单洗剪吹": wangOpenItem,
		"appointment:王姐预约":  appointment,
		"redemption:美团1":    redemption,
		"deal:美团剪发券":        deal,
		"position:大工":       big,
		"position:助理":       assistantPosition,
		"level:首席":          chief,
		"level:设计师":         designer,
	}
	f.Callers = map[string]ops.Caller{
		"owner":     s.MustCaller(s.Owner),
		"manager":   s.MustCaller(managerToken),
		"stylist":   s.MustCaller(stylistToken),
		"assistant": s.MustCaller(assistantToken),
	}
	f.Fingerprint = fixtureFingerprint(t, s, f)
	return f
}

// fixtureFingerprint 是固定门店数据的稳定指纹：业务读取结果里的 UUID 按符号引用归一，
// 未登记引用的 UUID 统一记为 <uuid>，因此名称、价格、次数、时间、订单/预约/核销内容变化都会改变指纹。
func fixtureFingerprint(t testing.TB, s *optest.Shop, f *Fixture) string {
	parts := fixtureFingerprintParts(t, s, f)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}

func fixtureFingerprintParts(t testing.TB, s *optest.Shop, f *Fixture) []string {
	refName := map[string]string{}
	keys := make([]string, 0, len(f.Refs))
	for key, id := range f.Refs {
		refName[id] = key
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := []string{"refs=" + strings.Join(keys, ",")}
	tomorrow := fixtureNow.AddDate(0, 0, 1).Format(settings.DateLayout)
	monthStart := time.Date(fixtureNow.Year(), fixtureNow.Month(), 1, 0, 0, 0, 0, settings.Location).Format(settings.DateLayout)
	today := fixtureNow.Format(settings.DateLayout)
	queries := []struct {
		operation string
		args      any
	}{
		{"list_items", map[string]any{}},
		{"list_card_types", map[string]any{}},
		{"list_staff_options", map[string]any{}},
		{"search_customers", map[string]any{"query": "", "include_unidentified": true}},
		{"list_open_orders", map[string]any{}},
		{"list_appointments", map[string]any{"from": tomorrow, "to": tomorrow}},
		{"list_redemptions", map[string]any{"from": today, "to": today}},
		{"list_groupbuy_deals", map[string]any{}},
		{"list_performance", map[string]any{"date_from": monthStart, "date_to": today}},
		{"list_commissions", map[string]any{"date_from": monthStart, "date_to": today}},
	}
	for _, query := range queries {
		body := s.Call(t, s.Owner, query.operation, query.args, 200).Body
		raw, err := json.Marshal(normalizeFixtureValue(body, refName))
		if err != nil {
			t.Fatalf("fixture fingerprint %s: %v", query.operation, err)
		}
		parts = append(parts, query.operation+"="+string(raw))
	}
	return parts
}

var volatileFixtureKeys = map[string]bool{
	"created_at": true, "updated_at": true, "opened_at": true, "added_at": true,
	"redeemed_at": true, "revoked_at": true, "linked_at": true, "decided_at": true,
	"settled_at": true, "at": true,
}

func normalizeFixtureValue(value any, refName map[string]string) any {
	switch v := value.(type) {
	case string:
		if name, ok := refName[v]; ok {
			return "@" + name
		}
		if _, err := uuid.Parse(v); err == nil {
			return "<uuid>"
		}
		return v
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = normalizeFixtureValue(item, refName)
		}
		sort.Slice(out, func(i, j int) bool {
			left, _ := json.Marshal(out[i])
			right, _ := json.Marshal(out[j])
			return string(left) < string(right)
		})
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			if volatileFixtureKeys[key] {
				out[key] = "<time>"
				continue
			}
			out[key] = normalizeFixtureValue(item, refName)
		}
		return out
	default:
		return value
	}
}

// ResolveString 把引用、日期宏和草稿占位符替换成实际值。
func (f *Fixture) ResolveString(value string) (string, error) {
	if v, ok := f.Refs[value]; ok {
		return v, nil
	}
	if value == "$draft" || strings.HasPrefix(value, "$draft:") {
		return value, nil
	}
	switch value {
	case "$today":
		return f.Now.Format(settings.DateLayout), nil
	case "$yesterday":
		return f.Now.AddDate(0, 0, -1).Format(settings.DateLayout), nil
	case "$tomorrow":
		return f.Now.AddDate(0, 0, 1).Format(settings.DateLayout), nil
	case "$day_after":
		return f.Now.AddDate(0, 0, 2).Format(settings.DateLayout), nil
	case "$month":
		return f.Now.Format("2006-01"), nil
	case "$month_start":
		return time.Date(f.Now.Year(), f.Now.Month(), 1, 0, 0, 0, 0, settings.Location).Format(settings.DateLayout), nil
	}
	for prefix, days := range map[string]int{"$tomorrow@": 1, "$day_after@": 2} {
		if clock, ok := strings.CutPrefix(value, prefix); ok {
			return clockOn(f.Now.AddDate(0, 0, days), clock)
		}
	}
	if strings.HasPrefix(value, "$") {
		return "", fmt.Errorf("未知日期或草稿引用 %q", value)
	}
	if looksLikeRef(value) {
		return "", fmt.Errorf("未知符号引用 %q", value)
	}
	return value, nil
}

func clockOn(day time.Time, clock string) (string, error) {
	parsed, err := time.ParseInLocation("15:04", clock, settings.Location)
	if err != nil {
		return "", fmt.Errorf("时间 %q 格式应为 HH:MM", clock)
	}
	return time.Date(day.Year(), day.Month(), day.Day(), parsed.Hour(), parsed.Minute(), 0, 0, settings.Location).Format(time.RFC3339), nil
}

func looksLikeRef(value string) bool {
	prefix, _, ok := strings.Cut(value, ":")
	if !ok {
		return false
	}
	switch prefix {
	case "customer", "staff", "item", "order", "orderitem", "card", "cardtype", "redemption", "deal", "appointment", "position", "level":
		return true
	}
	return false
}

func callID(t testing.TB, s *optest.Shop, token, operation string, args any) string {
	t.Helper()
	r := s.Call(t, token, operation, args, 200)
	id, ok := r.Body["id"].(string)
	if !ok {
		t.Fatalf("%s 没有返回 id: %v", operation, r.Body)
	}
	return id
}

func customerID(t testing.TB, s *optest.Shop, name, phone, gender string) string {
	t.Helper()
	r := s.Call(t, s.Owner, "create_customer", map[string]any{"name": name, "phone": phone, "gender": gender}, 200)
	customer := r.Body["customer"].(map[string]any)
	return customer["id"].(string)
}

func createItem(t testing.TB, s *optest.Shop, name, price string, duration int, bookable bool, positions, levelPrices, memberPrices []any) string {
	t.Helper()
	if positions == nil {
		positions = []any{}
	}
	if levelPrices == nil {
		levelPrices = []any{}
	}
	if memberPrices == nil {
		memberPrices = []any{}
	}
	return callID(t, s, s.Owner, "create_item", map[string]any{
		"name": name, "price": price, "discountable": true, "duration_minutes": duration, "bookable": bookable,
		"labor_fee_amount": nil, "labor_fee_percent": nil,
		"positions": positions, "level_prices": levelPrices, "member_prices": memberPrices,
	})
}

func openCard(t testing.TB, s *optest.Shop, customer, cardType, principal, openingTotal string, sales uuidLike) string {
	t.Helper()
	payments := []any{}
	if openingTotal != "0" && openingTotal != "0.00" {
		payments = []any{map[string]any{"method": "cash", "amount": openingTotal}}
	}
	return callID(t, s, s.Owner, "open_card", map[string]any{
		"customer_id": customer, "card_type_id": cardType, "principal": principal, "bonus": "0.00",
		"sales_staff_ids": []any{sales}, "payments": payments,
	})
}

type uuidLike interface{ String() string }

func openOrder(t testing.TB, s *optest.Shop, customer, gender string, designated bool, item string, staff []any) string {
	t.Helper()
	order, _ := openOrderWithItem(t, s, customer, gender, designated, item, staff)
	return order
}

func openOrderWithItem(t testing.TB, s *optest.Shop, customer, gender string, designated bool, item string, staff []any) (string, string) {
	t.Helper()
	r := s.Call(t, s.Owner, "open_order", map[string]any{
		"customer_id": customer, "gender": gender, "designated": designated,
		"items": []any{map[string]any{"item_id": item, "staff": staff}},
	}, 200)
	orderID := r.Body["id"].(string)
	line := r.Body["items"].([]any)[0].(map[string]any)
	return orderID, line["id"].(string)
}

func checkoutCash(t testing.TB, s *optest.Shop, order, amount string) {
	t.Helper()
	s.Call(t, s.Owner, "checkout_orders", map[string]any{
		"orders": []any{map[string]any{"order_id": order, "payments": []any{map[string]any{"method": "cash", "amount": amount}}}},
	}, 200)
}
