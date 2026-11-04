package attendance_test

import (
	"testing"
	"time"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func TestPunchWriteQueue(t *testing.T) {
	for _, op := range []string{"clock_in", "add_punch", "void_punch", "approve_attendance", "revoke_attendance_approval"} {
		t.Run(op, func(t *testing.T) {
			s := optest.OpenShop(t)
			clock(s)
			p := manual(t, s, s.Res.OwnerID, time.Date(2026, 6, 11, 10, 0, 0, 0, settings.Location))
			args := map[string]any{}
			switch op {
			case "add_punch":
				args = map[string]any{"staff_id": s.Res.OwnerID, "at": "2026-06-11T18:00:00+08:00", "reason": "漏打"}
			case "void_punch":
				args = map[string]any{"id": p["id"], "reason": "错"}
			case "approve_attendance":
				args = map[string]any{"staff_id": s.Res.OwnerID, "business_date": "2026-06-11", "reason": "出差"}
			case "revoke_attendance_approval":
				a := s.Call(t, s.Owner, "approve_attendance", map[string]any{"staff_id": s.Res.OwnerID, "business_date": "2026-06-11", "reason": "出差"}, 200).Body
				args = map[string]any{"id": a["id"]}
			}
			other, _ := s.Hire("另一人", "002", "other")
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			if err := dbgen.New(tx).LockStaffAttendance(t.Context(), dbgen.LockStaffAttendanceParams{StoreID: s.Res.StoreID, StaffID: other}); err != nil {
				t.Fatal(err)
			}
			done := make(chan optest.Result, 1)
			go func() { done <- s.HTTPWithToken(t, s.Owner, op, args) }()
			optest.WaitResult(t, done, 200)
			dbtest.Rollback(t, tx)
			// 再建一组可成功写入的数据，避免前一次状态遮住锁检查。
			if op == "void_punch" {
				p = manual(t, s, s.Res.OwnerID, time.Date(2026, 6, 10, 10, 0, 0, 0, settings.Location))
				args["id"] = p["id"]
			}
			if op == "approve_attendance" {
				args["business_date"] = "2026-06-10"
				manual(t, s, s.Res.OwnerID, time.Date(2026, 6, 10, 10, 0, 0, 0, settings.Location))
			}
			if op == "revoke_attendance_approval" {
				a := s.Call(t, s.Owner, "approve_attendance", map[string]any{"staff_id": s.Res.OwnerID, "business_date": "2026-06-11", "reason": "出差"}, 200).Body
				args["id"] = a["id"]
			}
			tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			if err := dbgen.New(tx).LockStaffAttendance(t.Context(), dbgen.LockStaffAttendanceParams{StoreID: s.Res.StoreID, StaffID: s.Res.OwnerID}); err != nil {
				t.Fatal(err)
			}
			go func() { done <- s.HTTPWithToken(t, s.Owner, op, args) }()
			optest.AssertWaiting(t, s.Pool, done, 1)
			dbtest.Rollback(t, tx)
			optest.WaitResult(t, done, 200)
		})
	}
}

func TestPunchRowLocks(t *testing.T) {
	for _, approval := range []bool{false, true} {
		s := optest.OpenShop(t)
		clock(s)
		p := manual(t, s, s.Res.OwnerID, time.Date(2026, 6, 11, 10, 0, 0, 0, settings.Location))
		op, table, id := "void_punch", "punches", p["id"]
		args := map[string]any{"id": id, "reason": "错"}
		if approval {
			a := s.Call(t, s.Owner, "approve_attendance", map[string]any{"staff_id": s.Res.OwnerID, "business_date": "2026-06-11", "reason": "出差"}, 200).Body
			op, table, id = "revoke_attendance_approval", "attendance_approvals", a["id"]
			args = map[string]any{"id": id}
		}
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		dbtest.Exec(t, tx, "SELECT id FROM "+table+" WHERE id=$1 FOR SHARE", id)
		done := make(chan optest.Result, 2)
		for range 2 {
			go func() { done <- s.HTTPWithToken(t, s.Owner, op, args) }()
		}
		optest.AssertWaiting(t, s.Pool, done, 2)
		dbtest.Rollback(t, tx)
		first, second := <-done, <-done
		// rejection-detail: 只交换顺序，下面断言完整拒绝。
		if first.Status == 409 {
			first, second = second, first
		}
		if first.Status != 200 {
			t.Fatal(first)
		}
		detail := "这次补卡已作废"
		if approval {
			detail = "这次核准已撤销"
		}
		reject(t, second, 409, detail)
	}
}

func TestPunchRowStateAfterLock(t *testing.T) {
	for _, approval := range []bool{false, true} {
		s := optest.OpenShop(t)
		clock(s)
		p := manual(t, s, s.Res.OwnerID, time.Date(2026, 6, 11, 10, 0, 0, 0, settings.Location))
		op, id, sql, detail := "void_punch", p["id"], "UPDATE punches SET voided_by=$2,voided_at='2026-06-12',void_reason='错' WHERE id=$1", "这次补卡已作废"
		args := map[string]any{"id": id, "reason": "错"}
		if approval {
			a := s.Call(t, s.Owner, "approve_attendance", map[string]any{"staff_id": s.Res.OwnerID, "business_date": "2026-06-11", "reason": "出差"}, 200).Body
			op, id, sql, detail = "revoke_attendance_approval", a["id"], "UPDATE attendance_approvals SET revoked_by=$2,revoked_at='2026-06-12' WHERE id=$1", "这次核准已撤销"
			args = map[string]any{"id": id}
		}
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		dbtest.Exec(t, tx, sql, id, s.Res.OwnerID)
		done := make(chan optest.Result, 1)
		go func() { done <- s.HTTPWithToken(t, s.Owner, op, args) }()
		optest.AssertWaiting(t, s.Pool, done, 1)
		dbtest.Commit(t, tx)
		reject(t, optest.WaitResult(t, done, 409), 409, detail)
	}
}

func TestApproveLocksStaff(t *testing.T) {
	s := optest.OpenShop(t)
	clock(s)
	manual(t, s, s.Res.OwnerID, time.Date(2026, 6, 11, 10, 0, 0, 0, settings.Location))
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "UPDATE staff SET active=false,deactivated_at=clock_timestamp() WHERE id=$1", s.Res.OwnerID)
	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "approve_attendance", map[string]any{"staff_id": s.Res.OwnerID, "business_date": "2026-06-11", "reason": "外出"})
	}()
	optest.AssertWaiting(t, s.Pool, done, 1)
	dbtest.Rollback(t, tx)
	optest.WaitResult(t, done, 200)
}
