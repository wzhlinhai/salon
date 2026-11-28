package storeai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// HandoverAction 是一次转交检查的结果：不动、再提醒同一个人、或改交给别人。
type HandoverAction string

const (
	// HandoverNone 表示交办已经不在 open，什么都不做。
	HandoverNone HandoverAction = "none"
	// HandoverRemind 表示再提醒原收件人一次。
	HandoverRemind HandoverAction = "remind"
	// HandoverReassign 表示改交给另一位员工。
	HandoverReassign HandoverAction = "reassign"
)

// HandoverResult 是 NextHandover 的判断结果。Request 在提醒时是原条，改交时是新条。
type HandoverResult struct {
	Action       HandoverAction
	Request      dbgen.AiRequest
	NextFollowUp *time.Time
}

type staffDuty struct {
	firstAt time.Time
	onDuty  bool
}

type handoverStore struct {
	staff      map[uuid.UUID]dbgen.Staff
	duty       map[uuid.UUID]staffDuty
	roles      map[uuid.UUID]dbgen.Role
	staffRoles map[uuid.UUID][]uuid.UUID
}

// NextHandover 按门店设置判断一条交办下一步怎么办，并算出下一次跟进的时刻。
// 锁序与 A 步一致：先锁交办行，再读员工和打卡；改交时 ReassignRequest 再锁员工行。
func NextHandover(ctx context.Context, q *dbgen.Queries, now time.Time, requestID uuid.UUID) (HandoverResult, error) {
	request, err := q.LockRequest(ctx, requestID)
	if errors.Is(err, pgx.ErrNoRows) {
		return HandoverResult{}, ErrRequestNotFound
	}
	if err != nil {
		return HandoverResult{}, err
	}
	result := HandoverResult{Action: HandoverNone, Request: request}
	if request.Status != "open" {
		return result, nil
	}
	day, rules, err := settings.CurrentRules(ctx, q, request.StoreID, now)
	if err != nil {
		return HandoverResult{}, err
	}
	store, err := loadHandoverStore(ctx, q, request.StoreID, day)
	if err != nil {
		return HandoverResult{}, err
	}
	original := request.ToStaffID
	managerOrOwner := store.isManagerOrOwner(original)
	nearDue := request.DueAt.Valid && !request.DueAt.Time.After(now.Add(time.Duration(rules.AIHandoverManagerMarginMinutes())*time.Minute))
	originalActive := store.staff[original].Active

	var next dbgen.AiRequest
	if !originalActive {
		if target, ok := store.managerTarget(original); ok {
			next, err = reassignHandover(ctx, q, now, request, target)
		} else if target, ok := store.ownerTarget(original); ok {
			next, err = reassignHandover(ctx, q, now, request, target)
		} else {
			return HandoverResult{}, ErrNoHandoverTarget
		}
	} else {
		switch {
		case nearDue:
			if managerOrOwner {
				next, err = remindRequest(ctx, q, now, request)
			} else if target, ok := store.managerTarget(original); ok {
				next, err = reassignHandover(ctx, q, now, request, target)
			} else if target, ok := store.ownerTarget(original); ok {
				next, err = reassignHandover(ctx, q, now, request, target)
			} else {
				next, err = remindRequest(ctx, q, now, request)
			}
		case request.Attempt == 0:
			next, err = remindRequest(ctx, q, now, request)
		default:
			if managerOrOwner {
				next, err = remindRequest(ctx, q, now, request)
			} else if excluded, historyErr := requestHistory(ctx, q, request); historyErr != nil {
				return HandoverResult{}, historyErr
			} else if target, ok := store.samePositionTarget(request, excluded); ok {
				next, err = reassignHandover(ctx, q, now, request, target)
			} else if target, ok := store.managerTarget(original); ok {
				next, err = reassignHandover(ctx, q, now, request, target)
			} else if target, ok := store.ownerTarget(original); ok {
				next, err = reassignHandover(ctx, q, now, request, target)
			} else {
				next, err = remindRequest(ctx, q, now, request)
			}
		}
	}
	if err != nil {
		return HandoverResult{}, err
	}
	if next.ID == request.ID {
		result.Action = HandoverRemind
	} else {
		result.Action = HandoverReassign
	}
	result.Request = next
	result.NextFollowUp = nextHandoverFollowUp(now, request.DueAt, rules)
	return result, nil
}

