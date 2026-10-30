package orders_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/auth"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/orders"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func (s *fixture) windowSetting(n int) {
	s.t.Helper()
	s.call("change_setting", map[string]any{"key": settings.KeyVoidWindowMinutes, "value": n, "effective_from": settings.Date(time.Now()).Format(time.DateOnly)}, 200)
}
func (s *fixture) openWindow(c ops.Caller, password string, status int, detail string) map[string]any {
	s.t.Helper()
	r := s.HTTP(s.t, &c, "open_void_window", orders.OpenVoidWindowInput{Password: password})
	if r.Status != status || detail != "" && r.Body["detail"] != detail {
		s.t.Fatal(r)
	}
	return r.Body
}
func (s *fixture) failures(c ops.Caller) int {
	s.t.Helper()
	tx := dbtest.AppTx(s.t, s.Pool, c.TenantID)
	var n int
	err := tx.QueryRow(context.Background(), "SELECT failed_logins FROM staff WHERE id=$1", c.StaffID).Scan(&n)
	dbtest.Rollback(s.t, tx)
	if err != nil {
		s.t.Fatal(err)
	}
	return n
}
func (s *fixture) windowSnapshot() string {
	return dbtest.Snapshot(s.t, s.Pool, s.Res.TenantID, "void_windows", "audit_log")
}
func TestVoidWindowOffAndAccess(t *testing.T) {
	s := open(t)
	c := s.MustCaller(s.Owner)
	before := s.windowSnapshot()
	s.openWindow(c, "wrong", 409, "撤单授权窗口未开启")
	if s.failures(c) != 0 || before != s.windowSnapshot() {
		t.Fatal("disabled window wrote data")
	}
	s.windowSetting(10)
	denied := c
	denied.Permissions = map[ops.Permission]ops.Grant{}
	before = s.windowSnapshot()
	s.openWindow(denied, optest.OwnerPassword, 403, "permission denied")
	r := s.AI(t, c, "open_void_window", orders.OpenVoidWindowInput{Password: optest.OwnerPassword})
	if r.Status != 404 || !errors.Is(r.Err, ops.ErrUnknownOperation) || before != s.windowSnapshot() {
		t.Fatal(r)
	}
}
func TestVoidWindowLifecycle(t *testing.T) {
	s := open(t)
	c := s.MustCaller(s.Owner)
	line := s.checkoutLine()
	o := s.paid(s.customerOrder(line), payment("cash", "100"))
	s.windowSetting(10)
	s.voidRefused(c, voidInput(o), 409, "请先输入登录密码开启撤单授权窗口")
	// 无顾客查看或完整手机号权限也只返回本人的窗口字段。
	delete(c.Permissions, perm.CustomerView)
	delete(c.Permissions, perm.CustomerFullPhone)
	w := s.openWindow(c, optest.OwnerPassword, 200, "")
	start, err := time.Parse(time.RFC3339Nano, w["opened_at"].(string))
	if err != nil {
		t.Fatal(err)
	}
	end, err := time.Parse(time.RFC3339Nano, w["expires_at"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if end.Sub(start) != 10*time.Minute || w["staff_id"] != c.StaffID.String() || len(w) != 4 {
		t.Fatal(w)
	}
	logs := s.call("list_audit_log", map[string]any{"target_id": c.StaffID}, 200)["entries"].([]any)
	log := logs[0].(map[string]any)
	raw, _ := json.Marshal(log)
	if log["action"] != "open_void_window" || log["target_kind"] != "staff" || log["before"] != nil || !reflect.DeepEqual(log["after"], w) || strings.Contains(string(raw), "password") || strings.Contains(string(raw), optest.OwnerPassword) {
		t.Fatal(log)
	}
	at := end
	orders.RegisterVoidWindowAtForTest(s.Registry, func() time.Time { return at })
	before := s.voidSnapshot()
	r := s.HTTP(t, &c, "test_void_order", voidInput(o))
	if r.Status != 409 || r.Body["detail"] != "请先输入登录密码开启撤单授权窗口" || before != s.voidSnapshot() {
		t.Fatal(r)
	}
	s.windowSetting(1) // 缩短设置不影响已有窗口。
	at = start.Add(5 * time.Minute)
	for range 2 {
		if s.HTTP(t, &c, "test_void_order", voidInput(o)).Status != 200 {
			t.Fatal("window did not allow void")
		}
		o = s.paid(s.customerOrder(line), payment("cash", "100"))
	}
	at = end
	r = s.HTTP(t, &c, "test_open_void_window", orders.OpenVoidWindowInput{Password: optest.OwnerPassword})
	if r.Status != 200 {
		t.Fatal(r)
	}
	if r = s.HTTP(t, &c, "test_void_order", voidInput(o)); r.Status != 200 {
		t.Fatal(r)
	}

	o = s.paid(s.customerOrder(line), payment("cash", "100"))
	at = end.Add(30 * time.Second)
	r = s.HTTP(t, &c, "test_open_void_window", orders.OpenVoidWindowInput{Password: optest.OwnerPassword})
	if r.Status != 200 {
		t.Fatal(r)
	}
	at = end.Add(time.Minute) // 上一个窗口已过期，重开的较晚窗口仍有效。
	if r = s.HTTP(t, &c, "test_void_order", voidInput(o)); r.Status != 200 {
		t.Fatal(r)
	}
	s.windowSetting(0)
	s.void(s.paid(s.customerOrder(line), payment("cash", "100")))
}
func TestVoidWindowOnlyOpenerAndStore(t *testing.T) {
	s := open(t)
	a := s.MustCaller(s.Owner)
	_, token := s.Hire("另一员工", "003", "voider", s.Role("老板"))
	b := s.MustCaller(token)
	s.windowSetting(10)
	o := s.paid(s.customerOrder(s.checkoutLine()), payment("cash", "100"))
	s.openWindow(a, optest.OwnerPassword, 200, "")
	s.voidRefused(b, voidInput(o), 409, "请先输入登录密码开启撤单授权窗口")
	other := s.otherStore()
	a.StoreID = other
	r := s.HTTP(t, &a, "change_setting", map[string]any{"key": settings.KeyVoidWindowMinutes, "value": 10, "effective_from": settings.Date(time.Now()).Format(time.DateOnly)})
	if r.Status != 200 {
		t.Fatal(r)
	}
	// 在另店开窗的员工在本店仍然没有窗口。
	b.StoreID = other
	s.openWindow(b, "password-voider", 200, "")
	b.StoreID = s.Res.StoreID
	s.voidRefused(b, voidInput(o), 409, "请先输入登录密码开启撤单授权窗口")
	second := optest.OpenShop(t)
	foreign := second.MustCaller(second.Owner)
	second.Call(t, second.Owner, "change_setting", map[string]any{"key": settings.KeyVoidWindowMinutes, "value": 10, "effective_from": settings.Date(time.Now()).Format(time.DateOnly)}, 200)
	second.Call(t, second.Owner, "open_void_window", orders.OpenVoidWindowInput{Password: optest.OwnerPassword}, 200)
	tx := dbtest.AppTx(t, second.Pool, s.Res.TenantID)
	var count int
	err := tx.QueryRow(context.Background(), "SELECT count(*) FROM void_windows WHERE staff_id=$1", foreign.StaffID).Scan(&count)
	dbtest.Rollback(t, tx)
	if err != nil || count != 0 {
		t.Fatal(count, err)
	}

	tx = dbtest.AppTx(t, second.Pool, s.Res.TenantID)
	_, err = tx.Exec(context.Background(), `INSERT INTO void_windows (tenant_id,store_id,staff_id,opened_at,expires_at) VALUES($1,$2,$3,clock_timestamp(),clock_timestamp()+interval '1 minute')`, foreign.TenantID, foreign.StoreID, foreign.StaffID)
	dbtest.Rollback(t, tx)
	voidState(t, err, "42501")
	s.voidRefused(b, voidInput(o), 409, "请先输入登录密码开启撤单授权窗口")
}
func TestVoidWindowSharedLockout(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(map[bool]string{false: "window", true: "mixed"}[mixed], func(t *testing.T) {
			s := open(t)
			c := s.MustCaller(s.Owner)
			s.windowSetting(10)
			before := s.windowSnapshot()
			s.openWindow(c, "wrong", 409, "密码不正确")
			if s.failures(c) != 1 || before != s.windowSnapshot() {
				t.Fatal("failure was not committed independently")
			}
			s.openWindow(c, optest.OwnerPassword, 200, "")
			if s.failures(c) != 0 {
				t.Fatal("correct password did not reset counter")
			}
			// 新员工没有窗口，锁定期间不能开窗。
			_, token := s.Hire("撤单员工", "003", "locked", s.Role("老板"))
			c = s.MustCaller(token)
			before = s.windowSnapshot()
			for i := 0; i < 15; i++ {
				if mixed && i%2 == 0 {
					if _, err := s.Auth.Login(context.Background(), "locked", "wrong"); !errors.Is(err, auth.ErrInvalidLogin) {
						t.Fatal(err)
					}
				} else {
					s.openWindow(c, "wrong", 409, "密码不正确")
				}
			}
			if _, err := s.Auth.Login(context.Background(), "locked", "password-locked"); !errors.Is(err, auth.ErrLoginLocked) {
				t.Fatal(err)
			}
			s.openWindow(c, "password-locked", 409, "密码错误次数过多，请 15 分钟后再试")
			if before != s.windowSnapshot() {
				t.Fatal("failed attempts wrote windows or audit")
			}
		})
	}
}
func TestVoidWindowBusinessDate(t *testing.T) {
	s := open(t)
	c := s.MustCaller(s.Owner)
	day := settings.Date(time.Now()).AddDate(0, 0, 2)
	s.call("change_setting", map[string]any{"key": "business_day_end", "value": "02:00", "effective_from": day.Format(time.DateOnly)}, 200)
	s.call("change_setting", map[string]any{"key": settings.KeyVoidWindowMinutes, "value": 10, "effective_from": day.Format(time.DateOnly)}, 200)
	s.call("change_setting", map[string]any{"key": settings.KeyVoidWindowMinutes, "value": 0, "effective_from": day.AddDate(0, 0, 1).Format(time.DateOnly)}, 200)
	at := time.Date(day.Year(), day.Month(), day.Day()+1, 1, 30, 0, 0, settings.Location)
	orders.RegisterVoidWindowAtForTest(s.Registry, func() time.Time { return at })
	o := s.paid(s.customerOrder(s.checkoutLine()), payment("cash", "100"))
	r := s.HTTP(t, &c, "test_void_order", voidInput(o))
	if r.Status != 409 || r.Body["detail"] != "请先输入登录密码开启撤单授权窗口" {
		t.Fatal(r)
	}
	r = s.HTTP(t, &c, "test_open_void_window", orders.OpenVoidWindowInput{Password: optest.OwnerPassword})
	if r.Status != 200 {
		t.Fatal(r)
	}
	end, err := time.Parse(time.RFC3339Nano, r.Body["expires_at"].(string))
	if err != nil || !end.Equal(at.Add(10*time.Minute)) {
		t.Fatal(r, err)
	}
	if r = s.HTTP(t, &c, "test_void_order", voidInput(o)); r.Status != 200 {
		t.Fatal(r)
	}
}
func TestVoidWindowDatabase(t *testing.T) {
	s := open(t)
	c := s.MustCaller(s.Owner)
	s.windowSetting(10)
	w := s.openWindow(c, optest.OwnerPassword, 200, "")
	for _, overrides := range []map[string]any{{"expires_at": w["opened_at"]}, {"expires_at": "2000-01-01T00:00:00Z"}} {
		s.cloneRefused("void_windows", w["id"], overrides, "23514")
	}

	for _, col := range []string{"tenant_id", "store_id", "staff_id", "opened_at", "expires_at"} {
		state := "23502"
		if col == "tenant_id" {
			state = "42501"
		}
		s.cloneRefused("void_windows", w["id"], map[string]any{col: nil}, state)
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, err := tx.Exec(context.Background(), "INSERT INTO void_windows SELECT * FROM void_windows WHERE id=$1", w["id"])
	dbtest.Rollback(t, tx)
	voidState(t, err, "23505")
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var paired bool
	err = tx.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='void_windows'::regclass AND contype='u' AND pg_get_constraintdef(oid)='UNIQUE (tenant_id, id)') AND EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='void_windows'::regclass AND contype='p' AND pg_get_constraintdef(oid)='PRIMARY KEY (id)')`).Scan(&paired)
	dbtest.Rollback(t, tx)
	if err != nil || !paired {
		t.Fatal("missing tenant identity constraint", paired, err)
	}
	for _, col := range []string{"store_id", "staff_id"} {
		s.cloneRefused("void_windows", w["id"], map[string]any{col: uuid.NewString()}, "23503")
	}
	for _, sql := range []string{"UPDATE void_windows SET id=id", "DELETE FROM void_windows"} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(context.Background(), sql)
		dbtest.Rollback(t, tx)
		voidState(t, err, "42501")
	}
	dbtest.MigrateDownTo(t, s.Pool, 24)
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var gone bool
	err = tx.QueryRow(context.Background(), "SELECT to_regclass('void_windows') IS NULL").Scan(&gone)
	dbtest.Rollback(t, tx)
	if err != nil || !gone {
		t.Fatal(gone, err)
	}
}
