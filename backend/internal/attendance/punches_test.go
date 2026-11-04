package attendance_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func manual(t *testing.T, s *optest.Shop, id uuid.UUID, at time.Time) map[string]any {
	t.Helper()
	return s.Call(t, s.Owner, "add_punch", map[string]any{"staff_id": id, "at": at, "reason": " 漏打 "}, 200).Body
}

func attendanceView(t *testing.T, s *optest.Shop, id uuid.UUID) map[string]any {
	t.Helper()
	var result map[string]any
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "get_staff_attendance", map[string]any{"staff_id": id, "month": "2026-06"})
		if r.Status != 200 {
			t.Fatal(r)
		}
		if result != nil && !reflect.DeepEqual(result, r.Body) {
			t.Fatal(result, r.Body)
		}
		result = r.Body
	}
	return result
}

func summary(t *testing.T, s *optest.Shop, id uuid.UUID) map[string]any {
	t.Helper()
	return attendanceView(t, s, id)["summary"].(map[string]any)
}

func TestClockIn(t *testing.T) {
	s := optest.OpenShop(t)
	day := clock(s)
	first := s.Call(t, s.Owner, "clock_in", map[string]any{}, 200).Body
	if first["source"] != "self" || first["business_date"] != "2026-06-12" || first["reason"] != "" || first["staff"].(map[string]any)["id"] != s.Res.OwnerID.String() {
		t.Fatal(first)
	}
	s.Advance(59 * time.Second)
	if r := s.Call(t, s.Owner, "clock_in", map[string]any{}, 200).Body; !reflect.DeepEqual(r, first) {
		t.Fatal(r, first)
	}
	s.Advance(time.Second)
	second := s.Call(t, s.Owner, "clock_in", map[string]any{}, 200).Body
	if second["id"] == first["id"] {
		t.Fatal(second)
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var n, a int
	err := tx.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM punches),(SELECT count(*) FROM audit_log WHERE target_kind='punch')").Scan(&n, &a)
	dbtest.Rollback(t, tx)
	if err != nil || n != 2 || a != 0 {
		t.Fatal(n, a, err)
	}
	manual(t, s, s.Res.OwnerID, time.Date(2026, 6, 12, 12, 0, 30, 0, settings.Location))
	s.Advance(30 * time.Second)
	if r := s.Call(t, s.Owner, "clock_in", map[string]any{}, 200).Body; r["id"] != second["id"] {
		t.Fatal(r, second)
	}
	s.SetNow(day.AddDate(0, 0, -1).Add(12 * time.Hour))
	s.Call(t, s.Owner, "change_setting", map[string]any{"key": "business_day_end", "value": "02:00", "effective_from": "2026-06-12"}, 200)
	s.SetNow(time.Date(2026, 6, 13, 1, 0, 0, 0, settings.Location))
	if r := s.Call(t, s.Owner, "clock_in", map[string]any{}, 200).Body; r["business_date"] != "2026-06-12" {
		t.Fatal(r)
	}
	caller := s.MustCaller(s.Owner)
	for _, tool := range s.Registry.Tools(caller) {
		if tool.Name == "clock_in" {
			t.Fatal(tool)
		}
	}
	r := s.AI(t, caller, "clock_in", map[string]any{})
	if !errors.Is(r.Err, ops.ErrUnknownOperation) {
		t.Fatal(r)
	}
}