func loadHandoverStore(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, day time.Time) (*handoverStore, error) {
	punches, err := q.AttendancePunches(ctx, dbgen.AttendancePunchesParams{
		StoreID: storeID, FirstDay: pgtype.Date{Time: day, Valid: true}, LastDay: pgtype.Date{Time: day, Valid: true},
	})
	if err != nil {
		return nil, err
	}
	valid := map[uuid.UUID][]dbgen.Punch{}
	for _, punch := range punches {
		if punch.VoidedAt.Valid {
			continue
		}
		valid[punch.StaffID] = append(valid[punch.StaffID], punch)
	}
	rows, err := q.ListPayrollStaff(ctx, storeID)
	if err != nil {
		return nil, err
	}
	staff := make(map[uuid.UUID]dbgen.Staff, len(rows))
	duty := map[uuid.UUID]staffDuty{}
	for _, row := range rows {
		staff[row.ID] = row
		if !row.Active {
			continue
		}
		// 采用 attendance.calculate 的现有口径：当天第一条有效打卡是上班，
		// 出现第二条时最后一条视为下班；因此当班 = 当天恰好只有一条有效打卡。
		if dayPunches := valid[row.ID]; len(dayPunches) == 1 {
			duty[row.ID] = staffDuty{firstAt: dayPunches[0].At.Time, onDuty: true}
		}
	}
	roleRows, err := q.ListRoles(ctx, storeID)
	if err != nil {
		return nil, err
	}
	roles := make(map[uuid.UUID]dbgen.Role, len(roleRows))
	for _, role := range roleRows {
		roles[role.ID] = role
	}
	assignments, err := q.ListAllStaffRoles(ctx, storeID)
	if err != nil {
		return nil, err
	}
	staffRoles := map[uuid.UUID][]uuid.UUID{}
	for _, row := range assignments {
		staffRoles[row.StaffID] = append(staffRoles[row.StaffID], row.RoleID)
	}
	return &handoverStore{staff: staff, duty: duty, roles: roles, staffRoles: staffRoles}, nil
}

func (s *handoverStore) isManagerOrOwner(staffID uuid.UUID) bool {
	row, ok := s.staff[staffID]
	if !ok || !row.Active {
		return false
	}
	for _, roleID := range s.staffRoles[staffID] {
		role, ok := s.roles[roleID]
		if ok && (role.IsOwner || role.Name == perm.ManagerRoleName) {
			return true
		}
	}
	return false
}

// managerTarget 选当班店长：当天上班且之后没下班、角色名为「店长」，
// 多位时取当天最早打卡的一位，再按 id。
func (s *handoverStore) managerTarget(original uuid.UUID) (uuid.UUID, bool) {
	return s.earliestOnDuty(map[uuid.UUID]bool{original: true}, func(staffID uuid.UUID) bool {
		for _, roleID := range s.staffRoles[staffID] {
			if role, ok := s.roles[roleID]; ok && role.Name == perm.ManagerRoleName {
				return true
			}
		}
		return false
	})
}

// samePositionTarget 选当班、同岗位的另一位在职员工，排除当前收件人和 previous_id 链上的人，
// 按当天最早打卡，再按 id。
func (s *handoverStore) samePositionTarget(request dbgen.AiRequest, excluded map[uuid.UUID]bool) (uuid.UUID, bool) {
	original, ok := s.staff[request.ToStaffID]
	if !ok || original.PositionID == nil {
		return uuid.Nil, false
	}
	return s.earliestOnDuty(excluded, func(staffID uuid.UUID) bool {
		row, ok := s.staff[staffID]
		return ok && row.Active && row.PositionID != nil && *row.PositionID == *original.PositionID
	})
}

func (s *handoverStore) earliestOnDuty(excluded map[uuid.UUID]bool, match func(uuid.UUID) bool) (uuid.UUID, bool) {
	var best uuid.UUID
	var bestAt time.Time
	for staffID, d := range s.duty {
		if excluded[staffID] || !d.onDuty || !match(staffID) {
			continue
		}
		if best == uuid.Nil || d.firstAt.Before(bestAt) || (d.firstAt.Equal(bestAt) && staffID.String() < best.String()) {
			best, bestAt = staffID, d.firstAt
		}
	}
	return best, best != uuid.Nil
}

// ownerTarget 选在职老板：不要求当班，多位时取最早建档的一位，再按 id。
func (s *handoverStore) ownerTarget(original uuid.UUID) (uuid.UUID, bool) {
	var best uuid.UUID
	var bestCreated time.Time
	for staffID, row := range s.staff {
		if staffID == original || !row.Active || !s.hasOwnerRole(staffID) {
			continue
		}
		created := row.CreatedAt.Time
		if best == uuid.Nil || created.Before(bestCreated) || (created.Equal(bestCreated) && staffID.String() < best.String()) {
			best, bestCreated = staffID, created
		}
	}
	return best, best != uuid.Nil
}

