package customers_test

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

type overviewLine struct {
	item  uuid.UUID
	staff []uuid.UUID
}

type overviewFixture struct {
	*shop
	positionA uuid.UUID
	positionB uuid.UUID
	staffA    uuid.UUID
	staffB    uuid.UUID
	tokenB    string
	itemA     uuid.UUID
	itemB     uuid.UUID
	itemC     uuid.UUID
	foreign   uuid.UUID
	today     time.Time
	ids       map[string]uuid.UUID
}

// overviewShop 准备一家店和覆盖顾客概览各条规则的顾客数据。
func overviewShop(t *testing.T) *overviewFixture {
	t.Helper()
	s := open(t)
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, settings.Location)
	s.SetNow(now)
	f := &overviewFixture{shop: s, today: now, ids: map[string]uuid.UUID{}}
	f.positionA = idOf(s.call("create_position", map[string]any{"name": "大工"}, 200)["id"])
	f.positionB = idOf(s.call("create_position", map[string]any{"name": "助理"}, 200)["id"])
	f.staffA, _ = s.Hire("发型师甲", "002", "overview-a")
	f.staffB, f.tokenB = s.Hire("发型师乙", "003", "overview-b")
	f.itemA = idOf(s.call("create_item", map[string]any{
		"name": "剪发", "price": "100.00", "discountable": true, "labor_fee_amount": "10.00", "labor_fee_percent": nil,
		"positions": []any{
			map[string]any{"position_id": f.positionA, "required": true},
			map[string]any{"position_id": f.positionB, "required": false},
		},
		"level_prices": []any{},
	}, 200)["id"])
	f.itemB = idOf(s.call("create_item", map[string]any{
		"name": "护理", "price": "100.00", "discountable": true, "labor_fee_amount": "10.00", "labor_fee_percent": nil,
		"positions":    []any{map[string]any{"position_id": f.positionA, "required": true}},
		"level_prices": []any{},
	}, 200)["id"])
	f.itemC = idOf(s.call("create_item", map[string]any{
		"name": "烫发", "price": "1000.00", "discountable": true, "labor_fee_amount": "10.00", "labor_fee_percent": nil,
		"positions":    []any{map[string]any{"position_id": f.positionA, "required": true}},
		"level_prices": []any{},
	}, 200)["id"])
	source := idOf(s.call("create_customer_source", map[string]any{"name": "概览来源"}, 200)["id"])

	create := func(key, name, phone string) uuid.UUID {
		in := map[string]any{"name": name, "phone": phone, "source_id": source}
		c := s.create(in)
		f.ids[key] = aID(c)
		s.call("assign_customer_responsible", map[string]any{"customer_ids": []any{c["id"]}, "staff_id": s.Res.OwnerID}, 200)
		s.call("add_safety_label", map[string]any{"customer_id": c["id"], "kind": "allergy", "source": "customer_said", "observed_on": "2020-01-01"}, 200)
		return aID(c)
	}

	day := func(offset int) time.Time { return settings.Date(now).AddDate(0, 0, offset) }
	create("none", "未到店客", "13800000001")
	create("one", "顾客一", "13800000002")
	create("cycle", "周期客", "13800000003")
	create("same_day", "同日两单客", "13800000004")
	create("due_balance", "余额超期客", "13800000005")
	create("due_empty", "无余额超期客", "13800000006")
	create("due_appt", "有预约超期客", "13800000007")
	create("due_cancelled", "取消预约超期客", "13800000008")
	create("old_cycle", "久未到店客", "13800000009")
	create("sleeping", "沉睡客", "13800000010")
	create("new_imported", "导入新客", "13800000011")
	create("new_old", "三十天前客", "13800000012")
	create("new_two", "两次新客", "13800000013")
	create("cards", "持卡客", "13800000014")
	create("removed_line", "去掉项目客", "13800000015")
	create("voided", "撤单客", "13800000016")
	create("merged", "合并单客", "13800000017")
	create("open", "挂单客", "13800000018")
	create("today_walkin", "今天到店客", "13800000019")
	create("today_booked", "今天预约客", "13800000020")
	create("today_done", "今天完成客", "13800000021")
	create("keep_for_merge", "保留客", "13800000022")
	create("appt", "有下次预约客", "13800000024")
	create("voided_only", "只有撤单客", "13800000025")
	create("due_past", "过去预约超期客", "13800000026")
	create("due_foreign", "别店预约超期客", "13800000032")
	create("due_block", "占位超期客", "13800000033")
	create("big_spend", "大额消费客", "13800000027")
	create("today_voided", "今天撤单客", "13800000028")
	create("today_merged", "今天合并单客", "13800000029")
	create("today_old_paid", "今天预约旧单客", "13800000030")
	create("today_block", "今天占位客", "13800000031")
	mergedAway := s.create(map[string]any{"name": "被合并客", "phone": "13800000023"})
	f.ids["merged_away"] = aID(mergedAway)
	s.call("merge_customers", map[string]any{"keep_id": f.ids["keep_for_merge"], "merged_id": mergedAway["id"]}, 200)
	unidentified := s.create(map[string]any{"name": "未识别"})
	f.ids["unidentified"] = aID(unidentified)
	f.foreign = f.otherStore(day(-1))

	oneOrder := f.order("one", day(-5), overviewLine{item: f.itemA, staff: []uuid.UUID{f.staffA}})
	oneLine := oneOrder["items"].([]any)[0].(map[string]any)
	f.exec(`WITH oi AS (
  INSERT INTO order_items (tenant_id,store_id,order_id,item_id,item_name,item_kind,price,price_source,added_by)
  VALUES ($1,$2,$3,$4,'别店项目','service',999,'standard',$5) RETURNING id)
INSERT INTO order_item_positions (tenant_id,store_id,order_item_id,slot,position_id,required,staff_id)
SELECT $1,$6,id,1,$7,false,$8 FROM oi`, f.Res.TenantID, f.foreign, aID(oneOrder), f.itemB, f.Res.OwnerID, f.Res.StoreID, f.positionB, f.staffB)
	f.exec(`INSERT INTO order_item_positions (tenant_id,store_id,order_item_id,slot,position_id,required,staff_id)
VALUES ($1,$2,$3,99,$4,false,$5)`, f.Res.TenantID, f.foreign, idOf(oneLine["id"]), f.positionB, f.staffB)
	f.order("cycle", day(-60), overviewLine{item: f.itemA, staff: []uuid.UUID{f.staffA}})
	f.order("cycle", day(-30), overviewLine{item: f.itemA, staff: []uuid.UUID{f.staffA}})
	f.order("cycle", day(-10), overviewLine{item: f.itemA, staff: []uuid.UUID{f.staffA}})
	f.order("same_day", day(-2), overviewLine{item: f.itemA, staff: []uuid.UUID{f.staffA}})
	f.order("same_day", day(-2), overviewLine{item: f.itemA, staff: []uuid.UUID{f.staffA}})
	for _, key := range []string{"due_balance", "due_appt", "due_cancelled", "due_past", "due_foreign", "due_block"} {
		f.order(key, day(-100), overviewLine{item: f.itemA, staff: []uuid.UUID{f.staffA}})
		f.order(key, day(-70), overviewLine{item: f.itemA, staff: []uuid.UUID{f.staffA}})
		f.order(key, day(-40), overviewLine{item: f.itemA, staff: []uuid.UUID{f.staffA}})
	}
	f.order("due_empty", day(-100), overviewLine{item: f.itemA, staff: []uuid.UUID{f.staffA}})
	f.order("due_empty", day(-70), overviewLine{item: f.itemA, staff: []uuid.UUID{f.staffA}})
	f.order("due_empty", day(-40), overviewLine{item: f.itemA, staff: []uuid.UUID{f.staffA}})
	f.order("old_cycle", day(-200), overviewLine{item: f.itemA, staff: []uuid.UUID{f.staffA}})
	f.order("old_cycle", day(-150), overviewLine{item: f.itemA, staff: []uuid.UUID{f.staffA}})
	f.order("old_cycle", day(-100), overviewLine{item: f.itemA, staff: []uuid.UUID{f.staffA}})
	f.order("sleeping", day(-120), overviewLine{item: f.itemA, staff: []uuid.UUID{f.staffA}})
	f.order("new_imported", day(-1), overviewLine{item: f.itemA, staff: []uuid.UUID{f.staffA}})
	f.markImported(f.ids["new_imported"])
	f.order("new_old", day(-30), overviewLine{item: f.itemA, staff: []uuid.UUID{f.staffA}})
	f.order("new_two", day(-3), overviewLine{item: f.itemA, staff: []uuid.UUID{f.staffA}})
	f.order("new_two", day(-1), overviewLine{item: f.itemA, staff: []uuid.UUID{f.staffA}})
	f.order("cards", day(-40), overviewLine{item: f.itemA, staff: []uuid.UUID{f.staffA}})
	f.order("removed_line", day(-40), overviewLine{item: f.itemA, staff: []uuid.UUID{f.staffA}})
	f.orderAmount("big_spend", day(-1), f.itemC, 1000)
	f.order("today_old_paid", day(-40), overviewLine{item: f.itemA, staff: []uuid.UUID{f.staffA}})

	removed := f.orderWith("removed_line", day(-1), []overviewLine{
		{item: f.itemA, staff: []uuid.UUID{f.staffA}},
		{item: f.itemB, staff: []uuid.UUID{f.staffB}},
	}, false)
	lines := removed["items"].([]any)
	s.call("remove_order_item", map[string]any{"order_id": removed["id"], "order_item_id": lines[1].(map[string]any)["id"]}, 200)
	f.checkout(removed, 100)
	f.setOrderDate(aID(removed), day(-1))

	f.order("voided", day(-40), overviewLine{item: f.itemA, staff: []uuid.UUID{f.staffA}})
	voided := f.orderWith("voided", day(-2), []overviewLine{{item: f.itemA, staff: []uuid.UUID{f.staffA}}}, false)
	f.checkout(voided, 100)
	f.setOrderDate(aID(voided), day(-2))
	s.call("void_order", map[string]any{"order_id": voided["id"], "reason": "测试撤单"}, 200)
	voidedOnly := f.orderWith("voided_only", day(-2), []overviewLine{{item: f.itemA, staff: []uuid.UUID{f.staffA}}}, false)
	f.checkout(voidedOnly, 100)
	f.setOrderDate(aID(voidedOnly), day(-2))
	s.call("void_order", map[string]any{"order_id": voidedOnly["id"], "reason": "测试撤单"}, 200)

	older := f.orderWith("merged", day(-40), []overviewLine{{item: f.itemA, staff: []uuid.UUID{f.staffA}}}, false)
	f.checkout(older, 100)
	f.setOrderDate(aID(older), day(-40))
	keep := f.orderWith("merged", day(-2), []overviewLine{{item: f.itemA, staff: []uuid.UUID{f.staffA}}}, false)
	extra := f.orderWith("merged", day(-2), []overviewLine{{item: f.itemB, staff: []uuid.UUID{f.staffB}}}, false)
	s.call("merge_orders", map[string]any{"order_id": keep["id"], "merged_order_ids": []any{extra["id"]}}, 200)
	f.checkout(keep, 200)
	f.setOrderDate(aID(keep), day(-2))

	_ = f.orderWith("open", day(0), []overviewLine{{item: f.itemA, staff: []uuid.UUID{f.staffA}}}, false)
	_ = f.orderWith("today_walkin", day(0), []overviewLine{{item: f.itemA, staff: []uuid.UUID{f.staffA}}}, false)
	voidedToday := f.orderWith("today_voided", day(0), []overviewLine{{item: f.itemA, staff: []uuid.UUID{f.staffA}}}, false)
	f.checkout(voidedToday, 100)
	s.call("void_order", map[string]any{"order_id": voidedToday["id"], "reason": "今天撤单"}, 200)
	mergedToday := f.orderWith("today_merged", day(0), []overviewLine{{item: f.itemA, staff: []uuid.UUID{f.staffA}}}, false)
	mergedExtra := f.orderWith("today_merged", day(0), []overviewLine{{item: f.itemB, staff: []uuid.UUID{f.staffB}}}, false)
	s.call("merge_orders", map[string]any{"order_id": mergedToday["id"], "merged_order_ids": []any{mergedExtra["id"]}}, 200)
	f.exec("UPDATE orders SET status='merged', merged_into=$2 WHERE id=$1", aID(mergedToday), aID(mergedExtra))

	f.openCard("due_balance", 500, 0)
	storedCard := f.openCard("cards", 500, 50)
	// 退卡会清零余额；这里直接标为已退，保留余额，单独核对状态过滤。
	refundedCard := f.openCard("cards", 300, 0)
	f.exec("UPDATE cards SET status='refunded' WHERE id=$1", refundedCard)
	expiredCard := f.openCard("cards", 200, 0)
	f.exec("UPDATE cards SET expires_on=$2 WHERE id=$1", expiredCard, day(-1))
	countCard := f.openCountCard("cards", 10, 3)
	// 同一张卡有多条流水时取 at/id 最新的一条；赠送金和赠送次数都计入余额与剩余次数。
	f.exec(`INSERT INTO card_ledger (tenant_id,store_id,card_id,kind,principal_before,principal_delta,principal_after,bonus_before,bonus_delta,bonus_after,business_date,actor_id,at)
VALUES ($1,$2,$3,'recharge',500,100,600,50,20,70,$4,$5,clock_timestamp()+interval '1 hour')`, f.Res.TenantID, f.Res.StoreID, storedCard, day(0), f.Res.OwnerID)
	f.exec(`INSERT INTO card_ledger (tenant_id,store_id,card_id,kind,principal_before,principal_delta,principal_after,bonus_before,bonus_delta,bonus_after,business_date,actor_id,at)
VALUES ($1,$2,$3,'recharge',9999,0,9999,9999,0,9999,$4,$5,clock_timestamp()+interval '2 hours')`, f.Res.TenantID, f.foreign, storedCard, day(0), f.Res.OwnerID)
	// 别店发行的卡不属于本店余额。
	f.exec(`WITH c AS (
  INSERT INTO cards (tenant_id,store_id,customer_id,card_type_id,opened_at,created_by)
  VALUES ($1,$2,$3,(SELECT card_type_id FROM cards WHERE id=$4),clock_timestamp(),$5) RETURNING id)
INSERT INTO card_ledger (tenant_id,store_id,card_id,kind,principal_before,principal_delta,principal_after,bonus_before,bonus_delta,bonus_after,business_date,actor_id,at)
SELECT $1,$7,c.id,'open',0,888,888,0,0,0,$6,$5,clock_timestamp()+interval '3 hours' FROM c`, f.Res.TenantID, f.foreign, f.ids["cards"], storedCard, f.Res.OwnerID, day(0), f.Res.StoreID)
	f.exec(`INSERT INTO card_count_ledger (tenant_id,store_id,card_id,item_id,kind,paid_before,paid_delta,paid_after,gift_before,gift_delta,gift_after,business_date,actor_id,at)
VALUES ($1,$2,$3,$4,'adjust',10,-2,8,3,-1,2,$5,$6,clock_timestamp()+interval '1 hour')`, f.Res.TenantID, f.Res.StoreID, countCard, f.itemA, day(0), f.Res.OwnerID)
	f.exec(`INSERT INTO card_items (tenant_id,store_id,card_id,item_id,unit_price,paid_count_opened,gift_count_opened)
VALUES ($1,$2,$3,$4,30,2,0)`, f.Res.TenantID, f.Res.StoreID, countCard, f.itemB)
	f.exec(`INSERT INTO card_count_ledger (tenant_id,store_id,card_id,item_id,kind,paid_before,paid_delta,paid_after,gift_before,gift_delta,gift_after,business_date,actor_id,at)
VALUES ($1,$2,$3,$4,'adjust',2,0,2,0,0,0,$5,$6,clock_timestamp()+interval '1 hour')`, f.Res.TenantID, f.Res.StoreID, countCard, f.itemB, day(0), f.Res.OwnerID)
	f.exec(`INSERT INTO card_count_ledger (tenant_id,store_id,card_id,item_id,kind,paid_before,paid_delta,paid_after,gift_before,gift_delta,gift_after,business_date,actor_id,at)
VALUES ($1,$2,$3,$4,'adjust',2,97,99,0,0,0,$5,$6,clock_timestamp()+interval '2 hours')`, f.Res.TenantID, f.foreign, countCard, f.itemB, day(0), f.Res.OwnerID)
	f.exec(`INSERT INTO card_items (tenant_id,store_id,card_id,item_id,unit_price,paid_count_opened,gift_count_opened)
VALUES ($1,$2,$3,$4,30,50,0)`, f.Res.TenantID, f.foreign, countCard, f.itemC)
	f.exec(`INSERT INTO card_count_ledger (tenant_id,store_id,card_id,item_id,kind,paid_before,paid_delta,paid_after,gift_before,gift_delta,gift_after,business_date,actor_id,at)
VALUES ($1,$2,$3,$4,'adjust',50,0,50,0,0,0,$5,$6,clock_timestamp()+interval '1 hour')`, f.Res.TenantID, f.Res.StoreID, countCard, f.itemC, day(0), f.Res.OwnerID)

	f.insertAppointment("today_walkin", "confirmed", f.at(0, 9), f.staffA)
	f.insertAppointment("today_booked", "pending", f.at(0, 10), f.staffA)
	f.insertAppointment("today_done", "completed", f.at(0, 11), f.staffA)
	f.insertAppointment("today_voided", "confirmed", f.at(0, 13), f.staffA)
	f.insertAppointment("today_merged", "pending", f.at(0, 14), f.staffA)
	f.insertAppointment("today_old_paid", "confirmed", f.at(0, 15), f.staffA)
	f.allowBlockWithCustomer()
	f.insertBlockFor("today_block", "confirmed", f.at(0, 16), f.staffA)
	f.insertAppointment("appt", "pending", f.at(3, 10), f.staffA)
	f.insertAppointment("appt", "pending", f.at(5, 10), f.staffA)
	f.insertAppointment("appt", "cancelled", f.at(2, 10), f.staffA)
	f.insertAppointment("appt", "confirmed", f.at(-1, 10), f.staffA)
	f.insertBlockFor("appt", "confirmed", f.at(1, 12), f.staffA)
	f.insertAppointment("due_appt", "pending", f.at(5, 9), f.staffA)
	f.insertAppointment("due_cancelled", "cancelled", f.at(4, 9), f.staffA)
	f.insertAppointment("due_past", "confirmed", f.at(-1, 9), f.staffA)
	f.insertAppointmentIn(f.foreign, "one", "confirmed", f.at(0, 17), f.staffA)
	f.insertAppointmentIn(f.foreign, "appt", "confirmed", f.at(1, 11), f.staffA)
	f.insertAppointmentIn(f.foreign, "due_foreign", "pending", f.at(2, 13), f.staffA)
	f.insertBlockFor("due_block", "confirmed", f.at(3, 13), f.staffA)

	f.foreignOrderFor("one", day(-1))

	for _, key := range []string{"one", "cards"} {
		s.call("assign_customer_responsible", map[string]any{"customer_ids": []any{f.ids[key]}, "staff_id": f.staffB}, 200)
	}
	// 建档时间相同的两位顾客用来核对 created 排序的 ID 平局规则。
	f.exec("UPDATE customers SET created_at=$2 WHERE id=ANY($1::uuid[])", []uuid.UUID{f.ids["none"], f.ids["open"]}, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	return f
}

