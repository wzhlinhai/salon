package booking_test

import (
	"context"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/booking"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

func parseTime(t *testing.T, v any) time.Time {
	t.Helper()
	s, ok := v.(string)
	if !ok {
		t.Fatalf("time = %v, want string", v)
	}
	parsed, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestOverlapRejectsAppointmentAndBlock(t *testing.T) {
	s := openBooking(t)
	start, end := s.at(0, "13:00"), s.at(0, "14:00")
	first := s.booked(map[string]any{"staff_id": s.staff, "starts_at": start, "ends_at": end})
	firstID := first["id"].(string)
	day := settings.Date(s.Now()).Format(settings.DateLayout)
	reject(t, s.appointment(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "13:30"), "ends_at": s.at(0, "14:30")}, 0), http.StatusConflict, "该时段已有预约或占位："+firstID)
	reject(t, s.block(s.staff, s.at(0, "13:30"), s.at(0, "14:30"), 0), http.StatusConflict, "该时段已有预约或占位："+firstID)
	if got := s.appointmentCount(day); got != 1 {
		t.Fatalf("rejected overlaps changed appointment count to %d", got)
	}
	s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "12:00"), "ends_at": start})
	s.booked(map[string]any{"staff_id": s.staff, "starts_at": end, "ends_at": s.at(0, "15:00")})
	s.booked(map[string]any{"staff_id": s.other, "starts_at": start, "ends_at": end})
	block := s.bookedBlock(s.staff, s.at(0, "15:00"), s.at(0, "16:00"))
	reject(t, s.appointment(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "15:30"), "ends_at": s.at(0, "16:30")}, 0), http.StatusConflict, "该时段已有预约或占位："+block["id"].(string))
	reject(t, s.block(s.staff, s.at(0, "15:30"), s.at(0, "16:30"), 0), http.StatusConflict, "该时段已有预约或占位："+block["id"].(string))
}

func TestFinishedAppointmentsDoNotBlock(t *testing.T) {
	s := openBooking(t)
	start, end := s.at(0, "15:00"), s.at(0, "16:00")

	cancelled := s.booked(map[string]any{"staff_id": s.staff, "starts_at": start, "ends_at": end})
	s.call("cancel_appointment", map[string]any{"id": cancelled["id"], "reason": "顾客取消"}, 200)
	s.booked(map[string]any{"staff_id": s.staff, "starts_at": start, "ends_at": end})

	block := s.bookedBlock(s.staff, s.at(0, "17:00"), s.at(0, "18:00"))
	s.call("cancel_block", map[string]any{"id": block["id"]}, 200)
	if kinds := s.eventKinds(id(block["id"])); !slices.Equal(kinds, []string{"create", "cancel"}) {
		t.Fatal(kinds)
	}
	s.bookedBlock(s.staff, s.at(0, "17:00"), s.at(0, "18:00"))

	completed := s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "18:00"), "ends_at": s.at(0, "19:00")})
	s.call("complete_appointment", map[string]any{"id": completed["id"]}, 200)
	s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "18:00"), "ends_at": s.at(0, "19:00")})

	noShow := s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "19:00"), "ends_at": s.at(0, "20:00")})
	s.SetNow(s.at(0, "19:01"))
	s.call("mark_no_show", map[string]any{"id": noShow["id"]}, 200)
	s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "19:00"), "ends_at": s.at(0, "20:00")})
}

func TestDurationFromItemsDefaultAndManualEnd(t *testing.T) {
	s := openBooking(t)
	short := s.createItem("洗吹", "30", 30, true)
	zero := s.createItem("未设时长", "20", 0, true)

	one := s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "13:00"), "items": []any{map[string]any{"item_id": s.item}}})
	if got := parseTime(t, one["ends_at"]).Sub(parseTime(t, one["starts_at"])); got != 45*time.Minute {
		t.Fatalf("one item duration = %v", got)
	}
	two := s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "14:00"), "items": []any{map[string]any{"item_id": s.item}, map[string]any{"item_id": short}}})
	if got := parseTime(t, two["ends_at"]).Sub(parseTime(t, two["starts_at"])); got != 75*time.Minute {
		t.Fatalf("two item duration = %v", got)
	}
	noItems := s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "15:15")})
	if got := parseTime(t, noItems["ends_at"]).Sub(parseTime(t, noItems["starts_at"])); got != 60*time.Minute {
		t.Fatalf("default duration = %v", got)
	}
	manual := s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "16:15"), "ends_at": s.at(0, "16:35")})
	if got := parseTime(t, manual["ends_at"]).Sub(parseTime(t, manual["starts_at"])); got != 20*time.Minute {
		t.Fatalf("manual duration = %v", got)
	}
	zeroItem := s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "17:00"), "items": []any{map[string]any{"item_id": zero}}})
	if got := parseTime(t, zeroItem["ends_at"]).Sub(parseTime(t, zeroItem["starts_at"])); got != 60*time.Minute {
		t.Fatalf("zero item duration = %v", got)
	}
	s.setSetting(settings.KeyBookingDefaultMinutes, 90)
	changedDefault := s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "18:00")})
	if got := parseTime(t, changedDefault["ends_at"]).Sub(parseTime(t, changedDefault["starts_at"])); got != 90*time.Minute {
		t.Fatalf("changed default duration = %v", got)
	}
	reject(t, s.appointment(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "20:00"), "ends_at": s.at(0, "19:00")}, 0), http.StatusConflict, "结束时间要晚于开始时间")
}

