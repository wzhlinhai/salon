package rotation

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

type turnState struct {
	turn        dbgen.RotationTurn
	staffName   string
	staffNumber string
	dirty       bool
}

func dateValue(t time.Time) pgtype.Date { return pgtype.Date{Time: t, Valid: true} }

func timeValue(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func timePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}

func position(v int32) *int32 { return &v }

func queueError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("轮牌队列不存在：%w", ops.ErrNotFound)
	}
	return err
}

func names(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]staff.Ref, error) {
	rows, err := q.StaffNames(ctx, dbgen.StaffNamesParams{StoreID: storeID, Ids: ids})
	if err != nil {
		return nil, err
	}
	refs := make(map[uuid.UUID]staff.Ref, len(rows))
	for _, row := range rows {
		refs[row.ID] = staff.Ref{ID: row.ID, Name: row.Name}
	}
	return refs, nil
}

func lockQueueRow(ctx context.Context, env ops.Env, id uuid.UUID, rename bool) (dbgen.RotationQueue, error) {
	var row dbgen.RotationQueue
	var err error
	if rename {
		row, err = env.Q.LockRotationQueueForUpdate(ctx, dbgen.LockRotationQueueForUpdateParams{StoreID: env.Caller.StoreID, ID: id})
	} else {
		row, err = env.Q.LockRotationQueue(ctx, dbgen.LockRotationQueueParams{StoreID: env.Caller.StoreID, ID: id})
	}
	if err != nil {
		return row, queueError(err)
	}
	return row, nil
}

func lockQueueAny(ctx context.Context, env ops.Env, id uuid.UUID) (dbgen.RotationQueue, error) {
	return lockQueueRow(ctx, env, id, false)
}

func lockQueueForRename(ctx context.Context, env ops.Env, id uuid.UUID) (dbgen.RotationQueue, error) {
	return lockQueueRow(ctx, env, id, true)
}

func lockQueue(ctx context.Context, env ops.Env, id uuid.UUID) (dbgen.RotationQueue, error) {
	row, err := lockQueueAny(ctx, env, id)
	if err != nil {
		return row, err
	}
	if !row.Active {
		return row, ops.Rule("轮牌队列已停用")
	}
	return row, nil
}

func requireMember(ctx context.Context, env ops.Env, queueID, staffID uuid.UUID) error {
	_, err := env.Q.GetRotationMember(ctx, dbgen.GetRotationMemberParams{StoreID: env.Caller.StoreID, QueueID: queueID, StaffID: staffID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ops.Rule("员工不属于这个轮牌队列")
	}
	return err
}

// queueActiveMembers 锁住队列当前成员并返回仍在职的成员集合。
func queueActiveMembers(ctx context.Context, env ops.Env, queueID uuid.UUID) (map[uuid.UUID]bool, error) {
	members, err := env.Q.ListRotationMembers(ctx, dbgen.ListRotationMembersParams{StoreID: env.Caller.StoreID, QueueID: queueID})
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, len(members))
	for i, member := range members {
		ids[i] = member.StaffID
	}
	rows, err := staff.LockReferences(ctx, env.Q, env.Caller.StoreID, ids...)
	if err != nil {
		return nil, err
	}
	active := make(map[uuid.UUID]bool, len(members))
	for _, member := range members {
		active[member.StaffID] = rows[member.StaffID].Active
	}
	return active, nil
}

func rotationDate(ctx context.Context, env ops.Env, s string) (time.Time, error) {
	today, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, env.Now)
	if err != nil {
		return time.Time{}, err
	}
	if s == "" {
		return today, nil
	}
	day, err := time.Parse(settings.DateLayout, s)
	if err != nil {
		return time.Time{}, &ops.ToolError{Messages: []string{"请填写有效的营业日"}}
	}
	if day.After(today) {
		return time.Time{}, &ops.ToolError{Messages: []string{"营业日不能晚于今天"}}
	}
	return day, nil
}