func idOf(v any) uuid.UUID { return uuid.MustParse(v.(string)) }

func (f *overviewFixture) date(offset int) time.Time {
	return settings.Date(f.today).AddDate(0, 0, offset)
}

func (f *overviewFixture) at(offset, hour int) time.Time {
	day := f.date(offset)
	return time.Date(day.Year(), day.Month(), day.Day(), hour, 0, 0, 0, settings.Location)
}

func (f *overviewFixture) order(key string, day time.Time, line overviewLine) map[string]any {
	f.t.Helper()
	return f.orderWith(key, day, []overviewLine{line}, true)
}

func (f *overviewFixture) orderAmount(key string, day time.Time, item uuid.UUID, amount int) map[string]any {
	f.t.Helper()
	order := f.call("open_order", map[string]any{
		"customer_id": f.ids[key], "gender": "female", "designated": false,
		"items": []any{map[string]any{"item_id": item, "staff": []any{map[string]any{"slot": 1, "staff_id": f.staffA}}}},
	}, 200)
	f.checkout(order, amount)
	f.setOrderDate(aID(order), day)
	return order
}

func (f *overviewFixture) orderWith(key string, day time.Time, lines []overviewLine, pay bool) map[string]any {
	f.t.Helper()
	items := make([]any, len(lines))
	total := 0
	for i, line := range lines {
		slots := make([]any, len(line.staff))
		for j, staffID := range line.staff {
			slots[j] = map[string]any{"slot": j + 1, "staff_id": staffID}
		}
		items[i] = map[string]any{"item_id": line.item, "staff": slots}
		total += 100
	}
	order := f.call("open_order", map[string]any{"customer_id": f.ids[key], "gender": "female", "designated": false, "items": items}, 200)
	if pay {
		f.checkout(order, total)
		f.setOrderDate(aID(order), day)
	}
	return order
}