func TestCatalogueDurationAndBookable(t *testing.T) {
	s := openBooking(t)
	defaulted := s.call("create_item", map[string]any{"name": "默认可约", "price": "10", "discountable": true, "labor_fee_amount": nil, "labor_fee_percent": nil, "positions": []any{}, "level_prices": []any{}, "member_prices": []any{}}, 200)
	if defaulted["bookable"] != true || defaulted["duration_minutes"] != float64(0) {
		t.Fatal(defaulted)
	}
	item := s.call("list_items", map[string]any{}, 200)["items"].([]any)[0].(map[string]any)
	if item["duration_minutes"] != float64(45) || item["bookable"] != true {
		t.Fatal(item)
	}
	closed := s.createItem("不可约", "10", 30, false)
	blocked := s.appointment(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "13:00"), "items": []any{map[string]any{"item_id": closed}}}, 0)
	reject(t, blocked, http.StatusConflict, "该项目不可预约")
	updated := s.call("update_item", map[string]any{"id": s.item, "name": "剪发", "price": "100", "discountable": true, "active": true, "duration_minutes": 25, "bookable": false, "labor_fee_amount": nil, "labor_fee_percent": nil, "positions": []any{}, "level_prices": []any{}, "member_prices": []any{}}, 200)
	if updated["duration_minutes"] != float64(25) || updated["bookable"] != false {
		t.Fatal(updated)
	}
	reject(t, s.appointment(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "14:00"), "items": []any{map[string]any{"item_id": s.item}}}, 0), http.StatusConflict, "该项目不可预约")
}

func TestBookingScheduleRules(t *testing.T) {
	s := openBooking(t)
	cases := []struct {
		name string
		in   map[string]any
		text string
	}{
		{"before open", map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "08:30")}, "预约时间要在营业时段（09:00–22:00）内"},
		{"after close", map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "21:30"), "ends_at": s.at(0, "22:30")}, "预约时间要在营业时段（09:00–22:00）内"},
		{"crosses midnight", map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "21:00"), "ends_at": s.at(1, "01:00")}, "预约不能跨天"},
		{"too far", map[string]any{"staff_id": s.staff, "starts_at": s.at(31, "13:00")}, "最多只能预约 30 天内的时段"},
		{"yesterday", map[string]any{"staff_id": s.staff, "starts_at": s.at(-1, "13:00")}, "预约日期不能早于今天"},
		{"past beyond grace", map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "11:50")}, "不能预约已经过去的时段"},
	}
	day := settings.Date(s.Now()).Format(settings.DateLayout)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reject(t, s.appointment(tc.in, 0), http.StatusConflict, tc.text)
			if got := s.appointmentCount(day); got != 0 {
				t.Fatalf("rejected schedule changed appointment count to %d", got)
			}
		})
	}
	s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "11:56")})
	s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(30, "13:00")})
	s.setSetting(settings.KeyBusinessHours, map[string]any{"open": "10:00", "close": "21:00"})
	reject(t, s.appointment(map[string]any{"staff_id": s.staff, "starts_at": s.at(1, "09:30")}, 0), http.StatusConflict, "预约时间要在营业时段（10:00–21:00）内")
	s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(1, "20:30"), "ends_at": s.at(1, "21:00")})
}

func TestLeadTimeCheck(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, settings.Location)
	if err := booking.CheckLeadTime(0, now, now); err != nil {
		t.Fatal(err)
	}
	if err := booking.CheckLeadTime(30, now, now.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	err := booking.CheckLeadTime(30, now, now.Add(29*time.Minute))
	if err == nil || !strings.Contains(err.Error(), "预约至少要提前 30 分钟") {
		t.Fatalf("err = %v", err)
	}
}