func loadTurns(ctx context.Context, env ops.Env, queueID uuid.UUID, day time.Time) ([]turnState, error) {
	rows, err := env.Q.LockRotationTurns(ctx, dbgen.LockRotationTurnsParams{StoreID: env.Caller.StoreID, QueueID: queueID, BusinessDate: dateValue(day)})
	if err != nil {
		return nil, err
	}
	out := make([]turnState, 0, len(rows))
	for _, row := range rows {
		out = append(out, turnState{
			turn: dbgen.RotationTurn{
				TenantID: row.TenantID, StoreID: row.StoreID, ID: row.ID, QueueID: row.QueueID,
				BusinessDate: row.BusinessDate, StaffID: row.StaffID, Position: row.Position, Status: row.Status,
				HalfPending: row.HalfPending, RestUntil: row.RestUntil, ServingOrderID: row.ServingOrderID,
				ServingDesignated: row.ServingDesignated, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
			},
			staffName: row.StaffName, staffNumber: row.StaffNumber,
		})
	}
	return out, nil
}

// loadTurnsRead 读取队列当天全部轮牌行，不加锁；纯读路径不得在只读事务里使用 FOR UPDATE。
func loadTurnsRead(ctx context.Context, env ops.Env, queueID uuid.UUID, day time.Time) ([]turnState, error) {
	rows, err := env.Q.ListRotationTurns(ctx, dbgen.ListRotationTurnsParams{StoreID: env.Caller.StoreID, QueueID: queueID, BusinessDate: dateValue(day)})
	if err != nil {
		return nil, err
	}
	out := make([]turnState, 0, len(rows))
	for _, row := range rows {
		out = append(out, turnState{
			turn: dbgen.RotationTurn{
				TenantID: row.TenantID, StoreID: row.StoreID, ID: row.ID, QueueID: row.QueueID,
				BusinessDate: row.BusinessDate, StaffID: row.StaffID, Position: row.Position, Status: row.Status,
				HalfPending: row.HalfPending, RestUntil: row.RestUntil, ServingOrderID: row.ServingOrderID,
				ServingDesignated: row.ServingDesignated, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
			},
			staffName: row.StaffName, staffNumber: row.StaffNumber,
		})
	}
	return out, nil
}

// prepareTurnsRead 在内存中得出当天名单：按顺序规则预建初始行，并把到点的休息显示为等待，不写任何表。
func prepareTurnsRead(ctx context.Context, env ops.Env, queue dbgen.RotationQueue, day time.Time, rules settings.Rules) ([]turnState, error) {
	turns, err := loadTurnsRead(ctx, env, queue.ID, day)
	if err != nil {
		return nil, err
	}
	if len(turns) == 0 && rules.RotationOrderRule() != "first_come" {
		turns, err = prefillStates(ctx, env, queue, day, rules.RotationOrderRule())
		if err != nil {
			return nil, err
		}
	}
	expireRestsRead(env.Now, turns)
	return turns, nil
}

// prepareTurnsWrite 锁定队列当天全部轮牌行，先持久化待建的初始行和到点的休息，再由调用方应用自己的修改。
func prepareTurnsWrite(ctx context.Context, env ops.Env, queue dbgen.RotationQueue, day time.Time, rules settings.Rules) ([]turnState, error) {
	turns, err := loadTurns(ctx, env, queue.ID, day)
	if err != nil {
		return nil, err
	}
	if len(turns) == 0 && rules.RotationOrderRule() != "first_come" {
		if err := prefillTurns(ctx, env, queue, day, rules.RotationOrderRule()); err != nil {
			return nil, err
		}
		if turns, err = loadTurns(ctx, env, queue.ID, day); err != nil {
			return nil, err
		}
	}
	changed, err := expireRests(ctx, env, queue.ID, day, turns)
	if err != nil {
		return nil, err
	}
	if changed {
		if err := persistTurns(ctx, env, queue.ID, day, turns, false); err != nil {
			return nil, err
		}
	}
	return turns, nil
}

