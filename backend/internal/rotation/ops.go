package rotation

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/catalog"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

// Register 声明轮牌队列、轮牌操作、事件和统计。
func Register(r *ops.Registry) {
	ops.Register(r, ops.Operation[CreateRotationQueueInput, Queue]{Name: "create_rotation_queue", Summary: "新建轮牌队列，例如剪发牌、助理牌", Kind: ops.Write, Permission: perm.QueueOperate, AI: ops.AIDraft, Handle: createQueue})
	ops.Register(r, ops.Operation[UpdateRotationQueueInput, Queue]{Name: "update_rotation_queue", Summary: "轮牌队列改名或停用", Kind: ops.Write, Permission: perm.QueueOperate, AI: ops.AIDraft, Handle: updateQueue})
	ops.Register(r, ops.Operation[SetQueueMembersInput, Queue]{Name: "set_queue_members", Summary: "替换轮牌队列成员及其默认顺序", Kind: ops.Write, Permission: perm.QueueOperate, AI: ops.AIDraft, Handle: setQueueMembers})
	ops.Register(r, ops.Operation[ListRotationQueuesInput, QueueList]{Name: "list_rotation_queues", Summary: "列出本店轮牌队列和成员", Kind: ops.Read, Permission: perm.QueueView, AI: ops.AIDirect, Handle: listQueues})
	ops.Register(r, ops.Operation[GetRotationInput, Rotation]{Name: "get_rotation", Summary: "查看一个轮牌队列在某个营业日的有序名单", Kind: ops.Read, Permission: perm.QueueView, AI: ops.AIDirect, Handle: getRotation})
	ops.Register(r, ops.Operation[JoinRotationInput, Rotation]{Name: "join_rotation", Summary: "员工到岗加入轮牌，保留预排的位置", Kind: ops.Write, Permission: perm.QueueOperate, AI: ops.AIDraft, Handle: joinRotation})
	ops.Register(r, ops.Operation[LeaveRotationInput, Rotation]{Name: "leave_rotation", Summary: "员工离岗，保留当天位置但不再接客", Kind: ops.Write, Permission: perm.QueueOperate, AI: ops.AIDraft, Handle: leaveRotation})
	ops.Register(r, ops.Operation[AssignTurnInput, Rotation]{Name: "assign_turn", Summary: "轮牌自动取下一人，或点客指定员工，开始服务", Kind: ops.Write, Permission: perm.QueueOperate, AI: ops.AIDraft, Handle: assignTurn})
	ops.Register(r, ops.Operation[ReleaseTurnInput, Rotation]{Name: "release_turn", Summary: "撤销这次接客，不消耗轮牌", Kind: ops.Write, Permission: perm.QueueOperate, AI: ops.AIDraft, Handle: releaseTurn})
	ops.Register(r, ops.Operation[SkipStaffInput, Rotation]{Name: "skip_staff", Summary: "跳过员工并记录原因，按设置排到队尾或保留原位", Kind: ops.Write, Permission: perm.QueueOperate, AI: ops.AIDraft, Handle: skipStaff})
	ops.Register(r, ops.Operation[CoverTurnInput, Rotation]{Name: "cover_turn", Summary: "盖牌：保留位置但暂不接客", Kind: ops.Write, Permission: perm.QueueOperate, AI: ops.AIDraft, Handle: coverTurn})
	ops.Register(r, ops.Operation[UncoverTurnInput, Rotation]{Name: "uncover_turn", Summary: "取消盖牌，恢复等待", Kind: ops.Write, Permission: perm.QueueOperate, AI: ops.AIDraft, Handle: uncoverTurn})
	ops.Register(r, ops.Operation[RestStaffInput, Rotation]{Name: "rest_staff", Summary: "临时休息指定分钟数，到点自动恢复等待", Kind: ops.Write, Permission: perm.QueueOperate, AI: ops.AIDraft, Handle: restStaff})
	ops.Register(r, ops.Operation[DragTurnInput, Rotation]{Name: "drag_turn", Summary: "手动拖动员工到指定位置并记录原因", Kind: ops.Write, Permission: perm.QueueOperate, AI: ops.AIDraft, Handle: dragTurn})
	ops.Register(r, ops.Operation[SetHalfTurnItemsInput, HalfTurnItemList]{Name: "set_half_turn_items", Summary: "设置按项目记半轮的项目清单", Kind: ops.Write, Permission: perm.QueueOperate, AI: ops.AIDraft, Handle: setHalfTurnItems})
	ops.Register(r, ops.Operation[ListRotationEventsInput, RotationEventList]{Name: "list_rotation_events", Summary: "按日期范围查看轮牌跳过、拖动、接客和推进记录", Kind: ops.Read, Permission: perm.QueueView, AI: ops.AIDirect, Handle: listEvents})
	ops.Register(r, ops.Operation[GetRotationStatsInput, RotationStats]{Name: "get_rotation_stats", Summary: "按员工统计点客和轮牌接客数", Kind: ops.Read, Permission: perm.QueueView, AI: ops.AIDirect, Handle: getStats})
}