func TestManualPunchAndApproval(t *testing.T) {
	s := optest.OpenShop(t)
	day := clock(s)
	sh := create(t, s, "班", "09:00", "18:00")
	assign(t, s, s.Res.OwnerID, sh["id"], day)
	now := time.Date(2026, 6, 13, 20, 0, 0, 0, settings.Location)
	s.SetNow(now)
	p := manual(t, s, s.Res.OwnerID, time.Date(2026, 6, 12, 9, 10, 59, 0, settings.Location))
	auditEntry(t, s, "punch", "add_punch", uuid.MustParse(p["id"].(string)), nil, p)
	if p["reason"] != "漏打" {
		t.Fatal(p)
	}
	if got := summary(t, s, s.Res.OwnerID); got["late_count"] != float64(1) || got["late_minutes"] != float64(10) || got["missing_count"] != float64(1) {
		t.Fatal(got)
	}
	args := map[string]any{"staff_id": s.Res.OwnerID, "business_date": "2026-06-12", "reason": " 外出 "}
	a := s.Call(t, s.Owner, "approve_attendance", args, 200).Body
	auditEntry(t, s, "attendance_approval", "approve_attendance", uuid.MustParse(a["id"].(string)), nil, a)
	reject(t, s.HTTPWithToken(t, s.Owner, "approve_attendance", args), 409, "这一天已经核准过")
	if got := summary(t, s, s.Res.OwnerID); got["late_count"] != float64(0) || got["missing_count"] != float64(0) || got["approved_count"] != float64(1) || got["attendance_days"] != float64(1) {
		t.Fatal(got)
	}
	revoked := s.Call(t, s.Owner, "revoke_attendance_approval", map[string]any{"id": a["id"]}, 200).Body
	if revoked["revoked_by"].(map[string]any)["id"] != s.Res.OwnerID.String() {
		t.Fatal(revoked)
	}
	revokedAt, err := time.Parse(time.RFC3339Nano, revoked["revoked_at"].(string))
	if err != nil || !revokedAt.Equal(now) {
		t.Fatal(revoked, err)
	}
	auditEntry(t, s, "attendance_approval", "revoke_attendance_approval", uuid.MustParse(a["id"].(string)), a, revoked)
	reject(t, s.HTTPWithToken(t, s.Owner, "revoke_attendance_approval", map[string]any{"id": a["id"]}), 409, "这次核准已撤销")
	if got := summary(t, s, s.Res.OwnerID); got["late_count"] != float64(1) || got["missing_count"] != float64(1) || got["approved_count"] != float64(0) {
		t.Fatal(got)
	}
	a = s.Call(t, s.Owner, "approve_attendance", args, 200).Body
	earlier := manual(t, s, s.Res.OwnerID, time.Date(2026, 6, 12, 9, 0, 0, 0, settings.Location))
	manual(t, s, s.Res.OwnerID, time.Date(2026, 6, 12, 18, 0, 0, 0, settings.Location))
	v := attendanceView(t, s, s.Res.OwnerID)
	d := v["days"].([]any)[0].(map[string]any)
	if d["late"] != false || d["missing"] != false || d["approval"] == nil || v["summary"].(map[string]any)["approved_count"] != float64(1) {
		t.Fatal(v)
	}
	reject(t, s.HTTPWithToken(t, s.Owner, "approve_attendance", args), 409, "这一天已经核准过")
	s.Call(t, s.Owner, "revoke_attendance_approval", map[string]any{"id": a["id"]}, 200)
	reject(t, s.HTTPWithToken(t, s.Owner, "approve_attendance", args), 409, "这一天没有异常，不需要核准")
	voided := s.Call(t, s.Owner, "void_punch", map[string]any{"id": earlier["id"], "reason": " 重复 "}, 200).Body
	auditEntry(t, s, "punch", "void_punch", uuid.MustParse(earlier["id"].(string)), earlier, voided)
	if voided["voided"] != true || voided["void_reason"] != "重复" {
		t.Fatal(voided)
	}
	if voided["voided_by"].(map[string]any)["id"] != s.Res.OwnerID.String() {
		t.Fatal(voided)
	}
	voidedAt, err := time.Parse(time.RFC3339Nano, voided["voided_at"].(string))
	if err != nil || !voidedAt.Equal(now) {
		t.Fatal(voided, err)
	}
	if got := summary(t, s, s.Res.OwnerID); got["late_count"] != float64(1) || got["missing_count"] != float64(0) {
		t.Fatal(got)
	}
	d = attendanceView(t, s, s.Res.OwnerID)["days"].([]any)[0].(map[string]any)
	if len(d["punches"].([]any)) != 3 {
		t.Fatal(d)
	}
	reject(t, s.HTTPWithToken(t, s.Owner, "void_punch", map[string]any{"id": earlier["id"], "reason": "重复"}), 409, "这次补卡已作废")
}

func TestPunchRejections(t *testing.T) {
	s := optest.OpenShop(t)
	clock(s)
	self := s.Call(t, s.Owner, "clock_in", map[string]any{}, 200).Body
	reject(t, s.HTTPWithToken(t, s.Owner, "void_punch", map[string]any{"id": self["id"], "reason": "错"}), 409, "员工自己打的卡不能作废")
	for _, tc := range []struct {
		op     string
		args   map[string]any
		status int
		detail string
	}{
		{"add_punch", map[string]any{"staff_id": s.Res.OwnerID, "at": "2026-06-12T13:00:00+08:00", "reason": "漏打"}, 409, "补卡时间不能晚于现在"},
		{"add_punch", map[string]any{"staff_id": s.Res.OwnerID, "at": "2026-06-12T09:00:00+08:00", "reason": "  "}, 409, "补卡要写原因"},
		{"add_punch", map[string]any{"staff_id": s.Res.OwnerID, "at": "2026-06-12T09:00:00+08:00", "reason": strings.Repeat("理", 101)}, 422, "validation failed"},
		{"void_punch", map[string]any{"id": self["id"], "reason": "  "}, 409, "作废要写原因"},
		{"approve_attendance", map[string]any{"staff_id": s.Res.OwnerID, "business_date": "2026-06-12", "reason": "  "}, 409, "核准要写原因"},
		{"approve_attendance", map[string]any{"staff_id": s.Res.OwnerID, "business_date": "2026-06-11", "reason": "出差"}, 409, "这一天没有异常，不需要核准"},
		{"void_punch", map[string]any{"id": uuid.New(), "reason": "错"}, 404, "打卡不存在"},
		{"revoke_attendance_approval", map[string]any{"id": uuid.New()}, 404, "核准不存在"},
	} {
		reject(t, s.HTTPWithToken(t, s.Owner, tc.op, tc.args), tc.status, tc.detail)
	}
	left, _ := s.Hire("离职", "002", "left")
	s.Call(t, s.Owner, "deactivate_staff", map[string]any{"staff_id": left}, 200)
	manual(t, s, left, time.Date(2026, 6, 11, 9, 0, 0, 0, settings.Location))
	if v := attendanceView(t, s, left); v["summary"].(map[string]any)["active"] != false {
		t.Fatal(v)
	}
}

