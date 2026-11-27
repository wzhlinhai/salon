package storeai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
)

const (
	// RunInterval 是 RunLoop 的轮询间隔；Wake 会提前叫醒它。
	RunInterval = 5 * time.Second
	// LeaseDuration 是一个 worker 认领收件箱后的租约时长。
	LeaseDuration = 5 * time.Minute
	// RunTimeout 是每一组事件的一次运行时限。
	RunTimeout = 4 * time.Minute
	// MaxAttempts 是一组事件最多尝试几次，之后置 failed。
	MaxAttempts = 3

	storePhaseTimeout = 5 * time.Minute
	inboxTimeout      = 15 * time.Minute
	inboxParallelism  = 4
)

// RunFunc 是收件箱 worker 一次可注入的运行。本步不接模型。
type RunFunc func(ctx context.Context, in RunInput) (RunResult, error)

// RunInput 是一次运行拿到的全部输入。Session 是按对象接回或新建的会话。
type RunInput struct {
	TenantID uuid.UUID
	StoreID  uuid.UUID
	Inbox    dbgen.AiInbox
	Session  dbgen.AiSession
	Events   []dbgen.AiInboxEvent
	Now      time.Time
	// Tx 在本租户的 db.InTenant 事务里执行，运行里的工具用它读写业务数据。
	Tx func(ctx context.Context, fn func(q *dbgen.Queries) error) error
}

// RunResult 是一次运行要保存的结果。Messages 为空时沿用会话原来的消息。
type RunResult struct {
	Messages   json.RawMessage
	Status     string
	WaitingFor *string
}

// RunOnce 对全部门店执行一轮：调度器、派发器，然后处理有待处理事件的收件箱。
// 一家门店、一个收件箱出错只记日志，不影响其他门店和收件箱。
func RunOnce(ctx context.Context, pool *pgxpool.Pool, clock func() time.Time, run RunFunc, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	if clock == nil {
		panic("storeai: RunOnce requires a clock")
	}
	roundNow := clock()
	process := run != nil
	stores, err := dbgen.New(pool).ListStoreTenants(ctx)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			logger.Error("store ai: list stores", "err", err)
		}
		return
	}
	for _, store := range stores {
		if ctx.Err() != nil {
			return
		}
		inboxes, err := runStoreSafely(ctx, pool, store.TenantID, store.ID, roundNow, logger, process)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				logger.Error("store ai", "tenant_id", store.TenantID, "store_id", store.ID, "err", err)
			}
			continue
		}
		if !process {
			continue
		}
		processInboxes(ctx, pool, store.TenantID, store.ID, inboxes, clock, run, logger)
	}
}

// RunLoop 立即执行一轮，之后每 RunInterval 一轮；Wake 会提前触发一轮，直到 ctx 结束。
func RunLoop(ctx context.Context, pool *pgxpool.Pool, now func() time.Time, run RunFunc, logger *slog.Logger) {
	RunOnce(ctx, pool, now, run, logger)
	ticker := time.NewTicker(RunInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-wake:
			RunOnce(ctx, pool, now, run, logger)
		case <-ticker.C:
			RunOnce(ctx, pool, now, run, logger)
		}
	}
}