func (f *overviewFixture) checkout(order map[string]any, total int) {
	f.t.Helper()
	f.call("checkout_orders", map[string]any{"orders": []any{map[string]any{"order_id": order["id"], "payments": []any{map[string]any{"method": "cash", "amount": strconv.Itoa(total)}}}}}, 200)
}

func (f *overviewFixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.Pool.Exec(context.Background(), sql, args...); err != nil {
		f.t.Fatal(err)
	}
}

func (f *overviewFixture) setOrderDate(orderID uuid.UUID, day time.Time) {
	f.t.Helper()
	f.exec("UPDATE orders SET business_date=$2 WHERE id=$1", orderID, day)
}

func (f *overviewFixture) markImported(customerID uuid.UUID) {
	f.t.Helper()
	var batch uuid.UUID
	if err := f.Pool.QueryRow(f.t.Context(), `INSERT INTO import_batches (tenant_id,store_id,status,file_name,created_by,committed_by,committed_at) VALUES ($1,$2,'committed','历史.xlsx',$3,$3,clock_timestamp()) RETURNING id`, f.Res.TenantID, f.Res.StoreID, f.Res.OwnerID).Scan(&batch); err != nil {
		f.t.Fatal(err)
	}
	f.exec(`INSERT INTO import_batch_records (tenant_id,store_id,batch_id,kind,entity_id,created) VALUES ($1,$2,$3,'customer',$4,true)`, f.Res.TenantID, f.Res.StoreID, batch, customerID)
}