func TestPunchGraceToday(t *testing.T) {
	s := optest.OpenShop(t)
	day := clock(s)
	sh := create(t, s, "班", "09:00", "18:00")
	assign(t, s, s.Res.OwnerID, sh["id"], day)
	manual(t, s, s.Res.OwnerID, time.Date(2026, 6, 12, 9, 5, 59, 0, settings.Location))
	if got := summary(t, s, s.Res.OwnerID); got["late_count"] != float64(1) {
		t.Fatal(got)
	}
	s.Call(t, s.Owner, "change_setting", map[string]any{"key": "attendance_grace_minutes", "value": 5, "effective_from": "2026-06-12"}, 200)
	if got := summary(t, s, s.Res.OwnerID); got["late_count"] != float64(0) {
		t.Fatal(got)
	}
	for _, n := range []int{-1, 61} {
		reject(t, s.HTTPWithToken(t, s.Owner, "change_setting", map[string]any{"key": "attendance_grace_minutes", "value": n, "effective_from": "2026-06-12"}), 409, "考勤宽限分钟须为 0 到 60 的整数")
	}
}

func TestPunchPermissionsAI(t *testing.T) {
	s := optest.OpenShop(t)
	clock(s)
	inputs := map[string]any{
		"clock_in":                   map[string]any{},
		"add_punch":                  map[string]any{"staff_id": s.Res.OwnerID, "at": "2026-06-11T09:00:00+08:00", "reason": "漏打"},
		"void_punch":                 map[string]any{"id": uuid.New(), "reason": "错"},
		"approve_attendance":         map[string]any{"staff_id": s.Res.OwnerID, "business_date": "2026-06-11", "reason": "外出"},
		"revoke_attendance_approval": map[string]any{"id": uuid.New()},
		"list_my_attendance":         map[string]any{"month": "2026-06"},
		"get_staff_attendance":       map[string]any{"month": "2026-06", "staff_id": s.Res.OwnerID},
		"list_attendance_summary":    map[string]any{"month": "2026-06"},
	}
	_, token := s.Hire("发型师", "002", "stylist", s.Role("发型师"))
	caller := s.MustCaller(token)
	for _, ch := range s.Channels() {
		for op, args := range inputs {
			if op == "clock_in" || op == "list_my_attendance" {
				continue
			}
			reject(t, ch.Call(t, caller, op, args), 403, "permission denied")
		}
		if r := ch.Call(t, caller, "list_my_attendance", inputs["list_my_attendance"]); r.Status != 200 || r.Body["summary"].(map[string]any)["staff"].(map[string]any)["id"] != caller.StaffID.String() {
			t.Fatal(r)
		}
	}
	role := s.Call(t, s.Owner, "create_role", map[string]any{"name": "无考勤", "permissions": []any{}}, 200).Body
	_, token = s.Hire("无权", "003", "none", uuid.MustParse(role["id"].(string)))
	denied := s.MustCaller(token)
	for _, op := range []string{"clock_in", "list_my_attendance"} {
		reject(t, s.HTTP(t, &denied, op, inputs[op]), 403, "permission denied")
	}
	before := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "punches", "attendance_approvals", "audit_log")
	for _, op := range []string{"add_punch", "void_punch", "approve_attendance", "revoke_attendance_approval"} {
		if r := s.AI(t, s.MustCaller(s.Owner), op, inputs[op]); !errors.Is(r.Err, ops.ErrDraftRequired) {
			t.Fatal(r)
		}
	}
	if after := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "punches", "attendance_approvals", "audit_log"); before != after {
		t.Fatal("AI wrote data")
	}
	caller.Permissions = map[ops.Permission]ops.Grant{perm.AttendanceClock: {}}
	for _, ch := range s.Channels() {
		if r := ch.Call(t, caller, "list_my_attendance", inputs["list_my_attendance"]); r.Status != 200 {
			t.Fatal(r)
		}
	}
}

func TestClockLatestValid(t *testing.T) {
	s := optest.OpenShop(t)
	clock(s)
	first := s.Call(t, s.Owner, "clock_in", map[string]any{}, 200).Body
	s.Advance(30 * time.Second)
	p := manual(t, s, s.Res.OwnerID, time.Date(2026, 6, 12, 12, 0, 30, 0, settings.Location))
	s.Call(t, s.Owner, "void_punch", map[string]any{"id": p["id"], "reason": "错"}, 200)
	r := s.Call(t, s.Owner, "clock_in", map[string]any{}, 200).Body
	if !reflect.DeepEqual(r, first) {
		t.Fatal(r, first)
	}
}
