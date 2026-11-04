package attendance_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func TestStaffShiftQueue(t *testing.T) {
	s := optest.OpenShop(t)
	day := clock(s)
	shift := create(t, s, "早班", "09:00", "18:00")
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	if err := dbgen.New(tx).LockStaffShifts(t.Context(), s.Res.StoreID); err != nil {
		t.Fatal(err)
	}
	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "set_staff_shift", map[string]any{"staff_id": s.Res.OwnerID, "shift_id": shift["id"], "effective_from": day.Format(settings.DateLayout)})
	}()
	optest.AssertWaiting(t, s.Pool, done, 1)
	dbtest.Rollback(t, tx)
	optest.WaitResult(t, done, 200)
}

func TestShiftAssignmentShare(t *testing.T) {
	s := optest.OpenShop(t)
	day := clock(s)
	shift := create(t, s, "早班", "09:00", "18:00")
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "UPDATE shifts SET active=false WHERE id=$1", shift["id"])
	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "set_staff_shift", map[string]any{"staff_id": s.Res.OwnerID, "shift_id": shift["id"], "effective_from": day.Format(settings.DateLayout)})
	}()
	optest.AssertWaiting(t, s.Pool, done, 1)
	dbtest.Commit(t, tx)
	reject(t, optest.WaitResult(t, done, 409), 409, "班次已停用，不能指定给员工")
}

func TestDeactivateShiftLock(t *testing.T) {
	s := optest.OpenShop(t)
	shift := create(t, s, "早班", "09:00", "18:00")
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	if _, err := dbgen.New(tx).ShareShift(t.Context(), dbgen.ShareShiftParams{StoreID: s.Res.StoreID, ID: uuid.MustParse(shift["id"].(string))}); err != nil {
		t.Fatal(err)
	}
	done := make(chan optest.Result, 2)
	for range 2 {
		go func() { done <- s.HTTPWithToken(t, s.Owner, "deactivate_shift", map[string]any{"id": shift["id"]}) }()
	}
	optest.AssertWaiting(t, s.Pool, done, 2)
	dbtest.Rollback(t, tx)
	first := <-done
	second := <-done
	// rejection-detail: 此分支只交换返回顺序，下面 reject 同时断言状态码和文字
	if first.Status == 409 {
		first, second = second, first
	}
	if first.Status != 200 {
		t.Fatal(first)
	}
	reject(t, second, 409, "班次已停用")
}

func TestAssignmentBlocksDeactivation(t *testing.T) {
	s := optest.OpenShop(t)
	day := clock(s)
	shift := create(t, s, "早班", "09:00", "18:00")
	// 插入时停在测试屏障，让真实指定操作持有班次共享锁。
	if _, err := s.Pool.Exec(t.Context(), `CREATE FUNCTION attendance_barrier() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(917381); RETURN NEW; END $$;
 CREATE TRIGGER attendance_barrier BEFORE INSERT ON staff_shifts FOR EACH ROW EXECUTE FUNCTION attendance_barrier()`); err != nil {
		t.Fatal(err)
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "SELECT pg_advisory_xact_lock(917381)")
	assigned := make(chan optest.Result, 1)
	go func() {
		assigned <- s.HTTPWithToken(t, s.Owner, "set_staff_shift", map[string]any{"staff_id": s.Res.OwnerID, "shift_id": shift["id"], "effective_from": day.Format(settings.DateLayout)})
	}()
	optest.AssertWaiting(t, s.Pool, assigned, 1)
	deactivated := make(chan optest.Result, 1)
	go func() {
		deactivated <- s.HTTPWithToken(t, s.Owner, "deactivate_shift", map[string]any{"id": shift["id"]})
	}()
	optest.AssertWaiting(t, s.Pool, deactivated, 2)
	dbtest.Rollback(t, tx)
	optest.WaitResult(t, assigned, 200)
	optest.WaitResult(t, deactivated, 200)
	current := view(t, s, day)[0]["current"].(map[string]any)["shift"].(map[string]any)
	if current["id"] != shift["id"] || current["active"] != false {
		t.Fatal(current)
	}
}

func TestAssignmentLocksStaff(t *testing.T) {
	s := optest.OpenShop(t)
	day := clock(s)
	id, _ := s.Hire("员工", "002", "worker")
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "UPDATE staff SET active=false,deactivated_at=clock_timestamp() WHERE id=$1", id)
	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "set_staff_shift", map[string]any{"staff_id": id, "effective_from": day.Format(settings.DateLayout)})
	}()
	optest.AssertWaiting(t, s.Pool, done, 1)
	dbtest.Commit(t, tx)
	reject(t, optest.WaitResult(t, done, 409), 409, "员工必须是本店在职员工")
}
