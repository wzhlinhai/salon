package storeai

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
)

// ResolveSubject 结束一件事：撤回这个对象所有未撤回的 AI 提醒、取消门店收件箱的跟进，
// 并把所有 open 的交办置 cancelled。业务操作在同一个事务里调用它，任一步失败整体回滚。
func ResolveSubject(ctx context.Context, q *dbgen.Queries, now time.Time, storeID uuid.UUID, subject Subject) error {
	if subject.Empty() || !subject.Valid() {
		return ErrInvalidEvent
	}
	if err := q.CancelOpenRequestsForSubject(ctx, dbgen.CancelOpenRequestsForSubjectParams{
		StoreID: storeID, SubjectKind: subject.Kind, SubjectID: subject.ID,
	}); err != nil {
		return err
	}
	if err := WithdrawReminders(ctx, q, now, storeID, subject); err != nil {
		return err
	}
	inbox, err := EnsureInbox(ctx, q, storeID, nil)
	if err != nil {
		return err
	}
	return CancelFollowUps(ctx, q, inbox, subject)
}
