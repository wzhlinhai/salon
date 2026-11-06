package rotation_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func (s *fixture) setSetting(key string, value any) {
	s.t.Helper()
	day := settings.Date(s.Now()).Format(settings.DateLayout)
	s.call("change_setting", map[string]any{"key": key, "value": value, "effective_from": day}, 200)
}

func TestQueues(t *testing.T) {
	s := openRotation(t)
	listed := s.call("list_rotation_queues", map[string]any{}, 200)
	queue := listed["entries"].([]any)[0].(map[string]any)
	if queue["name"] != "剪发牌" || len(queue["members"].([]any)) != 3 || queue["created_by"].(map[string]any)["id"] != s.Res.OwnerID.String() {
		t.Fatal(queue)
	}
	active := s.call("create_rotation_queue", map[string]any{"name": "新牌"}, 200)
	renamed := s.call("update_rotation_queue", map[string]any{"id": s.queue, "name": " 助理牌 ", "active": false}, 200)
	if renamed["name"] != "助理牌" || renamed["active"] != false {
		t.Fatal(renamed)
	}
	reject(t, s.HTTPWithToken(t, s.Owner, "create_rotation_queue", map[string]any{"name": "助理牌"}), 409, "轮牌队列名称已存在")
	reject(t, s.HTTPWithToken(t, s.Owner, "create_rotation_queue", map[string]any{"name": " \t "}), 409, "轮牌队列名称不能为空")
	reject(t, s.HTTPWithToken(t, s.Owner, "create_rotation_queue", map[string]any{"name": ""}), 422, "validation failed")
	reject(t, s.HTTPWithToken(t, s.Owner, "update_rotation_queue", map[string]any{"id": active["id"]}), 409, "没有要修改的内容")
	reject(t, s.HTTPWithToken(t, s.Owner, "update_rotation_queue", map[string]any{"id": active["id"], "name": " "}), 409, "轮牌队列名称不能为空")
	reject(t, s.HTTPWithToken(t, s.Owner, "update_rotation_queue", map[string]any{"id": uuid.New(), "name": "无"}), 404, "轮牌队列不存在")
	reject(t, s.HTTPWithToken(t, s.Owner, "join_rotation", map[string]any{"queue_id": s.queue, "staff_id": s.members[0]}), 409, "轮牌队列已停用")
	left, _ := s.Hire("离职", "199", "left")
	s.call("deactivate_staff", map[string]any{"staff_id": left}, 200)
	reject(t, s.HTTPWithToken(t, s.Owner, "set_queue_members", map[string]any{"queue_id": active["id"], "staff_ids": []uuid.UUID{left}}), 409, "员工必须是本店在职员工")
	if got := s.call("list_rotation_queues", map[string]any{}, 200); len(got["entries"].([]any)) != 2 {
		t.Fatal(got)
	}
}