func (f *overviewFixture) openCard(customerKey string, principal, bonus int) uuid.UUID {
	f.t.Helper()
	typ := idOf(f.call("create_card_type", map[string]any{"name": fmt.Sprintf("储值卡-%s-%d", customerKey, principal), "kind": "stored_value", "validity_kind": "none"}, 200)["id"])
	card := f.call("open_card", map[string]any{
		"customer_id": f.ids[customerKey], "card_type_id": typ, "principal": strconv.Itoa(principal), "bonus": strconv.Itoa(bonus),
		"sales_staff_ids": []any{f.Res.OwnerID},
		"payments":        []any{map[string]any{"method": "cash", "amount": strconv.Itoa(principal)}},
	}, 200)
	return idOf(card["id"])
}

func (f *overviewFixture) openCountCard(customerKey string, paid, gift int) uuid.UUID {
	f.t.Helper()
	typ := idOf(f.call("create_card_type", map[string]any{
		"name": "次卡-" + customerKey, "kind": "count", "validity_kind": "none",
		"items": []any{map[string]any{"item_id": f.itemA, "paid_count": paid, "gift_count": gift, "unit_price": "30.00"}},
	}, 200)["id"])
	card := f.call("open_card", map[string]any{
		"customer_id": f.ids[customerKey], "card_type_id": typ, "principal": "0", "bonus": "0",
		"sales_staff_ids": []any{f.Res.OwnerID},
		"payments":        []any{map[string]any{"method": "cash", "amount": strconv.Itoa(paid * 30)}},
	}, 200)
	return idOf(card["id"])
}

func (f *overviewFixture) insertAppointment(customerKey, status string, start time.Time, staffID uuid.UUID) {
	f.t.Helper()
	f.insertAppointmentIn(f.Res.StoreID, customerKey, status, start, staffID)
}

func (f *overviewFixture) insertAppointmentIn(storeID uuid.UUID, customerKey, status string, start time.Time, staffID uuid.UUID) {
	f.t.Helper()
	f.exec(`INSERT INTO appointments (tenant_id,store_id,staff_id,customer_id,kind,starts_at,ends_at,status,created_by)
VALUES ($1,$2,$3,$4,'appointment',$5,$6,$7,$8)`, f.Res.TenantID, storeID, staffID, f.ids[customerKey], start, start.Add(30*time.Minute), status, f.Res.OwnerID)
}

// allowBlockWithCustomer 去掉"占位不能关联顾客"的检查，用来验证查询仍按 kind 过滤。
func (f *overviewFixture) allowBlockWithCustomer() {
	f.t.Helper()
	f.exec(`DO $$ DECLARE n text;
BEGIN
  SELECT conname INTO n FROM pg_constraint
  WHERE conrelid='appointments'::regclass AND contype='c'
    AND pg_get_constraintdef(oid) LIKE '%customer_id IS NOT NULL%'
    AND pg_get_constraintdef(oid) LIKE '%kind = ''appointment''%';
  IF n IS NOT NULL THEN EXECUTE format('ALTER TABLE appointments DROP CONSTRAINT %I', n); END IF;
END $$`)
}

func (f *overviewFixture) insertBlockFor(customerKey, status string, start time.Time, staffID uuid.UUID) {
	f.t.Helper()
	f.exec(`INSERT INTO appointments (tenant_id,store_id,staff_id,customer_id,kind,starts_at,ends_at,status,created_by)
VALUES ($1,$2,$3,$4,'block',$5,$6,$7,$8)`, f.Res.TenantID, f.Res.StoreID, staffID, f.ids[customerKey], start, start.Add(30*time.Minute), status, f.Res.OwnerID)
}

// otherStore 建第二家门店和一个同租户的别店顾客。
func (f *overviewFixture) otherStore(day time.Time) uuid.UUID {
	f.t.Helper()
	var store uuid.UUID
	if err := f.Pool.QueryRow(f.t.Context(), `INSERT INTO stores (tenant_id,name) VALUES ($1,'二店') RETURNING id`, f.Res.TenantID).Scan(&store); err != nil {
		f.t.Fatal(err)
	}
	var customer uuid.UUID
	if err := f.Pool.QueryRow(f.t.Context(), `INSERT INTO customers (tenant_id,store_id,name,phone,created_by) VALUES ($1,$2,'别店顾客','13999999999',$3) RETURNING id`, f.Res.TenantID, store, f.Res.OwnerID).Scan(&customer); err != nil {
		f.t.Fatal(err)
	}
	f.ids["other_store"] = customer
	return store
}

// foreignOrderFor 给本店顾客写一张别店的已结账单，用来核对订单的 store_id 过滤。
func (f *overviewFixture) foreignOrderFor(customerKey string, day time.Time) {
	f.t.Helper()
	var storeID, snapshotID, checkoutID uuid.UUID
	if err := f.Pool.QueryRow(f.t.Context(), `INSERT INTO stores (tenant_id,name) VALUES ($1,'三店') RETURNING id`, f.Res.TenantID).Scan(&storeID); err != nil {
		f.t.Fatal(err)
	}
	if err := f.Pool.QueryRow(f.t.Context(), `INSERT INTO settings_snapshots (tenant_id,store_id,rules,digest)
SELECT tenant_id,$1::uuid,rules,digest||'-'||$2::text FROM settings_snapshots WHERE store_id=$3 ORDER BY created_at LIMIT 1 RETURNING id`, storeID, storeID, f.Res.StoreID).Scan(&snapshotID); err != nil {
		f.t.Fatal(err)
	}
	if err := f.Pool.QueryRow(f.t.Context(), `INSERT INTO checkouts (tenant_id,store_id,business_date,checked_out_by) VALUES ($1,$2,$3,$4) RETURNING id`, f.Res.TenantID, storeID, day, f.Res.OwnerID).Scan(&checkoutID); err != nil {
		f.t.Fatal(err)
	}
	f.exec(`INSERT INTO orders (tenant_id,store_id,customer_id,gender,designated,settings_snapshot_id,business_date,opened_by,status,checkout_id)
VALUES ($1,$2,$3,'female',false,$4,$5,$6,'paid',$7)`, f.Res.TenantID, storeID, f.ids[customerKey], snapshotID, day, f.Res.OwnerID, checkoutID)
}