func TestStatusTransitionsAndEvents(t *testing.T) {
	s := openBooking(t)
	created := s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "13:00"), "ends_at": s.at(0, "14:00")})
	appointmentID := id(created["id"])
	if created["status"] != "confirmed" || !strings.Contains(strings.Join(s.eventKinds(appointmentID), ","), "create") {
		t.Fatal(created, s.eventKinds(appointmentID))
	}
	reject(t, s.fail("confirm_appointment", map[string]any{"id": appointmentID}), http.StatusConflict, "预约状态不能从 confirmed 变更为 confirmed")
	reject(t, s.fail("mark_no_show", map[string]any{"id": appointmentID}), http.StatusConflict, "只有开始时间之后才能记爽约")
	s.SetNow(s.at(0, "13:00"))
	noShow := s.call("mark_no_show", map[string]any{"id": appointmentID}, 200)
	if noShow["status"] != "no_show" || !contains(s.eventKinds(appointmentID), "no_show") {
		t.Fatal(noShow, s.eventKinds(appointmentID))
	}
	reject(t, s.fail("cancel_appointment", map[string]any{"id": appointmentID, "reason": "顾客取消"}), http.StatusConflict, "预约状态不能从 no_show 变更为 cancelled")

	pending := s.insertPending(s.staff, s.customer, s.at(1, "13:00"), s.at(1, "14:00"))
	confirmed := s.call("confirm_appointment", map[string]any{"id": pending}, 200)
	if confirmed["status"] != "confirmed" || !contains(s.eventKinds(pending), "confirm") {
		t.Fatal(confirmed, s.eventKinds(pending))
	}
	completed := s.call("complete_appointment", map[string]any{"id": pending}, 200)
	if completed["status"] != "completed" || !contains(s.eventKinds(pending), "complete") {
		t.Fatal(completed, s.eventKinds(pending))
	}
	reject(t, s.fail("cancel_appointment", map[string]any{"id": pending, "reason": "顾客取消"}), http.StatusConflict, "预约状态不能从 completed 变更为 cancelled")
	reject(t, s.fail("reschedule_appointment", map[string]any{"id": pending, "starts_at": s.at(2, "13:00")}), http.StatusConflict, "只有待确认或已确认的预约可以改约")

	block := s.bookedBlock(s.staff, s.at(2, "13:00"), s.at(2, "14:00"))
	blockID := id(block["id"])
	for _, tc := range []struct {
		op   string
		in   map[string]any
		text string
	}{
		{"confirm_appointment", map[string]any{"id": blockID}, "私约占位不能确认"},
		{"complete_appointment", map[string]any{"id": blockID}, "私约占位不能完成"},
		{"mark_no_show", map[string]any{"id": blockID}, "私约占位不能记爽约"},
		{"reschedule_appointment", map[string]any{"id": blockID, "starts_at": s.at(2, "15:00")}, "私约占位不能改约"},
		{"cancel_appointment", map[string]any{"id": blockID, "reason": "顾客取消"}, "私约占位请在占位操作中取消"},
	} {
		reject(t, s.fail(tc.op, tc.in), http.StatusConflict, tc.text)
	}
	reject(t, s.fail("cancel_block", map[string]any{"id": created["id"]}), http.StatusConflict, "只有私约占位可以这样取消")
}

func TestCancelReasonsAndReschedule(t *testing.T) {
	s := openBooking(t)
	appointment := s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "13:00"), "ends_at": s.at(0, "14:00")})
	appointmentID := id(appointment["id"])
	reject(t, s.Call(t, s.Owner, "cancel_appointment", map[string]any{"id": appointmentID}, 0), http.StatusUnprocessableEntity, "validation failed")
	s.rejectedUnchanged(t, appointmentID, s.fail("cancel_appointment", map[string]any{"id": appointmentID, "reason": "其他"}), http.StatusConflict, "取消原因为其他时要填写说明")
	cancelled := s.call("cancel_appointment", map[string]any{"id": appointmentID, "reason": "门店取消", "note": "临时停水"}, 200)
	if cancelled["status"] != "cancelled" {
		t.Fatal(cancelled)
	}
	events := s.events(appointmentID)
	last := events[len(events)-1]
	if last["kind"] != "cancel" || last["reason"] != "门店取消" || last["note"] != "临时停水" {
		t.Fatal(last)
	}

	moved := s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "15:00"), "ends_at": s.at(0, "16:00")})
	movedID := id(moved["id"])
	reject(t, s.fail("reschedule_appointment", map[string]any{"id": movedID}), http.StatusConflict, "没有要修改的内容")
	rescheduled := s.call("reschedule_appointment", map[string]any{"id": movedID, "staff_id": s.other, "starts_at": s.at(0, "16:00"), "reason": "顾客晚到"}, 200)
	if rescheduled["staff"].(map[string]any)["id"] != s.other.String() || !sameDay(t, rescheduled["starts_at"], s.at(0, "16:00")) {
		t.Fatal(rescheduled)
	}
	event := s.events(movedID)[1]
	if event["kind"] != "reschedule" || event["from_staff"].(map[string]any)["id"] != s.staff.String() || event["to_staff"].(map[string]any)["id"] != s.other.String() || event["note"] != "顾客晚到" {
		t.Fatal(event)
	}
	conflict := s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "18:00"), "ends_at": s.at(0, "19:00")})
	s.rejectedUnchanged(t, movedID, s.fail("reschedule_appointment", map[string]any{"id": movedID, "staff_id": s.staff, "starts_at": s.at(0, "18:30"), "ends_at": s.at(0, "19:30")}), http.StatusConflict, "该时段已有预约或占位："+conflict["id"].(string))
}

func TestCompleteLinksSameCustomerOrder(t *testing.T) {
	s := openBooking(t)
	appointment := s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "13:00"), "ends_at": s.at(0, "14:00")})
	appointmentID := id(appointment["id"])
	order := s.call("open_order", map[string]any{"customer_id": s.customer, "gender": "female", "designated": false, "items": []any{}}, 200)
	completed := s.call("complete_appointment", map[string]any{"id": appointmentID, "order_id": order["id"]}, 200)
	if completed["order_id"] != order["id"] || completed["status"] != "completed" {
		t.Fatal(completed)
	}
	otherCustomer := id(s.call("create_customer", map[string]any{"name": "顾客乙", "phone": "13800000002"}, 200)["customer"].(map[string]any)["id"])
	otherAppointment := s.booked(map[string]any{"customer_id": otherCustomer, "staff_id": s.staff, "starts_at": s.at(0, "15:00"), "ends_at": s.at(0, "16:00")})
	reject(t, s.fail("complete_appointment", map[string]any{"id": otherAppointment["id"], "order_id": order["id"]}), http.StatusConflict, "只能关联同一顾客的单")
	reject(t, s.fail("complete_appointment", map[string]any{"id": otherAppointment["id"], "order_id": uuid.New()}), http.StatusNotFound, "not found")
}

