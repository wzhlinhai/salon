package reports_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

type fixture struct {
	*optest.Shop
	t        *testing.T
	position uuid.UUID
	level    uuid.UUID
	staffA   uuid.UUID
	staffB   uuid.UUID
	item     uuid.UUID
	goods    uuid.UUID
	loose    uuid.UUID
	stored   uuid.UUID
	count    uuid.UUID
}

func open(t *testing.T) *fixture {
	t.Helper()
	s := optest.OpenShop(t)
	f := &fixture{Shop: s, t: t}
	at := time.Date(2026, 11, 2, 12, 0, 0, 0, settings.Location)
	s.SetNow(at)
	f.position = id(f.call("create_position", map[string]any{"name": "大工"}, 200)["id"])
	f.level = id(f.call("create_level", map[string]any{"name": "首席"}, 200)["id"])
	f.staffA, _ = s.Hire("员工甲", "002", "staff-a")
	f.call("update_staff", map[string]any{"staff_id": f.staffA, "name": "员工甲", "number": "002", "level_id": f.level, "position_id": nil}, 200)
	f.staffB, _ = s.Hire("员工乙", "003", "staff-b")
	f.call("update_staff", map[string]any{"staff_id": f.staffB, "name": "员工乙", "number": "003", "level_id": f.level, "position_id": nil}, 200)
	f.item = f.createItem("剪发", "100.00", true)
	f.goods = f.createItem("洗发水", "50.00", false)
	f.loose = f.createItem("不计次服务", "40.00", false)
	f.stored = id(f.call("create_card_type", map[string]any{"name": "储值卡", "kind": "stored_value", "validity_kind": "none"}, 200)["id"])
	f.count = id(f.call("create_card_type", map[string]any{"name": "次卡", "kind": "count", "validity_kind": "none", "items": []any{map[string]any{"item_id": f.item, "paid_count": 10, "gift_count": 0, "unit_price": "30.00"}}}, 200)["id"])
	return f
}

func (f *fixture) createItem(name, price string, required bool) uuid.UUID {
	f.t.Helper()
	return id(f.call("create_item", map[string]any{
		"name": name, "price": price, "discountable": true,
		"labor_fee_amount": "10.00", "labor_fee_percent": nil,
		"positions":    []any{map[string]any{"position_id": f.position, "required": required}},
		"level_prices": []any{},
	}, 200)["id"])
}

func id(v any) uuid.UUID { return uuid.MustParse(v.(string)) }

func (f *fixture) today() string { return settings.Date(f.Now()).Format(settings.DateLayout) }

func (f *fixture) setting(key string, value any) {
	f.t.Helper()
	f.call("change_setting", map[string]any{"key": key, "value": value, "effective_from": f.today()}, 200)
}

func (f *fixture) now(day string, hour int) {
	d, err := time.ParseInLocation(settings.DateLayout, day, settings.Location)
	if err != nil {
		f.t.Fatal(err)
	}
	f.SetNow(time.Date(d.Year(), d.Month(), d.Day(), hour, 0, 0, 0, settings.Location))
}

func (f *fixture) call(op string, in any, want int) map[string]any {
	f.t.Helper()
	return f.Call(f.t, f.Owner, op, in, want).Body
}

func (f *fixture) merge(keep, merged uuid.UUID) {
	f.t.Helper()
	f.call("merge_customers", map[string]any{"keep_id": keep, "merged_id": merged}, 200)
}

func (f *fixture) markImported(customer uuid.UUID) {
	f.t.Helper()
	tx := dbtest.AppTx(f.t, f.Pool, f.Res.TenantID)
	var batch uuid.UUID
	if err := tx.QueryRow(f.t.Context(), `INSERT INTO import_batches (tenant_id,store_id,status,file_name,created_by,committed_by,committed_at) VALUES ($1,$2,'committed','历史.xlsx',$3,$3,clock_timestamp()) RETURNING id`, f.Res.TenantID, f.Res.StoreID, f.Res.OwnerID).Scan(&batch); err != nil {
		f.t.Fatal(err)
	}
	dbtest.Exec(f.t, tx, `INSERT INTO import_batch_records (tenant_id,store_id,batch_id,kind,entity_id,created) VALUES ($1,$2,$3,'customer',$4,true)`, f.Res.TenantID, f.Res.StoreID, batch, customer)
	dbtest.Commit(f.t, tx)
}