func (f *overviewFixture) list(ch optest.Channel, caller ops.Caller, args map[string]any) map[string]any {
	f.t.Helper()
	r := ch.Call(f.t, caller, "list_customer_overview", args)
	if r.Status != 200 {
		f.t.Fatalf("%s list_customer_overview: %d %v %v", ch.Name, r.Status, r.Body, r.Err)
	}
	return r.Body
}

func (f *overviewFixture) segments(ch optest.Channel, caller ops.Caller, args map[string]any) map[string]any {
	f.t.Helper()
	r := ch.Call(f.t, caller, "get_customer_segments", args)
	if r.Status != 200 {
		f.t.Fatalf("%s get_customer_segments: %d %v %v", ch.Name, r.Status, r.Body, r.Err)
	}
	return r.Body
}

func findOverview(t *testing.T, body map[string]any, key string, fixtures map[string]uuid.UUID) map[string]any {
	t.Helper()
	for _, raw := range body["items"].([]any) {
		item := raw.(map[string]any)
		if item["customer"].(map[string]any)["id"] == fixtures[key].String() {
			return item
		}
	}
	t.Fatalf("overview row %s not found", key)
	return nil
}

var overviewAllKeys = []string{
	"none", "one", "cycle", "same_day", "due_balance", "due_empty", "due_appt", "due_cancelled",
	"old_cycle", "sleeping", "new_imported", "new_old", "new_two", "cards", "removed_line", "voided",
	"merged", "open", "today_walkin", "today_booked", "today_done", "keep_for_merge", "appt",
	"voided_only", "due_past", "due_foreign", "due_block", "big_spend", "today_voided", "today_merged",
	"today_old_paid", "today_block",
}

var overviewSegmentKeys = map[string][]string{
	"all":      overviewAllKeys,
	"today":    {"today_walkin", "today_booked", "today_done", "today_voided", "today_merged", "today_old_paid"},
	"due":      {"due_balance", "due_empty", "due_cancelled", "due_past", "due_foreign", "due_block"},
	"balance":  {"due_balance", "cards"},
	"new":      {"one", "same_day", "new_two", "big_spend"},
	"sleeping": {"old_cycle", "sleeping"},
}

var overviewStaffSets = map[string][]string{
	"all":      {"one", "cards"},
	"today":    {},
	"due":      {},
	"balance":  {"cards"},
	"new":      {"one"},
	"sleeping": {},
}

func overviewIDSet(body map[string]any) map[string]bool {
	set := map[string]bool{}
	for _, raw := range body["items"].([]any) {
		set[raw.(map[string]any)["customer"].(map[string]any)["id"].(string)] = true
	}
	return set
}

func assertOverviewIDs(t *testing.T, ch optest.Channel, segment string, body map[string]any, ids map[string]uuid.UUID, keys []string) {
	t.Helper()
	want := map[string]bool{}
	for _, key := range keys {
		want[ids[key].String()] = true
	}
	if got := overviewIDSet(body); !reflect.DeepEqual(got, want) {
		t.Fatalf("%s segment %s ids = %v, want %v", ch.Name, segment, got, want)
	}
}