func TestStylistSelfScope(t *testing.T) {
	s := openBooking(t)
	stylist := s.MustCaller(s.MustLogin("stylist-a", "password-stylist-a"))
	own := s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "13:00"), "ends_at": s.at(0, "14:00")})
	other := s.booked(map[string]any{"staff_id": s.other, "starts_at": s.at(0, "14:00"), "ends_at": s.at(0, "15:00")})
	day := settings.Date(s.Now()).Format(settings.DateLayout)
	list := s.HTTP(t, &stylist, "list_appointments", map[string]any{"from": day, "to": day})
	if list.Status != 200 || len(list.Body["entries"].([]any)) != 1 || list.Body["entries"].([]any)[0].(map[string]any)["id"] != own["id"] {
		t.Fatal(list)
	}
	reject(t, s.HTTP(t, &stylist, "get_appointment", map[string]any{"id": other["id"]}), http.StatusNotFound, "not found")
	reject(t, s.HTTP(t, &stylist, "create_appointment", map[string]any{"staff_id": s.other, "starts_at": s.at(0, "16:00")}), http.StatusNotFound, "not found")
	reject(t, s.HTTP(t, &stylist, "create_block", map[string]any{"staff_id": s.other, "starts_at": s.at(0, "17:00"), "ends_at": s.at(0, "18:00")}), http.StatusNotFound, "not found")
	reject(t, s.HTTP(t, &stylist, "reschedule_appointment", map[string]any{"id": other["id"], "starts_at": s.at(0, "18:00")}), http.StatusNotFound, "not found")
	if r := s.HTTP(t, &stylist, "create_appointment", map[string]any{"customer_id": s.customer, "staff_id": s.staff, "starts_at": s.at(0, "16:00")}); r.Status != 200 {
		t.Fatal(r)
	}
	if r := s.HTTP(t, &stylist, "create_block", map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "17:00"), "ends_at": s.at(0, "18:00")}); r.Status != 200 {
		t.Fatal(r)
	}
	frontID, frontToken := s.Hire("前台甲", "004", "front", s.Role("前台"))
	front := s.MustCaller(frontToken)
	if r := s.HTTP(t, &front, "create_appointment", map[string]any{"customer_id": s.customer, "staff_id": s.other, "starts_at": s.at(0, "19:00")}); r.Status != 200 {
		t.Fatal(frontID, r)
	}
}

func TestBookingPermissions(t *testing.T) {
	s := openBooking(t)
	_, assistantToken := s.Hire("助理甲", "006", "assistant", s.Role("助理"))
	assistant := s.MustCaller(assistantToken)
	day := settings.Date(s.Now()).Format(settings.DateLayout)
	id := uuid.New()
	cases := []struct {
		op   string
		args map[string]any
	}{
		{"list_appointments", map[string]any{"from": day, "to": day}},
		{"get_appointment", map[string]any{"id": id}},
		{"create_appointment", map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "13:00")}},
		{"reschedule_appointment", map[string]any{"id": id, "starts_at": s.at(0, "13:00")}},
		{"cancel_appointment", map[string]any{"id": id, "reason": "顾客取消"}},
		{"confirm_appointment", map[string]any{"id": id}},
		{"complete_appointment", map[string]any{"id": id}},
		{"mark_no_show", map[string]any{"id": id}},
		{"create_block", map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "13:00"), "ends_at": s.at(0, "14:00")}},
		{"cancel_block", map[string]any{"id": id}},
	}
	for _, ch := range s.Channels() {
		for _, tc := range cases {
			if r := ch.Call(t, assistant, tc.op, tc.args); r.Status != http.StatusForbidden || r.Detail() != "permission denied" {
				t.Fatal(ch.Name, tc.op, r)
			}
		}
	}
}

func TestAIWritesCreateDrafts(t *testing.T) {
	s := openBooking(t)
	owner := s.MustCaller(s.Owner)
	appointment := s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "13:00"), "ends_at": s.at(0, "14:00")})
	before := s.snapshot()
	cases := []struct {
		op   string
		args map[string]any
	}{
		{"create_appointment", map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "15:00")}},
		{"reschedule_appointment", map[string]any{"id": appointment["id"], "starts_at": s.at(0, "16:00")}},
		{"cancel_appointment", map[string]any{"id": appointment["id"], "reason": "顾客取消"}},
		{"confirm_appointment", map[string]any{"id": appointment["id"]}},
		{"complete_appointment", map[string]any{"id": appointment["id"]}},
		{"mark_no_show", map[string]any{"id": appointment["id"]}},
		{"create_block", map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "17:00"), "ends_at": s.at(0, "18:00")}},
		{"cancel_block", map[string]any{"id": appointment["id"]}},
	}
	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			optest.RequireDraftCreated(t, s.AI(t, owner, tc.op, tc.args), s.snapshot() == before)
		})
	}
}