func orderedMembers(ctx context.Context, env ops.Env, queue dbgen.RotationQueue, day time.Time, rule string) ([]dbgen.ListRotationMembersRow, error) {
	members, err := env.Q.ListRotationMembers(ctx, dbgen.ListRotationMembersParams{StoreID: env.Caller.StoreID, QueueID: queue.ID})
	if err != nil {
		return nil, err
	}
	ordered := slices.Clone(members)
	if rule == "continue_yesterday" {
		previous, err := env.Q.ListPreviousRotationTurns(ctx, dbgen.ListPreviousRotationTurnsParams{
			StoreID: env.Caller.StoreID, QueueID: queue.ID, BusinessDate: dateValue(day),
		})
		if err != nil {
			return nil, err
		}
		rank := make(map[uuid.UUID]int32, len(previous))
		for _, row := range previous {
			rank[row.StaffID] = row.Position
		}
		slices.SortStableFunc(ordered, func(a, b dbgen.ListRotationMembersRow) int {
			ap, aok := rank[a.StaffID]
			bp, bok := rank[b.StaffID]
			switch {
			case aok && bok:
				return int(ap - bp)
			case aok:
				return -1
			case bok:
				return 1
			default:
				return int(a.SortOrder - b.SortOrder)
			}
		})
	} else {
		slices.SortStableFunc(ordered, func(a, b dbgen.ListRotationMembersRow) int { return int(a.SortOrder - b.SortOrder) })
	}
	return ordered, nil
}

func prefillStates(ctx context.Context, env ops.Env, queue dbgen.RotationQueue, day time.Time, rule string) ([]turnState, error) {
	ordered, err := orderedMembers(ctx, env, queue, day, rule)
	if err != nil {
		return nil, err
	}
	out := make([]turnState, 0, len(ordered))
	for i, member := range ordered {
		out = append(out, turnState{
			turn: dbgen.RotationTurn{
				TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, QueueID: queue.ID,
				BusinessDate: dateValue(day), StaffID: member.StaffID, Position: int32(i + 1), Status: StatusOff,
			},
			staffName: member.StaffName, staffNumber: member.StaffNumber,
		})
	}
	return out, nil
}

func prefillTurns(ctx context.Context, env ops.Env, queue dbgen.RotationQueue, day time.Time, rule string) error {
	ordered, err := orderedMembers(ctx, env, queue, day, rule)
	if err != nil {
		return err
	}
	if len(ordered) == 0 {
		return nil
	}
	staffIDs := make([]uuid.UUID, len(ordered))
	for i, member := range ordered {
		staffIDs[i] = member.StaffID
	}
	return env.Q.InsertRotationTurns(ctx, dbgen.InsertRotationTurnsParams{
		TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, QueueID: queue.ID,
		BusinessDate: dateValue(day), StaffIds: staffIDs,
	})
}

func expireRests(ctx context.Context, env ops.Env, queueID uuid.UUID, day time.Time, turns []turnState) (bool, error) {
	changed := false
	for i := range turns {
		t := &turns[i]
		if t.turn.Status != StatusResting || !t.turn.RestUntil.Valid || t.turn.RestUntil.Time.After(env.Now) {
			continue
		}
		from := t.turn.Position
		ended := t.turn.RestUntil.Time
		t.turn.Status = StatusWaiting
		t.turn.RestUntil = pgtype.Timestamptz{}
		t.dirty = true
		changed = true
		if err := recordEventAt(ctx, env, queueID, day, &t.turn.StaffID, "rest_end", nil, nil, position(from), position(from), nil, nil, nil, ended); err != nil {
			return false, err
		}
	}
	return changed, nil
}

func expireRestsRead(now time.Time, turns []turnState) {
	for i := range turns {
		t := &turns[i]
		if t.turn.Status != StatusResting || !t.turn.RestUntil.Valid || t.turn.RestUntil.Time.After(now) {
			continue
		}
		t.turn.Status = StatusWaiting
		t.turn.RestUntil = pgtype.Timestamptz{}
	}
}

func persistTurns(ctx context.Context, env ops.Env, queueID uuid.UUID, day time.Time, turns []turnState, renumber bool) error {
	if len(turns) == 0 {
		return nil
	}
	if renumber {
		if err := env.Q.ShiftRotationTurnPositions(ctx, dbgen.ShiftRotationTurnPositionsParams{StoreID: env.Caller.StoreID, QueueID: queueID, BusinessDate: dateValue(day)}); err != nil {
			return err
		}
		for i := range turns {
			turns[i].turn.Position = int32(i + 1)
			turns[i].dirty = true
		}
	}
	for _, t := range turns {
		if !t.dirty {
			continue
		}
		if err := env.Q.UpdateRotationTurn(ctx, dbgen.UpdateRotationTurnParams{
			StoreID: env.Caller.StoreID, QueueID: queueID, BusinessDate: dateValue(day), StaffID: t.turn.StaffID,
			Position: t.turn.Position, Status: t.turn.Status, HalfPending: t.turn.HalfPending,
			RestUntil: t.turn.RestUntil, ServingOrderID: t.turn.ServingOrderID, ServingDesignated: t.turn.ServingDesignated,
		}); err != nil {
			return err
		}
	}
	return nil
}

