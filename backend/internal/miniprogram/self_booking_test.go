package miniprogram_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

func openSelf(t *testing.T) *fixture {
	t.Helper()
	s := openFixture(t)
	call(t, s.Shop, "update_staff", map[string]any{"staff_id": s.stylist, "name": "发型师", "number": "002", "self_bookable": true}, 200)
	return s
}

func (s *fixture) day(offset int) string {
	return settings.Date(s.Now()).AddDate(0, 0, offset).Format(settings.DateLayout)
}

func (s *fixture) at(offset int, clock string) time.Time {
	s.t.Helper()
	parsed, err := time.ParseInLocation(settings.DateLayout+" 15:04", s.day(offset)+" "+clock, settings.Location)
	if err != nil {
		s.t.Fatal(err)
	}
	return parsed
}

func (s *fixture) selfBook(token string, staffID uuid.UUID, startsAt time.Time, itemIDs []uuid.UUID) optest.Result {
	s.t.Helper()
	return s.customerHTTP(token, "book_appointment", map[string]any{"staff_id": staffID, "starts_at": startsAt, "item_ids": itemIDs})
}

func (s *fixture) selfSlots(token string, staffID uuid.UUID, day string, itemIDs []uuid.UUID) optest.Result {
	s.t.Helper()
	return s.customerHTTP(token, "my_available_slots", map[string]any{"staff_id": staffID, "date": day, "item_ids": itemIDs})
}

func (s *fixture) setSetting(key string, value any) {
	s.t.Helper()
	s.setSettingFrom(key, value, s.day(0))
}

func (s *fixture) setSettingFrom(key string, value any, from string) {
	s.t.Helper()
	call(s.t, s.Shop, "change_setting", map[string]any{"key": key, "value": value, "effective_from": from}, 200)
}

func (s *fixture) createShift(staffID uuid.UUID, start, end, from string) {
	s.t.Helper()
	out := call(s.t, s.Shop, "create_shift", map[string]any{"name": "班次" + uuid.NewString()[:6], "start_time": start, "end_time": end}, 200)
	call(s.t, s.Shop, "set_staff_shift", map[string]any{"staff_id": staffID, "shift_id": out["id"], "effective_from": from}, 200)
}

func (s *fixture) createItemWith(name string, duration int, bookable, active bool) uuid.UUID {
	s.t.Helper()
	out := call(s.t, s.Shop, "create_item", map[string]any{
		"name": name + uuid.NewString()[:6], "price": "10.00", "discountable": true, "duration_minutes": duration,
		"bookable": bookable, "labor_fee_amount": nil, "labor_fee_percent": nil,
		"positions": []any{}, "level_prices": []any{}, "member_prices": []any{},
	}, 200)
	if active {
		return uuid.MustParse(out["id"].(string))
	}
	call(s.t, s.Shop, "update_item", map[string]any{
		"id": out["id"], "name": out["name"], "price": "10.00", "discountable": true, "active": false,
		"duration_minutes": duration, "bookable": bookable, "labor_fee_amount": nil, "labor_fee_percent": nil,
		"positions": []any{}, "level_prices": []any{}, "member_prices": []any{},
	}, 200)
	return uuid.MustParse(out["id"].(string))
}

func slotsOf(t *testing.T, r optest.Result) []string {
	t.Helper()
	if r.Status != http.StatusOK {
		t.Fatalf("slots: %d %v", r.Status, r.Body)
	}
	raw := r.Body["slots"].([]any)
	out := make([]string, 0, len(raw))
	for _, value := range raw {
		out = append(out, value.(string))
	}
	return out
}

func hasSlot(slots []string, want string) bool {
	for _, slot := range slots {
		if slot == want {
			return true
		}
	}
	return false
}

func requireDetail(t *testing.T, r optest.Result, status int, detail string) {
	t.Helper()
	if r.Status != status || r.Detail() != detail {
		t.Fatalf("got %d %q (%v), want %d %q", r.Status, r.Detail(), r.Body, status, detail)
	}
}

func TestSelfBookingOptionsAndSlots(t *testing.T) {
	s := openSelf(t)
	inactive, _ := s.Hire("停用可约", "004", "self-inactive")
	call(t, s.Shop, "update_staff", map[string]any{"staff_id": inactive, "name": "停用可约", "number": "004", "self_bookable": true}, 200)
	call(t, s.Shop, "deactivate_staff", map[string]any{"staff_id": inactive}, 200)
	s.createItemWith("不可约", 30, false, true)
	s.createItemWith("已停用", 30, true, false)
	options := s.customerHTTP(s.tokenA, "my_booking_options", map[string]any{})
	requireDetail(t, options, http.StatusOK, "")
	people := options.Body["staff"].([]any)
	if len(people) != 1 || people[0].(map[string]any)["id"] != s.stylist.String() || people[0].(map[string]any)["level_name"] != nil {
		t.Fatalf("staff options = %v", people)
	}
	items := options.Body["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("item options = %v", items)
	}
	for _, raw := range items {
		item := raw.(map[string]any)
		if item["duration_minutes"] != float64(30) || item["price"] != nil {
			t.Fatalf("item option leaked price or wrong duration: %v", item)
		}
	}
	rules := options.Body["rules"].(map[string]any)
	hours := rules["business_hours"].(map[string]any)
	if hours["open"] != "09:00" || hours["close"] != "22:00" || rules["slot_minutes"] != float64(30) || rules["default_duration_minutes"] != float64(60) {
		t.Fatalf("rules = %v", rules)
	}

	one := slotsOf(t, s.selfSlots(s.tokenA, s.stylist, s.day(1), []uuid.UUID{s.item}))
	if !hasSlot(one, "09:00") || !hasSlot(one, "21:30") || hasSlot(one, "22:00") {
		t.Fatalf("30-minute slots = %v", one)
	}
	two := slotsOf(t, s.selfSlots(s.tokenA, s.stylist, s.day(1), []uuid.UUID{s.item, s.otherItem}))
	if !hasSlot(two, "21:00") || hasSlot(two, "21:30") {
		t.Fatalf("60-minute slots = %v", two)
	}
	longItem := s.createItemWith("九十分钟", 90, true, true)
	long := slotsOf(t, s.selfSlots(s.tokenA, s.stylist, s.day(1), []uuid.UUID{longItem}))
	if !hasSlot(long, "20:30") || hasSlot(long, "21:00") {
		t.Fatalf("90-minute slots = %v", long)
	}
	for _, slot := range two {
		if !strings.HasSuffix(slot, ":00") && !strings.HasSuffix(slot, ":30") {
			t.Fatalf("slot %q is not on the 30-minute grid", slot)
		}
	}
	requireDetail(t, s.selfSlots(s.tokenA, uuid.New(), s.day(1), []uuid.UUID{s.item}), http.StatusNotFound, "not found")
	requireDetail(t, s.selfSlots(s.tokenA, s.Res.OwnerID, s.day(1), []uuid.UUID{s.item}), http.StatusNotFound, "not found")
}