func TestBookingReadsOnBothChannels(t *testing.T) {
	s := openBooking(t)
	owner := s.MustCaller(s.Owner)
	appointment := s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "13:00"), "ends_at": s.at(0, "14:00")})
	day := settings.Date(s.Now()).Format(settings.DateLayout)
	for _, ch := range s.Channels() {
		list := ch.Call(t, owner, "list_appointments", map[string]any{"from": day, "to": day})
		if list.Status != 200 || len(list.Body["entries"].([]any)) != 1 {
			t.Fatal(ch.Name, list)
		}
		got := ch.Call(t, owner, "get_appointment", map[string]any{"id": appointment["id"]})
		if got.Status != 200 || got.Body["id"] != appointment["id"] {
			t.Fatal(ch.Name, got)
		}
	}
}

func TestListAppointmentsDateRange(t *testing.T) {
	s := openBooking(t)
	owner := s.MustCaller(s.Owner)
	day := settings.Date(s.Now()).Format(settings.DateLayout)
	appointment := s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "13:00")})
	other := s.booked(map[string]any{"staff_id": s.other, "starts_at": s.at(0, "15:00")})
	filtered := s.call("list_appointments", map[string]any{"from": day, "to": day, "staff_id": s.staff}, 200)
	if len(filtered["entries"].([]any)) != 1 || filtered["entries"].([]any)[0].(map[string]any)["id"] != appointment["id"] {
		t.Fatal(filtered)
	}
	for _, ch := range s.Channels() {
		r := ch.Call(t, owner, "list_appointments", map[string]any{"from": day, "to": day, "staff_id": s.other})
		if r.Status != 200 || len(r.Body["entries"].([]any)) != 1 || r.Body["entries"].([]any)[0].(map[string]any)["id"] != other["id"] {
			t.Fatal(ch.Name, r)
		}
		r = ch.Call(t, owner, "list_appointments", map[string]any{"from": day, "to": day, "staff_id": uuid.Nil})
		if r.Status != 200 || len(r.Body["entries"].([]any)) != 0 {
			t.Fatal(ch.Name, r)
		}
	}
	reject(t, s.Call(t, s.Owner, "list_appointments", map[string]any{"from": "2026-02-01", "to": "2026-01-01"}, 0), http.StatusUnprocessableEntity, "开始营业日不能晚于结束营业日")
	reject(t, s.Call(t, s.Owner, "list_appointments", map[string]any{"from": "2026-01-01", "to": "2026-04-04"}, 0), http.StatusUnprocessableEntity, "查询范围最多 92 天")
}

// sameDay 用在 reschedule 断言里，只比较营业日。
func sameDay(t *testing.T, v any, want time.Time) bool {
	t.Helper()
	s, ok := v.(string)
	if !ok {
		return false
	}
	parsed, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return false
	}
	return settings.Date(parsed).Equal(settings.Date(want))
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestConcurrentOverlappingCreates(t *testing.T) {
	s := openBooking(t)
	owner := s.MustCaller(s.Owner)
	input := map[string]any{"customer_id": s.customer, "staff_id": s.staff, "starts_at": s.at(0, "13:00"), "ends_at": s.at(0, "14:00")}
	done := make(chan optest.Result, 2)
	for range 2 {
		go func() { done <- s.HTTP(t, &owner, "create_appointment", input) }()
	}
	results := []optest.Result{<-done, <-done}
	success, conflict := 0, 0
	for _, r := range results {
		switch r.Status {
		case http.StatusOK:
			success++
		default:
			reject(t, r, http.StatusConflict, "该时段已有预约或占位")
			conflict++
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("concurrent results = %+v, want one success and one conflict", results)
	}
	day := settings.Date(s.Now()).Format(settings.DateLayout)
	list := s.call("list_appointments", map[string]any{"from": day, "to": day}, 200)
	if len(list["entries"].([]any)) != 1 {
		t.Fatal(list)
	}
}

func TestBookingSettingsAccessors(t *testing.T) {
	s := openBooking(t)
	read := func() (int, int, int, settings.TimeRange) {
		var lead, days, duration int
		var hours settings.TimeRange
		err := db.InTenant(context.Background(), s.Pool, s.Res.TenantID, func(q *dbgen.Queries) error {
			rules, err := settings.Effective(context.Background(), q, s.Res.StoreID, settings.Date(s.Now()))
			if err != nil {
				return err
			}
			lead, days, duration, hours = rules.BookingMinLeadMinutes(), rules.BookingMaxDaysAhead(), rules.BookingDefaultMinutes(), rules.BusinessHours()
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return lead, days, duration, hours
	}
	if lead, days, duration, hours := read(); lead != 0 || days != 30 || duration != 60 || hours != (settings.TimeRange{Open: "09:00", Close: "22:00"}) {
		t.Fatal(lead, days, duration, hours)
	}
	s.setSetting(settings.KeyBookingMinLeadMinutes, 45)
	s.setSetting(settings.KeyBookingMaxDaysAhead, 7)
	s.setSetting(settings.KeyBookingDefaultMinutes, 90)
	if lead, days, duration, _ := read(); lead != 45 || days != 7 || duration != 90 {
		t.Fatal(lead, days, duration)
	}
}

func TestCustomerMergeMovesAppointments(t *testing.T) {
	s := openBooking(t)
	other := id(s.call("create_customer", map[string]any{"name": "顾客丙", "phone": "13800000003"}, 200)["customer"].(map[string]any)["id"])
	appointment := s.booked(map[string]any{"customer_id": other, "staff_id": s.other, "starts_at": s.at(0, "13:00"), "ends_at": s.at(0, "14:00")})
	s.call("merge_customers", map[string]any{"keep_id": s.customer, "merged_id": other}, 200)
	view := s.call("get_appointment", map[string]any{"id": appointment["id"]}, 200)
	if view["customer"].(map[string]any)["id"] != s.customer.String() {
		t.Fatal(view)
	}
}

func TestBookingAudit(t *testing.T) {
	s := openBooking(t)
	appointment := s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "13:00"), "ends_at": s.at(0, "14:00")})
	s.call("cancel_appointment", map[string]any{"id": appointment["id"], "reason": "顾客取消"}, 200)
	logs := s.call("list_audit_log", map[string]any{"target_id": appointment["id"]}, 200)["entries"].([]any)
	if len(logs) != 2 || logs[0].(map[string]any)["action"] != "cancel_appointment" || logs[0].(map[string]any)["target_kind"] != "appointment" || logs[1].(map[string]any)["action"] != "create_appointment" {
		t.Fatal(logs)
	}
	block := s.bookedBlock(s.staff, s.at(0, "15:00"), s.at(0, "16:00"))
	logs = s.call("list_audit_log", map[string]any{"target_id": block["id"]}, 200)["entries"].([]any)
	if len(logs) != 1 || logs[0].(map[string]any)["target_kind"] != "appointment_block" {
		t.Fatal(logs)
	}
}