func queueView(ctx context.Context, env ops.Env, row dbgen.RotationQueue) (Queue, error) {
	members, err := env.Q.ListRotationMembers(ctx, dbgen.ListRotationMembersParams{StoreID: env.Caller.StoreID, QueueID: row.ID})
	if err != nil {
		return Queue{}, err
	}
	ids := []uuid.UUID{row.CreatedBy}
	for _, m := range members {
		ids = append(ids, m.StaffID)
	}
	refs, err := names(ctx, env.Q, env.Caller.StoreID, ids)
	if err != nil {
		return Queue{}, err
	}
	out := Queue{ID: row.ID, Name: row.Name, Active: row.Active, CreatedBy: refs[row.CreatedBy], CreatedAt: row.CreatedAt.Time, Members: make([]Member, 0, len(members))}
	for _, m := range members {
		out.Members = append(out.Members, Member{Staff: refs[m.StaffID], SortOrder: int(m.SortOrder)})
	}
	return out, nil
}

func uniqueQueueError(err error) error {
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return ops.Rule("轮牌队列名称已存在")
	}
	return err
}

func createQueue(ctx context.Context, env ops.Env, in *CreateRotationQueueInput) (*Queue, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, ops.Rule("轮牌队列名称不能为空")
	}
	row, err := env.Q.CreateRotationQueue(ctx, dbgen.CreateRotationQueueParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, Name: name, CreatedBy: env.Caller.StaffID})
	if err != nil {
		return nil, uniqueQueueError(err)
	}
	view, err := queueView(ctx, env, row)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "create_rotation_queue", audit.RotationQueueTarget(row.ID), nil, view); err != nil {
		return nil, err
	}
	return &view, nil
}

func updateQueue(ctx context.Context, env ops.Env, in *UpdateRotationQueueInput) (*Queue, error) {
	if in.Name == nil && in.Active == nil {
		return nil, ops.Rule("没有要修改的内容")
	}
	var row dbgen.RotationQueue
	var err error
	if in.Name != nil {
		row, err = lockQueueForRename(ctx, env, in.ID)
	} else {
		row, err = lockQueueAny(ctx, env, in.ID)
	}
	if err != nil {
		return nil, err
	}
	before, err := queueView(ctx, env, row)
	if err != nil {
		return nil, err
	}
	name := row.Name
	if in.Name != nil {
		name = strings.TrimSpace(*in.Name)
		if name == "" {
			return nil, ops.Rule("轮牌队列名称不能为空")
		}
	}
	active := row.Active
	if in.Active != nil {
		active = *in.Active
	}
	row, err = env.Q.UpdateRotationQueue(ctx, dbgen.UpdateRotationQueueParams{StoreID: env.Caller.StoreID, ID: in.ID, Name: name, Active: active})
	if err != nil {
		return nil, uniqueQueueError(err)
	}
	view, err := queueView(ctx, env, row)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "update_rotation_queue", audit.RotationQueueTarget(row.ID), before, view); err != nil {
		return nil, err
	}
	return &view, nil
}

func setQueueMembers(ctx context.Context, env ops.Env, in *SetQueueMembersInput) (*Queue, error) {
	row, err := lockQueue(ctx, env, in.QueueID)
	if err != nil {
		return nil, err
	}
	ids := uniqueIDs(in.StaffIDs)
	if err := staff.LockAssignable(ctx, env.Q, env.Caller.StoreID, ids...); err != nil {
		if errors.Is(err, staff.ErrNotAssignable) {
			return nil, ops.Rule("员工必须是本店在职员工")
		}
		return nil, err
	}
	before, err := queueView(ctx, env, row)
	if err != nil {
		return nil, err
	}
	existing, err := env.Q.ListRotationMembers(ctx, dbgen.ListRotationMembersParams{StoreID: env.Caller.StoreID, QueueID: row.ID})
	if err != nil {
		return nil, err
	}
	existingByStaff := make(map[uuid.UUID]dbgen.ListRotationMembersRow, len(existing))
	for _, member := range existing {
		existingByStaff[member.StaffID] = member
	}
	desired := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		desired[id] = true
	}
	for _, member := range existing {
		if desired[member.StaffID] {
			continue
		}
		if err := env.Q.DeleteRotationMember(ctx, dbgen.DeleteRotationMemberParams{StoreID: env.Caller.StoreID, QueueID: row.ID, StaffID: member.StaffID}); err != nil {
			return nil, err
		}
	}
	changed := []uuid.UUID{}
	for i, id := range ids {
		member, ok := existingByStaff[id]
		if ok && member.SortOrder == int32(i+1) {
			continue
		}
		changed = append(changed, id)
	}
	for _, id := range changed {
		if _, ok := existingByStaff[id]; !ok {
			continue
		}
		if err := env.Q.ShiftRotationMemberOrder(ctx, dbgen.ShiftRotationMemberOrderParams{StoreID: env.Caller.StoreID, QueueID: row.ID, StaffID: id}); err != nil {
			return nil, err
		}
	}
	for i, id := range ids {
		if _, ok := existingByStaff[id]; ok {
			continue
		}
		if err := env.Q.AddRotationMember(ctx, dbgen.AddRotationMemberParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, QueueID: row.ID, StaffID: id, SortOrder: int32(i + 1)}); err != nil {
			return nil, err
		}
	}
	for i, id := range ids {
		member, ok := existingByStaff[id]
		if !ok || member.SortOrder == int32(i+1) {
			continue
		}
		if err := env.Q.UpdateRotationMemberOrder(ctx, dbgen.UpdateRotationMemberOrderParams{StoreID: env.Caller.StoreID, QueueID: row.ID, StaffID: id, SortOrder: int32(i + 1)}); err != nil {
			return nil, err
		}
	}
	view, err := queueView(ctx, env, row)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "set_queue_members", audit.RotationQueueTarget(row.ID), before, view); err != nil {
		return nil, err
	}
	return &view, nil
}

