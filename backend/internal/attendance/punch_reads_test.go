package attendance_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func TestPunchStoreAndReads(t *testing.T) {
	s := optest.OpenShop(t)
	clock(s)
	own := manual(t, s, s.Res.OwnerID, time.Date(2026, 6, 11, 10, 0, 0, 0, settings.Location))
	active, token := s.Hire("在职", "009", "active", s.Role("发型师"))
	left, _ := s.Hire("有历史", "000", "left")
	empty, _ := s.Hire("无历史", "002", "empty")
	for _, id := range []uuid.UUID{left, empty} {
		s.Call(t, s.Owner, "deactivate_staff", map[string]any{"staff_id": id}, 200)
	}
	p := manual(t, s, left, time.Date(2026, 6, 10, 10, 0, 0, 0, settings.Location))
	s.Call(t, s.Owner, "void_punch", map[string]any{"id": p["id"], "reason": "错"}, 200)
	other, id := otherStore(t, s)
	reject(t, s.HTTPWithToken(t, s.Owner, "add_punch", map[string]any{"staff_id": id, "at": "2026-06-11T10:00:00+08:00", "reason": "漏打"}), 404, "not found")
	reject(t, s.HTTPWithToken(t, s.Owner, "approve_attendance", map[string]any{"staff_id": id, "business_date": "2026-06-11", "reason": "外出"}), 404, "not found")
	foreign := s.HTTP(t, &other, "add_punch", map[string]any{"staff_id": id, "at": "2026-06-11T10:00:00+08:00", "reason": "漏打"})
	if foreign.Status != 200 {
		t.Fatal(foreign)
	}
	approval := s.HTTP(t, &other, "approve_attendance", map[string]any{"staff_id": id, "business_date": "2026-06-11", "reason": "外出"})
	if approval.Status != 200 {
		t.Fatal(approval)
	}
	reject(t, s.HTTPWithToken(t, s.Owner, "void_punch", map[string]any{"id": foreign.Body["id"], "reason": "错"}), 404, "打卡不存在")
	reject(t, s.HTTPWithToken(t, s.Owner, "revoke_attendance_approval", map[string]any{"id": approval.Body["id"]}), 404, "核准不存在")
	for _, ch := range s.Channels() {
		for _, bad := range []uuid.UUID{id, uuid.New()} {
			reject(t, ch.Call(t, s.MustCaller(s.Owner), "get_staff_attendance", map[string]any{"month": "2026-06", "staff_id": bad}), 404, "员工不存在")
		}
		r := ch.Call(t, s.MustCaller(token), "list_my_attendance", map[string]any{"month": "2026-06"})
		if r.Status != 200 || len(r.Body["days"].([]any)) != 0 || r.Body["summary"].(map[string]any)["staff"].(map[string]any)["id"] != active.String() {
			t.Fatal(r)
		}
		r = ch.Call(t, s.MustCaller(s.Owner), "list_my_attendance", map[string]any{"month": "2026-06"})
		if r.Status != 200 || r.Body["days"].([]any)[0].(map[string]any)["punches"].([]any)[0].(map[string]any)["id"] != own["id"] {
			t.Fatal(r)
		}
		rows := entries(t, ch.Call(t, s.MustCaller(s.Owner), "list_attendance_summary", map[string]any{"month": "2026-06"}))
		if len(rows) != 3 {
			t.Fatal(rows)
		}
		for i, want := range []uuid.UUID{left, s.Res.OwnerID, active} {
			if rows[i].(map[string]any)["staff"].(map[string]any)["id"] != want.String() {
				t.Fatal(rows)
			}
		}
		r = ch.Call(t, s.MustCaller(s.Owner), "get_staff_attendance", map[string]any{"month": "1900-01", "staff_id": left})
		if r.Status != 200 || len(r.Body["days"].([]any)) != 0 {
			t.Fatal(r)
		}
	}
}