func TestCustomerOverview(t *testing.T) {
	f := overviewShop(t)
	owner := f.MustCaller(f.Owner)
	for _, ch := range f.Channels() {
		body := f.list(ch, owner, map[string]any{"limit": 100})
		items := body["items"].([]any)
		if len(items) != 32 || body["total"].(float64) != 32 {
			t.Fatalf("%s rows=%d total=%v", ch.Name, len(items), body["total"])
		}
		assertOverviewIDs(t, ch, "all", body, f.ids, overviewAllKeys)

		none := findOverview(t, body, "none", f.ids)
		if none["visit_count"].(float64) != 0 || none["first_visit_date"] != nil || none["days_since_last_visit"] != nil ||
			none["cycle_days"] != nil || none["overdue_days"] != nil || none["next_appointment"] != nil ||
			len(none["last_visit_items"].([]any)) != 0 || len(none["last_visit_staff"].([]any)) != 0 {
			t.Fatalf("%s empty customer = %v", ch.Name, none)
		}
		if none["total_spend"] != "0.00" || none["stored_balance"] != "0.00" || none["count_remaining"].(float64) != 0 {
			t.Fatalf("%s empty money = %v", ch.Name, none)
		}

		one := findOverview(t, body, "one", f.ids)
		if one["visit_count"].(float64) != 1 || one["first_visit_date"] != f.date(-5).Format(settings.DateLayout) ||
			one["last_visit_date"] != f.date(-5).Format(settings.DateLayout) || one["days_since_last_visit"].(float64) != 5 ||
			one["cycle_days"] != nil || one["overdue_days"] != nil || one["total_spend"] != "100.00" {
			t.Fatalf("%s one-visit customer = %v", ch.Name, one)
		}
		itemsNames := one["last_visit_items"].([]any)
		if fmt.Sprint(itemsNames) != fmt.Sprint([]any{"剪发"}) {
			t.Fatalf("%s one-visit items = %v", ch.Name, itemsNames)
		}
		staffNames := one["last_visit_staff"].([]any)
		if len(staffNames) != 1 || staffNames[0].(map[string]any)["name"] != "发型师甲" {
			t.Fatalf("%s one-visit staff = %v", ch.Name, staffNames)
		}

		cycle := findOverview(t, body, "cycle", f.ids)
		if cycle["visit_count"].(float64) != 3 || cycle["days_since_last_visit"].(float64) != 10 ||
			cycle["cycle_days"].(float64) != 25 || cycle["overdue_days"] != nil || cycle["total_spend"] != "300.00" {
			t.Fatalf("%s cycle = %v", ch.Name, cycle)
		}
		sameDay := findOverview(t, body, "same_day", f.ids)
		if sameDay["visit_count"].(float64) != 2 || sameDay["first_visit_date"] != sameDay["last_visit_date"] || sameDay["cycle_days"] != nil {
			t.Fatalf("%s same-day cycle = %v", ch.Name, sameDay)
		}
		overdue := findOverview(t, body, "due_balance", f.ids)
		if overdue["visit_count"].(float64) != 3 || overdue["days_since_last_visit"].(float64) != 40 ||
			overdue["cycle_days"].(float64) != 30 || overdue["overdue_days"].(float64) != 10 ||
			overdue["stored_balance"] != "500.00" {
			t.Fatalf("%s overdue = %v", ch.Name, overdue)
		}
		cards := findOverview(t, body, "cards", f.ids)
		if cards["stored_balance"] != "670.00" || cards["count_remaining"].(float64) != 12 {
			t.Fatalf("%s cards = %v", ch.Name, cards)
		}
		removed := findOverview(t, body, "removed_line", f.ids)
		if fmt.Sprint(removed["last_visit_items"]) != fmt.Sprint([]any{"剪发"}) || len(removed["last_visit_staff"].([]any)) != 1 || removed["total_spend"] != "200.00" {
			t.Fatalf("%s removed line = %v", ch.Name, removed)
		}
		if big := findOverview(t, body, "big_spend", f.ids); big["total_spend"] != "1000.00" {
			t.Fatalf("%s big spend = %v", ch.Name, big)
		}
		voided := findOverview(t, body, "voided", f.ids)
		if voided["visit_count"].(float64) != 1 || voided["last_visit_date"] != f.date(-40).Format(settings.DateLayout) || voided["total_spend"] != "100.00" {
			t.Fatalf("%s voided = %v", ch.Name, voided)
		}
		merged := findOverview(t, body, "merged", f.ids)
		if merged["visit_count"].(float64) != 2 || merged["total_spend"] != "300.00" || merged["last_visit_date"] != f.date(-2).Format(settings.DateLayout) {
			t.Fatalf("%s merged orders = %v", ch.Name, merged)
		}
		appt := findOverview(t, body, "appt", f.ids)
		next, ok := appt["next_appointment"].(map[string]any)
		if !ok || next["starts_at"] != f.at(3, 10).Format(time.RFC3339) || next["staff"].(map[string]any)["name"] != "发型师甲" {
			t.Fatalf("%s next appointment = %v", ch.Name, appt["next_appointment"])
		}
		if findOverview(t, body, "due_cancelled", f.ids)["next_appointment"] != nil {
			t.Fatalf("%s cancelled appointment became next", ch.Name)
		}
		for _, key := range []string{"due_foreign", "due_block"} {
			if findOverview(t, body, key, f.ids)["next_appointment"] != nil {
				t.Fatalf("%s %s appointment became next", ch.Name, key)
			}
		}

		segments := f.segments(ch, owner, map[string]any{})
		assertEqual(t, segments["all"].(map[string]any)["count"].(float64), 32.0)
		assertEqual(t, segments["all"].(map[string]any)["visited_this_month"].(float64), 8.0)
		assertEqual(t, segments["today"].(map[string]any)["count"].(float64), 6.0)
		assertEqual(t, segments["today"].(map[string]any)["arrived"].(float64), 1.0)
		assertEqual(t, segments["due"].(map[string]any)["count"].(float64), 6.0)
		assertEqual(t, segments["due"].(map[string]any)["with_balance"].(float64), 1.0)
		assertEqual(t, segments["balance"].(map[string]any)["count"].(float64), 2.0)
		assertEqual(t, segments["balance"].(map[string]any)["stored_total"], "1170.00")
		assertEqual(t, segments["new"].(map[string]any)["count"].(float64), 4.0)
		assertEqual(t, segments["new"].(map[string]any)["single_visit"].(float64), 2.0)
		assertEqual(t, segments["sleeping"].(map[string]any)["count"].(float64), 2.0)

		for _, segment := range []string{"all", "today", "due", "balance", "new", "sleeping"} {
			scoped := f.list(ch, owner, map[string]any{"segment": segment, "limit": 100})
			if int(scoped["total"].(float64)) != int(segments[segment].(map[string]any)["count"].(float64)) {
				t.Fatalf("%s segment %s list total %v != count %v", ch.Name, segment, scoped["total"], segments[segment])
			}
			assertOverviewIDs(t, ch, segment, scoped, f.ids, overviewSegmentKeys[segment])
		}

		for _, tc := range []struct {
			key string
			q   string
		}{
			{"cycle", "周期客"}, {"cycle", "0003"}, {"cycle", "13800000003"},
		} {
			found := f.list(ch, owner, map[string]any{"query": tc.q})
			if found["total"].(float64) != 1 || found["items"].([]any)[0].(map[string]any)["customer"].(map[string]any)["id"] != f.ids[tc.key].String() {
				t.Fatalf("%s query %s = %v", ch.Name, tc.q, found)
			}
		}
		if r := f.list(ch, owner, map[string]any{"responsible_staff_id": f.staffB}); len(r["items"].([]any)) != 2 {
			t.Fatalf("%s responsible filter = %v", ch.Name, r)
		}
		if r := f.list(ch, owner, map[string]any{"limit": 1}); r["total"].(float64) != 32 || len(r["items"].([]any)) != 1 {
			t.Fatalf("%s paging total = %v", ch.Name, r)
		}
		if r := f.list(ch, owner, map[string]any{"limit": 1, "offset": 100}); r["total"].(float64) != 32 || len(r["items"].([]any)) != 0 {
			t.Fatalf("%s past-end paging = %v", ch.Name, r)
		}

		scopedOwner := owner
		if scopedOwner.Has(perm.CustomerFullPhone) {
			oneCustomer := one["customer"].(map[string]any)
			if oneCustomer["phone"] != "13800000002" || oneCustomer["phone_masked"] != false {
				t.Fatalf("%s full phone = %v", ch.Name, oneCustomer)
			}
		}
		self := f.MustCaller(f.tokenB)
		self.Permissions = map[ops.Permission]ops.Grant{perm.CustomerView: {Scope: ops.ScopeSelf}}
		selfBody := f.list(ch, self, map[string]any{"limit": 100})
		if selfBody["total"].(float64) != 2 {
			t.Fatalf("%s scope total = %v", ch.Name, selfBody["total"])
		}
		for _, raw := range selfBody["items"].([]any) {
			customer := raw.(map[string]any)["customer"].(map[string]any)
			if customer["phone_masked"] != true || customer["phone"] == "13800000002" || customer["phone"] == "13800000014" {
				t.Fatalf("%s scoped phone = %v", ch.Name, customer)
			}
		}
		selfSegments := f.segments(ch, self, map[string]any{})
		for _, segment := range []string{"all", "today", "due", "balance", "new", "sleeping"} {
			scoped := f.list(ch, self, map[string]any{"segment": segment, "limit": 100})
			if int(scoped["total"].(float64)) != int(selfSegments[segment].(map[string]any)["count"].(float64)) {
				t.Fatalf("%s scoped segment %s mismatch", ch.Name, segment)
			}
			assertOverviewIDs(t, ch, "self "+segment, scoped, f.ids, overviewStaffSets[segment])
		}
		staffSegments := f.segments(ch, owner, map[string]any{"responsible_staff_id": f.staffB})
		for _, segment := range []string{"all", "today", "due", "balance", "new", "sleeping"} {
			scoped := f.list(ch, owner, map[string]any{"segment": segment, "responsible_staff_id": f.staffB, "limit": 100})
			if int(scoped["total"].(float64)) != int(staffSegments[segment].(map[string]any)["count"].(float64)) {
				t.Fatalf("%s staff segment %s list total %v != count %v", ch.Name, segment, scoped["total"], staffSegments[segment])
			}
			assertOverviewIDs(t, ch, "staff "+segment, scoped, f.ids, overviewStaffSets[segment])
		}

		assertOverviewSort(t, ch, "last_visit", body)
		assertOverviewSort(t, ch, "overdue", f.list(ch, owner, map[string]any{"sort": "overdue", "limit": 100}))
		assertOverviewSort(t, ch, "spend", f.list(ch, owner, map[string]any{"sort": "spend", "limit": 100}))
		assertOverviewSort(t, ch, "created", f.list(ch, owner, map[string]any{"sort": "created", "limit": 100}))
	}
}

