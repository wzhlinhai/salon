package attendance_test

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/attendance"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func reject(t *testing.T, r optest.Result, status int, detail string) {
	t.Helper()
	if r.Status != status || !strings.Contains(r.Detail(), detail) {
		t.Fatalf("got %d %s %v, want %d %s", r.Status, r.Detail(), r.Body, status, detail)
	}
}

func entries(t *testing.T, r optest.Result) []any {
	t.Helper()
	if r.Status != 200 {
		t.Fatal(r)
	}
	return r.Body["entries"].([]any)
}

func auditEntry(t *testing.T, s *optest.Shop, kind, action string, id uuid.UUID, before, after any) {
	t.Helper()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var old, newValue []byte
	err := tx.QueryRow(t.Context(), "SELECT before,after FROM audit_log WHERE target_kind=$1 AND action=$2 AND target_id=$3 ORDER BY at DESC,id DESC LIMIT 1", kind, action, id).Scan(&old, &newValue)
	dbtest.Rollback(t, tx)
	if err != nil {
		t.Fatal(err)
	}
	for i, raw := range [][]byte{old, newValue} {
		var got any
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
		}
		if !reflect.DeepEqual(got, []any{before, after}[i]) {
			t.Fatalf("audit %s %s: %v want %v", kind, action, got, []any{before, after}[i])
		}
	}
}

func clock(s *optest.Shop) time.Time {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, settings.Location)
	s.SetNow(now)
	return settings.Date(now)
}

func create(t *testing.T, s *optest.Shop, name, start, end string) map[string]any {
	t.Helper()
	return s.Call(t, s.Owner, "create_shift", map[string]any{"name": name, "start_time": start, "end_time": end}, 200).Body
}

func assign(t *testing.T, s *optest.Shop, id uuid.UUID, shift any, day time.Time) map[string]any {
	t.Helper()
	args := map[string]any{"staff_id": id, "effective_from": day.Format(settings.DateLayout)}
	if shift != nil {
		args["shift_id"] = shift
	}
	return s.Call(t, s.Owner, "set_staff_shift", args, 200).Body
}

func view(t *testing.T, s *optest.Shop, day time.Time) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, ch := range s.Channels() {
		rows := entries(t, ch.Call(t, s.MustCaller(s.Owner), "list_staff_shifts", map[string]any{"date": day.Format(settings.DateLayout)}))
		if out != nil && !reflect.DeepEqual(out[0], rows[0]) {
			t.Fatal(out, rows)
		}
		out = nil
		for _, r := range rows {
			out = append(out, r.(map[string]any))
		}
	}
	return out
}

func TestCreateShift(t *testing.T) {
	s := optest.OpenShop(t)
	clock(s)
	first := create(t, s, " 早班 ", "09:00", "18:30")
	if first["name"] != "早班" || first["start_time"] != "09:00" || first["end_time"] != "18:30" || first["active"] != true || first["created_by"].(map[string]any)["id"] != s.Res.OwnerID.String() || first["created_at"] == nil {
		t.Fatal(first)
	}
	auditEntry(t, s, "shift", "create_shift", uuid.MustParse(first["id"].(string)), nil, first)
	for _, tc := range []struct {
		name, start, end string
		status           int
		detail           string
	}{
		{"", "09:00", "18:00", 422, "validation failed"},
		{"同点", "10:00", "10:00", 409, "上班时间要早于下班时间"},
		{"倒序", "11:00", "10:00", 409, "上班时间要早于下班时间"},
		{" \t ", "09:00", "18:00", 409, "班次名称不能为空"},
		{"早班", "09:00", "18:00", 409, "班次名称已存在"},
		{strings.Repeat("名", 21), "09:00", "18:00", 422, "validation failed"},
	} {
		reject(t, s.HTTPWithToken(t, s.Owner, "create_shift", map[string]any{"name": tc.name, "start_time": tc.start, "end_time": tc.end}), tc.status, tc.detail)
	}
	for _, field := range []string{"start_time", "end_time"} {
		for _, bad := range []string{"9:00", "24:00", "12:60", "09:00:00", "garbage"} {
			args := map[string]any{"name": "格式", "start_time": "09:00", "end_time": "18:00"}
			args[field] = bad
			reject(t, s.HTTPWithToken(t, s.Owner, "create_shift", args), 422, "validation failed")
		}
	}
}

