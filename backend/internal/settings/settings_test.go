package settings_test

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

type shop struct {
	t     *testing.T
	pool  *pgxpool.Pool
	app   *optest.App
	res   staff.BootstrapResult
	owner ops.Caller
}

func open(t *testing.T) *shop {
	t.Helper()
	pool := dbtest.NewPool(t)
	res, err := staff.Bootstrap(context.Background(), pool, staff.BootstrapInput{
		TenantName: "本店", StoreName: "本店", OwnerName: "王老板", OwnerNumber: "001",
		OwnerAccount: "boss", OwnerPassword: "owner-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	s := &shop{t: t, pool: pool, app: optest.New(t, pool), res: res}
	s.owner = s.callerOf("boss", "owner-password")
	return s
}

// callerOf 登录并返回该员工本人的调用者，HTTP 和 AI 两条路径都用它。
func (s *shop) callerOf(account, password string) ops.Caller {
	s.t.Helper()
	code, token := s.app.Login(s.t, account, password)
	if code != http.StatusOK {
		s.t.Fatalf("login %s: %d", account, code)
	}
	c, ok := s.app.CallerFor(s.t, token)
	if !ok {
		s.t.Fatalf("authenticate %s", account)
	}
	return c
}

// hire 由老板建一名有指定角色的员工，返回其调用者。
func (s *shop) hire(name, number, account, role string) ops.Caller {
	s.t.Helper()
	r := s.app.HTTP(s.t, &s.owner, "create_staff", map[string]any{"name": name, "number": number, "role_ids": []uuid.UUID{s.res.Roles[role]}})
	if r.Status != http.StatusOK {
		s.t.Fatalf("create_staff: %d %v", r.Status, r.Body)
	}
	password := "password-" + account
	r = s.app.HTTP(s.t, &s.owner, "set_staff_login", map[string]any{"staff_id": r.Body["id"], "login_account": account, "password": password})
	if r.Status != http.StatusOK {
		s.t.Fatalf("set_staff_login: %d %v", r.Status, r.Body)
	}
	return s.callerOf(account, password)
}

// setting 从 get_settings 的结果里取出一项。
func setting(t *testing.T, r optest.Result, key string) map[string]any {
	t.Helper()
	if r.Status != http.StatusOK {
		t.Fatalf("get_settings: %d %v %v", r.Status, r.Body, r.Err)
	}
	for _, it := range r.Body["settings"].([]any) {
		if m := it.(map[string]any); m["key"] == key {
			return m
		}
	}
	t.Fatalf("setting %s missing from %v", key, r.Body)
	return nil
}

func TestUnconfiguredSettingsShowSystemDefault(t *testing.T) {
	s := open(t)
	stylist := s.hire("小王", "002", "stylist", "发型师")
	for _, ch := range s.app.Channels() {
		t.Run(ch.Name, func(t *testing.T) {
			r := ch.Call(t, stylist, "get_settings", map[string]any{})
			end := setting(t, r, "business_day_end")
			if end["value"] != "00:00" || end["default"] != "00:00" || end["is_default"] != true {
				t.Fatalf("business_day_end = %v, want default 00:00 unconfigured", end)
			}
			hours := setting(t, r, "business_hours")
			want := map[string]any{"open": "09:00", "close": "22:00"}
			if !reflect.DeepEqual(hours["value"], want) || hours["is_default"] != true {
				t.Fatalf("business_hours = %v, want default %v unconfigured", hours, want)
			}
		})
	}
}

// day 返回今天之后第 n 天的日期（n 可以为负）。
func day(n int) string {
	return settings.Date(time.Now()).AddDate(0, 0, n).Format(settings.DateLayout)
}

// change 由 caller 通过 HTTP 修改一项设置；want 不为 0 时检查状态码。
func (s *shop) change(caller ops.Caller, key string, value any, from string, want int) optest.Result {
	s.t.Helper()
	r := s.app.HTTP(s.t, &caller, "change_setting", map[string]any{"key": key, "value": value, "effective_from": from})
	if want != 0 && r.Status != want {
		s.t.Fatalf("change_setting %s=%v from %s: got %d %v, want %d", key, value, from, r.Status, r.Body, want)
	}
	return r
}

func TestChangeTakesEffectOnlyFromItsEffectiveDate(t *testing.T) {
	s := open(t)
	s.change(s.owner, "business_day_end", "02:00", day(7), http.StatusOK)

	for _, ch := range s.app.Channels() {
		t.Run(ch.Name, func(t *testing.T) {
			before := setting(t, ch.Call(t, s.owner, "get_settings", map[string]any{"date": day(6)}), "business_day_end")
			if before["value"] != "00:00" || before["is_default"] != true {
				t.Fatalf("day before effective date: %v, want default 00:00", before)
			}
			upcoming := before["upcoming"].([]any)
			if len(upcoming) != 1 || upcoming[0].(map[string]any)["effective_from"] != day(7) || upcoming[0].(map[string]any)["value"] != "02:00" {
				t.Fatalf("upcoming = %v, want 02:00 from %s", upcoming, day(7))
			}
			on := setting(t, ch.Call(t, s.owner, "get_settings", map[string]any{"date": day(7)}), "business_day_end")
			if on["value"] != "02:00" || on["is_default"] != false || on["effective_from"] != day(7) {
				t.Fatalf("on effective date: %v, want configured 02:00 from %s", on, day(7))
			}
		})
	}
}

// inTenant 在本店租户的事务里执行 fn。
func (s *shop) inTenant(fn func(ctx context.Context, q *dbgen.Queries) error) {
	s.t.Helper()
	ctx := context.Background()
	if err := db.InTenant(ctx, s.pool, s.res.TenantID, func(q *dbgen.Queries) error { return fn(ctx, q) }); err != nil {
		s.t.Fatal(err)
	}
}

// at 返回第 n 天（相对今天）门店时区的 hh:mm。
func at(n int, clock string) time.Time {
	d, _ := time.ParseInLocation(time.DateOnly+" 15:04", day(n)+" "+clock, settings.Location)
	return d
}

func TestBusinessDateFollowsDayEndEffectiveThatDay(t *testing.T) {
	s := open(t)
	s.change(s.owner, "business_day_end", "02:00", day(7), http.StatusOK)
	cases := []struct {
		t    time.Time
		want string
	}{
		// 生效前：营业日在 0 点结束，凌晨 1:30 算当天。
		{at(3, "01:30"), day(3)},
		// 生效后：营业日在次日 2 点结束，凌晨 1:30 算前一天。
		{at(8, "01:30"), day(7)},
		{at(8, "02:00"), day(8)},
		// 第 7 天凌晨按第 6 个营业日的设置判断（0 点结束），算第 7 天。
		{at(7, "01:30"), day(7)},
	}
	s.inTenant(func(ctx context.Context, q *dbgen.Queries) error {
		for _, c := range cases {
			got, err := settings.BusinessDate(ctx, q, s.res.StoreID, c.t)
			if err != nil {
				return err
			}
			if got.Format(settings.DateLayout) != c.want {
				t.Errorf("BusinessDate(%v) = %s, want %s", c.t, got.Format(settings.DateLayout), c.want)
			}
		}
		return nil
	})
}

func TestSnapshotKeepsRulesInForceWhenTaken(t *testing.T) {
	s := open(t)
	// 已安排但还没生效的修改不进入今天的快照。
	s.change(s.owner, "business_day_end", "05:00", day(30), http.StatusOK)
	var before, again uuid.UUID
	s.inTenant(func(ctx context.Context, q *dbgen.Queries) error {
		var err error
		if before, err = settings.TakeSnapshot(ctx, q, s.res.TenantID, s.res.StoreID, time.Now()); err != nil {
			return err
		}
		again, err = settings.TakeSnapshot(ctx, q, s.res.TenantID, s.res.StoreID, time.Now())
		return err
	})
	if again != before {
		t.Fatalf("identical rules gave snapshots %s and %s, want one shared snapshot", before, again)
	}

	// 下单之后，把今天起执行的设置改掉。
	s.change(s.owner, "business_hours", map[string]any{"open": "10:00", "close": "21:00"}, day(0), http.StatusOK)

	s.inTenant(func(ctx context.Context, q *dbgen.Queries) error {
		rules, err := settings.LoadSnapshot(ctx, q, s.res.StoreID, before)
		if err != nil {
			return err
		}
		if got := rules.BusinessDayEnd(); got != "00:00" {
			t.Errorf("snapshot business day end = %s, want 00:00 as when taken", got)
		}
		if got := rules.BusinessHours(); got != (settings.TimeRange{Open: "09:00", Close: "22:00"}) {
			t.Errorf("snapshot business hours = %v, want 09:00-22:00 as when taken", got)
		}
		after, err := settings.TakeSnapshot(ctx, q, s.res.TenantID, s.res.StoreID, time.Now())
		if err != nil {
			return err
		}
		if after == before {
			t.Errorf("snapshot after the change reused %s, want a new one", before)
		}
		if rules, err = settings.LoadSnapshot(ctx, q, s.res.StoreID, after); err != nil {
			return err
		}
		if got := rules.BusinessHours(); got != (settings.TimeRange{Open: "10:00", Close: "21:00"}) {
			t.Errorf("new snapshot business hours = %v, want 10:00-21:00", got)
		}
		return nil
	})
}

func TestEveryChangeIsInTheLog(t *testing.T) {
	s := open(t)
	start := time.Now()
	s.change(s.owner, "business_day_end", "02:00", day(1), http.StatusOK)
	s.change(s.owner, "business_day_end", "03:00", day(1), http.StatusOK)
	s.change(s.owner, "business_day_end", nil, day(5), http.StatusOK)
	s.change(s.owner, "business_hours", map[string]any{"open": "10:00", "close": "21:00"}, day(1), http.StatusOK)
	manager := s.hire("张店长", "002", "manager", "店长")

	for _, ch := range s.app.Channels() {
		t.Run(ch.Name, func(t *testing.T) {
			r := ch.Call(t, manager, "list_setting_changes", map[string]any{"key": "business_day_end"})
			if r.Status != http.StatusOK {
				t.Fatalf("list_setting_changes: %d %v %v", r.Status, r.Body, r.Err)
			}
			type entry struct {
				old, value          any
				oldDefault, isReset bool
				from                string
			}
			// 最新的在前。
			want := []entry{
				{"03:00", "00:00", false, true, day(5)},
				{"02:00", "03:00", false, false, day(1)},
				{"00:00", "02:00", true, false, day(1)},
			}
			changes := r.Body["changes"].([]any)
			if len(changes) != len(want) {
				t.Fatalf("got %d changes %v, want %d", len(changes), changes, len(want))
			}
			for i, w := range want {
				c := changes[i].(map[string]any)
				got := entry{c["old_value"], c["value"], c["old_is_default"] == true, c["is_default"] == true, c["effective_from"].(string)}
				if got != w {
					t.Errorf("change %d = %+v, want %+v", i, got, w)
				}
				by := c["changed_by"].(map[string]any)
				when, _ := time.Parse(time.RFC3339Nano, c["changed_at"].(string))
				if by["id"] != s.owner.StaffID.String() || by["name"] != "王老板" || when.Before(start.Add(-time.Second)) || when.After(time.Now()) {
					t.Errorf("change %d by %v at %v, want 王老板 just now", i, by, c["changed_at"])
				}
			}
			pending := setting(t, ch.Call(t, s.owner, "get_settings", map[string]any{"date": day(1)}), "business_day_end")["upcoming"].([]any)
			if len(pending) != 1 || pending[0].(map[string]any)["is_default"] != true || pending[0].(map[string]any)["value"] != "00:00" {
				t.Fatalf("upcoming on day 1 = %v, want reset to default from day 5", pending)
			}
			reset := setting(t, ch.Call(t, s.owner, "get_settings", map[string]any{"date": day(5)}), "business_day_end")
			if reset["value"] != "00:00" || reset["is_default"] != true || reset["effective_from"] != nil {
				t.Fatalf("after reset: %v, want system default", reset)
			}
			all := ch.Call(t, manager, "list_setting_changes", map[string]any{})
			if n := len(all.Body["changes"].([]any)); n != 4 {
				t.Fatalf("unfiltered log has %d changes, want 4", n)
			}
		})
	}
}

// changeCount 返回修改记录的条数。
func (s *shop) changeCount() int {
	s.t.Helper()
	r := s.app.HTTP(s.t, &s.owner, "list_setting_changes", map[string]any{})
	if r.Status != http.StatusOK {
		s.t.Fatalf("list_setting_changes: %d %v", r.Status, r.Body)
	}
	return len(r.Body["changes"].([]any))
}

func TestInvalidChangesAreRejected(t *testing.T) {
	s := open(t)
	cases := []struct {
		name  string
		key   string
		value any
		from  string
	}{
		{"effective before today", "business_hours", map[string]any{"open": "10:00", "close": "21:00"}, day(-1)},
		// 当天改营业日结束时间，会改变今天已经发生的业务属于哪一天。
		{"day end effective today", "business_day_end", "02:00", day(0)},
		{"unknown setting", "no_such_setting", "02:00", day(1)},
		{"not a time", "business_day_end", "0:30", day(1)},
		{"day end at noon", "business_day_end", "12:00", day(1)},
		{"hours with wrong shape", "business_hours", "09:00-22:00", day(1)},
		{"hours with extra field", "business_hours", map[string]any{"open": "09:00", "close": "22:00", "lunch": "12:00"}, day(1)},
		{"open not before close", "business_hours", map[string]any{"open": "22:00", "close": "09:00"}, day(1)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s.t = t
			s.change(s.owner, c.key, c.value, c.from, http.StatusConflict)
		})
	}
	s.t = t
	if n := s.changeCount(); n != 0 {
		t.Fatalf("%d changes recorded after rejected calls, want 0", n)
	}
	// 其他设置可以今天生效，营业日结束时间可以明天生效。
	s.change(s.owner, "business_hours", map[string]any{"open": "10:00", "close": "21:00"}, day(0), http.StatusOK)
	s.change(s.owner, "business_day_end", "02:00", day(1), http.StatusOK)
}

func TestPermissionDeniedOnBothChannels(t *testing.T) {
	s := open(t)
	manager := s.hire("张店长", "002", "manager", "店长") // 有操作日志，没有门店设置
	stylist := s.hire("小王", "003", "stylist", "发型师") // 两项都没有
	change := map[string]any{"key": "business_day_end", "value": "02:00", "effective_from": day(1)}
	cases := []struct {
		name   string
		caller ops.Caller
		op     string
		args   any
	}{
		{"change without settings.manage", manager, "change_setting", change},
		{"log without audit_log.view", stylist, "list_setting_changes", map[string]any{}},
	}
	for _, ch := range s.app.Channels() {
		for _, c := range cases {
			t.Run(ch.Name+"/"+c.name, func(t *testing.T) {
				if r := ch.Call(t, c.caller, c.op, c.args); r.Status != http.StatusForbidden {
					t.Fatalf("got %d %v %v, want 403", r.Status, r.Body, r.Err)
				}
			})
		}
	}
	if n := s.changeCount(); n != 0 {
		t.Fatalf("%d changes recorded after denied calls, want 0", n)
	}
}

func TestAICannotChangeSettingsDirectly(t *testing.T) {
	s := open(t)
	r := s.app.AI(t, s.owner, "change_setting", map[string]any{"key": "business_day_end", "value": "02:00", "effective_from": day(1)})
	if r.Status != http.StatusConflict {
		t.Fatalf("got %d %v %v, want draft required", r.Status, r.Body, r.Err)
	}
	if n := s.changeCount(); n != 0 {
		t.Fatalf("%d changes recorded after AI call, want 0", n)
	}
}

func TestChangesAndSnapshotsCannotBeAlteredOrDeleted(t *testing.T) {
	s := open(t)
	s.change(s.owner, "business_day_end", "02:00", day(1), http.StatusOK)
	s.inTenant(func(ctx context.Context, q *dbgen.Queries) error {
		_, err := settings.TakeSnapshot(ctx, q, s.res.TenantID, s.res.StoreID, time.Now())
		return err
	})
	for _, stmt := range []string{
		"UPDATE setting_changes SET value = '\"03:00\"'",
		"DELETE FROM setting_changes",
		"UPDATE settings_snapshots SET rules = '{}'",
		"DELETE FROM settings_snapshots",
	} {
		t.Run(stmt, func(t *testing.T) {
			ctx := context.Background()
			err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, "SET LOCAL ROLE salon_app"); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", s.res.TenantID.String()); err != nil {
					return err
				}
				_, err := tx.Exec(ctx, stmt)
				return err
			})
			if err == nil || !strings.Contains(err.Error(), "permission denied") {
				t.Fatalf("err = %v, want permission denied", err)
			}
		})
	}
}