func TestSelfBookingRuleStaff(t *testing.T) {
	t.Run("not self bookable", func(t *testing.T) {
		s := openSelf(t)
		requireDetail(t, s.selfBook(s.tokenA, s.Res.OwnerID, s.at(1, "10:00"), []uuid.UUID{s.item}), http.StatusConflict, "该发型师不可预约")
		requireDetail(t, s.selfSlots(s.tokenA, s.Res.OwnerID, s.day(1), []uuid.UUID{s.item}), http.StatusNotFound, "not found")
	})
	t.Run("inactive", func(t *testing.T) {
		s := openSelf(t)
		call(t, s.Shop, "deactivate_staff", map[string]any{"staff_id": s.stylist}, 200)
		requireDetail(t, s.selfBook(s.tokenA, s.stylist, s.at(1, "10:00"), []uuid.UUID{s.item}), http.StatusConflict, "该发型师不可预约")
		requireDetail(t, s.selfSlots(s.tokenA, s.stylist, s.day(1), []uuid.UUID{s.item}), http.StatusNotFound, "not found")
	})
}

func TestSelfBookingRuleItems(t *testing.T) {
	s := openSelf(t)
	book := func(itemIDs ...uuid.UUID) optest.Result {
		return s.selfBook(s.tokenA, s.stylist, s.at(1, "10:00"), itemIDs)
	}
	requireDetail(t, book(s.item, s.item), http.StatusConflict, "项目不能重复")
	requireDetail(t, s.selfSlots(s.tokenA, s.stylist, s.day(1), []uuid.UUID{s.item, s.item}), http.StatusConflict, "项目不能重复")
	unbookable := s.createItemWith("不可约", 30, false, true)
	requireDetail(t, book(unbookable), http.StatusConflict, "该项目不可预约")
	requireDetail(t, s.selfSlots(s.tokenA, s.stylist, s.day(1), []uuid.UUID{unbookable}), http.StatusConflict, "该项目不可预约")
	inactive := s.createItemWith("已停用", 30, true, false)
	requireDetail(t, book(inactive), http.StatusConflict, "项目已停用")
	requireDetail(t, s.selfSlots(s.tokenA, s.stylist, s.day(1), []uuid.UUID{inactive}), http.StatusConflict, "项目已停用")
	requireDetail(t, book(uuid.New()), http.StatusConflict, "项目不存在")
	requireDetail(t, s.selfSlots(s.tokenA, s.stylist, s.day(1), []uuid.UUID{uuid.New()}), http.StatusConflict, "项目不存在")
	requireDetail(t, s.selfSlots(s.tokenA, s.stylist, s.day(1), []uuid.UUID{}), http.StatusConflict, "请至少选择一个项目")
	if r := book(); r.Status != http.StatusUnprocessableEntity {
		t.Fatalf("empty book items = %d %v", r.Status, r.Body)
	}
}

func TestSelfBookingRuleGrid(t *testing.T) {
	s := openSelf(t)
	requireDetail(t, s.selfBook(s.tokenA, s.stylist, s.at(1, "10:10"), []uuid.UUID{s.item}), http.StatusConflict, "预约开始时间要在整点或半点")
	slots := slotsOf(t, s.selfSlots(s.tokenA, s.stylist, s.day(1), []uuid.UUID{s.item}))
	for _, slot := range slots {
		if !strings.HasSuffix(slot, ":00") && !strings.HasSuffix(slot, ":30") {
			t.Fatalf("slot %q is not on the 30-minute grid", slot)
		}
	}
}

func TestSelfBookingRuleLeadTime(t *testing.T) {
	s := openSelf(t)
	s.setSetting(settings.KeyBookingMinLeadMinutes, 60)
	requireDetail(t, s.selfBook(s.tokenA, s.stylist, s.at(0, "10:30"), []uuid.UUID{s.item}), http.StatusConflict, "预约至少要提前 60 分钟")
	slots := slotsOf(t, s.selfSlots(s.tokenA, s.stylist, s.day(0), []uuid.UUID{s.item}))
	if hasSlot(slots, "10:00") || hasSlot(slots, "10:30") || !hasSlot(slots, "11:00") {
		t.Fatalf("lead-time slots = %v", slots)
	}
}