func TestBlockRules(t *testing.T) {
	s := openBooking(t)
	s.block(s.staff, s.at(1, "08:00"), s.at(1, "08:30"), 200)
	s.block(s.other, s.at(1, "23:00"), s.at(2, "01:00"), 200)
	s.block(s.Res.OwnerID, s.at(60, "12:00"), s.at(60, "13:00"), 200)
	s.block(s.staff, s.Now().Add(-2*time.Hour), s.Now().Add(time.Hour), 200)
	reject(t, s.block(s.other, s.at(3, "10:00"), s.at(3, "10:00"), 0), http.StatusConflict, "结束时间要晚于开始时间")
	reject(t, s.block(s.other, s.at(4, "10:00"), s.at(5, "11:00"), 0), http.StatusConflict, "私约占位最长 24 小时")
	reject(t, s.block(s.Res.OwnerID, s.at(-1, "10:00"), s.at(-1, "11:00"), 0), http.StatusConflict, "私约占位结束时间要在当前时刻之后")
}

func TestBookingRulesUseAppointmentDate(t *testing.T) {
	s := openBooking(t)
	tomorrow := s.at(1, "00:00").Format(settings.DateLayout)
	s.call("change_setting", map[string]any{"key": settings.KeyBusinessHours, "value": map[string]any{"open": "08:00", "close": "23:00"}, "effective_from": tomorrow}, 200)
	s.call("change_setting", map[string]any{"key": settings.KeyBookingDefaultMinutes, "value": 90, "effective_from": tomorrow}, 200)
	s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(1, "08:30")})
	reject(t, s.appointment(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "08:30"), "ends_at": s.at(0, "09:30")}, 0), http.StatusConflict, "预约时间要在营业时段（09:00–22:00）内")
	booked := s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(1, "13:00")})
	if got := parseTime(t, booked["ends_at"]).Sub(parseTime(t, booked["starts_at"])); got != 90*time.Minute {
		t.Fatalf("tomorrow default duration = %v", got)
	}
}

func TestRescheduleChecksCurrentStaff(t *testing.T) {
	s := openBooking(t)
	appointment := s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "13:00"), "ends_at": s.at(0, "14:00")})
	appointmentID := id(appointment["id"])
	before := s.appointmentState(appointmentID)
	s.call("deactivate_staff", map[string]any{"staff_id": s.staff}, 200)
	reject(t, s.fail("reschedule_appointment", map[string]any{"id": appointmentID, "starts_at": s.at(0, "15:00")}), http.StatusConflict, "员工必须是本店在职员工")
	after := s.appointmentState(appointmentID)
	if after.status != before.status || !reflect.DeepEqual(after.events, before.events) {
		t.Fatal(before, after)
	}
}