func TestDeactivateShift(t *testing.T) {
	s := optest.OpenShop(t)
	first := create(t, s, "早班", "09:00", "18:00")
	after := s.Call(t, s.Owner, "deactivate_shift", map[string]any{"id": first["id"]}, 200).Body
	if after["active"] != false {
		t.Fatal(after)
	}
	auditEntry(t, s, "shift", "deactivate_shift", uuid.MustParse(first["id"].(string)), first, after)
	reject(t, s.HTTPWithToken(t, s.Owner, "deactivate_shift", map[string]any{"id": first["id"]}), 409, "班次已停用")
	reject(t, s.HTTPWithToken(t, s.Owner, "deactivate_shift", map[string]any{"id": uuid.New()}), 404, "班次不存在")
}

func TestStaffShiftHistory(t *testing.T) {
	s := optest.OpenShop(t)
	day := clock(s)
	a := create(t, s, "早班", "09:00", "18:00")
	b := create(t, s, "晚班", "12:00", "21:00")
	first := assign(t, s, s.Res.OwnerID, a["id"], day)
	auditEntry(t, s, "staff_shift", "set_staff_shift", s.Res.OwnerID, nil, first)
	for _, d := range []time.Time{day, day.AddDate(0, 0, 10)} {
		if got := view(t, s, d)[0]["current"]; !reflect.DeepEqual(got, first) {
			t.Fatal(got, first)
		}
	}
	if got := view(t, s, day.AddDate(0, 0, -1))[0]["current"]; got != nil {
		t.Fatal(got)
	}
	second := assign(t, s, s.Res.OwnerID, b["id"], day.AddDate(0, 0, 2))
	auditEntry(t, s, "staff_shift", "set_staff_shift", s.Res.OwnerID, first, second)
	today := view(t, s, day)[0]
	if !reflect.DeepEqual(today["current"], first) || !reflect.DeepEqual(today["upcoming"], []any{second}) {
		t.Fatal(today)
	}
	third := assign(t, s, s.Res.OwnerID, a["id"], day.AddDate(0, 0, 2))
	auditEntry(t, s, "staff_shift", "set_staff_shift", s.Res.OwnerID, second, third)
	if got := view(t, s, day)[0]["upcoming"]; !reflect.DeepEqual(got, []any{third}) {
		t.Fatal(got)
	}
	// 较晚保存的早生效记录不能覆盖较晚生效日。
	assign(t, s, s.Res.OwnerID, b["id"], day)
	if got := view(t, s, day.AddDate(0, 0, 3))[0]["current"]; !reflect.DeepEqual(got, third) {
		t.Fatal(got, third)
	}
	cleared := assign(t, s, s.Res.OwnerID, nil, day.AddDate(0, 0, 4))
	if got := view(t, s, day)[0]["upcoming"]; !reflect.DeepEqual(got, []any{third, cleared}) {
		t.Fatal(got)
	}
	if cleared["shift"] != nil {
		t.Fatal(cleared)
	}
	got := view(t, s, day.AddDate(0, 0, 5))[0]["current"]
	if !reflect.DeepEqual(got, cleared) {
		t.Fatal(got, cleared)
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var n int
	err := tx.QueryRow(t.Context(), "SELECT count(*) FROM staff_shifts").Scan(&n)
	dbtest.Rollback(t, tx)
	if err != nil || n != 5 {
		t.Fatal(n, err)
	}
}

func TestStaffShiftValidation(t *testing.T) {
	s := optest.OpenShop(t)
	day := clock(s)
	shift := create(t, s, "早班", "09:00", "18:00")
	args := map[string]any{"staff_id": s.Res.OwnerID, "shift_id": shift["id"], "effective_from": day.AddDate(0, 0, -1).Format(settings.DateLayout)}
	reject(t, s.HTTPWithToken(t, s.Owner, "set_staff_shift", args), 409, "生效日期不能早于今天（2026-06-12）")
	args["effective_from"] = day.Format(settings.DateLayout)
	left, _ := s.Hire("离职", "002", "left")
	s.Call(t, s.Owner, "deactivate_staff", map[string]any{"staff_id": left}, 200)
	args["staff_id"] = left
	reject(t, s.HTTPWithToken(t, s.Owner, "set_staff_shift", args), 409, "员工必须是本店在职员工")
	args["staff_id"] = s.Res.OwnerID
	args["shift_id"] = uuid.New()
	reject(t, s.HTTPWithToken(t, s.Owner, "set_staff_shift", args), 404, "班次不存在")
	assigned := assign(t, s, s.Res.OwnerID, shift["id"], day)
	s.Call(t, s.Owner, "deactivate_shift", map[string]any{"id": shift["id"]}, 200)
	args["shift_id"] = shift["id"]
	reject(t, s.HTTPWithToken(t, s.Owner, "set_staff_shift", args), 409, "班次已停用，不能指定给员工")
	current := view(t, s, day)[0]["current"].(map[string]any)
	assigned["shift"].(map[string]any)["active"] = false
	if !reflect.DeepEqual(current, assigned) {
		t.Fatal(current, assigned)
	}
	// 次日凌晨一时仍属于前一营业日，允许前一日生效。
	s.SetNow(day.AddDate(0, 0, -2).In(settings.Location).Add(12 * time.Hour))
	s.Call(t, s.Owner, "change_setting", map[string]any{"key": "business_day_end", "value": "02:00", "effective_from": day.Format(settings.DateLayout)}, 200)
	s.SetNow(time.Date(2026, 6, 13, 1, 0, 0, 0, settings.Location))
	assign(t, s, s.Res.OwnerID, nil, day)
	args["shift_id"] = nil
	args["effective_from"] = day.AddDate(0, 0, -1).Format(settings.DateLayout)
	reject(t, s.HTTPWithToken(t, s.Owner, "set_staff_shift", args), 409, "生效日期不能早于今天（2026-06-12）")
	for _, op := range []string{"list_staff_shifts", "set_staff_shift"} {
		bad := map[string]any{"date": "2026-02-30"}
		if op == "set_staff_shift" {
			bad = map[string]any{"effective_from": "2026-02-30", "staff_id": s.Res.OwnerID}
		}
		reject(t, s.HTTPWithToken(t, s.Owner, op, bad), 422, "validation failed")
	}
}

func otherStore(t *testing.T, s *optest.Shop) (ops.Caller, uuid.UUID) {
	t.Helper()
	id := uuid.New()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "INSERT INTO stores(id,tenant_id,name) VALUES($1,$2,'外店')", id, s.Res.TenantID)
	dbtest.Commit(t, tx)
	caller := s.MustCaller(s.Owner)
	caller.StoreID = id
	r := s.HTTP(t, &caller, "create_staff", map[string]any{"name": "外店员工", "number": "000", "role_ids": []uuid.UUID{}})
	if r.Status != 200 {
		t.Fatal(r)
	}
	return caller, uuid.MustParse(r.Body["id"].(string))
}