func TestSelfBookingRuleSchedule(t *testing.T) {
	s := openSelf(t)
	requireDetail(t, s.selfBook(s.tokenA, s.stylist, s.at(0, "08:30"), []uuid.UUID{s.item}), http.StatusConflict, "预约时间要在营业时段（09:00–22:00）内")
	requireDetail(t, s.selfBook(s.tokenA, s.stylist, s.at(31, "10:00"), []uuid.UUID{s.item}), http.StatusConflict, "最多只能预约 30 天内的时段")
	requireDetail(t, s.selfBook(s.tokenA, s.stylist, s.at(-1, "10:00"), []uuid.UUID{s.item}), http.StatusConflict, "预约日期不能早于今天")
	requireDetail(t, s.selfBook(s.tokenA, s.stylist, s.at(0, "09:30"), []uuid.UUID{s.item}), http.StatusConflict, "不能预约已经过去的时段")
	if slots := slotsOf(t, s.selfSlots(s.tokenA, s.stylist, s.day(31), []uuid.UUID{s.item})); len(slots) != 0 {
		t.Fatalf("slots beyond max days = %v", slots)
	}
	todaySlots := slotsOf(t, s.selfSlots(s.tokenA, s.stylist, s.day(0), []uuid.UUID{s.item}))
	if hasSlot(todaySlots, "09:30") || !hasSlot(todaySlots, "10:00") {
		t.Fatalf("today slots = %v", todaySlots)
	}
}

func TestSelfBookingRuleShift(t *testing.T) {
	s := openSelf(t)
	s.createShift(s.stylist, "12:00", "16:00", s.day(0))
	slots := slotsOf(t, s.selfSlots(s.tokenA, s.stylist, s.day(1), []uuid.UUID{s.item}))
	if !hasSlot(slots, "12:00") || !hasSlot(slots, "15:30") || hasSlot(slots, "11:30") || hasSlot(slots, "16:00") {
		t.Fatalf("shift slots = %v", slots)
	}
	longSlots := slotsOf(t, s.selfSlots(s.tokenA, s.stylist, s.day(1), []uuid.UUID{s.item, s.otherItem}))
	if !hasSlot(longSlots, "12:00") || hasSlot(longSlots, "15:30") {
		t.Fatalf("long shift slots = %v", longSlots)
	}
	requireDetail(t, s.selfBook(s.tokenA, s.stylist, s.at(1, "11:00"), []uuid.UUID{s.item}), http.StatusConflict, "该时段发型师不上班")
	if created := s.selfBook(s.tokenA, s.stylist, s.at(1, "12:00"), []uuid.UUID{s.item}); created.Status != http.StatusOK {
		t.Fatalf("touching shift start: %v", created)
	}
	if created := s.selfBook(s.tokenA, s.stylist, s.at(1, "15:30"), []uuid.UUID{s.item}); created.Status != http.StatusOK {
		t.Fatalf("touching shift end: %v", created)
	}
	requireDetail(t, s.selfBook(s.tokenA, s.stylist, s.at(1, "15:30"), []uuid.UUID{s.item, s.otherItem}), http.StatusConflict, "该时段发型师不上班")
}

func TestSelfBookingRuleOverlap(t *testing.T) {
	s := openSelf(t)
	call(t, s.Shop, "create_appointment", map[string]any{
		"customer_id": s.customerA, "staff_id": s.stylist, "starts_at": s.at(1, "13:00"), "ends_at": s.at(1, "13:30"), "items": []any{map[string]any{"item_id": s.item}},
	}, 200)
	call(t, s.Shop, "create_block", map[string]any{"staff_id": s.stylist, "starts_at": s.at(1, "15:00"), "ends_at": s.at(1, "15:30")}, 200)
	slots := slotsOf(t, s.selfSlots(s.tokenA, s.stylist, s.day(1), []uuid.UUID{s.item}))
	if hasSlot(slots, "13:00") || !hasSlot(slots, "13:30") || hasSlot(slots, "15:00") {
		t.Fatalf("overlap slots = %v", slots)
	}
	requireDetail(t, s.selfBook(s.tokenA, s.stylist, s.at(1, "13:00"), []uuid.UUID{s.item}), http.StatusConflict, "该时段已有预约或占位")
	if created := s.selfBook(s.tokenB, s.stylist, s.at(1, "12:30"), []uuid.UUID{s.item}); created.Status != http.StatusOK {
		t.Fatalf("touching before: %v", created)
	}
	if created := s.selfBook(s.tokenB, s.stylist, s.at(1, "13:30"), []uuid.UUID{s.item}); created.Status != http.StatusOK {
		t.Fatalf("touching after: %v", created)
	}
	requireDetail(t, s.selfBook(s.tokenA, s.stylist, s.at(1, "15:00"), []uuid.UUID{s.item}), http.StatusConflict, "该时段已有预约或占位")
}

