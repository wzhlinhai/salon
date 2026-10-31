package orders_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/orders"
	"github.com/wzhlinhai/salon/backend/internal/performance"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func (s *fixture) guestList() map[string]any {
	s.t.Helper()
	day := settings.Date(time.Now()).Format(time.DateOnly)
	return s.call("list_guest_visits", map[string]any{"date_from": day, "date_to": day, "limit": 200}, 200)
}

func TestGuestVisitCheckout(t *testing.T) {
	s := open(t)
	a := s.performanceLine(nil, nil)
	b := s.performanceLine(nil)
	b.Staff[0].StaffID = s.employee
	// First line has an empty lowest slot: the next filled slot must win.
	a.Staff = a.Staff[1:]
	a.Staff[0].StaffID = s.Res.OwnerID
	first := s.customerOrder(a, b)
	s.performanceSetting(settings.KeyGuestCountSplit, "equal")
	in := s.opening(a, b)
	in.Gender = "male"
	in.Designated = false
	equal := s.call("open_order", in, 200)
	s.performanceSetting(settings.KeyGuestCountSplit, "first_slot")
	checkout := s.call("checkout_orders", checkoutInput(checkoutOrder(first, payment("cash", "200")), checkoutOrder(equal, payment("cash", "200"))), 200)
	list := s.guestList()
	entries := list["entries"].([]any)
	if len(entries) != 3 || !reflect.DeepEqual(list["totals"], map[string]any{"total": "2.0000", "designated": "1.0000", "not_designated": "1.0000", "male": "1.0000", "female": "1.0000"}) {
		t.Fatal(list)
	}
	if len(checkout["items"].([]any)) != 2 {
		t.Fatal(checkout)
	}
	var checkoutID any
	for _, v := range entries {
		e := v.(map[string]any)
		isFirst := e["order_id"] == first["id"]
		share, gender, designated := "0.5000", "male", false
		if isFirst {
			share, gender, designated = "1.0000", "female", true
			if e["staff"].(map[string]any)["id"] != s.Res.OwnerID.String() {
				t.Fatal(e)
			}
		}
		if e["share"] != share || e["gender"] != gender || e["designated"] != designated || e["reversal"] != false || e["reverses_id"] != nil || e["settings_snapshot_id"] == nil {
			t.Fatal(e)
		}
		if checkoutID != nil && e["checkout_id"] != checkoutID {
			t.Fatal(e)
		}
		checkoutID = e["checkout_id"]
	}
}

func TestGuestVisitRemovedMergedAndStore(t *testing.T) {
	s := open(t)
	s.performanceSetting(settings.KeyGuestCountSplit, "equal")
	removed := s.performanceLine(nil)
	kept := s.performanceLine(nil)
	kept.Staff[0].StaffID = s.employee
	a := s.customerOrder(removed, kept)
	s.call("remove_order_item", orders.RemoveOrderItemInput{OrderID: id(a["id"]), OrderItemID: id(firstLine(a)["id"])}, 200)
	b := s.customerOrder(kept)
	s.call("merge_orders", mergeInput(a, b), 200)
	s.paid(a, payment("cash", "200"))
	entries := s.guestList()["entries"].([]any)
	if len(entries) != 1 || entries[0].(map[string]any)["order_id"] != a["id"] || entries[0].(map[string]any)["staff"].(map[string]any)["id"] != s.employee.String() || entries[0].(map[string]any)["share"] != "1.0000" {
		t.Fatal(entries)
	}
	empty := s.performanceLine(nil)
	empty.Staff = []orders.OrderItemStaffInput{}
	o := s.paid(s.customerOrder(empty), payment("cash", "100"))
	list := s.guestList()
	e := list["entries"].([]any)[0].(map[string]any)
	if len(list["entries"].([]any)) != 2 || e["staff"] != nil || e["share"] != "1.0000" || e["order_id"] != o["id"] || list["totals"].(map[string]any)["total"] != "2.0000" {
		t.Fatal(list)
	}
}