func TestAttendanceStoreAndLists(t *testing.T) {
	s := optest.OpenShop(t)
	day := clock(s)
	own := create(t, s, "早班", "09:00", "18:00")
	create(t, s, "乙班", "10:00", "18:00")
	create(t, s, "甲班", "10:00", "19:00")
	inactive := create(t, s, "停用", "08:00", "17:00")
	s.Call(t, s.Owner, "deactivate_shift", map[string]any{"id": inactive["id"]}, 200)
	left, _ := s.Hire("离职", "000", "left")
	s.Call(t, s.Owner, "deactivate_staff", map[string]any{"staff_id": left}, 200)
	other, id := otherStore(t, s)
	foreign := s.HTTP(t, &other, "create_shift", map[string]any{"name": "外店班次", "start_time": "06:00", "end_time": "15:00"})
	if foreign.Status != 200 {
		t.Fatal(foreign)
	}
	r := s.HTTP(t, &other, "set_staff_shift", map[string]any{"staff_id": id, "shift_id": foreign.Body["id"], "effective_from": day.Format(settings.DateLayout)})
	if r.Status != 200 {
		t.Fatal(r)
	}
	reject(t, s.HTTPWithToken(t, s.Owner, "set_staff_shift", map[string]any{"staff_id": id, "effective_from": day.Format(settings.DateLayout)}), 409, "员工必须是本店在职员工")
	reject(t, s.HTTPWithToken(t, s.Owner, "set_staff_shift", map[string]any{"staff_id": s.Res.OwnerID, "shift_id": foreign.Body["id"], "effective_from": day.Format(settings.DateLayout)}), 404, "班次不存在")
	reject(t, s.HTTPWithToken(t, s.Owner, "deactivate_shift", map[string]any{"id": foreign.Body["id"]}), 404, "班次不存在")
	assign(t, s, s.Res.OwnerID, own["id"], day)
	for _, ch := range s.Channels() {
		rows := entries(t, ch.Call(t, s.MustCaller(s.Owner), "list_shifts", map[string]any{}))
		var names []string
		for _, row := range rows {
			names = append(names, row.(map[string]any)["name"].(string))
		}
		if !reflect.DeepEqual(names, []string{"早班", "乙班", "甲班", "停用"}) {
			t.Fatal(names)
		}
		rows = entries(t, ch.Call(t, s.MustCaller(s.Owner), "list_staff_shifts", map[string]any{"date": day.Format(settings.DateLayout)}))
		if len(rows) != 2 || rows[0].(map[string]any)["staff"].(map[string]any)["id"] != left.String() || rows[0].(map[string]any)["active"] != false {
			t.Fatal(rows)
		}
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	timelines, err := attendance.StaffShiftTimelines(t.Context(), dbgen.New(tx), s.Res.StoreID)
	dbtest.Rollback(t, tx)
	if err != nil || len(timelines) != 1 {
		t.Fatal(timelines, err)
	}
}

func TestAttendancePermissionsAndAI(t *testing.T) {
	s := optest.OpenShop(t)
	day := clock(s)
	shift := create(t, s, "早班", "09:00", "18:00")
	assign(t, s, s.Res.OwnerID, shift["id"], day)
	inputs := map[string]any{
		"create_shift":      map[string]any{"name": "新班", "start_time": "10:00", "end_time": "19:00"},
		"deactivate_shift":  map[string]any{"id": shift["id"]},
		"set_staff_shift":   map[string]any{"staff_id": s.Res.OwnerID, "effective_from": day.Format(settings.DateLayout)},
		"list_shifts":       map[string]any{},
		"list_staff_shifts": map[string]any{"date": day.Format(settings.DateLayout)},
	}
	caller := s.MustCaller(s.Owner)
	before := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "shifts", "staff_shifts", "audit_log")
	for _, op := range []string{"create_shift", "deactivate_shift", "set_staff_shift"} {
		r := s.AI(t, caller, op, inputs[op])
		if !errors.Is(r.Err, ops.ErrDraftRequired) {
			t.Fatal(r)
		}
	}
	if got := dbtest.Snapshot(t, s.Pool, s.Res.TenantID, "shifts", "staff_shifts", "audit_log"); got != before {
		t.Fatal("AI changed data")
	}
	_, token := s.Hire("发型师", "003", "stylist", s.Role("发型师"))
	caller = s.MustCaller(token)
	for _, ch := range s.Channels() {
		for op, args := range inputs {
			reject(t, ch.Call(t, caller, op, args), 403, "permission denied")
		}
	}
	caller.Permissions = map[ops.Permission]ops.Grant{perm.AuditLogView: {}}
	for _, ch := range s.Channels() {
		rows := entries(t, ch.Call(t, caller, "list_audit_log", map[string]any{}))
		seen := map[string]bool{}
		for _, row := range rows {
			seen[row.(map[string]any)["target_kind"].(string)] = true
		}
		if !seen["shift"] || !seen["staff_shift"] {
			t.Fatal(rows)
		}
	}
}