func requestHistory(ctx context.Context, q *dbgen.Queries, request dbgen.AiRequest) (map[uuid.UUID]bool, error) {
	excluded := map[uuid.UUID]bool{request.ToStaffID: true}
	seen := map[uuid.UUID]bool{request.ID: true}
	current := request
	for current.PreviousID != nil {
		if seen[*current.PreviousID] {
			break
		}
		previous, err := q.GetRequest(ctx, *current.PreviousID)
		if errors.Is(err, pgx.ErrNoRows) {
			break
		}
		if err != nil {
			return nil, err
		}
		seen[previous.ID] = true
		excluded[previous.ToStaffID] = true
		current = previous
	}
	return excluded, nil
}

func (s *handoverStore) hasOwnerRole(staffID uuid.UUID) bool {
	for _, roleID := range s.staffRoles[staffID] {
		if role, ok := s.roles[roleID]; ok && role.IsOwner {
			return true
		}
	}
	return false
}

func remindRequest(ctx context.Context, q *dbgen.Queries, now time.Time, request dbgen.AiRequest) (dbgen.AiRequest, error) {
	if _, err := q.GetActiveStaffForShare(ctx, dbgen.GetActiveStaffForShareParams{
		TenantID: request.TenantID, StoreID: request.StoreID, StaffID: request.ToStaffID,
	}); errors.Is(err, pgx.ErrNoRows) {
		return dbgen.AiRequest{}, ErrRecipientNotFound
	} else if err != nil {
		return dbgen.AiRequest{}, err
	}
	updated, err := q.IncrementRequestAttempt(ctx, dbgen.IncrementRequestAttemptParams{ID: request.ID, StoreID: request.StoreID})
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.AiRequest{}, ErrRequestClosed
	}
	if err != nil {
		return dbgen.AiRequest{}, err
	}
	inbox, err := EnsureInbox(ctx, q, request.StoreID, &updated.ToStaffID)
	if err != nil {
		return dbgen.AiRequest{}, err
	}
	body := map[string]any{
		"request_id": updated.ID,
		"kind":       updated.Kind,
		"subject":    map[string]any{"kind": updated.SubjectKind, "id": updated.SubjectID},
		"options":    requestOptions(updated.Options),
		"due_at":     nil,
		"attempt":    updated.Attempt,
	}
	if updated.DueAt.Valid {
		body["due_at"] = updated.DueAt.Time
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return dbgen.AiRequest{}, err
	}
	if _, err := Emit(ctx, q, now, EmitInput{
		TenantID: updated.TenantID, StoreID: updated.StoreID, InboxID: inbox.ID,
		Kind: EventRequest, SourceKey: fmt.Sprintf("request:%s:remind:%d", updated.ID, updated.Attempt),
		Subject: Subject{Kind: updated.SubjectKind, ID: updated.SubjectID}, Payload: payload,
	}); err != nil {
		return dbgen.AiRequest{}, err
	}
	return updated, nil
}

func reassignHandover(ctx context.Context, q *dbgen.Queries, now time.Time, request dbgen.AiRequest, target uuid.UUID) (dbgen.AiRequest, error) {
	if err := WithdrawRemindersFor(ctx, q, now, request.StoreID, request.ToStaffID, Subject{Kind: request.SubjectKind, ID: request.SubjectID}); err != nil {
		return dbgen.AiRequest{}, err
	}
	next, err := ReassignRequest(ctx, q, now, request.ID, target)
	if err != nil {
		return dbgen.AiRequest{}, err
	}
	return next, nil
}

func nextHandoverFollowUp(now time.Time, dueAt pgtype.Timestamptz, rules settings.Rules) *time.Time {
	if dueAt.Valid && !now.Before(dueAt.Time) {
		return nil
	}
	next := now.Add(time.Duration(rules.AIHandoverRemindMinutes()) * time.Minute)
	if dueAt.Valid {
		edge := dueAt.Time.Add(-time.Duration(rules.AIHandoverManagerMarginMinutes()) * time.Minute)
		if edge.After(now) && edge.Before(next) {
			next = edge
		} else if !edge.After(now) && dueAt.Time.Before(next) {
			next = dueAt.Time
		}
	}
	return &next
}
