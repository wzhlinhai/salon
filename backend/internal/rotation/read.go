package rotation

import (
	"context"
	"errors"

	"github.com/wzhlinhai/salon/backend/internal/daterange"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/settings"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

func getRotation(ctx context.Context, env ops.Env, in *GetRotationInput) (*Rotation, error) {
	queue, err := lockQueueAny(ctx, env, in.QueueID)
	if err != nil {
		return nil, err
	}
	day, err := rotationDate(ctx, env, in.Date)
	if err != nil {
		return nil, err
	}
	rules, err := settings.Effective(ctx, env.Q, env.Caller.StoreID, day)
	if err != nil {
		return nil, err
	}
	turns, changed, err := prepareTurns(ctx, env, queue, day, rules)
	if err != nil {
		return nil, err
	}
	if changed {
		if err := persistTurns(ctx, env, queue.ID, day, turns, false); err != nil {
			return nil, err
		}
	}
	return finish(ctx, env, queue, day, turns)
}

func rotationRange(ctx context.Context, env ops.Env, from, to string) (daterange.Query, error) {
	q, err := daterange.Parse(env.Caller, perm.QueueView, from, to, nil, 0)
	if err != nil {
		var rule *ops.RuleError
		if errors.As(err, &rule) {
			return q, &ops.ToolError{Messages: []string{rule.Message}}
		}
		return q, err
	}
	today, err := settings.BusinessDate(ctx, env.Q, env.Caller.StoreID, env.Now)
	if err != nil {
		return q, err
	}
	if q.To.After(today) {
		return q, &ops.ToolError{Messages: []string{"营业日不能晚于今天"}}
	}
	return q, nil
}

func intPtr(v *int32) *int {
	if v == nil {
		return nil
	}
	out := int(*v)
	return &out
}

func listEvents(ctx context.Context, env ops.Env, in *ListRotationEventsInput) (*RotationEventList, error) {
	q, err := rotationRange(ctx, env, in.From, in.To)
	if err != nil {
		return nil, err
	}
	rows, err := env.Q.ListRotationEvents(ctx, dbgen.ListRotationEventsParams{StoreID: env.Caller.StoreID, FromDate: dateValue(q.From), ToDate: dateValue(q.To), QueueID: in.QueueID})
	if err != nil {
		return nil, err
	}
	out := &RotationEventList{Entries: make([]RotationEvent, 0, len(rows))}
	for _, row := range rows {
		event := RotationEvent{
			ID: row.ID, QueueID: row.QueueID, BusinessDate: row.BusinessDate.Time.Format(settings.DateLayout), Kind: row.Kind,
			Reason: row.Reason, Note: row.Note, FromPosition: intPtr(row.FromPosition), ToPosition: intPtr(row.ToPosition),
			Minutes: intPtr(row.Minutes), OrderID: row.OrderID, Designated: row.Designated,
			Actor: staff.Ref{ID: row.ActorStaffID, Name: row.ActorName}, CreatedAt: row.CreatedAt.Time,
		}
		if row.StaffID != nil {
			event.Staff = &staff.Ref{ID: *row.StaffID, Name: *row.StaffName}
		}
		out.Entries = append(out.Entries, event)
	}
	return out, nil
}

func getStats(ctx context.Context, env ops.Env, in *GetRotationStatsInput) (*RotationStats, error) {
	q, err := rotationRange(ctx, env, in.From, in.To)
	if err != nil {
		return nil, err
	}
	rows, err := env.Q.RotationStats(ctx, dbgen.RotationStatsParams{TenantID: env.Caller.TenantID, StoreID: env.Caller.StoreID, FromDate: dateValue(q.From), ToDate: dateValue(q.To)})
	if err != nil {
		return nil, err
	}
	out := &RotationStats{From: in.From, To: in.To, Entries: make([]StaffRotationStat, 0, len(rows))}
	for _, row := range rows {
		if row.StaffID == nil {
			continue
		}
		entry := StaffRotationStat{Staff: staff.Ref{ID: *row.StaffID, Name: row.StaffName}, Designated: int(row.DesignatedCount), Rotation: int(row.RotationCount), Total: int(row.DesignatedCount + row.RotationCount)}
		out.Designated += entry.Designated
		out.Rotation += entry.Rotation
		out.Total += entry.Total
		out.Entries = append(out.Entries, entry)
	}
	return out, nil
}