// runStoreSafely 给一家门店的调度器、派发器和选收件箱加超时与 panic 恢复。
func runStoreSafely(ctx context.Context, pool *pgxpool.Pool, tenantID, storeID uuid.UUID, now time.Time, logger *slog.Logger, listInboxes bool) (inboxes []dbgen.AiInbox, err error) {
	ctx, cancel := context.WithTimeout(ctx, storePhaseTimeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	err = db.InTenant(ctx, pool, tenantID, func(q *dbgen.Queries) error {
		if err := runScheduler(ctx, q, storeID, now); err != nil {
			return err
		}
		if err := runDispatcher(ctx, q, storeID, now); err != nil {
			return err
		}
		if !listInboxes {
			return nil
		}
		var err error
		inboxes, err = q.ListInboxesWithPending(ctx, dbgen.ListInboxesWithPendingParams{
			StoreID: storeID, PendingBefore: timestamp(now), LeaseBefore: timestamp(now),
		})
		return err
	})
	return inboxes, err
}

func processInboxes(ctx context.Context, pool *pgxpool.Pool, tenantID, storeID uuid.UUID, inboxes []dbgen.AiInbox, clock func() time.Time, run RunFunc, logger *slog.Logger) {
	sem := make(chan struct{}, inboxParallelism)
	var wg sync.WaitGroup
	for _, inbox := range inboxes {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(inbox dbgen.AiInbox) {
			defer wg.Done()
			defer func() { <-sem }()
			processInboxSafely(ctx, pool, tenantID, storeID, inbox, clock, run, logger)
		}(inbox)
	}
	wg.Wait()
}

func processInboxSafely(ctx context.Context, pool *pgxpool.Pool, tenantID, storeID uuid.UUID, inbox dbgen.AiInbox, clock func() time.Time, run RunFunc, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(ctx, inboxTimeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			logger.Error("store ai: inbox panic", "tenant_id", tenantID, "store_id", storeID, "inbox_id", inbox.ID, "panic", r)
		}
	}()
	if err := processInbox(ctx, pool, tenantID, storeID, inbox, clock, run, logger); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("store ai: inbox", "tenant_id", tenantID, "store_id", storeID, "inbox_id", inbox.ID, "err", err)
	}
}

func processInbox(ctx context.Context, pool *pgxpool.Pool, tenantID, storeID uuid.UUID, inbox dbgen.AiInbox, clock func() time.Time, run RunFunc, logger *slog.Logger) error {
	token := uuid.New()
	claimNow := clock()
	claimed, events, ok, err := claimInbox(ctx, pool, tenantID, storeID, inbox.ID, token, claimNow)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	defer releaseLease(ctx, pool, tenantID, storeID, inbox.ID, token, logger)
	if len(events) == 0 {
		return nil
	}
	for _, group := range groupEvents(events) {
		groupNow := clock()
		renewed, err := renewLease(ctx, pool, tenantID, storeID, inbox.ID, token, groupNow)
		if err != nil {
			return err
		}
		if !renewed {
			return ErrLeaseLost
		}
		session, err := sessionFor(ctx, pool, tenantID, storeID, inbox.ID, group.subject, groupNow)
		if err != nil {
			return err
		}
		result, runErr := runGroup(ctx, pool, tenantID, storeID, claimed, session, group.events, groupNow, run)
		if runErr != nil {
			if err := failGroup(ctx, pool, storeID, token, group.events, clock(), runErr); err != nil {
				return err
			}
			logger.Warn("store ai: run failed", "tenant_id", tenantID, "store_id", storeID, "inbox_id", inbox.ID, "err", runErr)
			continue
		}
		if err := saveGroup(ctx, pool, tenantID, storeID, inbox.ID, token, session, group.events, result, clock()); err != nil {
			if errors.Is(err, ErrLeaseLost) {
				return err
			}
			if failErr := failGroup(ctx, pool, storeID, token, group.events, clock(), err); failErr != nil {
				return failErr
			}
			logger.Warn("store ai: save failed", "tenant_id", tenantID, "store_id", storeID, "inbox_id", inbox.ID, "err", err)
		}
	}
	return nil
}

