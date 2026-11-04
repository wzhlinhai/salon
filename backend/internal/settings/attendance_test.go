package settings_test

import (
	"context"
	"github.com/jackc/pgx/v5"
	"reflect"
	"testing"
	"time"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func TestAttendanceGraceAndEffectiveDays(t *testing.T) {
	s := optest.OpenShop(t)
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, settings.Location)
	s.SetNow(now)
	days := []time.Time{settings.Date(now), settings.Date(now).AddDate(0, 0, 1), settings.Date(now).AddDate(0, 0, 2)}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	r, err := settings.Effective(t.Context(), dbgen.New(tx), s.Res.StoreID, days[0])
	dbtest.Rollback(t, tx)
	if err != nil || r.AttendanceGraceMinutes() != 0 {
		t.Fatal(r, err)
	}
	for _, n := range []int{-1, 61} {
		got := s.HTTPWithToken(t, s.Owner, "change_setting", map[string]any{"key": "attendance_grace_minutes", "value": n, "effective_from": "2026-06-12"})
		if got.Status != 409 || got.Detail() != "考勤宽限分钟须为 0 到 60 的整数" {
			t.Fatal(got)
		}
	}
	for i, n := range []int{5, 60} {
		s.Call(t, s.Owner, "change_setting", map[string]any{"key": "attendance_grace_minutes", "value": n, "effective_from": days[i+1].Format(settings.DateLayout)}, 200)
	}
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	q := dbgen.New(tx)
	batch, err := settings.EffectiveDays(t.Context(), q, s.Res.StoreID, days)
	if err != nil {
		t.Fatal(err)
	}
	for i, day := range days {
		single, err := settings.Effective(t.Context(), q, s.Res.StoreID, day)
		if err != nil || !reflect.DeepEqual(single, batch[i]) || batch[i].AttendanceGraceMinutes() != []int{0, 5, 60}[i] {
			t.Fatal(i, single, batch, err)
		}
	}
	dbtest.Rollback(t, tx)
}

type attendanceQueries struct {
	dbgen.DBTX
	queries int
}

func (q *attendanceQueries) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	q.queries++
	return q.DBTX.Query(ctx, sql, args...)
}
func TestEffectiveDaysBatch(t *testing.T) {
	s := optest.OpenShop(t)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	counter := &attendanceQueries{DBTX: tx}
	day := time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC)
	rules, err := settings.EffectiveDays(t.Context(), dbgen.New(counter), s.Res.StoreID, []time.Time{day, day.AddDate(0, 0, 1), day.AddDate(0, 0, 2)})
	dbtest.Rollback(t, tx)
	if err != nil || len(rules) != 3 || counter.queries != 1 {
		t.Fatal(rules, counter.queries, err)
	}
}
