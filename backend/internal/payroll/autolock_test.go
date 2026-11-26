package payroll_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/payroll"
	"github.com/wzhlinhai/salon/backend/internal/payrollmonth"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func autoLock(t *testing.T, s *optest.Shop, now time.Time) {
	t.Helper()
	payroll.AutoLockOnce(t.Context(), s.Pool, now, discardLogger())
}

func settlementAt(t *testing.T, s *optest.Shop, m payrollmonth.Month) *payrollmonth.Settlement {
	t.Helper()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	settlement, err := payrollmonth.GetSettlement(t.Context(), dbgen.New(tx), s.Res.StoreID, m)
	dbtest.Rollback(t, tx)
	if err != nil {
		t.Fatal(err)
	}
	return settlement
}

func settlementCount(t *testing.T, s *optest.Shop, storeID uuid.UUID) int {
	t.Helper()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var count int
	if err := tx.QueryRow(t.Context(), "SELECT count(*) FROM commission_settlements WHERE store_id=$1", storeID).Scan(&count); err != nil {
		dbtest.Rollback(t, tx)
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	return count
}

func nextMonth(m payrollmonth.Month) payrollmonth.Month {
	return payrollmonth.Of(m.First().AddDate(0, 1, 0))
}

func monthAt(day time.Time, date int) time.Time {
	return time.Date(day.Year(), day.Month(), date, 12, 0, 0, 0, settings.Location)
}

func autoDate(m payrollmonth.Month, date int) string {
	return time.Date(m.First().Year(), m.First().Month()+1, date, 0, 0, 0, 0, time.UTC).Format(settings.DateLayout)
}

func monthRow(t *testing.T, rows []any, m payrollmonth.Month) map[string]any {
	t.Helper()
	for _, v := range rows {
		row := v.(map[string]any)
		if row["month"] == m.String() {
			return row
		}
	}
	t.Fatalf("%s not in %v", m, rows)
	return nil
}

func TestPayrollAutoLockDueDay(t *testing.T) {
	s, first := pastShop(t)
	firstMonth := payrollmonth.Of(first)
	current := payrollmonth.Of(settings.Date(time.Now()))
	target := current.Prev()
	before := monthAt(current.First(), 14)
	on := monthAt(current.First(), 15)
	s.SetNow(before)
	autoLock(t, s, before)
	if settlementAt(t, s, target) != nil {
		t.Fatal("locked before the auto lock day")
	}
	if settlementAt(t, s, firstMonth) == nil {
		t.Fatal("opening month was not auto locked")
	}
	body := payrollView(t, s, target.First())
	want := autoDate(target, 15)
	if body["locked"] != false || body["locked_by"] != nil || body["auto_locked"] != false || body["auto_lock_date"] != want {
		t.Fatal(body)
	}
	row := monthRow(t, entries(t, s.Call(t, s.Owner, "list_payroll_months", map[string]any{}, 200)), target)
	if row["locked"] != false || row["auto_lock_date"] != want {
		t.Fatal(row)
	}
	s.SetNow(on)
	autoLock(t, s, on)
	settlement := settlementAt(t, s, target)
	if settlement == nil || settlement.SettledBy != uuid.Nil {
		t.Fatalf("target settlement = %+v", settlement)
	}
}

func TestPayrollAutoLockLocksConsecutiveMonths(t *testing.T) {
	s, first := pastShop(t)
	firstMonth := payrollmonth.Of(first)
	current := payrollmonth.Of(settings.Date(time.Now()))
	on := monthAt(current.First(), 20)
	s.SetNow(on)
	autoLock(t, s, on)
	for m := firstMonth; m.Before(current); m = nextMonth(m) {
		settlement := settlementAt(t, s, m)
		if settlement == nil || settlement.SettledBy != uuid.Nil {
			t.Fatalf("%s settlement = %+v", m, settlement)
		}
	}
}

func TestPayrollAutoLockUsesTodaysSetting(t *testing.T) {
	s, _ := pastShop(t)
	current := payrollmonth.Of(settings.Date(time.Now()))
	target := current.Prev()
	eighteen, twenty := monthAt(current.First(), 18), monthAt(current.First(), 20)
	s.SetNow(eighteen)
	s.Call(t, s.Owner, "change_setting", map[string]any{"key": "payroll_auto_lock_day", "value": 20, "effective_from": eighteen.Format(settings.DateLayout)}, 200)
	autoLock(t, s, eighteen)
	if settlementAt(t, s, target) != nil {
		t.Fatal("locked before the configured day")
	}
	s.SetNow(twenty)
	autoLock(t, s, twenty)
	if settlement := settlementAt(t, s, target); settlement == nil || settlement.SettledBy != uuid.Nil {
		t.Fatalf("target settlement = %+v", settlement)
	}
}

func TestPayrollAutoLockDayValidation(t *testing.T) {
	s := optest.OpenShop(t)
	from := settings.Date(time.Now()).Format(settings.DateLayout)
	for _, value := range []int{0, 29} {
		reject(t, s.HTTPWithToken(t, s.Owner, "change_setting", map[string]any{"key": "payroll_auto_lock_day", "value": value, "effective_from": from}), 409, "工资自动锁定日须为 1 到 28 的整数")
	}
}

func TestPayrollAutoLockSystemSettlement(t *testing.T) {
	s, first := pastShop(t)
	firstMonth := payrollmonth.Of(first)
	current := payrollmonth.Of(settings.Date(time.Now()))
	on := monthAt(current.First(), 20)
	s.SetNow(on)
	autoLock(t, s, on)
	settlement := settlementAt(t, s, firstMonth)
	if settlement == nil || settlement.SettledBy != uuid.Nil {
		t.Fatalf("settlement = %+v", settlement)
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var settledBy *uuid.UUID
	if err := tx.QueryRow(t.Context(), "SELECT settled_by FROM commission_settlements WHERE id=$1", settlement.ID).Scan(&settledBy); err != nil {
		dbtest.Rollback(t, tx)
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	if settledBy != nil {
		t.Fatalf("settled_by = %v, want NULL", settledBy)
	}
	body := payrollView(t, s, firstMonth.First())
	if body["locked"] != true || body["locked_by"] != nil || body["auto_locked"] != true || body["auto_lock_date"] != nil {
		t.Fatal(body)
	}
	row := monthRow(t, entries(t, s.Call(t, s.Owner, "list_payroll_months", map[string]any{}, 200)), firstMonth)
	if row["locked"] != true || row["locked_by"] != nil || row["auto_locked"] != true || row["auto_lock_date"] != nil {
		t.Fatal(row)
	}
	logs := entries(t, s.Call(t, s.Owner, "list_audit_log", map[string]any{"target_id": settlement.ID}, 200))
	if len(logs) != 1 || logs[0].(map[string]any)["action"] != "lock_payroll_month" || logs[0].(map[string]any)["actor"] != nil {
		t.Fatal(logs)
	}
	auditEntry(t, s, "payroll_month", "lock_payroll_month", settlement.ID, nil, body)
}

func TestPayrollAutoLockSkipsManualMonth(t *testing.T) {
	s, first := pastShop(t)
	firstMonth := payrollmonth.Of(first)
	current := payrollmonth.Of(settings.Date(time.Now()))
	on := monthAt(current.First(), 20)
	s.SetNow(on)
	manual := s.Call(t, s.Owner, "lock_payroll_month", map[string]any{"month": firstMonth.String()}, 200)
	if manual.Body["locked_by"].(map[string]any)["id"] != s.Res.OwnerID.String() {
		t.Fatal(manual)
	}
	autoLock(t, s, on)
	if settlement := settlementAt(t, s, firstMonth); settlement == nil || settlement.SettledBy != s.Res.OwnerID {
		t.Fatalf("first settlement = %+v", settlement)
	}
	next := nextMonth(firstMonth)
	if settlement := settlementAt(t, s, next); settlement == nil || settlement.SettledBy != uuid.Nil {
		t.Fatalf("next settlement = %+v", settlement)
	}
	target := mustSettlement(t, s, firstMonth).ID
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var count int
	var actor uuid.UUID
	if err := tx.QueryRow(t.Context(), "SELECT count(*) FROM audit_log WHERE target_kind='payroll_month' AND action='lock_payroll_month' AND target_id=$1", target).Scan(&count); err != nil {
		dbtest.Rollback(t, tx)
		t.Fatal(err)
	}
	if err := tx.QueryRow(t.Context(), "SELECT actor_id FROM audit_log WHERE target_kind='payroll_month' AND action='lock_payroll_month' AND target_id=$1 ORDER BY at, id LIMIT 1", target).Scan(&actor); err != nil {
		dbtest.Rollback(t, tx)
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	if count != 1 || actor != s.Res.OwnerID {
		t.Fatalf("manual audit = %d %v", count, actor)
	}
}

func mustSettlement(t *testing.T, s *optest.Shop, m payrollmonth.Month) *payrollmonth.Settlement {
	t.Helper()
	settlement := settlementAt(t, s, m)
	if settlement == nil {
		t.Fatalf("no settlement for %s", m)
	}
	return settlement
}

func TestPayrollAutoLockStopsAfterFailure(t *testing.T) {
	s, first := pastShop(t)
	firstMonth := payrollmonth.Of(first)
	current := payrollmonth.Of(settings.Date(time.Now()))
	on := monthAt(current.First(), 20)
	s.SetNow(on)
	constraint := "auto_lock_stop"
	if _, err := s.Pool.Exec(t.Context(), "ALTER TABLE commission_settlements ADD CONSTRAINT "+constraint+" CHECK (month <> DATE '"+firstMonth.First().Format(settings.DateLayout)+"')"); err != nil {
		t.Fatal(err)
	}
	err := payroll.AutoLockStore(t.Context(), s.Pool, s.Res.TenantID, s.Res.StoreID, on)
	if err == nil || !strings.Contains(err.Error(), firstMonth.String()) {
		t.Fatalf("err = %v, want failure for %s", err, firstMonth)
	}
	next := nextMonth(firstMonth)
	if settlementAt(t, s, firstMonth) != nil || settlementAt(t, s, next) != nil {
		t.Fatal("kept locking after the first failure")
	}
	if _, err := s.Pool.Exec(t.Context(), "ALTER TABLE commission_settlements DROP CONSTRAINT "+constraint); err != nil {
		t.Fatal(err)
	}
	if err := payroll.AutoLockStore(t.Context(), s.Pool, s.Res.TenantID, s.Res.StoreID, on); err != nil {
		t.Fatal(err)
	}
	if settlement := settlementAt(t, s, next); settlement == nil || settlement.SettledBy != uuid.Nil {
		t.Fatalf("next settlement = %+v", settlement)
	}
}

func TestPayrollAutoLockContinuesAfterStoreFailure(t *testing.T) {
	s, first := pastShop(t)
	current := payrollmonth.Of(settings.Date(time.Now()))
	on := monthAt(current.First(), 20)
	otherStore := uuid.New()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "INSERT INTO stores(id,tenant_id,name,created_at) VALUES($1,$2,'第二家',$3)", otherStore, s.Res.TenantID, first.Add(13*time.Hour))
	dbtest.Exec(t, tx, `INSERT INTO setting_changes(tenant_id,store_id,key,value,is_default,old_value,old_is_default,effective_from,changed_by) VALUES($1,$2,'payroll_auto_lock_day','"bad"'::jsonb,false,'null'::jsonb,true,$3,$4)`, s.Res.TenantID, s.Res.StoreID, first.Format(settings.DateLayout), s.Res.OwnerID)
	dbtest.Commit(t, tx)
	s.SetNow(on)
	autoLock(t, s, on)
	if settlementCount(t, s, otherStore) == 0 {
		t.Fatal("second store was not locked after the first store failed")
	}
}

func TestPayrollAutoLockBusinessDayEnd(t *testing.T) {
	s, _ := pastShop(t)
	current := payrollmonth.Of(settings.Date(time.Now()))
	target := current.Prev()
	day1 := time.Date(current.First().Year(), current.First().Month(), 1, 12, 0, 0, 0, settings.Location)
	effective := time.Date(current.First().Year(), current.First().Month(), 2, 0, 0, 0, 0, time.UTC)
	s.SetNow(day1)
	s.Call(t, s.Owner, "change_setting", map[string]any{"key": "business_day_end", "value": "03:00", "effective_from": effective.Format(settings.DateLayout)}, 200)
	early := time.Date(current.First().Year(), current.First().Month(), 15, 1, 0, 0, 0, settings.Location)
	late := time.Date(current.First().Year(), current.First().Month(), 15, 4, 0, 0, 0, settings.Location)
	s.SetNow(early)
	autoLock(t, s, early)
	if settlementAt(t, s, target) != nil {
		t.Fatal("locked on business day 14")
	}
	s.SetNow(late)
	autoLock(t, s, late)
	if settlement := settlementAt(t, s, target); settlement == nil || settlement.SettledBy != uuid.Nil {
		t.Fatalf("target settlement = %+v", settlement)
	}
}

func TestPayrollAutoLockRunStopsOnContext(t *testing.T) {
	s := optest.OpenShop(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var logs bytes.Buffer
	done := make(chan struct{})
	go func() {
		payroll.RunAutoLock(ctx, s.Pool, time.Now, slog.New(slog.NewTextHandler(&logs, nil)))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunAutoLock did not return after context cancellation")
	}
	if logs.Len() != 0 {
		t.Fatalf("canceled context logged: %s", logs.String())
	}
}