func TestStaffShiftSavedOrder(t *testing.T) {
	s := optest.OpenShop(t)
	day := clock(s)
	a := create(t, s, "早班", "09:00", "18:00")
	b := create(t, s, "晚班", "12:00", "21:00")
	assign(t, s, s.Res.OwnerID, a["id"], day)
	assign(t, s, s.Res.OwnerID, b["id"], day)
	if _, err := s.Pool.Exec(t.Context(), "UPDATE staff_shifts SET saved_at='2099-01-01' WHERE shift_id=$1", a["id"]); err != nil {
		t.Fatal(err)
	}
	if got := view(t, s, day)[0]["current"].(map[string]any)["shift"].(map[string]any)["id"]; got != a["id"] {
		t.Fatal(got)
	}
	if _, err := s.Pool.Exec(t.Context(), "UPDATE staff_shifts SET saved_at='2099-01-01'"); err != nil {
		t.Fatal(err)
	}
	if got := view(t, s, day)[0]["current"].(map[string]any)["shift"].(map[string]any)["id"]; got != b["id"] {
		t.Fatal(got)
	}
}

func TestAttendanceNullableSchema(t *testing.T) {
	s := optest.OpenShop(t)
	day := clock(s)
	r := s.Call(t, s.Owner, "set_staff_shift", map[string]any{"staff_id": s.Res.OwnerID, "shift_id": nil, "effective_from": day.Format(settings.DateLayout)}, 200)
	if r.Body["shift"] != nil {
		t.Fatal(r)
	}
	req := httptest.NewRequest("GET", "/openapi.json", nil)
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					AnyOf []struct{ Type string }
					Type  any
				}
			}
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	for schema, field := range map[string]string{"StaffShift": "shift", "StaffShiftEntry": "current"} {
		branches := doc.Components.Schemas[schema].Properties[field].AnyOf
		if len(branches) != 2 || branches[1].Type != "null" {
			t.Fatal(schema, branches)
		}
	}
	if got := doc.Components.Schemas["SetStaffShiftInput"].Properties["shift_id"].Type; !reflect.DeepEqual(got, []any{"string", "null"}) {
		t.Fatal(got)
	}
}