func TestRotationPermissionsAndAI(t *testing.T) {
	s := openRotation(t)
	s.join(s.members[0])
	_, stylist := s.Hire("发型师", "200", "stylist", s.Role("发型师"))
	caller := s.MustCaller(stylist)
	reads := []struct {
		op   string
		args map[string]any
	}{
		{"list_rotation_queues", map[string]any{}},
		{"get_rotation", map[string]any{"queue_id": s.queue}},
		{"list_rotation_events", map[string]any{"from": settingsDate(s).Format(settings.DateLayout), "to": settingsDate(s).Format(settings.DateLayout)}},
		{"get_rotation_stats", map[string]any{"from": settingsDate(s).Format(settings.DateLayout), "to": settingsDate(s).Format(settings.DateLayout)}},
	}
	for _, ch := range s.Channels() {
		for _, read := range reads {
			if r := ch.Call(t, caller, read.op, read.args); r.Status != 200 {
				t.Fatal(ch.Name, read.op, r)
			}
		}
		for _, write := range []struct {
			op   string
			args map[string]any
		}{
			{"create_rotation_queue", map[string]any{"name": "越权"}},
			{"update_rotation_queue", map[string]any{"id": s.queue, "name": "越权"}},
			{"set_queue_members", map[string]any{"queue_id": s.queue, "staff_ids": s.members[:1]}},
			{"join_rotation", map[string]any{"queue_id": s.queue, "staff_id": s.members[0]}},
			{"leave_rotation", map[string]any{"queue_id": s.queue, "staff_id": s.members[0]}},
			{"assign_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[0]}},
			{"release_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[0]}},
			{"skip_staff", map[string]any{"queue_id": s.queue, "staff_id": s.members[0], "reason": "休息"}},
			{"cover_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[0]}},
			{"uncover_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[0]}},
			{"rest_staff", map[string]any{"queue_id": s.queue, "staff_id": s.members[0], "minutes": 5}},
			{"drag_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[0], "to_position": 1, "reason": "越权"}},
			{"set_half_turn_items", map[string]any{"item_ids": []uuid.UUID{s.item}}},
		} {
			if r := ch.Call(t, caller, write.op, write.args); r.Status != 403 || r.Detail() != "permission denied" {
				t.Fatal(ch.Name, write.op, r)
			}
		}
	}
	if _, ok := caller.Permissions[perm.QueueOperate]; ok {
		t.Fatal("stylist should not operate rotation")
	}
	_, nobody := s.Hire("无权限", "201", "nobody")
	unprivileged := s.MustCaller(nobody)
	for _, ch := range s.Channels() {
		for _, read := range reads {
			if r := ch.Call(t, unprivileged, read.op, read.args); r.Status != 403 || r.Detail() != "permission denied" {
				t.Fatal(ch.Name, read.op, r)
			}
		}
	}
	owner := s.MustCaller(s.Owner)
	HTTPResult := s.HTTP(t, &owner, "create_rotation_queue", map[string]any{"name": "HTTP 牌"})
	if HTTPResult.Status != 200 {
		t.Fatal(HTTPResult)
	}
	before := s.snapshot()
	r := s.AI(t, s.MustCaller(s.Owner), "create_rotation_queue", map[string]any{"name": "AI 牌"})
	optest.RequireDraftCreated(t, r, s.snapshot() == before)
	read := s.AI(t, s.MustCaller(s.Owner), "get_rotation", map[string]any{"queue_id": s.queue})
	if read.Status != 200 {
		t.Fatal(read)
	}
}

func TestDefaultRotationRoles(t *testing.T) {
	s := openRotation(t)
	cases := []struct {
		role   string
		view   bool
		manage bool
	}{
		{"店长", true, true},
		{"发型师", true, false},
		{"助理", true, false},
		{"前台", true, true},
	}
	for i, tc := range cases {
		_, token := s.Hire("角色"+tc.role, "30"+string(rune('0'+i)), "role"+string(rune('a'+i)), s.Role(tc.role))
		caller := s.MustCaller(token)
		if caller.Has(perm.QueueView) != tc.view || caller.Has(perm.QueueOperate) != tc.manage {
			t.Fatalf("%s: view=%v operate=%v", tc.role, caller.Has(perm.QueueView), caller.Has(perm.QueueOperate))
		}
	}
	if !s.MustCaller(s.Owner).Has(perm.QueueOperate) {
		t.Fatal("owner missing queue.operate")
	}
}

func TestRotationOrderRules(t *testing.T) {
	a, b, c, d := 0, 1, 2, 3
	t.Run("first_come", func(t *testing.T) {
		s := openRotation(t)
		if got := s.names(s.today()); len(got) != 0 {
			t.Fatal(got)
		}
		s.join(s.members[a])
		s.join(s.members[b])
		assertNames(t, s.names(s.today()), []string{"甲", "乙"})
		s.call("drag_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[a], "to_position": 2, "reason": "先来后到"}, 200)
		assertNames(t, s.names(s.today()), []string{"乙", "甲"})
		s.Advance(24 * time.Hour)
		if got := s.names(s.today()); len(got) != 0 {
			t.Fatal("first_come should start empty", got)
		}
		s.join(s.members[c])
		s.join(s.members[a])
		assertNames(t, s.names(s.today()), []string{"丙", "甲"})
	})
	t.Run("continue_yesterday", func(t *testing.T) {
		s := openRotation(t)
		s.setSetting("rotation_order_rule", "continue_yesterday")
		view := s.today()
		assertNames(t, s.names(view), []string{"甲", "乙", "丙"})
		if got := s.statuses(view); len(got) != 3 || got[0] != "off" {
			t.Fatal(got)
		}
		s.join(s.members[a])
		s.join(s.members[b])
		view = s.today()
		if got := s.statuses(view); got[0] != "waiting" || got[1] != "waiting" || got[2] != "off" {
			t.Fatal(got)
		}
		s.join(s.members[c])
		s.call("drag_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[c], "to_position": 1, "reason": "换顺序"}, 200)
		assertNames(t, s.names(s.today()), []string{"丙", "甲", "乙"})
		s.Advance(24 * time.Hour)
		s.setMembers([]uuid.UUID{s.members[a], s.members[b], s.members[c], s.members[d]})
		assertNames(t, s.names(s.today()), []string{"丙", "甲", "乙", "丁"})
		s.join(s.members[c])
		view = s.today()
		if entries(view)[0]["status"] != "waiting" || entries(view)[0]["position"] != float64(1) {
			t.Fatal(view)
		}
	})
	t.Run("reset_nightly", func(t *testing.T) {
		s := openRotation(t)
		s.setSetting("rotation_order_rule", "reset_nightly")
		assertNames(t, s.names(s.today()), []string{"甲", "乙", "丙"})
		s.join(s.members[c])
		s.call("drag_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[c], "to_position": 1, "reason": "临时调整"}, 200)
		assertNames(t, s.names(s.today()), []string{"丙", "甲", "乙"})
		s.Advance(24 * time.Hour)
		s.setMembers([]uuid.UUID{s.members[a], s.members[b], s.members[c], s.members[d]})
		assertNames(t, s.names(s.today()), []string{"甲", "乙", "丙", "丁"})
	})
}

func TestInactiveQueue(t *testing.T) {
	s := openRotation(t)
	s.join(s.members[0])
	s.call("assign_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[0]}, 200)
	s.call("update_rotation_queue", map[string]any{"id": s.queue, "active": false}, 200)
	if r := s.call("get_rotation", map[string]any{"queue_id": s.queue}, 200); !servingStaff(r, s.members[0]) {
		t.Fatal(r)
	}
	reject(t, s.HTTPWithToken(t, s.Owner, "join_rotation", map[string]any{"queue_id": s.queue, "staff_id": s.members[1]}), 409, "轮牌队列已停用")
	s.call("release_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[0]}, 200)
	reopened := s.call("update_rotation_queue", map[string]any{"id": s.queue, "active": true}, 200)
	if reopened["active"] != true {
		t.Fatal(reopened)
	}
	s.call("update_rotation_queue", map[string]any{"id": s.queue, "active": false}, 200)
	reopened = s.call("update_rotation_queue", map[string]any{"id": s.queue, "name": "重新启用", "active": true}, 200)
	if reopened["active"] != true || reopened["name"] != "重新启用" {
		t.Fatal(reopened)
	}
	s.call("assign_turn", map[string]any{"queue_id": s.queue, "staff_id": s.members[0]}, 200)
}

func TestRotationDateRanges(t *testing.T) {
	s := openRotation(t)
	day := settingsDate(s)
	today := day.Format(settings.DateLayout)
	tomorrow := day.AddDate(0, 0, 1).Format(settings.DateLayout)
	owner := s.MustCaller(s.Owner)
	for _, ch := range s.Channels() {
		for _, tc := range []struct {
			op   string
			args map[string]any
			text string
		}{
			{"get_rotation", map[string]any{"queue_id": s.queue, "date": tomorrow}, "营业日不能晚于今天"},
			{"list_rotation_events", map[string]any{"from": today, "to": tomorrow}, "营业日不能晚于今天"},
			{"get_rotation_stats", map[string]any{"from": today, "to": tomorrow}, "营业日不能晚于今天"},
			{"list_rotation_events", map[string]any{"from": "2026-02-01", "to": "2026-01-01"}, "开始营业日不能晚于结束营业日"},
			{"get_rotation_stats", map[string]any{"from": "2026-01-01", "to": "2026-04-04"}, "查询范围最多 92 天"},
		} {
			r := ch.Call(t, owner, tc.op, tc.args)
			if r.Status != 422 || !strings.Contains(r.Detail(), tc.text) {
				t.Fatal(ch.Name, tc.op, r)
			}
		}
		for _, op := range []string{"list_rotation_events", "get_rotation_stats"} {
			r := ch.Call(t, owner, op, map[string]any{"from": "2026-02-30", "to": today})
			if r.Status != 422 || ch.Name == "http" && r.Detail() != "validation failed" || ch.Name == "ai" && !strings.Contains(r.Detail(), "2026-02-30") {
				t.Fatal(ch.Name, op, r)
			}
		}
	}
}

func (s *fixture) auditEntry(action string, target uuid.UUID) (string, map[string]any, map[string]any) {
	s.t.Helper()
	tx := dbtest.AppTx(s.t, s.Pool, s.Res.TenantID)
	defer dbtest.Rollback(s.t, tx)
	var kind string
	var before, after []byte
	if err := tx.QueryRow(context.Background(), "SELECT target_kind, before, after FROM audit_log WHERE action=$1 AND target_id=$2 ORDER BY at DESC, id DESC LIMIT 1", action, target).Scan(&kind, &before, &after); err != nil {
		s.t.Fatal(err)
	}
	var b, a map[string]any
	if before != nil {
		if err := json.Unmarshal(before, &b); err != nil {
			s.t.Fatal(err)
		}
	}
	if after != nil {
		if err := json.Unmarshal(after, &a); err != nil {
			s.t.Fatal(err)
		}
	}
	return kind, b, a
}

func TestRotationAudit(t *testing.T) {
	s := openRotation(t)
	created := s.call("create_rotation_queue", map[string]any{"name": "审计牌"}, 200)
	queueID := id(created["id"])
	kind, before, after := s.auditEntry("create_rotation_queue", queueID)
	if kind != "rotation_queue" || before != nil || after["name"] != "审计牌" {
		t.Fatal(kind, before, after)
	}
	s.call("update_rotation_queue", map[string]any{"id": queueID, "name": "审计牌二"}, 200)
	kind, before, after = s.auditEntry("update_rotation_queue", queueID)
	if kind != "rotation_queue" || before["name"] != "审计牌" || after["name"] != "审计牌二" {
		t.Fatal(kind, before, after)
	}
	s.call("set_queue_members", map[string]any{"queue_id": queueID, "staff_ids": s.members[:2]}, 200)
	kind, before, after = s.auditEntry("set_queue_members", queueID)
	if kind != "rotation_queue" || len(before["members"].([]any)) != 0 || len(after["members"].([]any)) != 2 {
		t.Fatal(kind, before, after)
	}
	s.call("set_half_turn_items", map[string]any{"item_ids": []uuid.UUID{s.item}}, 200)
	kind, before, after = s.auditEntry("set_half_turn_items", s.Res.StoreID)
	if kind != "rotation_queue" || len(before["entries"].([]any)) != 0 || len(after["entries"].([]any)) != 1 {
		t.Fatal(kind, before, after)
	}
}

type memberRow struct {
	ID        uuid.UUID
	StaffID   uuid.UUID
	SortOrder int32
	CreatedAt time.Time
}

func (s *fixture) memberRows() map[uuid.UUID]memberRow {
	s.t.Helper()
	tx := dbtest.AppTx(s.t, s.Pool, s.Res.TenantID)
	defer dbtest.Rollback(s.t, tx)
	rows, err := tx.Query(context.Background(), "SELECT id, staff_id, sort_order, created_at FROM rotation_members WHERE store_id=$1 AND queue_id=$2", s.Res.StoreID, s.queue)
	if err != nil {
		s.t.Fatal(err)
	}
	defer rows.Close()
	out := map[uuid.UUID]memberRow{}
	for rows.Next() {
		var row memberRow
		if err := rows.Scan(&row.ID, &row.StaffID, &row.SortOrder, &row.CreatedAt); err != nil {
			s.t.Fatal(err)
		}
		out[row.StaffID] = row
	}
	return out
}

func TestMemberSaveKeepsExistingRows(t *testing.T) {
	s := openRotation(t)
	first := s.memberRows()
	s.setMembers(s.members[:3])
	second := s.memberRows()
	for staffID, row := range first {
		if second[staffID].ID != row.ID || !second[staffID].CreatedAt.Equal(row.CreatedAt) {
			t.Fatal("unchanged member was recreated", staffID, row, second[staffID])
		}
	}
	s.setMembers([]uuid.UUID{s.members[1], s.members[0], s.members[2]})
	third := s.memberRows()
	for staffID, row := range second {
		if third[staffID].ID != row.ID || !third[staffID].CreatedAt.Equal(row.CreatedAt) {
			t.Fatal("reordered member was recreated", staffID, row, third[staffID])
		}
	}
	if third[s.members[1]].SortOrder != 1 || third[s.members[0]].SortOrder != 2 {
		t.Fatal(third)
	}
}

type halfItemRow struct {
	ID        uuid.UUID
	CreatedAt time.Time
}

func (s *fixture) halfItemRows() map[uuid.UUID]halfItemRow {
	s.t.Helper()
	tx := dbtest.AppTx(s.t, s.Pool, s.Res.TenantID)
	defer dbtest.Rollback(s.t, tx)
	rows, err := tx.Query(context.Background(), "SELECT id, item_id, created_at FROM rotation_half_items WHERE store_id=$1", s.Res.StoreID)
	if err != nil {
		s.t.Fatal(err)
	}
	defer rows.Close()
	out := map[uuid.UUID]halfItemRow{}
	for rows.Next() {
		var id, itemID uuid.UUID
		var row halfItemRow
		if err := rows.Scan(&id, &itemID, &row.CreatedAt); err != nil {
			s.t.Fatal(err)
		}
		row.ID = id
		out[itemID] = row
	}
	return out
}

func TestHalfItemSaveKeepsExistingRows(t *testing.T) {
	s := openRotation(t)
	s.call("set_half_turn_items", map[string]any{"item_ids": []uuid.UUID{s.item}}, 200)
	first := s.halfItemRows()[s.item]
	other := s.createItem("另一个半轮项目", "50")
	s.call("set_half_turn_items", map[string]any{"item_ids": []uuid.UUID{s.item}}, 200)
	second := s.halfItemRows()[s.item]
	if first.ID != second.ID || !first.CreatedAt.Equal(second.CreatedAt) {
		t.Fatal("unchanged half item was recreated", first, second)
	}
	s.call("set_half_turn_items", map[string]any{"item_ids": []uuid.UUID{other}}, 200)
	if _, ok := s.halfItemRows()[s.item]; ok {
		t.Fatal("removed half item still exists")
	}
}

func TestRenameQueueUsesForUpdate(t *testing.T) {
	s := openRotation(t)
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	if _, err := tx.Exec(context.Background(), "SELECT id FROM rotation_queues WHERE id=$1 FOR KEY SHARE", s.queue); err != nil {
		t.Fatal(err)
	}
	done := make(chan optest.Result, 1)
	go func() {
		done <- s.HTTPWithToken(t, s.Owner, "update_rotation_queue", map[string]any{"id": s.queue, "name": "等待改名"})
	}()
	dbtest.WaitForLockWait(t, s.Pool)
	var waiting string
	if err := s.Pool.QueryRow(context.Background(), "SELECT query FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%rotation_queues%' LIMIT 1").Scan(&waiting); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(waiting, "FOR UPDATE") {
		t.Fatalf("rename waited at %q, want the FOR UPDATE queue lock", waiting)
	}
	dbtest.Commit(t, tx)
	if r := <-done; r.Status != 200 || r.Body["name"] != "等待改名" {
		t.Fatal(r)
	}
}

func TestRotationStoreIsolation(t *testing.T) {
	s := openRotation(t)
	other := uuid.New()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	dbtest.Exec(t, tx, "INSERT INTO stores(id,tenant_id,name) VALUES($1,$2,'外店')", other, s.Res.TenantID)
	dbtest.Commit(t, tx)
	caller := s.MustCaller(s.Owner)
	caller.StoreID = other
	if r := s.HTTP(t, &caller, "list_rotation_queues", map[string]any{}); r.Status != 200 || len(r.Body["entries"].([]any)) != 0 {
		t.Fatal(r)
	}
	if r := s.HTTP(t, &caller, "get_rotation", map[string]any{"queue_id": s.queue}); r.Status != 404 || !strings.Contains(r.Detail(), "轮牌队列不存在") {
		t.Fatal(r)
	}
	if r := s.HTTP(t, &caller, "update_rotation_queue", map[string]any{"id": s.queue, "name": "外店"}); r.Status != 404 {
		t.Fatal(r)
	}
}

func TestRotationMigrationDown(t *testing.T) {
	s := openRotation(t)
	s.join(s.members[0])
	dbtest.MigrateDownTo(t, s.Pool, 42)
	for _, table := range []string{"rotation_queues", "rotation_members", "rotation_turns", "rotation_events", "rotation_half_items"} {
		var name *string
		if err := s.Pool.QueryRow(context.Background(), "SELECT to_regclass($1)::text", table).Scan(&name); err != nil || name != nil {
			t.Fatal(table, name, err)
		}
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var rows int
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM audit_log WHERE target_kind='rotation_queue'").Scan(&rows); err != nil || rows != 0 {
		t.Fatal("rotation audit rows survived down migration", rows, err)
	}
	_, err := tx.Exec(context.Background(), `INSERT INTO audit_log(tenant_id,store_id,actor_id,action,target_kind,target_id) VALUES($1,$2,$3,'test','rotation_queue',$4)`, s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, uuid.New())
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "23514" || pe.ConstraintName != "audit_log_target_kind_check" {
		t.Fatal("rotation_queue audit target still accepted after down", err)
	}
}

type rotationQueryCounter struct{ all atomic.Int64 }

func (c *rotationQueryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.all.Add(1)
	return ctx
}
func (*rotationQueryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestGetRotationQueryCount(t *testing.T) {
	s := openRotation(t)
	for _, member := range s.members[:3] {
		s.join(member)
	}
	second := s.call("create_rotation_queue", map[string]any{"name": "助理牌"}, 200)
	secondID := id(second["id"])
	s.call("set_queue_members", map[string]any{"queue_id": secondID, "staff_ids": s.members}, 200)
	for _, member := range s.members {
		s.call("join_rotation", map[string]any{"queue_id": secondID, "staff_id": member}, 200)
	}
	counter := &rotationQueryCounter{}
	config := s.Pool.Config().Copy()
	config.ConnConfig.Tracer = counter
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	app := optest.New(t, pool)
	caller := s.MustCaller(s.Owner)
	day := settings.Date(s.Now()).Format(settings.DateLayout)
	for _, ch := range app.Channels() {
		counts := []int64{}
		for _, queueID := range []uuid.UUID{s.queue, secondID} {
			counter.all.Store(0)
			r := ch.Call(t, caller, "get_rotation", map[string]any{"queue_id": queueID, "date": day})
			if r.Status != 200 {
				t.Fatal(ch.Name, r)
			}
			counts = append(counts, counter.all.Load())
		}
		if counts[0] != counts[1] {
			t.Fatalf("%s: query count grew with rows: %v", ch.Name, counts)
		}
	}
}