func TestSelfBookingRuleLimit(t *testing.T) {
	s := openSelf(t)
	past := s.at(-2, "10:00")
	if _, err := s.Pool.Exec(context.Background(), `INSERT INTO appointments (tenant_id,store_id,staff_id,customer_id,kind,starts_at,ends_at,status,created_by_customer)
 VALUES ($1,$2,$3,$4,'appointment',$5,$6,'pending',$4)`, s.Res.TenantID, s.Res.StoreID, s.stylist, s.customerA, past, past.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	ids := []uuid.UUID{}
	for i, clock := range []string{"10:00", "11:00", "12:00"} {
		r := s.selfBook(s.tokenA, s.stylist, s.at(i+1, clock), []uuid.UUID{s.item})
		if r.Status != http.StatusOK {
			t.Fatalf("booking %d: %v", i, r)
		}
		ids = append(ids, uuid.MustParse(r.Body["id"].(string)))
	}
	requireDetail(t, s.selfBook(s.tokenA, s.stylist, s.at(4, "10:00"), []uuid.UUID{s.item}), http.StatusConflict, "最多同时保留 3 个待到店的自助预约")
	if slots := slotsOf(t, s.selfSlots(s.tokenA, s.stylist, s.day(4), []uuid.UUID{s.item})); len(slots) != 0 {
		t.Fatalf("slots after limit = %v", slots)
	}
	if cancelled := s.customerHTTP(s.tokenA, "cancel_my_appointment", map[string]any{"id": ids[0]}); cancelled.Status != http.StatusOK {
		t.Fatalf("cancel to free a slot: %v", cancelled)
	}
	if r := s.selfBook(s.tokenA, s.stylist, s.at(5, "10:00"), []uuid.UUID{s.item}); r.Status != http.StatusOK {
		t.Fatalf("booking after cancellation: %v", r)
	}
}

func selfBookArgs(staffID uuid.UUID, startsAt time.Time, itemIDs []uuid.UUID) map[string]any {
	return map[string]any{"staff_id": staffID, "starts_at": startsAt, "item_ids": itemIDs}
}

func TestSelfBookingConcurrentSameSlot(t *testing.T) {
	s := openSelf(t)
	results := make(chan optest.Result, 2)
	var wg sync.WaitGroup
	for _, token := range []string{s.tokenA, s.tokenB} {
		wg.Add(1)
		go func(token string) {
			defer wg.Done()
			results <- s.HTTPWithToken(t, token, "book_appointment", selfBookArgs(s.stylist, s.at(5, "14:00"), []uuid.UUID{s.item}))
		}(token)
	}
	wg.Wait()
	close(results)
	ok, conflict := 0, 0
	for r := range results {
		switch r.Status {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			conflict++
			requireDetail(t, r, http.StatusConflict, "该时段已有预约或占位")
		default:
			t.Fatalf("concurrent same-slot booking: %v", r)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("same-slot results: ok=%d conflict=%d", ok, conflict)
	}
}

func TestSelfBookingLimitConcurrency(t *testing.T) {
	s := openSelf(t)
	for i, clock := range []string{"10:00", "11:00"} {
		if r := s.selfBook(s.tokenA, s.stylist, s.at(i+2, clock), []uuid.UUID{s.item}); r.Status != http.StatusOK {
			t.Fatalf("held booking %d: %v", i, r)
		}
	}
	results := make(chan optest.Result, 2)
	var wg sync.WaitGroup
	for i, clock := range []string{"10:00", "11:00"} {
		wg.Add(1)
		go func(i int, clock string) {
			defer wg.Done()
			results <- s.HTTPWithToken(t, s.tokenA, "book_appointment", selfBookArgs(s.stylist, s.at(i+4, clock), []uuid.UUID{s.item}))
		}(i, clock)
	}
	wg.Wait()
	close(results)
	ok, limited := 0, 0
	for r := range results {
		switch r.Status {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			limited++
			requireDetail(t, r, http.StatusConflict, "最多同时保留 3 个待到店的自助预约")
		default:
			t.Fatalf("concurrent limit booking: %v", r)
		}
	}
	if ok != 1 || limited != 1 {
		t.Fatalf("limit concurrency: ok=%d limited=%d", ok, limited)
	}
}

func TestSelfBookingIsolation(t *testing.T) {
	s := openSelf(t)
	created := s.selfBook(s.tokenA, s.stylist, s.at(1, "10:00"), []uuid.UUID{s.item})
	if created.Status != http.StatusOK {
		t.Fatal(created)
	}
	id := uuid.MustParse(created.Body["id"].(string))
	requireDetail(t, s.customerHTTP(s.tokenB, "cancel_my_appointment", map[string]any{"id": id}), http.StatusNotFound, "not found")

	other, err := staff.Bootstrap(context.Background(), s.Pool, staff.BootstrapInput{
		TenantName: "自助别租户", StoreName: "自助别店", OwnerName: "别老板", OwnerNumber: "001",
		OwnerAccount: "self-other-boss", OwnerPassword: "owner-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	status, body := s.CustomerLogin(t, other.StoreID, "fake-openid:self-other", "fake-phone:13800130011")
	if status != http.StatusOK {
		t.Fatal(status, body)
	}
	otherToken := body["token"].(string)
	requireDetail(t, s.HTTPWithToken(t, otherToken, "cancel_my_appointment", map[string]any{"id": id}), http.StatusNotFound, "not found")
	if list := s.HTTPWithToken(t, otherToken, "my_appointments", map[string]any{}); len(list.Body["entries"].([]any)) != 0 {
		t.Fatalf("other tenant appointments = %v", list.Body)
	}
	requireDetail(t, s.HTTPWithToken(t, otherToken, "my_available_slots", map[string]any{"staff_id": s.stylist, "date": s.day(1), "item_ids": []uuid.UUID{s.item}}), http.StatusNotFound, "not found")
	requireDetail(t, s.HTTPWithToken(t, otherToken, "book_appointment", selfBookArgs(s.stylist, s.at(1, "11:00"), []uuid.UUID{s.item})), http.StatusConflict, "该发型师不可预约")

	otherStore, otherStaff := uuid.New(), uuid.New()
	if _, err := s.Pool.Exec(context.Background(), "INSERT INTO stores (id,tenant_id,name) VALUES ($1,$2,'同租户别店')", otherStore, s.Res.TenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(context.Background(), "INSERT INTO staff (id,tenant_id,store_id,name,number,self_bookable) VALUES ($1,$2,$3,'别店发型师','002',true)", otherStaff, s.Res.TenantID, otherStore); err != nil {
		t.Fatal(err)
	}
	requireDetail(t, s.selfBook(s.tokenA, otherStaff, s.at(1, "11:00"), []uuid.UUID{s.item}), http.StatusConflict, "该发型师不可预约")
	requireDetail(t, s.selfSlots(s.tokenA, otherStaff, s.day(1), []uuid.UUID{s.item}), http.StatusNotFound, "not found")
	options := s.customerHTTP(s.tokenA, "my_booking_options", map[string]any{})
	for _, raw := range options.Body["staff"].([]any) {
		if raw.(map[string]any)["id"] == otherStaff.String() {
			t.Fatalf("options leaked another store's staff: %v", options.Body)
		}
	}
	otherAppointment := uuid.New()
	if _, err := s.Pool.Exec(context.Background(), `INSERT INTO appointments (id,tenant_id,store_id,staff_id,customer_id,kind,starts_at,ends_at,status,created_by)
 VALUES ($1,$2,$3,$4,$5,'appointment',$6,$7,'confirmed',$8)`, otherAppointment, s.Res.TenantID, otherStore, otherStaff, s.customerA, s.at(1, "10:00"), s.at(1, "10:30"), s.Res.OwnerID); err != nil {
		t.Fatal(err)
	}
	requireDetail(t, s.customerHTTP(s.tokenA, "cancel_my_appointment", map[string]any{"id": otherAppointment}), http.StatusNotFound, "not found")

	cancelled := s.customerHTTP(s.tokenA, "cancel_my_appointment", map[string]any{"id": id})
	if cancelled.Status != http.StatusOK || cancelled.Body["status"] != "cancelled" {
		t.Fatal(cancelled)
	}
	if again := s.selfBook(s.tokenA, s.stylist, s.at(1, "10:00"), []uuid.UUID{s.item}); again.Status != http.StatusOK {
		t.Fatalf("slot after cancellation: %v", again)
	}
}

func TestSelfBookingCustomerActor(t *testing.T) {
	s := openSelf(t)
	created := s.selfBook(s.tokenA, s.stylist, s.at(1, "10:00"), []uuid.UUID{s.item})
	if created.Status != http.StatusOK {
		t.Fatal(created)
	}
	id := uuid.MustParse(created.Body["id"].(string))
	view := call(t, s.Shop, "get_appointment", map[string]any{"id": id}, 200)
	creator := view["created_by"].(map[string]any)
	if creator["kind"] != "customer" || creator["name"] != "顾客甲" {
		t.Fatalf("created_by = %v", creator)
	}
	extra := call(t, s.Shop, "create_customer", map[string]any{"name": "事件顾客", "phone": "13800130098"}, 200)["customer"].(map[string]any)
	extraID := uuid.MustParse(extra["id"].(string))
	if _, err := s.Pool.Exec(context.Background(), `INSERT INTO appointment_events (tenant_id,store_id,appointment_id,kind,to_starts_at,to_ends_at,to_staff_id,reason,actor_customer_id)
 VALUES ($1,$2,$3,'cancel',$4,$5,$6,'顾客取消',$7)`, s.Res.TenantID, s.Res.StoreID, id, s.at(1, "10:00"), s.at(1, "10:30"), s.stylist, extraID); err != nil {
		t.Fatal(err)
	}
	events := view["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("events = %v", events)
	}
	actor := events[0].(map[string]any)["actor"].(map[string]any)
	if actor["kind"] != "customer" || actor["name"] != "顾客甲" {
		t.Fatalf("event actor = %v", actor)
	}
	view = call(t, s.Shop, "get_appointment", map[string]any{"id": id}, 200)
	sawExtra := false
	for _, raw := range view["events"].([]any) {
		event := raw.(map[string]any)
		actor := event["actor"].(map[string]any)
		if actor["id"] == extraID.String() {
			sawExtra = true
			if actor["kind"] != "customer" || actor["name"] != "事件顾客" {
				t.Fatalf("distinct customer event actor = %v", actor)
			}
		}
	}
	if !sawExtra {
		t.Fatalf("distinct customer event actor missing: %v", view["events"])
	}

	var createdBy, createdByCustomer, actorStaff, actorCustomer *uuid.UUID
	if err := s.Pool.QueryRow(context.Background(), "SELECT created_by, created_by_customer FROM appointments WHERE id=$1", id).Scan(&createdBy, &createdByCustomer); err != nil {
		t.Fatal(err)
	}
	if createdBy != nil || createdByCustomer == nil || *createdByCustomer != s.customerA {
		t.Fatalf("appointment actors: staff=%v customer=%v", createdBy, createdByCustomer)
	}
	if err := s.Pool.QueryRow(context.Background(), "SELECT actor_staff_id, actor_customer_id FROM appointment_events WHERE appointment_id=$1 AND kind='create'", id).Scan(&actorStaff, &actorCustomer); err != nil {
		t.Fatal(err)
	}
	if actorStaff != nil || actorCustomer == nil || *actorCustomer != s.customerA {
		t.Fatalf("event actors: staff=%v customer=%v", actorStaff, actorCustomer)
	}
	var logs int
	if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM audit_log WHERE action='book_appointment' AND actor_id IS NULL AND actor_customer_id=$1", s.customerA).Scan(&logs); err != nil || logs != 1 {
		t.Fatalf("customer booking audit = %d, %v", logs, err)
	}

	hidden := ops.Caller{TenantID: s.Res.TenantID, StoreID: s.Res.StoreID, StaffID: s.Res.OwnerID, Permissions: map[ops.Permission]ops.Grant{perm.BookingManage: {}}}
	hiddenView := s.HTTP(t, &hidden, "get_appointment", map[string]any{"id": id})
	if hiddenView.Status != http.StatusOK {
		t.Fatal(hiddenView)
	}
	if name := hiddenView.Body["created_by"].(map[string]any)["name"]; name != "顾客本人" {
		t.Fatalf("unviewable customer actor name = %v", name)
	}
	if customer := hiddenView.Body["customer"].(map[string]any); customer["name"] != nil {
		t.Fatalf("unviewable appointment customer = %v", customer)
	}
	if cancelled := s.customerHTTP(s.tokenA, "cancel_my_appointment", map[string]any{"id": id}); cancelled.Status != http.StatusOK {
		t.Fatal(cancelled)
	}
	var cancelLogs int
	if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM audit_log WHERE action='cancel_my_appointment' AND actor_id IS NULL AND actor_customer_id=$1", s.customerA).Scan(&cancelLogs); err != nil || cancelLogs != 1 {
		t.Fatalf("customer cancel audit = %d, %v", cancelLogs, err)
	}
	view = call(t, s.Shop, "get_appointment", map[string]any{"id": id}, 200)
	sawCancel := false
	for _, raw := range view["events"].([]any) {
		event := raw.(map[string]any)
		if event["kind"] != "cancel" {
			continue
		}
		actor := event["actor"].(map[string]any)
		if actor["id"] != s.customerA.String() {
			continue
		}
		sawCancel = true
		if actor["kind"] != "customer" || actor["name"] != "顾客甲" {
			t.Fatalf("cancel event actor = %v", actor)
		}
	}
	if !sawCancel {
		t.Fatalf("cancel event missing: %v", view["events"])
	}
	var cancelStaff, cancelCustomer *uuid.UUID
	if err := s.Pool.QueryRow(context.Background(), "SELECT actor_staff_id, actor_customer_id FROM appointment_events WHERE appointment_id=$1 AND kind='cancel' AND actor_customer_id=$2", id, s.customerA).Scan(&cancelStaff, &cancelCustomer); err != nil {
		t.Fatal(err)
	}
	if cancelStaff != nil || cancelCustomer == nil || *cancelCustomer != s.customerA {
		t.Fatalf("cancel event actors: staff=%v customer=%v", cancelStaff, cancelCustomer)
	}
}

func TestSelfBookingMergedCustomer(t *testing.T) {
	s := openSelf(t)
	created := s.selfBook(s.tokenB, s.stylist, s.at(1, "10:00"), []uuid.UUID{s.item})
	if created.Status != http.StatusOK {
		t.Fatal(created)
	}
	id := created.Body["id"].(string)
	call(t, s.Shop, "merge_customers", map[string]any{"keep_id": s.customerA, "merged_id": s.customerB}, 200)
	list := s.customerHTTP(s.tokenB, "my_appointments", map[string]any{})
	if list.Status != http.StatusOK {
		t.Fatal(list)
	}
	found := false
	for _, raw := range list.Body["entries"].([]any) {
		if raw.(map[string]any)["id"] == id {
			found = true
		}
	}
	if !found {
		t.Fatalf("merged customer appointments = %v", list.Body)
	}
	cancelled := s.customerHTTP(s.tokenB, "cancel_my_appointment", map[string]any{"id": id})
	if cancelled.Status != http.StatusOK || cancelled.Body["status"] != "cancelled" {
		t.Fatalf("merged customer cancel = %v", cancelled)
	}
}

func TestSelfBookingSlotsQueryCount(t *testing.T) {
	s := openSelf(t)
	counter := &queryCounter{}
	cfg := s.Pool.Config().Copy()
	cfg.ConnConfig.Tracer = counter
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	app := optest.New(t, pool)
	app.SetNow(s.Now())
	status, body := app.CustomerLogin(t, s.Res.StoreID, "fake-openid:customer-a", "")
	if status != http.StatusOK {
		t.Fatal(status, body)
	}
	token := body["token"].(string)
	day := s.day(10)
	args := map[string]any{"staff_id": s.stylist, "date": day, "item_ids": []uuid.UUID{s.item}}
	count := func() int64 {
		counter.all.Store(0)
		if r := app.HTTPWithToken(t, token, "my_available_slots", args); r.Status != http.StatusOK {
			t.Fatal(r)
		}
		return counter.all.Load()
	}
	before := count()
	if before == 0 {
		t.Fatal("my_available_slots baseline query count is zero")
	}
	for _, clock := range []string{"10:00", "11:00", "12:00"} {
		call(t, s.Shop, "create_appointment", map[string]any{
			"customer_id": s.customerA, "staff_id": s.stylist, "starts_at": s.at(10, clock), "ends_at": s.at(10, clock).Add(30 * time.Minute), "items": []any{map[string]any{"item_id": s.item}},
		}, 200)
	}
	after := count()
	if before != after {
		t.Fatalf("my_available_slots SQL grew with appointments: %d vs %d", before, after)
	}

	s.SetNow(s.at(0, "14:00"))
	walkinItem := s.createPositionedItem("主做长项目", 60, 1, "")
	s.openWalkin(s.customerA, s.stylist, walkinItem, 1)
	app.SetNow(s.Now())
	todayArgs := map[string]any{"staff_id": s.stylist, "date": s.day(0), "item_ids": []uuid.UUID{s.item}}
	counter.all.Store(0)
	if r := app.HTTPWithToken(t, token, "my_available_slots", todayArgs); r.Status != http.StatusOK {
		t.Fatal(r)
	}
	if today := counter.all.Load(); today == 0 {
		t.Fatal("today's my_available_slots query count is zero")
	}
}

func TestSelfBookingCancelRules(t *testing.T) {
	t.Run("already started", func(t *testing.T) {
		s := openSelf(t)
		created := s.selfBook(s.tokenA, s.stylist, s.at(1, "10:00"), []uuid.UUID{s.item})
		if created.Status != http.StatusOK {
			t.Fatal(created)
		}
		s.SetNow(s.at(1, "10:01"))
		requireDetail(t, s.customerHTTP(s.tokenA, "cancel_my_appointment", map[string]any{"id": created.Body["id"]}), http.StatusConflict, "已经开始的预约不能取消")
	})
	t.Run("missing", func(t *testing.T) {
		s := openSelf(t)
		requireDetail(t, s.customerHTTP(s.tokenA, "cancel_my_appointment", map[string]any{"id": uuid.New()}), http.StatusNotFound, "not found")
	})
	t.Run("status", func(t *testing.T) {
		s := openSelf(t)
		created := s.selfBook(s.tokenA, s.stylist, s.at(1, "11:00"), []uuid.UUID{s.item})
		if created.Status != http.StatusOK {
			t.Fatal(created)
		}
		if cancelled := s.customerHTTP(s.tokenA, "cancel_my_appointment", map[string]any{"id": created.Body["id"]}); cancelled.Status != http.StatusOK {
			t.Fatal(cancelled)
		}
		requireDetail(t, s.customerHTTP(s.tokenA, "cancel_my_appointment", map[string]any{"id": created.Body["id"]}), http.StatusConflict, "只有待确认或已确认的预约可以取消")
	})
}

func TestSelfBookingOpsCustomerOnly(t *testing.T) {
	s := optest.OpenShop(t)
	owner := s.MustCaller(s.Owner)
	want := map[string]bool{
		"my_booking_options":    true,
		"my_available_slots":    true,
		"book_appointment":      true,
		"cancel_my_appointment": true,
	}
	for _, op := range s.Registry.Operations() {
		if !want[op.Name] {
			continue
		}
		if op.Permission != ops.CustomerSelf || op.AI != ops.AINone {
			t.Fatalf("%s permission=%s ai=%v", op.Name, op.Permission, op.AI)
		}
		if err := s.Registry.Authorize(owner, op.Name); !errors.Is(err, ops.ErrForbidden) {
			t.Fatalf("staff caller on %s: %v", op.Name, err)
		}
		delete(want, op.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing customer ops: %v", want)
	}
}

func TestSelfBookingExclusionFallback(t *testing.T) {
	s := openSelf(t)
	startsAt := s.at(6, "14:00")
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	if _, err := tx.Exec(context.Background(), "SELECT pg_advisory_xact_lock(hashtextextended($1::text || '/self_booking/' || $2::text, 0))", s.Res.StoreID, s.customerA); err != nil {
		t.Fatal(err)
	}
	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTPWithToken(t, s.tokenA, "book_appointment", selfBookArgs(s.stylist, startsAt, []uuid.UUID{s.item}))
	}()
	optest.AssertWaiting(t, s.Pool, done, 1)
	call(t, s.Shop, "create_appointment", map[string]any{
		"customer_id": s.customerB, "staff_id": s.stylist, "starts_at": startsAt, "ends_at": startsAt.Add(30 * time.Minute), "items": []any{map[string]any{"item_id": s.item}},
	}, 200)
	dbtest.Rollback(t, tx)
	requireDetail(t, <-done, http.StatusConflict, "该时段已有预约或占位")
}

func TestSelfBookingCustomerLock(t *testing.T) {
	s := openSelf(t)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	if _, err := tx.Exec(context.Background(), "SELECT pg_advisory_xact_lock(hashtextextended($1::text || '/self_booking/' || $2::text, 0))", s.Res.StoreID, s.customerA); err != nil {
		t.Fatal(err)
	}
	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTPWithToken(t, s.tokenA, "book_appointment", selfBookArgs(s.stylist, s.at(7, "10:00"), []uuid.UUID{s.item}))
	}()
	optest.AssertWaiting(t, s.Pool, done, 1)
	dbtest.Rollback(t, tx)
	if r := <-done; r.Status != http.StatusOK {
		t.Fatalf("booking after customer lock: %v", r)
	}
}

func TestSelfBookingBusinessDay(t *testing.T) {
	s := openSelf(t)
	s.setSetting(settings.KeyBusinessHours, map[string]any{"open": "00:00", "close": "23:59"})
	s.setSettingFrom(settings.KeyBusinessDayEnd, "02:00", s.day(1))
	s.setSettingFrom(settings.KeyBookingMaxDaysAhead, 0, s.day(2))
	requireDetail(t, s.selfBook(s.tokenA, s.stylist, s.at(2, "00:30"), []uuid.UUID{s.item}), http.StatusConflict, "预约时间要在营业时段（00:00–23:59）内")
	slots := slotsOf(t, s.selfSlots(s.tokenA, s.stylist, s.day(1), []uuid.UUID{s.item}))
	if hasSlot(slots, "00:30") || !hasSlot(slots, "02:00") {
		t.Fatalf("business-day slots = %v", slots)
	}
}

func TestSelfBookingShiftEffectiveDay(t *testing.T) {
	s := openSelf(t)
	s.createShift(s.stylist, "10:00", "12:00", s.day(0))
	s.createShift(s.stylist, "14:00", "18:00", s.day(1))
	if r := s.selfBook(s.tokenA, s.stylist, s.at(0, "10:30"), []uuid.UUID{s.item}); r.Status != http.StatusOK {
		t.Fatalf("today shift: %v", r)
	}
	requireDetail(t, s.selfBook(s.tokenA, s.stylist, s.at(1, "10:30"), []uuid.UUID{s.item}), http.StatusConflict, "该时段发型师不上班")
	if r := s.selfBook(s.tokenA, s.stylist, s.at(1, "14:30"), []uuid.UUID{s.item}); r.Status != http.StatusOK {
		t.Fatalf("tomorrow shift: %v", r)
	}
}

func TestSelfBookingNullShiftMeansNoShift(t *testing.T) {
	s := openSelf(t)
	s.createShift(s.stylist, "14:00", "18:00", s.day(0))
	call(t, s.Shop, "set_staff_shift", map[string]any{"staff_id": s.stylist, "shift_id": nil, "effective_from": s.day(0)}, 200)
	if r := s.selfBook(s.tokenA, s.stylist, s.at(0, "10:00"), []uuid.UUID{s.item}); r.Status != http.StatusOK {
		t.Fatalf("null shift should mean no shift: %v", r)
	}
}

func TestSelfBookingLimitAfterMerge(t *testing.T) {
	s := openSelf(t)
	for i, clock := range []string{"10:00", "11:00"} {
		if r := s.selfBook(s.tokenA, s.stylist, s.at(i+1, clock), []uuid.UUID{s.item}); r.Status != http.StatusOK {
			t.Fatalf("A booking %d: %v", i, r)
		}
	}
	if r := s.selfBook(s.tokenB, s.stylist, s.at(3, "10:00"), []uuid.UUID{s.item}); r.Status != http.StatusOK {
		t.Fatalf("B booking: %v", r)
	}
	call(t, s.Shop, "merge_customers", map[string]any{"keep_id": s.customerA, "merged_id": s.customerB}, 200)
	requireDetail(t, s.selfBook(s.tokenB, s.stylist, s.at(4, "10:00"), []uuid.UUID{s.item}), http.StatusConflict, "最多同时保留 3 个待到店的自助预约")
}

func TestSelfBookingZeroDurationUsesDefault(t *testing.T) {
	s := openSelf(t)
	zero := s.createItemWith("零时长", 0, true, true)
	s.setSetting(settings.KeyBookingDefaultMinutes, 90)
	r := s.selfBook(s.tokenA, s.stylist, s.at(1, "10:00"), []uuid.UUID{zero})
	if r.Status != http.StatusOK {
		t.Fatal(r)
	}
	startsAt, err := time.Parse(time.RFC3339Nano, r.Body["starts_at"].(string))
	if err != nil {
		t.Fatal(err)
	}
	endsAt, err := time.Parse(time.RFC3339Nano, r.Body["ends_at"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if endsAt.Sub(startsAt) != 90*time.Minute {
		t.Fatalf("zero-duration booking = %v", endsAt.Sub(startsAt))
	}
}

func TestSelfBookingMalformedBusinessHours(t *testing.T) {
	s := openSelf(t)
	if _, err := s.Pool.Exec(context.Background(), `INSERT INTO setting_changes (tenant_id,store_id,key,value,is_default,old_value,old_is_default,effective_from,changed_by)
 VALUES ($1,$2,'business_hours','{"open":"bad","close":"bad"}'::jsonb,false,'{"open":"bad","close":"bad"}'::jsonb,false,$3,$4)`,
		s.Res.TenantID, s.Res.StoreID, s.at(0, "00:00"), s.Res.OwnerID); err != nil {
		t.Fatal(err)
	}
	r := s.selfSlots(s.tokenA, s.stylist, s.day(1), []uuid.UUID{s.item})
	if r.Status != http.StatusInternalServerError || r.Detail() != "internal error" {
		t.Fatalf("malformed business hours = %d %v", r.Status, r.Body)
	}
}

func TestSelfBookingMergeRace(t *testing.T) {
	t.Run("booking", func(t *testing.T) {
		s := openSelf(t)
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		if _, err := tx.Exec(context.Background(), "SELECT 1 FROM customers WHERE id=$1 FOR NO KEY UPDATE", s.customerB); err != nil {
			t.Fatal(err)
		}
		blocked := make(chan optest.Result)
		bookingDone := make(chan optest.Result, 1)
		mergeDone := make(chan optest.Result, 1)
		go func() {
			mergeDone <- s.HTTPWithToken(t, s.Owner, "merge_customers", map[string]any{"keep_id": s.customerA, "merged_id": s.customerB})
		}()
		optest.AssertWaiting(t, s.Pool, blocked, 1)
		go func() {
			bookingDone <- s.HTTPWithToken(t, s.tokenB, "book_appointment", selfBookArgs(s.stylist, s.at(1, "10:00"), []uuid.UUID{s.item}))
		}()
		optest.AssertWaiting(t, s.Pool, blocked, 2)
		dbtest.Rollback(t, tx)
		booking, merge := <-bookingDone, <-mergeDone
		if booking.Status != http.StatusOK || merge.Status != http.StatusOK {
			t.Fatalf("race booking=%v merge=%v", booking, merge)
		}
		var customerID uuid.UUID
		if err := s.Pool.QueryRow(context.Background(), "SELECT customer_id FROM appointments WHERE id=$1", booking.Body["id"]).Scan(&customerID); err != nil {
			t.Fatal(err)
		}
		if customerID != s.customerA {
			t.Fatalf("booking customer = %v, want survivor", customerID)
		}
		for i, clock := range []string{"10:00", "11:00"} {
			if r := s.selfBook(s.tokenB, s.stylist, s.at(i+2, clock), []uuid.UUID{s.item}); r.Status != http.StatusOK {
				t.Fatalf("survivor booking %d: %v", i, r)
			}
		}
		requireDetail(t, s.selfBook(s.tokenB, s.stylist, s.at(4, "10:00"), []uuid.UUID{s.item}), http.StatusConflict, "最多同时保留 3 个待到店的自助预约")
	})
	t.Run("cancel", func(t *testing.T) {
		s := openSelf(t)
		created := s.selfBook(s.tokenB, s.stylist, s.at(1, "10:00"), []uuid.UUID{s.item})
		if created.Status != http.StatusOK {
			t.Fatal(created)
		}
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		if _, err := tx.Exec(context.Background(), "SELECT 1 FROM customers WHERE id=$1 FOR NO KEY UPDATE", s.customerB); err != nil {
			t.Fatal(err)
		}
		blocked := make(chan optest.Result)
		cancelDone := make(chan optest.Result, 1)
		mergeDone := make(chan optest.Result, 1)
		go func() {
			mergeDone <- s.HTTPWithToken(t, s.Owner, "merge_customers", map[string]any{"keep_id": s.customerA, "merged_id": s.customerB})
		}()
		optest.AssertWaiting(t, s.Pool, blocked, 1)
		go func() {
			cancelDone <- s.HTTPWithToken(t, s.tokenB, "cancel_my_appointment", map[string]any{"id": created.Body["id"]})
		}()
		optest.AssertWaiting(t, s.Pool, blocked, 2)
		dbtest.Rollback(t, tx)
		cancelled, merge := <-cancelDone, <-mergeDone
		if cancelled.Status != http.StatusOK || merge.Status != http.StatusOK {
			t.Fatalf("race cancel=%v merge=%v", cancelled, merge)
		}
		var customerID uuid.UUID
		var status string
		if err := s.Pool.QueryRow(context.Background(), "SELECT customer_id,status FROM appointments WHERE id=$1", created.Body["id"]).Scan(&customerID, &status); err != nil {
			t.Fatal(err)
		}
		if customerID != s.customerA || status != "cancelled" {
			t.Fatalf("cancelled appointment = %v/%s", customerID, status)
		}
	})
}