func TestCustomerOverviewCustomerID(t *testing.T) {
	f := overviewShop(t)
	owner := f.MustCaller(f.Owner)
	today := f.create(map[string]any{"name": "今天到店客", "phone": "13800000040"})
	f.ids["customer_id_today"] = aID(today)
	f.call("assign_customer_responsible", map[string]any{"customer_ids": []any{today["id"]}, "staff_id": f.Res.OwnerID}, 200)
	f.order("customer_id_today", f.today, overviewLine{item: f.itemA, staff: []uuid.UUID{f.staffA}})

	for _, ch := range f.Channels() {
		all := f.list(ch, owner, map[string]any{"limit": 100})
		want := findOverview(t, all, "cycle", f.ids)
		got := f.list(ch, owner, map[string]any{"customer_id": f.ids["cycle"].String()})
		if got["total"].(float64) != 1 || len(got["items"].([]any)) != 1 {
			t.Fatalf("%s customer filter = %v", ch.Name, got)
		}
		if !reflect.DeepEqual(got["items"].([]any)[0], want) {
			t.Fatalf("%s customer filter row = %v, want %v", ch.Name, got["items"].([]any)[0], want)
		}

		self := f.MustCaller(f.tokenB)
		self.Permissions = map[ops.Permission]ops.Grant{perm.CustomerView: {Scope: ops.ScopeSelf}}
		scoped := f.list(ch, self, map[string]any{"customer_id": f.ids["cycle"].String()})
		if scoped["total"].(float64) != 0 || len(scoped["items"].([]any)) != 0 {
			t.Fatalf("%s scoped customer filter = %v", ch.Name, scoped)
		}

		merged := f.list(ch, owner, map[string]any{"customer_id": f.ids["merged_away"].String()})
		if merged["total"].(float64) != 0 || len(merged["items"].([]any)) != 0 {
			t.Fatalf("%s merged customer filter = %v", ch.Name, merged)
		}

		otherStore := f.list(ch, owner, map[string]any{"customer_id": f.ids["other_store"].String()})
		if otherStore["total"].(float64) != 0 || len(otherStore["items"].([]any)) != 0 {
			t.Fatalf("%s other-store customer filter = %v", ch.Name, otherStore)
		}

		base := f.list(ch, owner, map[string]any{"segment": "balance", "limit": 100})
		if base["total"].(float64) <= 1 || len(base["items"].([]any)) <= 1 {
			t.Fatalf("%s balance baseline = %v", ch.Name, base)
		}

		mismatch := f.list(ch, owner, map[string]any{"customer_id": f.ids["cards"].String(), "segment": "balance"})
		if mismatch["total"].(float64) != 1 || len(mismatch["items"].([]any)) != 1 {
			t.Fatalf("%s customer segment match = %v", ch.Name, mismatch)
		}
		if gotID := mismatch["items"].([]any)[0].(map[string]any)["customer"].(map[string]any)["id"]; gotID != f.ids["cards"].String() {
			t.Fatalf("%s customer segment row = %v", ch.Name, gotID)
		}

		sleeping := f.list(ch, owner, map[string]any{"customer_id": f.ids["cards"].String(), "segment": "sleeping"})
		if sleeping["total"].(float64) != 0 || len(sleeping["items"].([]any)) != 0 {
			t.Fatalf("%s segment mismatch = %v", ch.Name, sleeping)
		}

		mismatch = f.list(ch, owner, map[string]any{"customer_id": f.ids["customer_id_today"].String(), "segment": "sleeping"})
		if mismatch["total"].(float64) != 0 || len(mismatch["items"].([]any)) != 0 {
			t.Fatalf("%s segment mismatch = %v", ch.Name, mismatch)
		}
	}
}

func assertOverviewSort(t *testing.T, ch optest.Channel, sortKey string, body map[string]any) {
	t.Helper()
	items := body["items"].([]any)
	value := func(raw any) (string, bool) {
		item := raw.(map[string]any)
		switch sortKey {
		case "last_visit":
			v, ok := item["last_visit_date"].(string)
			return v, ok
		case "spend":
			return fmt.Sprintf("%020d", amountCents(t, item["total_spend"])), true
		case "overdue":
			if item["overdue_days"] == nil {
				return "", false
			}
			return fmt.Sprintf("%08.2f", item["overdue_days"].(float64)), true
		case "created":
			return item["customer"].(map[string]any)["created_at"].(string), true
		}
		return "", false
	}
	for i := 1; i < len(items); i++ {
		prev, prevOK := value(items[i-1])
		cur, curOK := value(items[i])
		if prevOK != curOK {
			if !prevOK && curOK {
				t.Fatalf("%s sort %s null not last at %d", ch.Name, sortKey, i)
			}
			continue
		}
		if !prevOK {
			continue
		}
		prevID := items[i-1].(map[string]any)["customer"].(map[string]any)["id"].(string)
		curID := items[i].(map[string]any)["customer"].(map[string]any)["id"].(string)
		if prev < cur || (prev == cur && prevID > curID) {
			t.Fatalf("%s sort %s order at %d: %s/%s then %s/%s", ch.Name, sortKey, i, prev, prevID, cur, curID)
		}
	}
}

