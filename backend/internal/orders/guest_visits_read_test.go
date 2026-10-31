package orders_test

import (
	"context"
	"slices"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"reflect"
	"testing"
	"time"

	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/orders"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func TestGuestVisitRead(t *testing.T) {
	s := open(t)
	mine, token := s.Hire("本人", "098", "guest-visits", s.Role("发型师"))
	s.performanceSetting(settings.KeyGuestCountSplit, "equal")
	line := s.performanceLine(nil, nil)
	line.Staff[0].StaffID = mine
	s.paid(s.customerOrder(line), payment("cash", "100"))
	empty := s.performanceLine(nil)
	empty.Staff = []orders.OrderItemStaffInput{}
	in := s.opening(empty)
	in.Gender = "male"
	in.Designated = false
	s.paid(s.call("open_order", in, 200), payment("cash", "100"))
	day := settings.Date(time.Now()).Format(time.DateOnly)
	args := map[string]any{"date_from": day, "date_to": day}
	owner, self := s.MustCaller(s.Owner), s.MustCaller(token)
	denied := owner
	denied.Permissions = map[ops.Permission]ops.Grant{}
	for _, ch := range s.Channels() {
		t.Run(ch.Name, func(t *testing.T) {
			all := ch.Call(t, owner, "list_guest_visits", args)
			totals := map[string]any{"total": "2.0000", "designated": "1.0000", "not_designated": "1.0000", "male": "1.0000", "female": "1.0000"}
			if all.Status != 200 || len(all.Body["entries"].([]any)) != 3 || !reflect.DeepEqual(all.Body["totals"], totals) {
				t.Fatal(all)
			}
			for _, v := range all.Body["entries"].([]any) {
				for _, k := range []string{"customer", "customer_id", "phone", "customer_name"} {
					if _, ok := v.(map[string]any)[k]; ok {
						t.Fatal(k, v)
					}
				}
			}
			own := ch.Call(t, self, "list_guest_visits", args)
			if own.Status != 200 || len(own.Body["entries"].([]any)) != 1 || !reflect.DeepEqual(own.Body["totals"], map[string]any{"total": "0.5000", "designated": "0.5000", "not_designated": "0.0000", "male": "0.0000", "female": "0.5000"}) {
				t.Fatal(own)
			}
			if own.Body["entries"].([]any)[0].(map[string]any)["staff"].(map[string]any)["id"] != mine.String() {
				t.Fatal(own)
			}
			filter := map[string]any{"date_from": day, "date_to": day, "staff_id": s.employee}
			r := ch.Call(t, self, "list_guest_visits", filter)
			if r.Status != 200 || len(r.Body["entries"].([]any)) != 0 || r.Body["totals"].(map[string]any)["total"] != "0.0000" {
				t.Fatal(r)
			}
			r = ch.Call(t, owner, "list_guest_visits", filter)
			if r.Status != 200 || len(r.Body["entries"].([]any)) != 1 || r.Body["totals"].(map[string]any)["total"] != "0.5000" {
				t.Fatal(r)
			}
			r = ch.Call(t, denied, "list_guest_visits", args)
			if r.Status != 403 || r.Detail() != "permission denied" {
				t.Fatal(r)
			}
			for _, tc := range []struct{ from, to, detail string }{{"2026-01-01", "2026-04-04", "查询范围最多 92 天"}, {"2026-02-01", "2026-01-01", "开始营业日不能晚于结束营业日"}} {
				r = ch.Call(t, owner, "list_guest_visits", map[string]any{"date_from": tc.from, "date_to": tc.to})
				if r.Status != 409 || r.Detail() != tc.detail {
					t.Fatal(r)
				}
			}
			r = ch.Call(t, owner, "list_guest_visits", map[string]any{"date_from": "2026-01-01", "date_to": "2026-04-03"})
			if r.Status != 200 {
				t.Fatal(r)
			}
			// Schema validation rejects invalid calendar dates on both channels.
			for _, arg := range []map[string]any{{"date_from": "2026-02-30", "date_to": day}, {"date_from": day, "date_to": "2026-02-30"}} {
				r = ch.Call(t, owner, "list_guest_visits", arg)
				detail := "validation failed"
				if ch.Name == "ai" {
					field := "date_from"
					if arg["date_to"] == "2026-02-30" {
						field = "date_to"
					}
					detail = "invalid arguments: expected string to be RFC 3339 date (" + field + ": 2026-02-30)"
				}
				if r.Status != 422 || r.Detail() != detail {
					t.Fatal(r)
				}
			}
			page := ch.Call(t, owner, "list_guest_visits", map[string]any{"date_from": day, "date_to": day, "limit": 1})
			if page.Status != 200 || len(page.Body["entries"].([]any)) != 1 || !reflect.DeepEqual(page.Body["totals"], all.Body["totals"]) {
				t.Fatal(page)
			}
			last := page.Body["entries"].([]any)[0].(map[string]any)["id"]
			next := ch.Call(t, owner, "list_guest_visits", map[string]any{"date_from": day, "date_to": day, "before_id": last})
			if next.Status != 200 || len(next.Body["entries"].([]any)) != 2 || !reflect.DeepEqual(next.Body["totals"], all.Body["totals"]) {
				t.Fatal(next)
			}
			for _, v := range next.Body["entries"].([]any) {
				if v.(map[string]any)["id"].(string) >= last.(string) {
					t.Fatal(next)
				}
			}
		})
	}
}

