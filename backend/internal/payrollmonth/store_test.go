package payrollmonth_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/payrollmonth"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func rule(t *testing.T, err error, want string) {
	t.Helper()
	var rejection *ops.RuleError
	if !errors.As(err, &rejection) || rejection.Message != want {
		t.Fatalf("got %v, want RuleError %q", err, want)
	}
}

func TestGuardAndLock(t *testing.T) {
	s := optest.OpenShop(t)
	current := payrollmonth.Of(settings.Date(time.Now()))
	opened := current.Prev().Prev().Prev()
	if _, err := s.Pool.Exec(t.Context(), "UPDATE stores SET created_at=$1 WHERE id=$2", opened.First().Add(12*time.Hour), s.Res.StoreID); err != nil {
		t.Fatal(err)
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	q := dbgen.New(tx)
	before := opened.Prev()
	floorText := "结算月不能早于开店的月份（" + opened.String() + "）"
	rule(t, payrollmonth.Guard(t.Context(), q, s.Res.StoreID, before, "设置底薪"), floorText)
	if err := payrollmonth.Guard(t.Context(), q, s.Res.StoreID, opened, "设置底薪"); err != nil {
		t.Fatal(err)
	}
	unlocked, err := payrollmonth.GetSettlement(t.Context(), q, s.Res.StoreID, opened)
	if err != nil || unlocked != nil {
		t.Fatal(unlocked, err)
	}
	for _, tc := range []struct {
		month payrollmonth.Month
		want  string
	}{
		{before, floorText},
		{current, current.String() + " 还没结束，不能锁定"},
		{current.Prev(), "请先锁定 " + current.Prev().Prev().String()},
	} {
		_, err := payrollmonth.Lock(t.Context(), q, s.Res.StoreID, tc.month.String(), s.Res.OwnerID)
		rule(t, err, tc.want)
	}
	locked, err := payrollmonth.Lock(t.Context(), q, s.Res.StoreID, opened.String(), s.Res.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	if locked.TenantID != s.Res.TenantID || locked.Month != opened || locked.SettledBy != s.Res.OwnerID || locked.SettledAt.IsZero() {
		t.Fatal(locked)
	}
	read, err := payrollmonth.GetSettlement(t.Context(), q, s.Res.StoreID, opened)
	if err != nil || !reflect.DeepEqual(read, locked) {
		t.Fatal(read, locked, err)
	}
	_, err = payrollmonth.Lock(t.Context(), q, s.Res.StoreID, opened.String(), s.Res.OwnerID)
	rule(t, err, opened.String()+" 已经锁定")
	for _, action := range []string{"设置底薪", "添加加减项", "删除加减项"} {
		rule(t, payrollmonth.Guard(t.Context(), q, s.Res.StoreID, opened, action), opened.String()+" 已经锁定，不能再"+action)
	}
	// 即使有未来月份的记录，列表也只列到当前营业月。
	future := payrollmonth.Of(current.First().AddDate(0, 1, 0))
	dbtest.Exec(t, tx, "INSERT INTO commission_settlements(tenant_id,store_id,month,settled_by) VALUES($1,$2,$3,$4)", s.Res.TenantID, s.Res.StoreID, future.First(), s.Res.OwnerID)
	statuses, err := payrollmonth.List(t.Context(), q, s.Res.StoreID)
	if err != nil || len(statuses) != 4 {
		t.Fatal(statuses, err)
	}
	for i, status := range statuses {
		if current.Before(status.Month) || status.Month != payrollmonth.Of(current.First().AddDate(0, -i, 0)) || status.Lockable != (i == 2) {
			t.Fatal(statuses)
		}
	}
	// 当前月已锁同时违反未结束条件，应先返回已锁定。
	dbtest.Exec(t, tx, "INSERT INTO commission_settlements(tenant_id,store_id,month,settled_by) VALUES($1,$2,$3,$4)", s.Res.TenantID, s.Res.StoreID, current.First(), s.Res.OwnerID)
	_, err = payrollmonth.Lock(t.Context(), q, s.Res.StoreID, current.String(), s.Res.OwnerID)
	rule(t, err, current.String()+" 已经锁定")
	// 开店月之前即使已有锁定记录，也先拒绝开店月下限。
	dbtest.Exec(t, tx, "INSERT INTO commission_settlements(tenant_id,store_id,month,settled_by) VALUES($1,$2,$3,$4)", s.Res.TenantID, s.Res.StoreID, before.First(), s.Res.OwnerID)
	_, err = payrollmonth.Lock(t.Context(), q, s.Res.StoreID, before.String(), s.Res.OwnerID)
	rule(t, err, floorText)
	rule(t, payrollmonth.Guard(t.Context(), q, s.Res.StoreID, before, "删除加减项"), floorText)
	dbtest.Rollback(t, tx)
}