func TestCustomerResolution(t *testing.T) {
	s := openBooking(t)
	existing := s.Call(s.t, s.Owner, "create_appointment", map[string]any{"customer_phone": "13800000001", "staff_id": s.staff, "starts_at": s.at(0, "13:00")}, 200).Body
	if existing["customer"].(map[string]any)["id"] != s.customer.String() {
		t.Fatal(existing)
	}
	found := s.call("search_customers", map[string]any{"query": "13800000001"}, 200)["items"].([]any)
	if len(found) != 1 {
		t.Fatal(found)
	}
	created := s.Call(s.t, s.Owner, "create_appointment", map[string]any{"customer_phone": "13800000088", "customer_name": "新顾客", "staff_id": s.other, "starts_at": s.at(0, "14:00")}, 200).Body
	newCustomer := created["customer"].(map[string]any)["id"].(string)
	items := s.call("search_customers", map[string]any{"query": "13800000088"}, 200)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != newCustomer {
		t.Fatal(items)
	}
	logs := s.call("list_audit_log", map[string]any{"target_id": newCustomer}, 200)["entries"].([]any)
	if len(logs) != 1 || logs[0].(map[string]any)["action"] != "create_customer" {
		t.Fatal(logs)
	}
	reject(t, s.Call(s.t, s.Owner, "create_appointment", map[string]any{"staff_id": s.other, "starts_at": s.at(0, "16:00")}, 0), http.StatusConflict, "预约必须选择顾客或填写手机号")

	outside := id(s.call("create_customer", map[string]any{"name": "别人员工顾客", "phone": "13800000077"}, 200)["customer"].(map[string]any)["id"])
	s.call("assign_customer_responsible", map[string]any{"customer_ids": []uuid.UUID{outside}, "staff_id": s.other}, 200)
	stylist := s.MustCaller(s.MustLogin("stylist-a", "password-stylist-a"))
	stylist.Permissions[perm.CustomerView] = ops.Grant{Scope: ops.ScopeSelf}
	reject(t, s.HTTP(t, &stylist, "create_appointment", map[string]any{"staff_id": s.staff, "customer_phone": "13800000077", "starts_at": s.at(0, "17:00")}), http.StatusConflict, "这个手机号已有档案，但不在你能查看的范围内")
}

func TestBookingReferenceRejections(t *testing.T) {
	s := openBooking(t)
	closed := s.createItem("停用项目", "10", 30, true)
	s.call("update_item", map[string]any{"id": closed, "name": "停用项目", "price": "10", "discountable": true, "active": false, "duration_minutes": 30, "bookable": true, "labor_fee_amount": nil, "labor_fee_percent": nil, "positions": []any{}, "level_prices": []any{}, "member_prices": []any{}}, 200)
	nonBookable := s.createItem("不可约项目", "10", 30, false)
	day := settings.Date(s.Now()).Format(settings.DateLayout)
	before := s.call("list_appointments", map[string]any{"from": day, "to": day}, 200)
	reject(t, s.appointment(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "13:00"), "items": []any{map[string]any{"item_id": s.item}, map[string]any{"item_id": s.item}}}, 0), http.StatusConflict, "项目不能重复")
	reject(t, s.appointment(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "13:00"), "items": []any{map[string]any{"item_id": uuid.New()}}}, 0), http.StatusConflict, "项目不存在")
	reject(t, s.appointment(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "13:00"), "items": []any{map[string]any{"item_id": closed}}}, 0), http.StatusConflict, "项目已停用")
	reject(t, s.appointment(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "13:00"), "items": []any{map[string]any{"item_id": nonBookable}}}, 0), http.StatusConflict, "该项目不可预约")
	inactive := s.hire("离职发型师", "009", "inactive", "发型师")
	s.call("deactivate_staff", map[string]any{"staff_id": inactive}, 200)
	reject(t, s.appointment(map[string]any{"staff_id": inactive, "starts_at": s.at(0, "13:00")}, 0), http.StatusConflict, "员工必须是本店在职员工")
	otherStore, err := staff.Bootstrap(context.Background(), s.Pool, staff.BootstrapInput{TenantName: "外店", StoreName: "外店", OwnerName: "外店老板", OwnerNumber: "001", OwnerAccount: "other-owner", OwnerPassword: "password-other"})
	if err != nil {
		t.Fatal(err)
	}
	reject(t, s.appointment(map[string]any{"staff_id": otherStore.OwnerID, "starts_at": s.at(0, "13:00")}, 0), http.StatusConflict, "员工必须是本店在职员工")
	after := s.call("list_appointments", map[string]any{"from": day, "to": day}, 200)
	if len(after["entries"].([]any)) != len(before["entries"].([]any)) {
		t.Fatal(before, after)
	}
}