func TestGuestVisitStoreTenantScope(t *testing.T) {
	s := open(t)
	s.paid(s.customerOrder(s.checkoutLine()), payment("cash", "100"))
	otherStore := s.otherStore()
	// Valid same-tenant references deliberately seed an otherwise invisible store row.
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, `INSERT INTO guest_visits(tenant_id,store_id,business_date,order_id,checkout_id,settings_snapshot_id,share,designated,gender)
 SELECT tenant_id,$1,business_date,order_id,checkout_id,settings_snapshot_id,share,designated,gender FROM guest_visits LIMIT 1`, otherStore)
	dbtest.Commit(t, tx)
	foreign := optest.OpenShop(t)
	foreignCaller := foreign.MustCaller(foreign.Owner)
	foreignCaller.StoreID = s.Res.StoreID
	// A caller authenticated in another tenant cannot see this tenant's rows at all.
	for _, ch := range s.Channels() {
		day := settings.Date(time.Now()).Format(time.DateOnly)
		args := map[string]any{"date_from": day, "date_to": day}
		r := ch.Call(t, s.MustCaller(s.Owner), "list_guest_visits", args)
		if r.Status != 200 || len(r.Body["entries"].([]any)) != 1 || r.Body["totals"].(map[string]any)["total"] != "1.0000" {
			t.Fatal(r)
		}
		r = ch.Call(t, foreignCaller, "list_guest_visits", args)
		if r.Status != 200 || len(r.Body["entries"].([]any)) != 0 || r.Body["totals"].(map[string]any)["total"] != "0.0000" {
			t.Fatal(r)
		}
	}
}

func TestGuestVisitReadDateRangeAndDimensions(t *testing.T) {
	s := open(t)
	source := s.paid(s.customerOrder(s.checkoutLine()), payment("cash", "100"))
	// Seed dated shares from a valid checkout under salon_app, preserving all source references.
	from := settings.Date(time.Now()).AddDate(0, 0, -10)
	to := from.AddDate(0, 0, 2)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var wantIDs []string
	for _, tc := range []struct {
		offset     int
		share      string
		designated bool
		gender     string
	}{
		{-1, "1.0000", true, "female"},
		{0, "1.0000", true, "male"},
		{1, "0.5000", true, "female"},
		{2, "0.3333", false, "male"},
		{2, "0.5000", false, "female"},
		{3, "1.0000", false, "male"},
	} {
		var visitID uuid.UUID
		err := tx.QueryRow(context.Background(), `INSERT INTO guest_visits
   (tenant_id,store_id,business_date,order_id,checkout_id,settings_snapshot_id,staff_id,share,designated,gender)
   SELECT tenant_id,store_id,$1,order_id,checkout_id,settings_snapshot_id,staff_id,$2,$3,$4
   FROM guest_visits WHERE order_id=$5 LIMIT 1 RETURNING id`,
			from.AddDate(0, 0, tc.offset), tc.share, tc.designated, tc.gender, source["id"]).Scan(&visitID)
		if err != nil {
			t.Fatal(err)
		}
		if tc.offset >= 0 && tc.offset <= 2 {
			wantIDs = append(wantIDs, visitID.String())
		}
	}
	dbtest.Commit(t, tx)
	slices.Sort(wantIDs)
	slices.Reverse(wantIDs)
	for _, ch := range s.Channels() {
		t.Run(ch.Name, func(t *testing.T) {
			r := ch.Call(t, s.MustCaller(s.Owner), "list_guest_visits", map[string]any{
				"date_from": from.Format(time.DateOnly), "date_to": to.Format(time.DateOnly),
			})
			wantTotals := map[string]any{"total": "2.3333", "designated": "1.5000", "not_designated": "0.8333", "male": "1.3333", "female": "1.0000"}
			if r.Status != 200 || !reflect.DeepEqual(r.Body["totals"], wantTotals) {
				t.Fatal(r)
			}
			var gotIDs []string
			for _, v := range r.Body["entries"].([]any) {
				gotIDs = append(gotIDs, v.(map[string]any)["id"].(string))
			}
			if !reflect.DeepEqual(gotIDs, wantIDs) {
				t.Fatalf("entry IDs = %v, want %v", gotIDs, wantIDs)
			}
		})
	}
}