func TestSnapshotOfAnotherStoreIsNotFound(t *testing.T) {
	s := open(t)
	var other, snap uuid.UUID
	s.inTenant(func(ctx context.Context, q *dbgen.Queries) error {
		st, err := q.CreateStore(ctx, dbgen.CreateStoreParams{TenantID: s.res.TenantID, Name: "二店"})
		if err != nil {
			return err
		}
		other = st.ID
		snap, err = settings.TakeSnapshot(ctx, q, s.res.TenantID, other, time.Now())
		return err
	})
	s.inTenant(func(ctx context.Context, q *dbgen.Queries) error {
		if _, err := settings.LoadSnapshot(ctx, q, s.res.StoreID, snap); !errors.Is(err, ops.ErrNotFound) {
			t.Fatalf("loading another store's snapshot: err = %v, want not found", err)
		}
		return nil
	})
}

func TestConcurrentChangesToOneSettingKeepTheTrail(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, stmt := range []string{"SET LOCAL ROLE salon_app", "SELECT set_config('app.tenant_id', '" + s.res.TenantID.String() + "', true)"} {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	// 一次修改正在进行：已锁住这项设置、写下修改，尚未提交。
	q := dbgen.New(tx)
	if err := q.LockSettingKey(ctx, dbgen.LockSettingKeyParams{StoreID: s.res.StoreID, Key: "business_day_end"}); err != nil {
		t.Fatal(err)
	}
	first := dbgen.AddSettingChangeParams{
		TenantID: s.res.TenantID, StoreID: s.res.StoreID, Key: "business_day_end",
		Value: []byte(`"02:00"`), OldValue: []byte(`"00:00"`), OldIsDefault: true,
		EffectiveFrom: pgtype.Date{Time: settings.Date(time.Now()).AddDate(0, 0, 1), Valid: true}, ChangedBy: s.owner.StaffID,
	}
	if _, err := q.AddSettingChange(ctx, first); err != nil {
		t.Fatal(err)
	}

	done := make(chan optest.Result, 1)
	go func() {
		done <- s.app.HTTP(t, &s.owner, "change_setting", map[string]any{"key": "business_day_end", "value": "03:00", "effective_from": day(1)})
	}()
	select {
	case r := <-done:
		t.Fatalf("second change finished with %d while the first was in progress; want it to wait", r.Status)
	case <-time.After(300 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if r := <-done; r.Status != http.StatusOK || r.Body["old_value"] != "02:00" || r.Body["old_is_default"] != false {
		t.Fatalf("second change = %d %v, want its old value to be the first change's 02:00", r.Status, r.Body)
	}
}