func recordEvent(ctx context.Context, env ops.Env, queueID uuid.UUID, day time.Time, staffID *uuid.UUID, kind string, reason, note *string, from, to *int32, minutes *int32, orderID *uuid.UUID, designated *bool) error {
	_, err := env.Q.AddRotationEvent(ctx, dbgen.AddRotationEventParams{
		TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, QueueID: queueID, BusinessDate: dateValue(day),
		StaffID: staffID, Kind: kind, Reason: reason, Note: note, FromPosition: from, ToPosition: to,
		Minutes: minutes, OrderID: orderID, Designated: designated, ActorStaffID: env.Caller.StaffID,
	})
	return err
}

func recordEventAt(ctx context.Context, env ops.Env, queueID uuid.UUID, day time.Time, staffID *uuid.UUID, kind string, reason, note *string, from, to *int32, minutes *int32, orderID *uuid.UUID, designated *bool, at time.Time) error {
	_, err := env.Q.AddRotationEventAt(ctx, dbgen.AddRotationEventAtParams{
		TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, QueueID: queueID, BusinessDate: dateValue(day),
		StaffID: staffID, Kind: kind, Reason: reason, Note: note, FromPosition: from, ToPosition: to,
		Minutes: minutes, OrderID: orderID, Designated: designated, ActorStaffID: env.Caller.StaffID,
		CreatedAt: timeValue(&at),
	})
	return err
}

func indexOf(turns []turnState, staffID uuid.UUID) int {
	for i := range turns {
		if turns[i].turn.StaffID == staffID {
			return i
		}
	}
	return -1
}

func renumber(turns []turnState) {
	for i := range turns {
		turns[i].turn.Position = int32(i + 1)
		turns[i].dirty = true
	}
}

func moveToTail(turns []turnState, staffID uuid.UUID) (int32, int32, bool) {
	index := indexOf(turns, staffID)
	if index < 0 {
		return 0, 0, false
	}
	from := turns[index].turn.Position
	row := turns[index]
	copy(turns[index:], turns[index+1:])
	turns[len(turns)-1] = row
	renumber(turns)
	return from, turns[indexOf(turns, staffID)].turn.Position, true
}

func moveToPosition(turns []turnState, staffID uuid.UUID, target int) (int32, int32, bool) {
	index := indexOf(turns, staffID)
	if index < 0 || target < 1 || target > len(turns) {
		return 0, 0, false
	}
	from := turns[index].turn.Position
	row := turns[index]
	destination := target - 1
	if destination > index {
		copy(turns[index:destination], turns[index+1:destination+1])
	} else if destination < index {
		copy(turns[destination+1:index+1], turns[destination:index])
	}
	turns[destination] = row
	renumber(turns)
	return from, turns[indexOf(turns, staffID)].turn.Position, true
}

func clearServing(t *turnState) {
	t.turn.ServingOrderID = nil
	t.turn.ServingDesignated = false
	t.dirty = true
}

func setWaiting(t *turnState) {
	t.turn.Status = StatusWaiting
	t.dirty = true
}

func (t turnState) ref() staff.Ref { return staff.Ref{ID: t.turn.StaffID, Name: t.staffName} }

func rotationView(queue dbgen.RotationQueue, day time.Time, turns []turnState, refs map[uuid.UUID]staff.Ref) Rotation {
	out := Rotation{Queue: Queue{ID: queue.ID, Name: queue.Name, Active: queue.Active, CreatedBy: refs[queue.CreatedBy], CreatedAt: queue.CreatedAt.Time, Members: []Member{}}, BusinessDate: day.Format(settings.DateLayout), Entries: make([]Turn, 0, len(turns))}
	for _, t := range turns {
		out.Entries = append(out.Entries, Turn{
			Position: int(t.turn.Position), Staff: t.ref(), Status: t.turn.Status, HalfPending: t.turn.HalfPending,
			RestUntil: timePtr(t.turn.RestUntil), ServingOrderID: t.turn.ServingOrderID, ServingDesignated: t.turn.ServingDesignated,
		})
	}
	return out
}