func TestStaffShiftIDOrder(t *testing.T) {
	s := optest.OpenShop(t)
	day := clock(s)
	a := create(t, s, "早班", "09:00", "18:00")
	b := create(t, s, "晚班", "12:00", "21:00")
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	// 大 ID 先插入，小 ID 后插入；同一生效日与保存时刻必须按 ID，而不是读取顺序选择。
	for i, shift := range []any{b["id"], a["id"]} {
		id := []string{"00000000-0000-7000-8000-000000000002", "00000000-0000-7000-8000-000000000001"}[i]
		dbtest.Exec(t, tx, `INSERT INTO staff_shifts(tenant_id,store_id,id,staff_id,shift_id,effective_from,saved_by,saved_at) VALUES($1,$2,$3,$4,$5,$6,$4,'2099-01-01')`, s.Res.TenantID, s.Res.StoreID, id, s.Res.OwnerID, shift, day)
	}
	dbtest.Commit(t, tx)
	for _, ch := range s.Channels() {
		for _, date := range []time.Time{day, day.AddDate(0, 0, -1)} {
			rows := entries(t, ch.Call(t, s.MustCaller(s.Owner), "list_staff_shifts", map[string]any{"date": date.Format(settings.DateLayout)}))
			entry := rows[0].(map[string]any)
			current := entry["current"]
			if date.Before(day) {
				upcoming := entry["upcoming"].([]any)
				if len(upcoming) != 1 {
					t.Fatal(upcoming)
				}
				current = upcoming[0]
			}
			if got := current.(map[string]any)["shift"].(map[string]any)["id"]; got != b["id"] {
				t.Fatal(got, b)
			}
		}
	}
}
