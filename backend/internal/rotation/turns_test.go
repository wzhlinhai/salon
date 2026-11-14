package rotation_test

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func TestJoinLeaveRelease(t *testing.T) {
	s := openRotation(t)
	s.join(s.members[0])
	reject(t, s.HTTPWithToken(t, s.Owner, "join_rotation", map[string]any{"queue_id": s.queue, "staff_id": s.members[0]}), 409, "员工已经在轮牌中")
	reject(t, s.HTTPWithToken(t, s.Owner, "join_rotation", map[string]any{"queue_id": s.queue, "staff_id": s.members[3]}), 409, "员工不属于这个轮牌队列")
	s.call("leave_rotation", map[string]any{"queue_id": s.queue, "staff_id": s.members[0]}, 200)
	reject(t, s.HTTPWithToken(t, s.Owner, "leave_rotation", map[string]any{"queue_id": s.queue, "staff_id": s.members[0]}), 409, "员工不在轮牌中")
	view := s.join(s.members[0])
	if entries(view)[0]["status"] != "waiting" || entries(view)[0]["position"] != float64(1) {
		t.Fatal(view)
	}
	s.call("assign_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[0]}, 200)
	reject(t, s.HTTPWithToken(t, s.Owner, "leave_rotation", map[string]any{"queue_id": s.queue, "staff_id": s.members[0]}), 409, "正在服务的员工不能离开轮牌")
	s.call("release_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[0]}, 200)
	if got := s.event("release"); got["from_position"] != float64(1) || got["to_position"] != float64(1) {
		t.Fatal(got)
	}
	reject(t, s.HTTPWithToken(t, s.Owner, "release_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[0]}), 409, "员工当前状态不能执行这个操作")
	reject(t, s.HTTPWithToken(t, s.Owner, "assign_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[1]}), 409, "员工不在轮牌中")
	s.call("assign_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[0]}, 200)
	reject(t, s.HTTPWithToken(t, s.Owner, "assign_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[0]}), 409, "员工当前不能接客")
	reject(t, s.HTTPWithToken(t, s.Owner, "assign_turn", map[string]any{"queue_id": s.queue}), 409, "没有可接客的员工")
	left, _ := s.Hire("离职", "198", "left2")
	s.call("set_queue_members", map[string]any{"queue_id": s.queue, "staff_ids": []uuid.UUID{s.members[0], s.members[1], s.members[2], left}}, 200)
	s.call("deactivate_staff", map[string]any{"staff_id": left}, 200)
	reject(t, s.HTTPWithToken(t, s.Owner, "join_rotation", map[string]any{"queue_id": s.queue, "staff_id": left}), 409, "员工必须是本店在职员工")
}

func TestSkipPolicyAndReasons(t *testing.T) {
	s := openRotation(t)
	for _, member := range s.members[:3] {
		s.join(member)
	}
	view := s.call("skip_staff", map[string]any{"queue_id": s.queue, "staff_id": s.members[0], "reason": "休息"}, 200)
	assertNames(t, s.names(view), []string{"乙", "丙", "甲"})
	event := s.event("skip")
	if event["reason"] != "休息" || event["from_position"] != float64(1) || event["to_position"] != float64(3) {
		t.Fatal(event)
	}
	s.setSetting("rotation_skip_policy", "keep_position")
	view = s.call("skip_staff", map[string]any{"queue_id": s.queue, "staff_id": s.members[1], "reason": "迟到"}, 200)
	assertNames(t, s.names(view), []string{"乙", "丙", "甲"})
	reject(t, s.HTTPWithToken(t, s.Owner, "skip_staff", map[string]any{"queue_id": s.queue, "staff_id": s.members[2], "reason": "其他"}), 409, "跳过原因为其他时要填写说明")
	s.call("skip_staff", map[string]any{"queue_id": s.queue, "staff_id": s.members[2], "reason": "其他", "note": "  顾客要求  "}, 200)
	found := false
	for _, e := range s.events(settingsDate(s), settingsDate(s)) {
		if e["kind"] == "skip" && e["staff"].(map[string]any)["id"] == s.members[2].String() {
			found = e["note"] == "顾客要求"
		}
	}
	if !found {
		t.Fatal("other reason note not recorded")
	}
	reject(t, s.HTTPWithToken(t, s.Owner, "skip_staff", map[string]any{"queue_id": s.queue, "staff_id": s.members[0], "reason": "随便"}), 422, "validation failed")
	before := s.snapshot()
	reject(t, s.HTTPWithToken(t, s.Owner, "skip_staff", map[string]any{"queue_id": s.queue, "staff_id": s.members[0]}), 422, "validation failed")
	reject(t, s.HTTPWithToken(t, s.Owner, "drag_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[0], "to_position": 1}), 422, "validation failed")
	if s.snapshot() != before {
		t.Fatal("rejected skip/drag changed data")
	}
	view = s.call("drag_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[2], "to_position": 1, "reason": "临时顶班"}, 200)
	assertNames(t, s.names(view), []string{"丙", "乙", "甲"})
	event = s.event("drag")
	if event["reason"] != "临时顶班" || event["from_position"] != float64(2) || event["to_position"] != float64(1) {
		t.Fatal(event)
	}
	reject(t, s.HTTPWithToken(t, s.Owner, "drag_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[0], "to_position": 4, "reason": "越界"}), 409, "目标位置超出轮牌范围")
}

func TestCoverUncover(t *testing.T) {
	s := openRotation(t)
	for _, member := range s.members[:3] {
		s.join(member)
	}
	view := s.call("cover_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[0]}, 200)
	if entries(view)[0]["status"] != "covered" || entries(view)[0]["position"] != float64(1) {
		t.Fatal(view)
	}
	view = s.call("assign_turn", map[string]any{"queue_id": s.queue}, 200)
	serving := findStatus(view, "serving")
	if serving == nil || serving["staff"].(map[string]any)["id"] != s.members[1].String() {
		t.Fatal(view)
	}
	reject(t, s.HTTPWithToken(t, s.Owner, "cover_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[1]}), 409, "员工当前状态不能执行这个操作")
	view = s.call("uncover_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[0]}, 200)
	if entries(view)[0]["status"] != "waiting" {
		t.Fatal(view)
	}
	reject(t, s.HTTPWithToken(t, s.Owner, "uncover_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[0]}), 409, "员工当前状态不能执行这个操作")
	if got := len(s.events(settingsDate(s), settingsDate(s))); got < 3 {
		t.Fatal(got)
	}
}

func TestRestExpiry(t *testing.T) {
	s := openRotation(t)
	s.join(s.members[0])
	s.join(s.members[1])
	view := s.call("rest_staff", map[string]any{"queue_id": s.queue, "staff_id": s.members[0], "minutes": 1}, 200)
	if entries(view)[0]["status"] != "resting" || entries(view)[0]["rest_until"] == nil {
		t.Fatal(view)
	}
	restUntil, err := time.Parse(time.RFC3339Nano, entries(view)[0]["rest_until"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if got := entries(s.today())[0]["status"]; got != "resting" {
		t.Fatal(got)
	}
	view = s.call("assign_turn", map[string]any{"queue_id": s.queue}, 200)
	serving := findStatus(view, "serving")
	if serving == nil || serving["staff"].(map[string]any)["id"] != s.members[1].String() {
		t.Fatal(view)
	}
	s.Advance(59 * time.Second)
	if got := entries(s.today())[0]["status"]; got != "resting" {
		t.Fatal(got)
	}
	s.Advance(time.Second)
	before := s.snapshot()
	if got := entries(s.today())[0]["status"]; got != "waiting" {
		t.Fatal(got)
	}
	if s.snapshot() != before {
		t.Fatal("get_rotation persisted the rest end")
	}
	s.join(s.members[2])
	if got := s.event("rest_end"); got["staff"].(map[string]any)["id"] != s.members[0].String() {
		t.Fatal(got)
	}
	ended, err := time.Parse(time.RFC3339Nano, s.event("rest_end")["created_at"].(string))
	if err != nil || !ended.Equal(restUntil) {
		t.Fatalf("rest_end created_at = %v, want %v (%v)", s.event("rest_end")["created_at"], restUntil, err)
	}
	reject(t, s.HTTPWithToken(t, s.Owner, "rest_staff", map[string]any{"queue_id": s.queue, "staff_id": s.members[1], "minutes": 0}), 422, "validation failed")
	reject(t, s.HTTPWithToken(t, s.Owner, "rest_staff", map[string]any{"queue_id": s.queue, "staff_id": s.members[1], "minutes": 241}), 422, "validation failed")
}

func TestConcurrentAssign(t *testing.T) {
	s := openRotation(t)
	s.join(s.members[0])
	s.join(s.members[1])
	var wg sync.WaitGroup
	done := make(chan optest.Result, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			done <- s.HTTPWithToken(t, s.Owner, "assign_turn", map[string]any{"queue_id": s.queue})
		}()
	}
	wg.Wait()
	close(done)
	for result := range done {
		if result.Status != 200 {
			t.Fatal(result)
		}
	}
	assigned := map[string]bool{}
	for _, event := range s.events(settingsDate(s), settingsDate(s)) {
		if event["kind"] == "assign" {
			assigned[event["staff"].(map[string]any)["id"].(string)] = true
		}
	}
	if len(assigned) != 2 {
		t.Fatal(assigned)
	}
}

func TestMembershipCheckedAfterQueueLock(t *testing.T) {
	s := openRotation(t)
	s.join(s.members[0])
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	if _, err := tx.Exec(context.Background(), "SELECT id FROM rotation_queues WHERE id=$1 FOR UPDATE", s.queue); err != nil {
		t.Fatal(err)
	}
	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "assign_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[0]})
	}()
	dbtest.WaitForLockWait(t, s.Pool)
	if _, err := tx.Exec(context.Background(), "DELETE FROM rotation_members WHERE queue_id=$1 AND staff_id=$2", s.queue, s.members[0]); err != nil {
		t.Fatal(err)
	}
	dbtest.Commit(t, tx)
	reject(t, <-done, 409, "员工不属于这个轮牌队列")
}

func TestAssignSkipsRemovedAndInactive(t *testing.T) {
	s := openRotation(t)
	for _, member := range s.members[:3] {
		s.join(member)
	}
	s.setMembers([]uuid.UUID{s.members[1], s.members[2]})
	s.call("deactivate_staff", map[string]any{"staff_id": s.members[1]}, 200)
	view := s.call("assign_turn", map[string]any{"queue_id": s.queue}, 200)
	if !servingStaff(view, s.members[2]) {
		t.Fatal(view)
	}
	reject(t, s.HTTPWithToken(t, s.Owner, "assign_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[0]}), 409, "员工不属于这个轮牌队列")
	reject(t, s.HTTPWithToken(t, s.Owner, "assign_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[1]}), 409, "员工必须是本店在职员工")
}

func TestWaitingStateGuards(t *testing.T) {
	s := openRotation(t)
	s.join(s.members[0])
	s.call("assign_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[0]}, 200)
	for _, tc := range []struct {
		op   string
		args map[string]any
	}{
		{"skip_staff", map[string]any{"queue_id": s.queue, "staff_id": s.members[0], "reason": "休息"}},
		{"rest_staff", map[string]any{"queue_id": s.queue, "staff_id": s.members[0], "minutes": 5}},
		{"drag_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[0], "to_position": 1, "reason": "顶班"}},
	} {
		reject(t, s.HTTPWithToken(t, s.Owner, tc.op, tc.args), 409, "员工当前状态不能执行这个操作")
	}
}

func TestBlankReasonRejected(t *testing.T) {
	s := openRotation(t)
	s.join(s.members[0])
	before := s.snapshot()
	reject(t, s.HTTPWithToken(t, s.Owner, "skip_staff", map[string]any{"queue_id": s.queue, "staff_id": s.members[0], "reason": "休息", "note": "  \t "}), 422, "跳过说明不能为空")
	reject(t, s.HTTPWithToken(t, s.Owner, "drag_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[0], "to_position": 1, "reason": " \n "}), 422, "拖动原因不能为空")
	if s.snapshot() != before {
		t.Fatal("blank reason changed data")
	}
}

func TestGetRotationReadDoesNotPersistPrefill(t *testing.T) {
	s := openRotation(t)
	s.setSetting("rotation_order_rule", "continue_yesterday")
	before := s.snapshot()
	for _, ch := range s.Channels() {
		r := ch.Call(t, s.MustCaller(s.Owner), "get_rotation", map[string]any{"queue_id": s.queue})
		if r.Status != 200 {
			t.Fatal(ch.Name, r)
		}
		if got := s.names(r.Body); !reflect.DeepEqual(got, []string{"甲", "乙", "丙"}) {
			t.Fatal(ch.Name, got)
		}
		if got := s.statuses(r.Body); !reflect.DeepEqual(got, []string{"off", "off", "off"}) {
			t.Fatal(ch.Name, got)
		}
		positions := []int{}
		for _, entry := range entries(r.Body) {
			positions = append(positions, int(entry["position"].(float64)))
		}
		if !reflect.DeepEqual(positions, []int{1, 2, 3}) {
			t.Fatal(ch.Name, positions)
		}
	}
	if s.snapshot() != before {
		t.Fatal("get_rotation persisted prefill turns")
	}
}

func TestContinueYesterdayUsesLastPersistedDay(t *testing.T) {
	s := openRotation(t)
	s.SetNow(time.Date(2026, 6, 15, 12, 0, 0, 0, settings.Location))
	s.setSetting("rotation_order_rule", "continue_yesterday")
	for _, member := range s.members[:3] {
		s.join(member)
	}
	s.call("drag_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[0], "to_position": 3, "reason": "固定周一顺序"}, 200)
	assertNames(t, s.names(s.today()), []string{"乙", "丙", "甲"})

	s.Advance(24 * time.Hour)
	before := s.snapshot()
	assertNames(t, s.names(s.today()), []string{"乙", "丙", "甲"})
	if s.snapshot() != before {
		t.Fatal("Tuesday read persisted turns")
	}

	s.Advance(24 * time.Hour)
	assertNames(t, s.names(s.today()), []string{"乙", "丙", "甲"})
	view := s.join(s.members[0])
	assertNames(t, s.names(view), []string{"乙", "丙", "甲"})
	if got := s.statuses(view); !reflect.DeepEqual(got, []string{"off", "off", "waiting"}) {
		t.Fatalf("Wednesday first write statuses = %v", got)
	}
}

func TestRestExpiryPersistsOnWrite(t *testing.T) {
	t.Run("normal write", func(t *testing.T) {
		s := openRotation(t)
		s.join(s.members[0])
		s.call("rest_staff", map[string]any{"queue_id": s.queue, "staff_id": s.members[0], "minutes": 1}, 200)
		s.Advance(time.Minute)
		s.call("skip_staff", map[string]any{"queue_id": s.queue, "staff_id": s.members[0], "reason": "休息"}, 200)
		assertRestPersisted(t, s, s.members[0], 1)
		s.call("skip_staff", map[string]any{"queue_id": s.queue, "staff_id": s.members[0], "reason": "休息"}, 200)
		assertRestPersisted(t, s, s.members[0], 1)
	})
	t.Run("advance with unchanged targets", func(t *testing.T) {
		s := openRotation(t)
		s.join(s.members[0])
		s.join(s.members[1])
		s.join(s.members[2])
		order := s.openFor(s.members[2], false, s.item)
		s.call("rest_staff", map[string]any{"queue_id": s.queue, "staff_id": s.members[1], "minutes": 1}, 200)
		s.assign(s.members[0], false, order)
		s.Advance(time.Minute)
		s.pay(order, "100.00")
		assertRestPersisted(t, s, s.members[1], 1)
		s.call("skip_staff", map[string]any{"queue_id": s.queue, "staff_id": s.members[1], "reason": "休息"}, 200)
		assertRestPersisted(t, s, s.members[1], 1)
	})
}

func assertRestPersisted(t *testing.T, s *fixture, staffID uuid.UUID, wantEvents int) {
	t.Helper()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var status string
	var restNull bool
	if err := tx.QueryRow(context.Background(), "SELECT status, rest_until IS NULL FROM rotation_turns WHERE queue_id=$1 AND staff_id=$2", s.queue, staffID).Scan(&status, &restNull); err != nil {
		t.Fatal(err)
	}
	var events int
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM rotation_events WHERE queue_id=$1 AND staff_id=$2 AND kind='rest_end'", s.queue, staffID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	if status != "waiting" || !restNull || events != wantEvents {
		t.Fatalf("rest state status=%s rest_null=%v events=%d, want waiting/NULL/%d", status, restNull, events, wantEvents)
	}
}

func TestConcurrentFirstWriteCreatesTurnsOnce(t *testing.T) {
	s := openRotation(t)
	s.setSetting("rotation_order_rule", "continue_yesterday")
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	if _, err := tx.Exec(context.Background(), "SELECT id FROM rotation_queues WHERE id=$1 FOR UPDATE", s.queue); err != nil {
		t.Fatal(err)
	}
	done := make(chan optest.Result, 2)
	for _, staffID := range s.members[:2] {
		go func() {
			done <- s.HTTPWithToken(t, s.Owner, "join_rotation", map[string]any{"queue_id": s.queue, "staff_id": staffID})
		}()
	}
	optest.AssertWaiting(t, s.Pool, done, 2)
	dbtest.Commit(t, tx)
	for range 2 {
		optest.WaitResult(t, done, 200)
	}
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var rows, staff int
	if err := tx.QueryRow(context.Background(), "SELECT count(*), count(DISTINCT staff_id) FROM rotation_turns WHERE queue_id=$1", s.queue).Scan(&rows, &staff); err != nil {
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	if rows != 3 || staff != 3 {
		t.Fatalf("turns = %d, distinct staff = %d, want 3/3", rows, staff)
	}
}
