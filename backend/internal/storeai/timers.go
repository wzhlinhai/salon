package storeai

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// ScheduleFollowUp 给一个对象定一个跟进；同一收件箱同一对象已有 scheduled 的跟进时替换它。
func ScheduleFollowUp(ctx context.Context, q *dbgen.Queries, now time.Time, inbox dbgen.AiInbox, subject Subject, dueAt time.Time, payload json.RawMessage) (dbgen.AiTimer, error) {
	if subject.Empty() || !subject.Valid() {
		return dbgen.AiTimer{}, ErrInvalidEvent
	}
	if !dueAt.After(now) {
		return dbgen.AiTimer{}, ErrFollowUpDue
	}
	if payload == nil {
		payload = json.RawMessage(`{}`)
	}
	body, err := objectPayload(payload)
	if err != nil {
		return dbgen.AiTimer{}, err
	}
	kind, id := subjectColumns(subject)
	return q.UpsertFollowUpTimer(ctx, dbgen.UpsertFollowUpTimerParams{
		TenantID: inbox.TenantID, StoreID: inbox.StoreID, InboxID: inbox.ID,
		SubjectKind: kind, SubjectID: id, DueAt: timestamp(dueAt), Payload: body, CreatedAt: timestamp(now),
	})
}

// CancelFollowUps 取消一个对象在这个收件箱里所有 scheduled 的跟进。
func CancelFollowUps(ctx context.Context, q *dbgen.Queries, inbox dbgen.AiInbox, subject Subject) error {
	if subject.Empty() || !subject.Valid() {
		return ErrInvalidEvent
	}
	kind, id := subjectColumns(subject)
	return q.CancelFollowUpTimers(ctx, dbgen.CancelFollowUpTimersParams{
		StoreID: inbox.StoreID, InboxID: inbox.ID, SubjectKind: kind, SubjectID: id,
	})
}

// SchedulePeriodic 按 periodic_key 建或更新一个周期定时器。
func SchedulePeriodic(ctx context.Context, q *dbgen.Queries, inbox dbgen.AiInbox, key string, firstDue time.Time, repeat string) (dbgen.AiTimer, error) {
	if key == "" || (repeat != "daily" && repeat != "weekly") {
		return dbgen.AiTimer{}, ErrInvalidPeriodic
	}
	keyCopy, repeatCopy := key, repeat
	return q.UpsertPeriodicTimer(ctx, dbgen.UpsertPeriodicTimerParams{
		TenantID: inbox.TenantID, StoreID: inbox.StoreID, InboxID: inbox.ID,
		PeriodicKey: &keyCopy, Repeat: &repeatCopy, DueAt: timestamp(firstDue), CreatedAt: timestamp(firstDue),
	})
}

// runScheduler 把到点的定时器变成收件箱事件，从不直接运行。跟进置 fired；
// 周期定时器把 due_at 挪到严格晚于 now 的下一次，错过的几次只发一条。
func runScheduler(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, now time.Time) error {
	timers, err := q.DueTimers(ctx, dbgen.DueTimersParams{StoreID: storeID, Now: timestamp(now)})
	if err != nil {
		return err
	}
	for _, timer := range timers {
		dueAt := timer.DueAt.Time
		kind := EventFollowUpDue
		switch timer.Kind {
		case "follow_up":
			if err := q.FireTimer(ctx, dbgen.FireTimerParams{ID: timer.ID, StoreID: storeID}); err != nil {
				return err
			}
		case "periodic":
			kind = EventPeriodicDue
			if timer.Repeat == nil {
				return ErrInvalidPeriodic
			}
			next, err := nextPeriodic(dueAt, now, *timer.Repeat)
			if err != nil {
				return err
			}
			if err := q.RescheduleTimer(ctx, dbgen.RescheduleTimerParams{ID: timer.ID, StoreID: storeID, DueAt: timestamp(next)}); err != nil {
				return err
			}
		default:
			return ErrInvalidPeriodic
		}
		subject := Subject{}
		if timer.SubjectKind != nil && timer.SubjectID != nil {
			subject = Subject{Kind: *timer.SubjectKind, ID: *timer.SubjectID}
		}
		payload := timerPayload(timer)
		if _, err := Emit(ctx, q, now, EmitInput{
			TenantID: timer.TenantID, StoreID: timer.StoreID, InboxID: timer.InboxID,
			Kind: kind, SourceKey: fmt.Sprintf("timer:%s:%d", timer.ID, dueAt.Unix()),
			Subject: subject, Payload: payload,
		}); err != nil {
			return err
		}
	}
	return nil
}

func timerPayload(timer dbgen.AiTimer) json.RawMessage {
	body := map[string]any{
		"timer_id": timer.ID,
		"payload":  json.RawMessage(timer.Payload),
	}
	if timer.SubjectKind != nil && timer.SubjectID != nil {
		body["subject_kind"] = *timer.SubjectKind
		body["subject_id"] = *timer.SubjectID
	}
	if timer.PeriodicKey != nil {
		body["periodic_key"] = *timer.PeriodicKey
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

func nextPeriodic(dueAt, now time.Time, repeat string) (time.Time, error) {
	step := 1
	switch repeat {
	case "daily":
	case "weekly":
		step = 7
	default:
		return time.Time{}, ErrInvalidPeriodic
	}
	next := dueAt.In(settings.Location)
	for !next.After(now) {
		next = next.AddDate(0, 0, step)
	}
	return next, nil
}