func claimInbox(ctx context.Context, pool *pgxpool.Pool, tenantID, storeID, inboxID, token uuid.UUID, now time.Time) (dbgen.AiInbox, []dbgen.AiInboxEvent, bool, error) {
	var claimed dbgen.AiInbox
	var events []dbgen.AiInboxEvent
	err := db.InTenant(ctx, pool, tenantID, func(q *dbgen.Queries) error {
		row, err := q.ClaimInboxLease(ctx, dbgen.ClaimInboxLeaseParams{
			LeaseToken: &token, LeaseUntil: timestamp(now.Add(LeaseDuration)), ID: inboxID, StoreID: storeID, Now: timestamp(now),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		claimed = row
		failure := "多次运行中断"
		if err := q.ResetRunningEvents(ctx, dbgen.ResetRunningEventsParams{
			MaxAttempts: MaxAttempts, FailureMessage: &failure, StoreID: storeID, InboxID: inboxID,
		}); err != nil {
			return err
		}
		rows, err := q.TakePendingEvents(ctx, dbgen.TakePendingEventsParams{
			LeaseToken: &token, StoreID: storeID, InboxID: inboxID, Now: timestamp(now), MaxAttempts: MaxAttempts,
		})
		if err != nil {
			return err
		}
		events = make([]dbgen.AiInboxEvent, len(rows))
		for i, row := range rows {
			events[i] = dbgen.AiInboxEvent(row)
		}
		sortEvents(events)
		return err
	})
	if err != nil {
		return dbgen.AiInbox{}, nil, false, err
	}
	return claimed, events, claimed.ID != uuid.Nil, nil
}

func sortEvents(events []dbgen.AiInboxEvent) {
	sort.SliceStable(events, func(i, j int) bool {
		a, b := events[i], events[j]
		if !a.CreatedAt.Time.Equal(b.CreatedAt.Time) {
			return a.CreatedAt.Time.Before(b.CreatedAt.Time)
		}
		return bytes.Compare(a.ID[:], b.ID[:]) < 0
	})
}

func renewLease(ctx context.Context, pool *pgxpool.Pool, tenantID, storeID, inboxID, token uuid.UUID, now time.Time) (bool, error) {
	renewed := false
	err := db.InTenant(ctx, pool, tenantID, func(q *dbgen.Queries) error {
		_, err := q.RenewInboxLease(ctx, dbgen.RenewInboxLeaseParams{
			LeaseUntil: timestamp(now.Add(LeaseDuration)), ID: inboxID, StoreID: storeID, LeaseToken: &token,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		renewed = true
		return nil
	})
	return renewed, err
}

func releaseLease(ctx context.Context, pool *pgxpool.Pool, tenantID, storeID, inboxID, token uuid.UUID, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := db.InTenant(ctx, pool, tenantID, func(q *dbgen.Queries) error {
		return q.ReleaseInboxLease(ctx, dbgen.ReleaseInboxLeaseParams{ID: inboxID, StoreID: storeID, LeaseToken: &token})
	}); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("store ai: release lease", "tenant_id", tenantID, "store_id", storeID, "inbox_id", inboxID, "err", err)
	}
}

type eventGroup struct {
	subject Subject
	events  []dbgen.AiInboxEvent
}

func groupEvents(events []dbgen.AiInboxEvent) []eventGroup {
	index := map[string]int{}
	groups := make([]eventGroup, 0)
	for _, event := range events {
		subject := Subject{}
		key := ""
		if event.SubjectKind != nil && event.SubjectID != nil {
			subject = Subject{Kind: *event.SubjectKind, ID: *event.SubjectID}
			key = *event.SubjectKind + "/" + event.SubjectID.String()
		}
		at, ok := index[key]
		if !ok {
			at = len(groups)
			index[key] = at
			groups = append(groups, eventGroup{subject: subject})
		}
		groups[at].events = append(groups[at].events, event)
	}
	return groups
}

func sessionFor(ctx context.Context, pool *pgxpool.Pool, tenantID, storeID, inboxID uuid.UUID, subject Subject, now time.Time) (dbgen.AiSession, error) {
	var session dbgen.AiSession
	err := db.InTenant(ctx, pool, tenantID, func(q *dbgen.Queries) error {
		if !subject.Empty() {
			kind, id := subjectColumns(subject)
			found, err := q.GetOpenSession(ctx, dbgen.GetOpenSessionParams{
				StoreID: storeID, InboxID: inboxID, SubjectKind: kind, SubjectID: id,
			})
			if err == nil {
				session = found
				return nil
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		kind, id := subjectColumns(subject)
		created, err := q.CreateSession(ctx, dbgen.CreateSessionParams{
			TenantID: tenantID, StoreID: storeID, InboxID: inboxID, SubjectKind: kind, SubjectID: id, Now: timestamp(now),
		})
		if err != nil {
			return err
		}
		session = created
		return nil
	})
	return session, err
}

func runGroup(ctx context.Context, pool *pgxpool.Pool, tenantID, storeID uuid.UUID, inbox dbgen.AiInbox, session dbgen.AiSession, events []dbgen.AiInboxEvent, now time.Time, run RunFunc) (result RunResult, err error) {
	runCtx, cancel := context.WithTimeout(ctx, RunTimeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return run(runCtx, RunInput{
		TenantID: tenantID, StoreID: storeID, Inbox: inbox, Session: session, Events: events, Now: now,
		Tx: func(ctx context.Context, fn func(q *dbgen.Queries) error) error {
			return db.InTenant(ctx, pool, tenantID, fn)
		},
	})
}

func saveGroup(ctx context.Context, pool *pgxpool.Pool, tenantID, storeID, inboxID, token uuid.UUID, session dbgen.AiSession, events []dbgen.AiInboxEvent, result RunResult, now time.Time) error {
	status := result.Status
	if status == "" {
		status = "active"
	}
	if !validSessionStatus(status) {
		return ErrInvalidRunResult
	}
	messages := result.Messages
	if len(messages) == 0 {
		messages = session.Messages
	}
	trimmed := bytes.TrimSpace(messages)
	if len(trimmed) == 0 || trimmed[0] != '[' || !json.Valid(trimmed) {
		return ErrInvalidRunResult
	}
	return db.InTenant(ctx, pool, tenantID, func(q *dbgen.Queries) error {
		if _, err := q.RenewInboxLease(ctx, dbgen.RenewInboxLeaseParams{
			LeaseUntil: timestamp(now.Add(LeaseDuration)), ID: inboxID, StoreID: storeID, LeaseToken: &token,
		}); errors.Is(err, pgx.ErrNoRows) {
			return ErrLeaseLost
		} else if err != nil {
			return err
		}
		if _, err := q.SaveSession(ctx, dbgen.SaveSessionParams{
			Status: status, WaitingFor: result.WaitingFor, Messages: trimmed, UpdatedAt: timestamp(now),
			ID: session.ID, StoreID: storeID,
		}); err != nil {
			return err
		}
		return q.FinishEvents(ctx, dbgen.FinishEventsParams{
			DoneAt: timestamp(now), SessionID: &session.ID, Ids: eventIDs(events), StoreID: storeID, LeaseToken: &token,
		})
	})
}

func failGroup(ctx context.Context, pool *pgxpool.Pool, storeID, token uuid.UUID, events []dbgen.AiInboxEvent, now time.Time, runErr error) error {
	message := truncateRunes(runErr.Error(), 200)
	return db.InTenant(ctx, pool, events[0].TenantID, func(q *dbgen.Queries) error {
		return q.FailOrRetryEvents(ctx, dbgen.FailOrRetryEventsParams{
			MaxAttemptsStatus: MaxAttempts, MaxAttemptsDue: MaxAttempts, Now: timestamp(now),
			LastError: &message, Ids: eventIDs(events), StoreID: storeID, LeaseToken: &token,
		})
	})
}

func eventIDs(events []dbgen.AiInboxEvent) []uuid.UUID {
	ids := make([]uuid.UUID, len(events))
	for i, event := range events {
		ids[i] = event.ID
	}
	return ids
}

func validSessionStatus(status string) bool {
	return status == "active" || status == "waiting" || status == "closed"
}