func TestGuestVisitVoid(t *testing.T) {
	s := open(t)
	s.performanceSetting(settings.KeyGuestCountSplit, "equal")
	o := s.paid(s.customerOrder(s.performanceLine(nil, nil)), payment("cash", "100"))
	original := s.guestList()["entries"].([]any)
	day := settings.Date(time.Now()).AddDate(0, 0, 1)
	// The business day ends after midnight, exercising BusinessDate rather than Date.
	s.call("change_setting", map[string]any{"key": settings.KeyBusinessDayEnd, "value": "02:00", "effective_from": day.Format(time.DateOnly)}, 200)
	at := time.Date(day.Year(), day.Month(), day.Day()+1, 1, 30, 0, 0, settings.Location)
	err := db.InTenant(context.Background(), s.Pool, s.Res.TenantID, func(q *dbgen.Queries) error {
		_, err := orders.VoidOrderAtForTest(context.Background(), ops.Env{Caller: s.MustCaller(s.Owner), Q: q}, new(voidInput(o)), at)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	from := day.AddDate(0, 0, -1).Format(time.DateOnly)
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "list_guest_visits", map[string]any{"date_from": from, "date_to": day.Format(time.DateOnly)})
		if r.Status != 200 || len(r.Body["entries"].([]any)) != 4 {
			t.Fatal(r)
		}
		for _, v := range r.Body["totals"].(map[string]any) {
			if v != "0.0000" {
				t.Fatal(r)
			}
		}
		for _, v := range r.Body["entries"].([]any)[:2] {
			e := v.(map[string]any)
			found := false
			for _, v := range original {
				orig := v.(map[string]any)
				if e["reverses_id"] != orig["id"] {
					continue
				}
				found = true
				for _, k := range []string{"order_id", "checkout_id", "settings_snapshot_id", "staff", "share", "designated", "gender"} {
					if !reflect.DeepEqual(e[k], orig[k]) {
						t.Fatal(k, e, orig)
					}
				}
			}
			if !found || e["reversal"] != true || e["business_date"] != day.Format(time.DateOnly) {
				t.Fatal(e)
			}
		}
		negative := ch.Call(t, s.MustCaller(s.Owner), "list_guest_visits", map[string]any{"date_from": day.Format(time.DateOnly), "date_to": day.Format(time.DateOnly)})
		if negative.Status != 200 || negative.Body["totals"].(map[string]any)["total"] != "-1.0000" {
			t.Fatal(negative)
		}
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var voidID uuid.UUID
	if err := tx.QueryRow(context.Background(), "SELECT id FROM order_voids WHERE order_id=$1", o["id"]).Scan(&voidID); err != nil {
		t.Fatal(err)
	}
	env := ops.Env{Caller: s.MustCaller(s.Owner), Q: dbgen.New(tx)}
	for range 2 {
		if err := performance.ReverseOrder(context.Background(), env, id(o["id"]), voidID, day); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM guest_visits").Scan(&n); err != nil {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	if n != 4 {
		t.Fatal(n)
	}
	before := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "guest_visits")
	r := s.HTTPWithToken(t, s.Owner, "void_order", voidInput(o))
	if r.Status != 409 || r.Detail() != "单已撤" {
		t.Fatal(r)
	}
	if before != dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "guest_visits") {
		t.Fatal("repeat void changed visits")
	}
}

func TestGuestVisitAIWritesRefused(t *testing.T) {
	s := open(t)
	line := s.checkoutLine()
	open := s.customerOrder(line)
	paid := s.paid(s.customerOrder(line), payment("cash", "100"))
	before := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "guest_visits")
	for _, tc := range []struct {
		op string
		in any
	}{{"checkout_orders", checkoutInput(checkoutOrder(open, payment("cash", "100")))}, {"void_order", voidInput(paid)}} {
		r := s.AI(t, s.MustCaller(s.Owner), tc.op, tc.in)
		if r.Status != 409 || !errors.Is(r.Err, ops.ErrDraftRequired) {
			t.Fatal(r)
		}
		if before != dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "guest_visits") {
			t.Fatal("AI wrote guest visits")
		}
	}
}

func TestGuestVisitCheckoutDimensions(t *testing.T) {
	s := open(t)
	s.performanceSetting(settings.KeyGuestCountSplit, "equal")
	third, _ := s.Hire("第三位", "099", "guest-dimensions", s.Role("发型师"))
	for _, tc := range []struct {
		designated bool
		gender     string
		staff      int
		share      string
	}{
		{true, "male", 1, "1.0000"},
		{true, "female", 2, "0.5000"},
		{false, "male", 3, "0.3334"},
		{false, "female", 2, "0.5000"},
	} {
		line := s.performanceLine(nil, nil, nil)
		line.Staff[0].StaffID = s.Res.OwnerID
		line.Staff[1].StaffID = s.employee
		line.Staff[2].StaffID = third
		line.Staff = line.Staff[:tc.staff]
		in := s.opening(line)
		in.Designated, in.Gender = tc.designated, tc.gender
		order := s.paid(s.call("open_order", in, 200), payment("cash", "100"))
		entries := s.guestList()["entries"].([]any)
		count := 0
		for _, v := range entries {
			e := v.(map[string]any)
			if e["order_id"] != order["id"] {
				continue
			}
			count++
			share := tc.share
			if tc.staff == 3 && e["staff"].(map[string]any)["id"] != s.Res.OwnerID.String() {
				share = "0.3333"
			}
			if e["share"] != share || e["designated"] != tc.designated || e["gender"] != tc.gender {
				t.Fatal(e)
			}
		}
		if count != tc.staff {
			t.Fatalf("visit count = %d, want %d", count, tc.staff)
		}
	}
	day := settings.Date(time.Now()).Format(time.DateOnly)
	for _, ch := range s.Channels() {
		t.Run(ch.Name, func(t *testing.T) {
			r := ch.Call(t, s.MustCaller(s.Owner), "list_guest_visits", map[string]any{
				"date_from": day, "date_to": day, "staff_id": s.Res.OwnerID,
			})
			totals := map[string]any{"total": "2.3334", "designated": "1.5000", "not_designated": "0.8334", "male": "1.3334", "female": "1.0000"}
			if r.Status != 200 || len(r.Body["entries"].([]any)) != 4 || !reflect.DeepEqual(r.Body["totals"], totals) {
				t.Fatal(r)
			}
		})
	}
}