func listQueues(ctx context.Context, env ops.Env, _ *ListRotationQueuesInput) (*QueueList, error) {
	rows, err := env.Q.ListRotationQueues(ctx, env.Caller.StoreID)
	if err != nil {
		return nil, err
	}
	members, err := env.Q.ListAllRotationMembers(ctx, env.Caller.StoreID)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(rows)+len(members))
	for _, row := range rows {
		ids = append(ids, row.CreatedBy)
	}
	for _, m := range members {
		ids = append(ids, m.StaffID)
	}
	refs, err := names(ctx, env.Q, env.Caller.StoreID, ids)
	if err != nil {
		return nil, err
	}
	byQueue := map[uuid.UUID][]Member{}
	for _, m := range members {
		byQueue[m.QueueID] = append(byQueue[m.QueueID], Member{Staff: refs[m.StaffID], SortOrder: int(m.SortOrder)})
	}
	out := &QueueList{Entries: make([]Queue, 0, len(rows))}
	for _, row := range rows {
		ms := byQueue[row.ID]
		if ms == nil {
			ms = []Member{}
		}
		out.Entries = append(out.Entries, Queue{ID: row.ID, Name: row.Name, Active: row.Active, CreatedBy: refs[row.CreatedBy], CreatedAt: row.CreatedAt.Time, Members: ms})
	}
	return out, nil
}

func uniqueIDs(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]bool, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func setHalfTurnItems(ctx context.Context, env ops.Env, in *SetHalfTurnItemsInput) (*HalfTurnItemList, error) {
	if err := env.Q.LockRotationHalfItems(ctx, env.Caller.StoreID); err != nil {
		return nil, err
	}
	ids := uniqueIDs(in.ItemIDs)
	if _, err := catalog.LockItems(ctx, env.Q, env.Caller.StoreID, ids...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ops.Rule("项目不存在")
		}
		return nil, err
	}
	before, err := halfItems(ctx, env)
	if err != nil {
		return nil, err
	}
	existing, err := env.Q.ListRotationHalfItems(ctx, env.Caller.StoreID)
	if err != nil {
		return nil, err
	}
	existingByItem := make(map[uuid.UUID]bool, len(existing))
	for _, row := range existing {
		existingByItem[row.ItemID] = true
	}
	desired := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		desired[id] = true
	}
	for _, row := range existing {
		if desired[row.ItemID] {
			continue
		}
		if err := env.Q.DeleteRotationHalfItem(ctx, dbgen.DeleteRotationHalfItemParams{StoreID: env.Caller.StoreID, ItemID: row.ItemID}); err != nil {
			return nil, err
		}
	}
	for _, id := range ids {
		if existingByItem[id] {
			continue
		}
		if err := env.Q.AddRotationHalfItem(ctx, dbgen.AddRotationHalfItemParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, ItemID: id}); err != nil {
			return nil, err
		}
	}
	after, err := halfItems(ctx, env)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, "set_half_turn_items", audit.RotationQueueTarget(env.Caller.StoreID), before, after); err != nil {
		return nil, err
	}
	return after, nil
}

func halfItems(ctx context.Context, env ops.Env) (*HalfTurnItemList, error) {
	rows, err := env.Q.ListRotationHalfItems(ctx, env.Caller.StoreID)
	if err != nil {
		return nil, err
	}
	out := &HalfTurnItemList{Entries: make([]HalfTurnItem, 0, len(rows))}
	for _, row := range rows {
		out.Entries = append(out.Entries, HalfTurnItem{ItemID: row.ItemID, Name: row.ItemName})
	}
	return out, nil
}