func TestPunchMonthFilters(t *testing.T) {
	s := optest.OpenShop(t)
	clock(s)
	manual(t, s, s.Res.OwnerID, time.Date(2026, 6, 11, 10, 0, 0, 0, settings.Location))
	s.Call(t, s.Owner, "approve_attendance", map[string]any{"staff_id": s.Res.OwnerID, "business_date": "2026-06-11", "reason": "外出"}, 200)
	june := s.Call(t, s.Owner, "get_staff_attendance", map[string]any{"month": "2026-06", "staff_id": s.Res.OwnerID}, 200).Body
	if len(june["days"].([]any)) != 1 || june["summary"].(map[string]any)["approved_count"] != float64(1) {
		t.Fatal(june)
	}
	for _, ch := range s.Channels() {
		for _, tc := range []struct {
			op   string
			args map[string]any
		}{
			{"get_staff_attendance", map[string]any{"month": "2026-07", "staff_id": s.Res.OwnerID}},
			{"list_my_attendance", map[string]any{"month": "2026-07"}},
		} {
			r := ch.Call(t, s.MustCaller(s.Owner), tc.op, tc.args)
			if r.Status != 200 {
				t.Fatal(r)
			}
			summary := r.Body["summary"].(map[string]any)
			if len(r.Body["days"].([]any)) != 0 || summary["approved_count"] != float64(0) || summary["attendance_days"] != float64(0) {
				t.Fatal(r)
			}
		}
	}
}

func TestApprovalOnlyHistory(t *testing.T) {
	s := optest.OpenShop(t)
	clock(s)
	left, _ := s.Hire("仅核准", "010", "approval-only")
	s.Call(t, s.Owner, "deactivate_staff", map[string]any{"staff_id": left}, 200)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "INSERT INTO attendance_approvals (tenant_id,store_id,staff_id,business_date,reason,approved_by) VALUES ($1,$2,$3,'2026-06-11','补录',$4)", s.Res.TenantID, s.Res.StoreID, left, s.Res.OwnerID)
	dbtest.Commit(t, tx)
	rows := entries(t, s.Call(t, s.Owner, "list_attendance_summary", map[string]any{"month": "2026-06"}, 200))
	for _, entry := range rows {
		row := entry.(map[string]any)
		if row["staff"].(map[string]any)["id"] != left.String() {
			continue
		}
		if row["active"] != false || row["approved_count"] != float64(1) {
			t.Fatal(row)
		}
		return
	}
	t.Fatal(rows)
}

func TestPunchQueryCount(t *testing.T) {
	s := optest.OpenShop(t)
	day := clock(s)
	sh := create(t, s, "班", "09:00", "18:00")
	assign(t, s, s.Res.OwnerID, sh["id"], day)
	manual(t, s, s.Res.OwnerID, time.Date(2026, 6, 11, 10, 0, 0, 0, settings.Location))
	counter := &queryCounter{}
	config := s.Pool.Config().Copy()
	config.ConnConfig.Tracer = counter
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(t.Context()); err != nil {
		t.Fatal(err)
	}
	app := optest.New(t, pool)
	app.SetNow(time.Date(2026, 6, 30, 20, 0, 0, 0, settings.Location))
	caller := s.MustCaller(s.Owner)
	counts := map[string]int64{}
	for _, n := range []int{1, 4} {
		if n > 1 {
			for i := 2; i <= n; i++ {
				id, _ := s.Hire(fmt.Sprintf("员工%d", i), fmt.Sprintf("%03d", i), fmt.Sprintf("worker%d", i))
				assign(t, s, id, sh["id"], day)
				for d := 10; d <= 12; d++ {
					for _, hour := range []int{9, 10, 11} {
						manual(t, s, id, time.Date(2026, 6, d, hour, 0, 0, 0, settings.Location))
						manual(t, s, s.Res.OwnerID, time.Date(2026, 6, d, hour, 0, 0, 0, settings.Location))
					}
				}
			}
		}
		for _, ch := range app.Channels() {
			for _, op := range []string{"list_my_attendance", "get_staff_attendance", "list_attendance_summary"} {
				args := map[string]any{"month": "2026-06"}
				if op == "get_staff_attendance" {
					args["staff_id"] = s.Res.OwnerID
				}
				counter.count.Store(0)
				r := ch.Call(t, caller, op, args)
				if r.Status != 200 {
					t.Fatal(r)
				}
				count := counter.count.Load()
				key := ch.Name + op
				if n == 1 {
					counts[key] = count
				} else if count != counts[key] {
					t.Fatalf("%s queries grew: %d -> %d", key, counts[key], count)
				}
			}
		}
	}
}