func TestAllStatusTransitions(t *testing.T) {
	s := openBooking(t)
	allowed := func(id uuid.UUID, op string, args map[string]any, want, event string) {
		t.Helper()
		view := s.call(op, args, 200)
		if view["status"] != want || !contains(s.eventKinds(id), event) {
			t.Fatalf("%s: %v events=%v", op, view, s.eventKinds(id))
		}
	}
	pendingConfirm := s.insertPending(s.staff, s.customer, s.at(0, "13:00"), s.at(0, "14:00"))
	allowed(pendingConfirm, "confirm_appointment", map[string]any{"id": pendingConfirm}, booking.StatusConfirmed, "confirm")
	pendingCancel := s.insertPending(s.staff, s.customer, s.at(0, "14:00"), s.at(0, "15:00"))
	allowed(pendingCancel, "cancel_appointment", map[string]any{"id": pendingCancel, "reason": "顾客取消"}, booking.StatusCancelled, "cancel")
	completed := s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "15:00"), "ends_at": s.at(0, "16:00")})
	allowed(id(completed["id"]), "complete_appointment", map[string]any{"id": completed["id"]}, booking.StatusCompleted, "complete")
	cancelled := s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "16:00"), "ends_at": s.at(0, "17:00")})
	allowed(id(cancelled["id"]), "cancel_appointment", map[string]any{"id": cancelled["id"], "reason": "门店取消"}, booking.StatusCancelled, "cancel")
	noShow := s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "17:00"), "ends_at": s.at(0, "18:00")})
	s.SetNow(s.at(0, "17:01"))
	allowed(id(noShow["id"]), "mark_no_show", map[string]any{"id": noShow["id"]}, booking.StatusNoShow, "no_show")

	forbidden := func(id uuid.UUID, from, to, op string, args map[string]any) {
		t.Helper()
		s.rejectedUnchanged(t, id, s.fail(op, args), http.StatusConflict, "预约状态不能从 "+from+" 变更为 "+to)
	}
	pendingComplete := s.insertPending(s.staff, s.customer, s.at(0, "18:00"), s.at(0, "19:00"))
	forbidden(pendingComplete, booking.StatusPending, booking.StatusCompleted, "complete_appointment", map[string]any{"id": pendingComplete})
	forbidden(pendingComplete, booking.StatusPending, booking.StatusNoShow, "mark_no_show", map[string]any{"id": pendingComplete})
	for _, tc := range []struct {
		to   string
		op   string
		args map[string]any
	}{
		{booking.StatusConfirmed, "confirm_appointment", map[string]any{"id": pendingCancel}},
		{booking.StatusCancelled, "cancel_appointment", map[string]any{"id": pendingCancel, "reason": "顾客取消"}},
		{booking.StatusCompleted, "complete_appointment", map[string]any{"id": pendingCancel}},
		{booking.StatusNoShow, "mark_no_show", map[string]any{"id": pendingCancel}},
	} {
		forbidden(pendingCancel, booking.StatusCancelled, tc.to, tc.op, tc.args)
	}
	for _, tc := range []struct {
		to   string
		op   string
		args map[string]any
	}{
		{booking.StatusConfirmed, "confirm_appointment", map[string]any{"id": completed["id"]}},
		{booking.StatusCancelled, "cancel_appointment", map[string]any{"id": completed["id"], "reason": "顾客取消"}},
		{booking.StatusCompleted, "complete_appointment", map[string]any{"id": completed["id"]}},
		{booking.StatusNoShow, "mark_no_show", map[string]any{"id": completed["id"]}},
	} {
		forbidden(id(completed["id"]), booking.StatusCompleted, tc.to, tc.op, tc.args)
	}
	for _, tc := range []struct {
		to   string
		op   string
		args map[string]any
	}{
		{booking.StatusConfirmed, "confirm_appointment", map[string]any{"id": noShow["id"]}},
		{booking.StatusCancelled, "cancel_appointment", map[string]any{"id": noShow["id"], "reason": "顾客取消"}},
		{booking.StatusCompleted, "complete_appointment", map[string]any{"id": noShow["id"]}},
		{booking.StatusNoShow, "mark_no_show", map[string]any{"id": noShow["id"]}},
	} {
		forbidden(id(noShow["id"]), booking.StatusNoShow, tc.to, tc.op, tc.args)
	}
}

func TestStatusEventsHaveNoFromStaff(t *testing.T) {
	s := openBooking(t)
	pending := s.insertPending(s.staff, s.customer, s.at(0, "13:00"), s.at(0, "14:00"))
	s.call("confirm_appointment", map[string]any{"id": pending}, 200)
	confirmed := s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "14:00"), "ends_at": s.at(0, "15:00")})
	s.call("complete_appointment", map[string]any{"id": confirmed["id"]}, 200)
	cancelled := s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "15:00"), "ends_at": s.at(0, "16:00")})
	s.call("cancel_appointment", map[string]any{"id": cancelled["id"], "reason": "顾客取消"}, 200)
	noShow := s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "16:00"), "ends_at": s.at(0, "17:00")})
	s.SetNow(s.at(0, "16:01"))
	s.call("mark_no_show", map[string]any{"id": noShow["id"]}, 200)
	for _, tc := range []struct {
		id   uuid.UUID
		kind string
	}{{pending, "confirm"}, {id(confirmed["id"]), "complete"}, {id(cancelled["id"]), "cancel"}, {id(noShow["id"]), "no_show"}} {
		for _, event := range s.events(tc.id) {
			if event["kind"] == tc.kind && (event["from_staff"] != nil || event["from_starts_at"] != nil) {
				t.Fatal(event)
			}
		}
	}
	moved := s.booked(map[string]any{"staff_id": s.staff, "starts_at": s.at(0, "18:00"), "ends_at": s.at(0, "19:00")})
	s.call("reschedule_appointment", map[string]any{"id": moved["id"], "starts_at": s.at(0, "19:00")}, 200)
	events := s.events(id(moved["id"]))
	if events[len(events)-1]["from_staff"] == nil {
		t.Fatal(events)
	}
}