func amountCents(t *testing.T, raw any) int64 {
	t.Helper()
	text, ok := raw.(string)
	if !ok {
		t.Fatalf("amount %v is not a string", raw)
	}
	whole, frac, _ := strings.Cut(text, ".")
	if len(frac) < 2 {
		frac += strings.Repeat("0", 2-len(frac))
	}
	units, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	cents, err := strconv.ParseInt(frac[:2], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return units*100 + cents
}

type overviewQueryCounter struct{ all, segments atomic.Int64 }

func (c *overviewQueryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	c.all.Add(1)
	if strings.Contains(data.SQL, "-- name: CustomerOverviewSegments :") {
		c.segments.Add(1)
	}
	return ctx
}
func (*overviewQueryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestCustomerOverviewQueryCount(t *testing.T) {
	s := open(t)
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, settings.Location)
	s.SetNow(now)
	position := idOf(s.call("create_position", map[string]any{"name": "计数大工"}, 200)["id"])
	staffID, _ := s.Hire("计数发型师", "002", "overview-count")
	item := idOf(s.call("create_item", map[string]any{
		"name": "计数剪发", "price": "100.00", "discountable": true, "labor_fee_amount": "10.00", "labor_fee_percent": nil,
		"positions": []any{map[string]any{"position_id": position, "required": true}}, "level_prices": []any{},
	}, 200)["id"])
	source := s.call("create_customer_source", map[string]any{"name": "计数来源"}, 200)
	ids := []any{}
	for i := range 20 {
		c := s.create(map[string]any{"name": fmt.Sprintf("计数客%02d", i), "phone": fmt.Sprintf("1370000%04d", i), "source_id": source["id"]})
		ids = append(ids, c["id"])
		s.call("assign_customer_responsible", map[string]any{"customer_ids": []any{c["id"]}, "staff_id": s.Res.OwnerID}, 200)
		s.call("add_safety_label", map[string]any{"customer_id": c["id"], "kind": "allergy", "source": "customer_said", "observed_on": "2020-01-01"}, 200)
		order := s.call("open_order", map[string]any{
			"customer_id": c["id"], "gender": "female", "designated": false,
			"items": []any{map[string]any{"item_id": item, "staff": []any{map[string]any{"slot": 1, "staff_id": staffID}}}},
		}, 200)
		s.call("checkout_orders", map[string]any{"orders": []any{map[string]any{"order_id": order["id"], "payments": []any{map[string]any{"method": "cash", "amount": "100"}}}}}, 200)
		start := time.Date(2026, 11, 24, 9, 0, 0, 0, settings.Location).Add(time.Duration(i) * 30 * time.Minute)
		if _, err := s.Pool.Exec(context.Background(), `INSERT INTO appointments (tenant_id,store_id,staff_id,customer_id,kind,starts_at,ends_at,status,created_by)
VALUES ($1,$2,$3,$4,'appointment',$5,$6,'pending',$7)`, s.Res.TenantID, s.Res.StoreID, staffID, aID(c), start, start.Add(30*time.Minute), s.Res.OwnerID); err != nil {
			t.Fatal(err)
		}
	}
	counter := &overviewQueryCounter{}
	cfg := s.Pool.Config().Copy()
	cfg.ConnConfig.Tracer = counter
	pool, err := db.NewPoolWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	app := optest.New(t, pool)
	app.SetNow(now)
	caller := s.MustCaller(s.Owner)
	for _, ch := range app.Channels() {
		counts := []int64{}
		for _, limit := range []int{1, 20} {
			counter.all.Store(0)
			r := ch.Call(t, caller, "list_customer_overview", map[string]any{"limit": limit})
			if r.Status != 200 || len(r.Body["items"].([]any)) != limit {
				t.Fatalf("%s query count call: %d %v %v", ch.Name, r.Status, r.Body, r.Err)
			}
			if limit == 20 {
				for _, raw := range r.Body["items"].([]any) {
					row := raw.(map[string]any)
					if len(row["last_visit_items"].([]any)) == 0 || len(row["last_visit_staff"].([]any)) == 0 || row["next_appointment"] == nil {
						t.Fatalf("%s batched loads are empty: %v", ch.Name, row)
					}
				}
			}
			counts = append(counts, counter.all.Load())
		}
		t.Logf("%s overview N=1: %d SQL; N=20: %d SQL", ch.Name, counts[0], counts[1])
		if counts[0] != counts[1] {
			t.Fatalf("%s overview SQL count grew with rows: %v", ch.Name, counts)
		}
		counter.segments.Store(0)
		if r := ch.Call(t, caller, "get_customer_segments", map[string]any{}); r.Status != 200 {
			t.Fatalf("%s segments: %d %v %v", ch.Name, r.Status, r.Body, r.Err)
		}
		t.Logf("%s segments: %d segment SQL", ch.Name, counter.segments.Load())
		if counter.segments.Load() != 1 {
			t.Fatalf("%s get_customer_segments ran %d queries, want 1", ch.Name, counter.segments.Load())
		}
	}
	if len(ids) != 20 {
		t.Fatal("fixture")
	}
}

func TestCustomerOverviewBusinessDate(t *testing.T) {
	s := open(t)
	now := time.Date(2026, 11, 21, 1, 30, 0, 0, settings.Location)
	s.SetNow(now)
	if _, err := s.Pool.Exec(context.Background(), `INSERT INTO setting_changes (tenant_id,store_id,key,value,is_default,old_value,old_is_default,effective_from,changed_by)
VALUES ($1,$2,'business_day_end','"03:00"',false,'"00:00"',true,'2026-01-01',$3)`, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID); err != nil {
		t.Fatal(err)
	}
	position := idOf(s.call("create_position", map[string]any{"name": "大工"}, 200)["id"])
	staffID, _ := s.Hire("营业日发型师", "002", "overview-day")
	item := idOf(s.call("create_item", map[string]any{
		"name": "营业日剪发", "price": "100.00", "discountable": true, "labor_fee_amount": "10.00", "labor_fee_percent": nil,
		"positions": []any{map[string]any{"position_id": position, "required": true}}, "level_prices": []any{},
	}, 200)["id"])
	customer := s.create(map[string]any{"name": "营业日客", "phone": "13800000090"})
	order := s.call("open_order", map[string]any{
		"customer_id": customer["id"], "paper_date": "2026-11-20", "gender": "female", "designated": false,
		"items": []any{map[string]any{"item_id": item, "staff": []any{map[string]any{"slot": 1, "staff_id": staffID}}}},
	}, 200)
	s.call("checkout_orders", map[string]any{"orders": []any{map[string]any{"order_id": order["id"], "payments": []any{map[string]any{"method": "cash", "amount": "100"}}}}}, 200)
	if _, err := s.Pool.Exec(context.Background(), `INSERT INTO appointments (tenant_id,store_id,staff_id,customer_id,kind,starts_at,ends_at,status,created_by)
VALUES ($1,$2,$3,$4,'appointment',$5,$6,'confirmed',$7),($1,$2,$3,$4,'appointment',$8,$9,'confirmed',$7)`,
		s.Res.TenantID, s.Res.StoreID, staffID, aID(customer),
		time.Date(2026, 11, 20, 10, 0, 0, 0, settings.Location), time.Date(2026, 11, 20, 10, 30, 0, 0, settings.Location),
		s.Res.OwnerID, time.Date(2026, 11, 21, 10, 0, 0, 0, settings.Location), time.Date(2026, 11, 21, 10, 30, 0, 0, settings.Location)); err != nil {
		t.Fatal(err)
	}
	caller := s.MustCaller(s.Owner)
	for _, ch := range s.Channels() {
		body := ch.Call(t, caller, "list_customer_overview", map[string]any{"limit": 100})
		if body.Status != 200 {
			t.Fatalf("%s business date list: %d %v", ch.Name, body.Status, body.Body)
		}
		itemBody := findOverview(t, body.Body, "biz", map[string]uuid.UUID{"biz": aID(customer)})
		if itemBody["days_since_last_visit"].(float64) != 0 || itemBody["last_visit_date"] != "2026-11-20" {
			t.Fatalf("%s business date row = %v", ch.Name, itemBody)
		}
		today := ch.Call(t, caller, "list_customer_overview", map[string]any{"segment": "today", "limit": 100})
		if today.Status != 200 || today.Body["total"].(float64) != 1 || len(today.Body["items"].([]any)) != 1 {
			t.Fatalf("%s business date today list: %d %v", ch.Name, today.Status, today.Body)
		}
		if id := today.Body["items"].([]any)[0].(map[string]any)["customer"].(map[string]any)["id"]; id != customer["id"] {
			t.Fatalf("%s business date today customer = %v", ch.Name, id)
		}
		segments := ch.Call(t, caller, "get_customer_segments", map[string]any{})
		if segments.Status != 200 || segments.Body["today"].(map[string]any)["count"].(float64) != 1 {
			t.Fatalf("%s business date today = %v", ch.Name, segments.Body)
		}
	}
}
