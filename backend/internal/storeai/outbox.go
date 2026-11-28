package storeai

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
)

// PutReminderInput 是写一条 AI 提醒所需的字段。
type PutReminderInput struct {
	StoreID     uuid.UUID
	RecipientID uuid.UUID
	Tier        string
	Subject     Subject
	Body        string
	SessionID   *uuid.UUID
}

// PutReminder 写一条 AI 提醒：同一收件人同一对象还没发就替换，已经发过就接在话题父条下面，
// 都没有就新建话题父条。同一对象的并发写入用事务咨询锁串行。
func PutReminder(ctx context.Context, q *dbgen.Queries, now time.Time, in PutReminderInput) (dbgen.AiOutbox, error) {
	if in.Tier != "now" && in.Tier != "daily" && in.Tier != "weekly" {
		return dbgen.AiOutbox{}, ErrInvalidReminder
	}
	if in.Subject.Empty() || !in.Subject.Valid() {
		return dbgen.AiOutbox{}, ErrInvalidReminder
	}
	if length := utf8.RuneCountInString(in.Body); length < 1 || length > 200 {
		return dbgen.AiOutbox{}, ErrInvalidReminder
	}
	tenantID, err := q.StoreTenant(ctx, in.StoreID)
	if err != nil {
		return dbgen.AiOutbox{}, err
	}
	if err := q.LockOutboxSubject(ctx, dbgen.LockOutboxSubjectParams{
		StoreID:     in.StoreID,
		SubjectKind: in.Subject.Kind, SubjectID: in.Subject.ID,
	}); err != nil {
		return dbgen.AiOutbox{}, err
	}
	if _, err := q.GetActiveStaffForShare(ctx, dbgen.GetActiveStaffForShareParams{
		TenantID: tenantID, StoreID: in.StoreID, StaffID: in.RecipientID,
	}); errors.Is(err, pgx.ErrNoRows) {
		return dbgen.AiOutbox{}, ErrRecipientNotFound
	} else if err != nil {
		return dbgen.AiOutbox{}, err
	}
	queued, err := q.GetQueuedOutbox(ctx, dbgen.GetQueuedOutboxParams{
		StoreID: in.StoreID, RecipientID: in.RecipientID, SubjectKind: in.Subject.Kind, SubjectID: in.Subject.ID,
	})
	if err == nil {
		replaced, replaceErr := q.ReplaceQueuedOutbox(ctx, dbgen.ReplaceQueuedOutboxParams{
			Body: in.Body, Tier: in.Tier, QueuedAt: timestamp(now), ID: queued.ID, StoreID: in.StoreID,
		})
		if replaceErr == nil {
			return replaced, nil
		}
		if !errors.Is(replaceErr, pgx.ErrNoRows) {
			return dbgen.AiOutbox{}, replaceErr
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return dbgen.AiOutbox{}, err
	}
	var parentID *uuid.UUID
	parent, err := q.GetSentOutboxParent(ctx, dbgen.GetSentOutboxParentParams{
		StoreID: in.StoreID, RecipientID: in.RecipientID, SubjectKind: in.Subject.Kind, SubjectID: in.Subject.ID,
	})
	if err == nil {
		parentID = &parent.ID
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return dbgen.AiOutbox{}, err
	}
	return q.InsertOutbox(ctx, dbgen.InsertOutboxParams{
		TenantID: tenantID, StoreID: in.StoreID, RecipientID: in.RecipientID, Tier: in.Tier,
		SubjectKind: in.Subject.Kind, SubjectID: in.Subject.ID, ParentID: parentID, Body: in.Body,
		SessionID: in.SessionID, QueuedAt: timestamp(now),
	})
}

// WithdrawReminders 撤回一个对象的所有未撤回提醒，各收件人都在内。
func WithdrawReminders(ctx context.Context, q *dbgen.Queries, now time.Time, storeID uuid.UUID, subject Subject) error {
	if subject.Empty() || !subject.Valid() {
		return ErrInvalidReminder
	}
	if err := q.LockOutboxSubject(ctx, dbgen.LockOutboxSubjectParams{
		StoreID: storeID, SubjectKind: subject.Kind, SubjectID: subject.ID,
	}); err != nil {
		return err
	}
	return q.WithdrawOutbox(ctx, dbgen.WithdrawOutboxParams{
		Now: timestamp(now), StoreID: storeID, SubjectKind: subject.Kind, SubjectID: subject.ID,
	})
}

// WithdrawRemindersFor 撤回一个对象在某位收件人那里所有未撤回的提醒；转交时只撤回原收件人的。
func WithdrawRemindersFor(ctx context.Context, q *dbgen.Queries, now time.Time, storeID, recipientID uuid.UUID, subject Subject) error {
	if subject.Empty() || !subject.Valid() {
		return ErrInvalidReminder
	}
	if err := q.LockOutboxSubject(ctx, dbgen.LockOutboxSubjectParams{
		StoreID:     storeID,
		SubjectKind: subject.Kind, SubjectID: subject.ID,
	}); err != nil {
		return err
	}
	return q.WithdrawOutboxForRecipient(ctx, dbgen.WithdrawOutboxForRecipientParams{
		Now: timestamp(now), StoreID: storeID, RecipientID: recipientID,
		SubjectKind: subject.Kind, SubjectID: subject.ID,
	})
}

// runDispatcher 把 tier=now 的 queued 提醒置为 sent；当日和每周简报留给第 2 步。
func runDispatcher(ctx context.Context, q *dbgen.Queries, storeID uuid.UUID, now time.Time) error {
	return q.DispatchNowOutbox(ctx, dbgen.DispatchNowOutboxParams{Now: timestamp(now), StoreID: storeID})
}