func (f *fixture) customer(name, phone string, source *uuid.UUID) uuid.UUID {
	f.t.Helper()
	in := map[string]any{"name": name}
	if phone != "" {
		in["phone"] = phone
	}
	if source != nil {
		in["source_id"] = *source
	}
	return id(f.call("create_customer", in, 200)["customer"].(map[string]any)["id"])
}

func (f *fixture) order(customer uuid.UUID, staff []uuid.UUID, designated bool, gender string, itemIDs ...uuid.UUID) map[string]any {
	f.t.Helper()
	return f.backfillOrder(customer, staff, designated, gender, "", itemIDs...)
}

func (f *fixture) backfillOrder(customer uuid.UUID, staff []uuid.UUID, designated bool, gender, paperDate string, itemIDs ...uuid.UUID) map[string]any {
	f.t.Helper()
	items := []any{}
	for _, itemID := range itemIDs {
		slots := []any{}
		for i, staffID := range staff {
			slots = append(slots, map[string]any{"slot": i + 1, "staff_id": staffID})
		}
		items = append(items, map[string]any{"item_id": itemID, "staff": slots})
	}
	in := map[string]any{"customer_id": customer, "gender": gender, "designated": designated, "items": items}
	if paperDate != "" {
		in["paper_date"] = paperDate
	}
	return f.call("open_order", in, 200)
}

func (f *fixture) checkout(order map[string]any, payments ...map[string]any) map[string]any {
	f.t.Helper()
	if payments == nil {
		payments = []map[string]any{}
	}
	list := make([]any, len(payments))
	for i, p := range payments {
		list[i] = p
	}
	body := f.call("checkout_orders", map[string]any{"orders": []any{map[string]any{"order_id": order["id"], "payments": list}}}, 200)
	return body["items"].([]any)[0].(map[string]any)
}

func payment(method, amount string) map[string]any {
	return map[string]any{"method": method, "amount": amount}
}

func (f *fixture) openCard(customer, typ uuid.UUID, principal, bonus string, payments ...map[string]any) map[string]any {
	f.t.Helper()
	list := make([]any, len(payments))
	for i, p := range payments {
		list[i] = p
	}
	return f.call("open_card", map[string]any{"customer_id": customer, "card_type_id": typ, "principal": principal, "bonus": bonus, "sales_staff_ids": []any{f.Res.OwnerID}, "payments": list}, 200)
}

func (f *fixture) cardPayment(card uuid.UUID, amount string) map[string]any {
	p := payment("card", amount)
	p["card_id"] = card
	return p
}

func (f *fixture) countPayment(card, orderItem uuid.UUID, amount string) map[string]any {
	p := payment("count", amount)
	p["card_id"] = card
	p["order_item_id"] = orderItem
	return p
}

func (f *fixture) line(order map[string]any, index int) map[string]any {
	f.t.Helper()
	return order["items"].([]any)[index].(map[string]any)
}

func changeOf(t *testing.T, value string) money.Change {
	t.Helper()
	var out money.Change
	if err := out.Scan(value); err != nil {
		t.Fatal(err)
	}
	return out
}

func shareScaled(t *testing.T, value string) int64 {
	t.Helper()
	whole, fraction, _ := strings.Cut(value, ".")
	if len(fraction) > 4 {
		fraction = fraction[:4]
	}
	fraction += strings.Repeat("0", 4-len(fraction))
	n, err := strconv.ParseInt(whole+fraction, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
