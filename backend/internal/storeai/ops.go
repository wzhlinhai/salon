package storeai

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

// Register 声明「我的提醒」的读和已读操作。
func Register(r *ops.Registry) {
	ops.Register(r, ops.Operation[struct{}, MyReminderList]{
		Name: "list_my_reminders", Summary: "查看本人的 AI 提醒和未读数", Kind: ops.Read,
		Permission: ops.LoggedIn, AI: ops.AINone, Handle: listMyReminders,
	})
	ops.Register(r, ops.Operation[MarkMyReminderReadInput, MarkMyReminderReadResult]{
		Name: "mark_my_reminder_read", Summary: "把一条 AI 提醒和它的回复标为已读", Kind: ops.Write,
		Permission: ops.LoggedIn, AI: ops.AINone, Handle: markMyReminderRead,
	})
}

// MyReminder 是「我的提醒」里的一条话题，replies 是同一话题已发出的后续。
type MyReminder struct {
	ID          uuid.UUID         `json:"id"`
	Tier        string            `json:"tier" enum:"now,daily,weekly"`
	SubjectKind string            `json:"subject_kind"`
	SubjectID   uuid.UUID         `json:"subject_id"`
	Body        string            `json:"body"`
	SentAt      time.Time         `json:"sent_at"`
	ReadAt      *time.Time        `json:"read_at" nullable:"true"`
	Replies     []MyReminderReply `json:"replies"`
}

// MyReminderReply 是同一话题下已发出的一条后续，不再嵌套 replies。
type MyReminderReply struct {
	ID          uuid.UUID  `json:"id"`
	Tier        string     `json:"tier" enum:"now,daily,weekly"`
	SubjectKind string     `json:"subject_kind"`
	SubjectID   uuid.UUID  `json:"subject_id"`
	Body        string     `json:"body"`
	SentAt      time.Time  `json:"sent_at"`
	ReadAt      *time.Time `json:"read_at" nullable:"true"`
}

// MyReminderList 是本人已发出的 AI 提醒，按话题最近一次发出时间倒序。
type MyReminderList struct {
	Items       []MyReminder `json:"items"`
	UnreadCount int64        `json:"unread_count"`
}

// MarkMyReminderReadInput 是要标为已读的话题父条。
type MarkMyReminderReadInput struct {
	ID uuid.UUID `json:"id" format:"uuid"`
}

// MarkMyReminderReadResult 是已读操作的结果。
type MarkMyReminderReadResult struct {
	ID uuid.UUID `json:"id"`
}

func listMyReminders(ctx context.Context, env ops.Env, _ *struct{}) (*MyReminderList, error) {
	storeID, staffID := env.Caller.StoreID, env.Caller.StaffID
	parents, err := env.Q.ListMyReminderParents(ctx, dbgen.ListMyReminderParentsParams{StoreID: storeID, RecipientID: staffID})
	if err != nil {
		return nil, err
	}
	unread, err := env.Q.CountMyUnreadReminders(ctx, dbgen.CountMyUnreadRemindersParams{StoreID: storeID, RecipientID: staffID})
	if err != nil {
		return nil, err
	}
	parentIDs := make([]uuid.UUID, len(parents))
	for i, parent := range parents {
		parentIDs[i] = parent.ID
	}
	replies, err := env.Q.ListMyReminderReplies(ctx, dbgen.ListMyReminderRepliesParams{
		StoreID: storeID, RecipientID: staffID, ParentIds: parentIDs,
	})
	if err != nil {
		return nil, err
	}
	byParent := map[uuid.UUID][]MyReminderReply{}
	for _, reply := range replies {
		if reply.ParentID == nil {
			continue
		}
		byParent[*reply.ParentID] = append(byParent[*reply.ParentID], MyReminderReply{
			ID: reply.ID, Tier: reply.Tier, SubjectKind: reply.SubjectKind, SubjectID: reply.SubjectID,
			Body: reply.Body, SentAt: reply.SentAt.Time, ReadAt: timePointer(reply.ReadAt),
		})
	}
	items := make([]MyReminder, 0, len(parents))
	for _, parent := range parents {
		replies := byParent[parent.ID]
		if replies == nil {
			replies = []MyReminderReply{}
		}
		items = append(items, MyReminder{
			ID: parent.ID, Tier: parent.Tier, SubjectKind: parent.SubjectKind, SubjectID: parent.SubjectID,
			Body: parent.Body, SentAt: parent.SentAt.Time, ReadAt: timePointer(parent.ReadAt), Replies: replies,
		})
	}
	return &MyReminderList{Items: items, UnreadCount: unread}, nil
}

func markMyReminderRead(ctx context.Context, env ops.Env, in *MarkMyReminderReadInput) (*MarkMyReminderReadResult, error) {
	params := dbgen.GetMyReminderParentParams{ID: in.ID, StoreID: env.Caller.StoreID, RecipientID: env.Caller.StaffID}
	if _, err := env.Q.GetMyReminderParent(ctx, params); errors.Is(err, pgx.ErrNoRows) {
		return nil, ops.ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if err := env.Q.MarkMyReminderRead(ctx, dbgen.MarkMyReminderReadParams{
		Now: timestamp(env.Now), StoreID: env.Caller.StoreID, RecipientID: env.Caller.StaffID, ID: in.ID,
	}); err != nil {
		return nil, err
	}
	return &MarkMyReminderReadResult{ID: in.ID}, nil
}
