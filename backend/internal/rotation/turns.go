package rotation

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

func daySetup(ctx context.Context, env ops.Env, queueID uuid.UUID, requireActive bool) (dbgen.RotationQueue, time.Time, settings.Rules, []turnState, error) {
	var queue dbgen.RotationQueue
	var err error
	if requireActive {
		queue, err = lockQueue(ctx, env, queueID)
	} else {
		queue, err = lockQueueAny(ctx, env, queueID)
	}
	if err != nil {
		return queue, time.Time{}, settings.Rules{}, nil, err
	}
	day, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, env.Now)
	if err != nil {
		return queue, time.Time{}, settings.Rules{}, nil, err
	}
	rules, err := settings.Effective(ctx, env.Q, env.Caller.StoreID, day)
	if err != nil {
		return queue, time.Time{}, settings.Rules{}, nil, err
	}
	turns, _, err := prepareTurns(ctx, env, queue, day, rules)
	if err != nil {
		return queue, time.Time{}, settings.Rules{}, nil, err
	}
	return queue, day, rules, turns, nil
}

func lockOpenOrder(ctx context.Context, env ops.Env, id uuid.UUID) error {
	row, err := env.Q.LockOrder(ctx, dbgen.LockOrderParams{StoreID: env.Caller.StoreID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return ops.ErrNotFound
	}
	if err != nil {
		return err
	}
	if row.Status != "open" {
		return ops.Rule("只能对未结账未撤单的单接客")
	}
	return nil
}

func finish(ctx context.Context, env ops.Env, queue dbgen.RotationQueue, day time.Time, turns []turnState) (*Rotation, error) {
	members, err := env.Q.ListRotationMembers(ctx, dbgen.ListRotationMembersParams{StoreID: env.Caller.StoreID, QueueID: queue.ID})
	if err != nil {
		return nil, err
	}
	ids := []uuid.UUID{queue.CreatedBy}
	for _, turn := range turns {
		ids = append(ids, turn.turn.StaffID)
	}
	for _, member := range members {
		ids = append(ids, member.StaffID)
	}
	refs, err := names(ctx, env.Q, env.Caller.StoreID, ids)
	if err != nil {
		return nil, err
	}
	view := rotationView(queue, day, turns, refs)
	view.Queue.Members = make([]Member, 0, len(members))
	for _, member := range members {
		view.Queue.Members = append(view.Queue.Members, Member{Staff: refs[member.StaffID], SortOrder: int(member.SortOrder)})
	}
	return &view, nil
}

func joinRotation(ctx context.Context, env ops.Env, in *JoinRotationInput) (*Rotation, error) {
	queue, day, _, turns, err := daySetup(ctx, env, in.QueueID, true)
	if err != nil {
		return nil, err
	}
	if err := requireMember(ctx, env, queue.ID, in.StaffID); err != nil {
		return nil, err
	}
	if err := staff.LockAssignable(ctx, env.Q, env.Caller.StoreID, in.StaffID); err != nil {
		if errors.Is(err, staff.ErrNotAssignable) {
			return nil, ops.Rule("员工必须是本店在职员工")
		}
		return nil, err
	}
	at := indexOf(turns, in.StaffID)
	if at >= 0 {
		turn := &turns[at]
		if turn.turn.Status != StatusOff {
			return nil, ops.Rule("员工已经在轮牌中")
		}
		setWaiting(turn)
		if err := recordEvent(ctx, env, queue.ID, day, &in.StaffID, "join", nil, nil, position(turn.turn.Position), position(turn.turn.Position), nil, nil, nil); err != nil {
			return nil, err
		}
	} else {
		max, err := env.Q.MaxRotationPosition(ctx, dbgen.MaxRotationPositionParams{StoreID: env.Caller.StoreID, QueueID: queue.ID, BusinessDate: dateValue(day)})
		if err != nil {
			return nil, err
		}
		_, err = env.Q.InsertRotationTurn(ctx, dbgen.InsertRotationTurnParams{
			TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, QueueID: queue.ID,
			BusinessDate: dateValue(day), StaffID: in.StaffID, Position: max + 1, Status: StatusWaiting,
		})
		if err != nil {
			return nil, err
		}
		turns, err = loadTurns(ctx, env, queue.ID, day)
		if err != nil {
			return nil, err
		}
		if err := recordEvent(ctx, env, queue.ID, day, &in.StaffID, "join", nil, nil, nil, position(max+1), nil, nil, nil); err != nil {
			return nil, err
		}
	}
	if err := persistTurns(ctx, env, queue.ID, day, turns, true); err != nil {
		return nil, err
	}
	return finish(ctx, env, queue, day, turns)
}

func leaveRotation(ctx context.Context, env ops.Env, in *LeaveRotationInput) (*Rotation, error) {
	queue, day, _, turns, err := daySetup(ctx, env, in.QueueID, true)
	if err != nil {
		return nil, err
	}
	if err := requireMember(ctx, env, queue.ID, in.StaffID); err != nil {
		return nil, err
	}
	index := indexOf(turns, in.StaffID)
	if index < 0 || turns[index].turn.Status == StatusOff {
		return nil, ops.Rule("员工不在轮牌中")
	}
	turn := &turns[index]
	if turn.turn.Status == StatusServing {
		return nil, ops.Rule("正在服务的员工不能离开轮牌")
	}
	turn.turn.Status = StatusOff
	turn.turn.RestUntil = timeValue(nil)
	turn.dirty = true
	if err := recordEvent(ctx, env, queue.ID, day, &in.StaffID, "leave", nil, nil, position(turn.turn.Position), position(turn.turn.Position), nil, nil, nil); err != nil {
		return nil, err
	}
	if err := persistTurns(ctx, env, queue.ID, day, turns, true); err != nil {
		return nil, err
	}
	return finish(ctx, env, queue, day, turns)
}

func assignTurn(ctx context.Context, env ops.Env, in *AssignTurnInput) (*Rotation, error) {
	if in.OrderID != nil {
		if err := lockOpenOrder(ctx, env, *in.OrderID); err != nil {
			return nil, err
		}
	}
	queue, day, rules, turns, err := daySetup(ctx, env, in.QueueID, true)
	if err != nil {
		return nil, err
	}
	var chosen *turnState
	if in.StaffID != nil {
		if err := requireMember(ctx, env, queue.ID, *in.StaffID); err != nil {
			return nil, err
		}
		if err := staff.LockAssignable(ctx, env.Q, env.Caller.StoreID, *in.StaffID); err != nil {
			if errors.Is(err, staff.ErrNotAssignable) {
				return nil, ops.Rule("员工必须是本店在职员工")
			}
			return nil, err
		}
		index := indexOf(turns, *in.StaffID)
		if index < 0 {
			return nil, ops.Rule("员工不在轮牌中")
		}
		if turns[index].turn.Status != StatusWaiting {
			return nil, ops.Rule("员工当前不能接客")
		}
		chosen = &turns[index]
	} else {
		activeMembers, err := queueActiveMembers(ctx, env, queue.ID)
		if err != nil {
			return nil, err
		}
		candidate := -1
		for i := range turns {
			if turns[i].turn.Status == StatusWaiting && activeMembers[turns[i].turn.StaffID] {
				candidate = i
				break
			}
		}
		if candidate < 0 {
			return nil, ops.Rule("没有可接客的员工")
		}
		candidateID := turns[candidate].turn.StaffID
		autoSkip := []uuid.UUID{}
		if rules.RotationSkipWhenServingDesignated() == "yes" {
			for i := 0; i < candidate; i++ {
				if turns[i].turn.Status == StatusServing && turns[i].turn.ServingDesignated && activeMembers[turns[i].turn.StaffID] {
					autoSkip = append(autoSkip, turns[i].turn.StaffID)
				}
			}
		}
		for _, staffID := range autoSkip {
			index := indexOf(turns, staffID)
			from := turns[index].turn.Position
			to := from
			if rules.RotationSkipPolicy() == "to_tail" {
				from, to, _ = moveToTail(turns, staffID)
			}
			reason := AutoSkipReason
			if err := recordEvent(ctx, env, queue.ID, day, &staffID, "auto_skip", &reason, nil, position(from), position(to), nil, nil, nil); err != nil {
				return nil, err
			}
		}
		chosen = &turns[indexOf(turns, candidateID)]
	}
	chosen.turn.Status = StatusServing
	chosen.turn.ServingOrderID = in.OrderID
	chosen.turn.ServingDesignated = in.StaffID != nil && in.Designated
	chosen.dirty = true
	if err := recordEvent(ctx, env, queue.ID, day, &chosen.turn.StaffID, "assign", nil, nil, position(chosen.turn.Position), position(chosen.turn.Position), nil, in.OrderID, &chosen.turn.ServingDesignated); err != nil {
		return nil, err
	}
	if err := persistTurns(ctx, env, queue.ID, day, turns, true); err != nil {
		return nil, err
	}
	return finish(ctx, env, queue, day, turns)
}

func releaseTurn(ctx context.Context, env ops.Env, in *ReleaseTurnInput) (*Rotation, error) {
	queue, day, _, turns, err := daySetup(ctx, env, in.QueueID, false)
	if err != nil {
		return nil, err
	}
	if err := requireMember(ctx, env, queue.ID, in.StaffID); err != nil {
		return nil, err
	}
	index := indexOf(turns, in.StaffID)
	if index < 0 {
		return nil, ops.Rule("员工不在轮牌中")
	}
	turn := &turns[index]
	if turn.turn.Status != StatusServing {
		return nil, ops.Rule("员工当前状态不能执行这个操作")
	}
	setWaiting(turn)
	clearServing(turn)
	if err := recordEvent(ctx, env, queue.ID, day, &in.StaffID, "release", nil, nil, position(turn.turn.Position), position(turn.turn.Position), nil, nil, nil); err != nil {
		return nil, err
	}
	if err := persistTurns(ctx, env, queue.ID, day, turns, true); err != nil {
		return nil, err
	}
	return finish(ctx, env, queue, day, turns)
}

func skipStaff(ctx context.Context, env ops.Env, in *SkipStaffInput) (*Rotation, error) {
	var note *string
	if in.Note != nil {
		trimmed := strings.TrimSpace(*in.Note)
		if trimmed == "" {
			return nil, &ops.ToolError{Messages: []string{"跳过说明不能为空"}}
		}
		note = &trimmed
	}
	if in.Reason == SkipReasonOther && note == nil {
		return nil, ops.Rule("跳过原因为其他时要填写说明")
	}
	queue, day, rules, turns, err := daySetup(ctx, env, in.QueueID, true)
	if err != nil {
		return nil, err
	}
	if err := requireMember(ctx, env, queue.ID, in.StaffID); err != nil {
		return nil, err
	}
	index := indexOf(turns, in.StaffID)
	if index < 0 {
		return nil, ops.Rule("员工不在轮牌中")
	}
	turn := &turns[index]
	if turn.turn.Status != StatusWaiting {
		return nil, ops.Rule("员工当前状态不能执行这个操作")
	}
	from := turn.turn.Position
	to := from
	if rules.RotationSkipPolicy() == "to_tail" {
		from, to, _ = moveToTail(turns, in.StaffID)
	}
	reason := in.Reason
	if err := recordEvent(ctx, env, queue.ID, day, &in.StaffID, "skip", &reason, note, position(from), position(to), nil, nil, nil); err != nil {
		return nil, err
	}
	if err := persistTurns(ctx, env, queue.ID, day, turns, true); err != nil {
		return nil, err
	}
	return finish(ctx, env, queue, day, turns)
}

func coverTurn(ctx context.Context, env ops.Env, in *CoverTurnInput) (*Rotation, error) {
	queue, day, _, turns, err := daySetup(ctx, env, in.QueueID, true)
	if err != nil {
		return nil, err
	}
	if err := requireMember(ctx, env, queue.ID, in.StaffID); err != nil {
		return nil, err
	}
	index := indexOf(turns, in.StaffID)
	if index < 0 {
		return nil, ops.Rule("员工不在轮牌中")
	}
	turn := &turns[index]
	if turn.turn.Status != StatusWaiting {
		return nil, ops.Rule("员工当前状态不能执行这个操作")
	}
	turn.turn.Status = StatusCovered
	turn.dirty = true
	if err := recordEvent(ctx, env, queue.ID, day, &in.StaffID, "cover", nil, nil, position(turn.turn.Position), position(turn.turn.Position), nil, nil, nil); err != nil {
		return nil, err
	}
	if err := persistTurns(ctx, env, queue.ID, day, turns, true); err != nil {
		return nil, err
	}
	return finish(ctx, env, queue, day, turns)
}

func uncoverTurn(ctx context.Context, env ops.Env, in *UncoverTurnInput) (*Rotation, error) {
	queue, day, _, turns, err := daySetup(ctx, env, in.QueueID, true)
	if err != nil {
		return nil, err
	}
	if err := requireMember(ctx, env, queue.ID, in.StaffID); err != nil {
		return nil, err
	}
	index := indexOf(turns, in.StaffID)
	if index < 0 {
		return nil, ops.Rule("员工不在轮牌中")
	}
	turn := &turns[index]
	if turn.turn.Status != StatusCovered {
		return nil, ops.Rule("员工当前状态不能执行这个操作")
	}
	setWaiting(turn)
	if err := recordEvent(ctx, env, queue.ID, day, &in.StaffID, "uncover", nil, nil, position(turn.turn.Position), position(turn.turn.Position), nil, nil, nil); err != nil {
		return nil, err
	}
	if err := persistTurns(ctx, env, queue.ID, day, turns, true); err != nil {
		return nil, err
	}
	return finish(ctx, env, queue, day, turns)
}

func restStaff(ctx context.Context, env ops.Env, in *RestStaffInput) (*Rotation, error) {
	queue, day, _, turns, err := daySetup(ctx, env, in.QueueID, true)
	if err != nil {
		return nil, err
	}
	if err := requireMember(ctx, env, queue.ID, in.StaffID); err != nil {
		return nil, err
	}
	index := indexOf(turns, in.StaffID)
	if index < 0 {
		return nil, ops.Rule("员工不在轮牌中")
	}
	turn := &turns[index]
	if turn.turn.Status != StatusWaiting {
		return nil, ops.Rule("员工当前状态不能执行这个操作")
	}
	until := env.Now.Add(time.Duration(in.Minutes) * time.Minute)
	turn.turn.Status = StatusResting
	turn.turn.RestUntil = timeValue(&until)
	turn.dirty = true
	minutes := int32(in.Minutes)
	if err := recordEvent(ctx, env, queue.ID, day, &in.StaffID, "rest", nil, nil, position(turn.turn.Position), position(turn.turn.Position), &minutes, nil, nil); err != nil {
		return nil, err
	}
	if err := persistTurns(ctx, env, queue.ID, day, turns, true); err != nil {
		return nil, err
	}
	return finish(ctx, env, queue, day, turns)
}

func dragTurn(ctx context.Context, env ops.Env, in *DragTurnInput) (*Rotation, error) {
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return nil, &ops.ToolError{Messages: []string{"拖动原因不能为空"}}
	}
	queue, day, _, turns, err := daySetup(ctx, env, in.QueueID, true)
	if err != nil {
		return nil, err
	}
	if err := requireMember(ctx, env, queue.ID, in.StaffID); err != nil {
		return nil, err
	}
	index := indexOf(turns, in.StaffID)
	if index < 0 {
		return nil, ops.Rule("员工不在轮牌中")
	}
	if turns[index].turn.Status != StatusWaiting {
		return nil, ops.Rule("员工当前状态不能执行这个操作")
	}
	if in.ToPosition > len(turns) {
		return nil, ops.Rule("目标位置超出轮牌范围")
	}
	from, to, ok := moveToPosition(turns, in.StaffID, in.ToPosition)
	if !ok {
		return nil, ops.Rule("目标位置超出轮牌范围")
	}
	if err := recordEvent(ctx, env, queue.ID, day, &in.StaffID, "drag", &reason, nil, position(from), position(to), nil, nil, nil); err != nil {
		return nil, err
	}
	if err := persistTurns(ctx, env, queue.ID, day, turns, true); err != nil {
		return nil, err
	}
	return finish(ctx, env, queue, day, turns)
}
