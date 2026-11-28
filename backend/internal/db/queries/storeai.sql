-- name: StoreTenant :one
SELECT tenant_id FROM stores WHERE id = sqlc.arg(store_id);

-- name: InsertStoreInbox :exec
INSERT INTO ai_inboxes (tenant_id, store_id, staff_id)
VALUES (sqlc.arg(tenant_id), sqlc.arg(store_id), NULL)
ON CONFLICT DO NOTHING;

-- name: InsertStaffInbox :exec
INSERT INTO ai_inboxes (tenant_id, store_id, staff_id)
VALUES (sqlc.arg(tenant_id), sqlc.arg(store_id), sqlc.arg(staff_id))
ON CONFLICT DO NOTHING;

-- name: GetStoreInbox :one
SELECT * FROM ai_inboxes
WHERE tenant_id = sqlc.arg(tenant_id) AND store_id = sqlc.arg(store_id) AND staff_id IS NULL;

-- name: GetStaffInbox :one
SELECT * FROM ai_inboxes
WHERE tenant_id = sqlc.arg(tenant_id) AND store_id = sqlc.arg(store_id) AND staff_id = sqlc.arg(staff_id);

-- name: ListInboxesWithPending :many
SELECT i.* FROM ai_inboxes i
WHERE i.store_id = sqlc.arg(store_id)
  AND (
    EXISTS (
      SELECT 1 FROM ai_inbox_events e
      WHERE e.tenant_id = i.tenant_id AND e.store_id = i.store_id AND e.inbox_id = i.id
        AND e.status = 'pending' AND e.not_before <= sqlc.arg(pending_before)
    )
    OR (
      (i.lease_until IS NULL OR i.lease_until < sqlc.arg(lease_before))
      AND EXISTS (
        SELECT 1 FROM ai_inbox_events e
        WHERE e.tenant_id = i.tenant_id AND e.store_id = i.store_id AND e.inbox_id = i.id
          AND e.status = 'running'
      )
    )
  )
ORDER BY i.id;

-- name: InsertInboxEvent :execrows
INSERT INTO ai_inbox_events (tenant_id, store_id, inbox_id, kind, source_key, subject_kind, subject_id, payload, status, attempts, not_before, created_at)
VALUES (sqlc.arg(tenant_id), sqlc.arg(store_id), sqlc.arg(inbox_id), sqlc.arg(kind), sqlc.arg(source_key),
        sqlc.narg(subject_kind), sqlc.narg(subject_id), sqlc.arg(payload)::jsonb, 'pending', 0, sqlc.arg(not_before), sqlc.arg(created_at))
ON CONFLICT (inbox_id, source_key) DO NOTHING;

-- name: ListInboxEvents :many
SELECT * FROM ai_inbox_events WHERE inbox_id = sqlc.arg(inbox_id) ORDER BY created_at, id;

-- name: ClaimInboxLease :one
UPDATE ai_inboxes
SET lease_token = sqlc.arg(lease_token), lease_until = sqlc.arg(lease_until)
WHERE id = sqlc.arg(id) AND store_id = sqlc.arg(store_id)
  AND (lease_until IS NULL OR lease_until < sqlc.arg(now))
RETURNING *;

-- name: RenewInboxLease :one
UPDATE ai_inboxes SET lease_until = sqlc.arg(lease_until)
WHERE id = sqlc.arg(id) AND store_id = sqlc.arg(store_id) AND lease_token = sqlc.arg(lease_token)
RETURNING *;

-- name: ReleaseInboxLease :exec
UPDATE ai_inboxes SET lease_until = NULL
WHERE id = sqlc.arg(id) AND store_id = sqlc.arg(store_id) AND lease_token = sqlc.arg(lease_token);

-- name: InboxLeaseHeld :one
SELECT EXISTS (
  SELECT 1 FROM ai_inboxes
  WHERE id = sqlc.arg(id) AND store_id = sqlc.arg(store_id) AND lease_token = sqlc.arg(lease_token)
) AS held;

-- name: ResetRunningEvents :exec
UPDATE ai_inbox_events
SET status = CASE WHEN attempts >= sqlc.arg(max_attempts)::int THEN 'failed' ELSE 'pending' END,
    last_error = CASE WHEN attempts >= sqlc.arg(max_attempts)::int THEN sqlc.arg(failure_message) ELSE last_error END
WHERE store_id = sqlc.arg(store_id) AND inbox_id = sqlc.arg(inbox_id) AND status = 'running';

-- name: TakePendingEvents :many
WITH picked AS (
  SELECT e.id FROM ai_inbox_events e
  WHERE e.store_id = sqlc.arg(store_id) AND e.inbox_id = sqlc.arg(inbox_id)
    AND e.status = 'pending' AND e.not_before <= sqlc.arg(now)
    AND e.attempts < sqlc.arg(max_attempts)::int
  ORDER BY e.created_at, e.id
  LIMIT 50
  FOR NO KEY UPDATE SKIP LOCKED
),
updated AS (
  UPDATE ai_inbox_events
  SET status = 'running', attempts = attempts + 1, lease_token = sqlc.arg(lease_token), last_error = NULL
  WHERE id IN (SELECT id FROM picked)
  RETURNING *
)
SELECT * FROM updated ORDER BY created_at, id;

-- name: FinishEvents :exec
UPDATE ai_inbox_events
SET status = 'done', done_at = sqlc.arg(done_at), session_id = sqlc.arg(session_id),
    last_error = NULL, lease_token = NULL
WHERE id = ANY(sqlc.arg(ids)::uuid[]) AND store_id = sqlc.arg(store_id) AND lease_token = sqlc.arg(lease_token);

-- name: FailOrRetryEvents :exec
UPDATE ai_inbox_events
SET status = CASE WHEN attempts >= sqlc.arg(max_attempts_status)::int THEN 'failed' ELSE 'pending' END,
    not_before = CASE WHEN attempts >= sqlc.arg(max_attempts_due)::int THEN not_before
                      ELSE sqlc.arg(now)::timestamptz + make_interval(mins => attempts) END,
    last_error = sqlc.arg(last_error),
    lease_token = NULL
WHERE id = ANY(sqlc.arg(ids)::uuid[]) AND store_id = sqlc.arg(store_id) AND lease_token = sqlc.arg(lease_token);

-- name: GetOpenSession :one
SELECT * FROM ai_sessions
WHERE store_id = sqlc.arg(store_id) AND inbox_id = sqlc.arg(inbox_id)
  AND subject_kind = sqlc.arg(subject_kind) AND subject_id = sqlc.arg(subject_id)
  AND status <> 'closed'
ORDER BY updated_at DESC, id DESC
LIMIT 1;

-- name: GetSession :one
SELECT * FROM ai_sessions WHERE store_id = sqlc.arg(store_id) AND id = sqlc.arg(id);

-- name: ListSessions :many
SELECT * FROM ai_sessions WHERE inbox_id = sqlc.arg(inbox_id) ORDER BY created_at, id;

-- name: CreateSession :one
INSERT INTO ai_sessions (tenant_id, store_id, inbox_id, subject_kind, subject_id, status, waiting_for, messages, created_at, updated_at)
VALUES (sqlc.arg(tenant_id), sqlc.arg(store_id), sqlc.arg(inbox_id), sqlc.narg(subject_kind), sqlc.narg(subject_id),
        'active', NULL, '[]'::jsonb, sqlc.arg(now), sqlc.arg(now))
RETURNING *;

-- name: SaveSession :one
UPDATE ai_sessions
SET status = sqlc.arg(status), waiting_for = sqlc.narg(waiting_for), messages = sqlc.arg(messages)::jsonb, updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND store_id = sqlc.arg(store_id)
RETURNING *;

-- name: DueTimers :many
SELECT * FROM ai_timers
WHERE store_id = sqlc.arg(store_id) AND status = 'scheduled' AND due_at <= sqlc.arg(now)
ORDER BY due_at, id
FOR NO KEY UPDATE SKIP LOCKED;

-- name: ListTimers :many
SELECT * FROM ai_timers WHERE inbox_id = sqlc.arg(inbox_id) ORDER BY created_at, id;

-- name: FireTimer :exec
UPDATE ai_timers SET status = 'fired' WHERE id = sqlc.arg(id) AND store_id = sqlc.arg(store_id);

-- name: RescheduleTimer :exec
UPDATE ai_timers SET due_at = sqlc.arg(due_at) WHERE id = sqlc.arg(id) AND store_id = sqlc.arg(store_id);

-- name: UpsertFollowUpTimer :one
INSERT INTO ai_timers (tenant_id, store_id, inbox_id, kind, subject_kind, subject_id, periodic_key, repeat, due_at, payload, status, created_at)
VALUES (sqlc.arg(tenant_id), sqlc.arg(store_id), sqlc.arg(inbox_id), 'follow_up', sqlc.arg(subject_kind), sqlc.arg(subject_id),
        NULL, NULL, sqlc.arg(due_at), sqlc.arg(payload)::jsonb, 'scheduled', sqlc.arg(created_at))
ON CONFLICT (tenant_id, store_id, inbox_id, subject_kind, subject_id)
  WHERE kind = 'follow_up' AND status = 'scheduled'
DO UPDATE SET due_at = EXCLUDED.due_at, payload = EXCLUDED.payload
RETURNING *;

-- name: CancelFollowUpTimers :exec
UPDATE ai_timers SET status = 'cancelled'
WHERE store_id = sqlc.arg(store_id) AND inbox_id = sqlc.arg(inbox_id) AND kind = 'follow_up' AND status = 'scheduled'
  AND subject_kind = sqlc.arg(subject_kind) AND subject_id = sqlc.arg(subject_id);

-- name: UpsertPeriodicTimer :one
INSERT INTO ai_timers (tenant_id, store_id, inbox_id, kind, subject_kind, subject_id, periodic_key, repeat, due_at, payload, status, created_at)
VALUES (sqlc.arg(tenant_id), sqlc.arg(store_id), sqlc.arg(inbox_id), 'periodic', NULL, NULL, sqlc.arg(periodic_key), sqlc.arg(repeat),
        sqlc.arg(due_at), '{}'::jsonb, 'scheduled', sqlc.arg(created_at))
ON CONFLICT (tenant_id, store_id, inbox_id, periodic_key)
  WHERE kind = 'periodic'
DO UPDATE SET due_at = EXCLUDED.due_at, repeat = EXCLUDED.repeat, payload = EXCLUDED.payload, status = 'scheduled'
RETURNING *;

-- name: LockInbox :one
SELECT * FROM ai_inboxes WHERE id = sqlc.arg(inbox_id) FOR NO KEY UPDATE;

-- name: GetMemoryFile :one
SELECT * FROM ai_memory_files WHERE inbox_id = sqlc.arg(inbox_id) AND path = sqlc.arg(path);

-- name: UpsertMemoryFile :one
INSERT INTO ai_memory_files (tenant_id, store_id, inbox_id, path, content, updated_at)
VALUES (sqlc.arg(tenant_id), sqlc.arg(store_id), sqlc.arg(inbox_id), sqlc.arg(path), sqlc.arg(content), sqlc.arg(updated_at))
ON CONFLICT (inbox_id, path) DO UPDATE SET content = EXCLUDED.content, updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: DeleteMemoryFile :exec
DELETE FROM ai_memory_files WHERE inbox_id = sqlc.arg(inbox_id) AND path = sqlc.arg(path);

-- name: ListMemoryFiles :many
SELECT path, octet_length(content)::int AS bytes, updated_at
FROM ai_memory_files WHERE inbox_id = sqlc.arg(inbox_id) ORDER BY path;

-- name: CountMemoryFiles :one
SELECT count(*)::bigint AS count FROM ai_memory_files WHERE inbox_id = sqlc.arg(inbox_id);

-- name: ListAlwaysMemoryFiles :many
SELECT path, content FROM ai_memory_files
WHERE inbox_id = sqlc.arg(inbox_id) AND path IN ('instructions.md','index.md','reported.md')
ORDER BY path;

-- name: LockOutboxSubject :exec
SELECT pg_advisory_xact_lock(hashtextextended(
  CAST(sqlc.arg(store_id)::uuid AS text) || '/ai_outbox/' ||
  sqlc.arg(subject_kind)::text || '/' || CAST(sqlc.arg(subject_id)::uuid AS text), 0));

-- name: GetActiveStaffForShare :one
SELECT id FROM staff
WHERE tenant_id = sqlc.arg(tenant_id) AND store_id = sqlc.arg(store_id) AND id = sqlc.arg(staff_id) AND active
FOR SHARE;

-- name: GetStaffInStoreForShare :one
SELECT id FROM staff
WHERE tenant_id = sqlc.arg(tenant_id) AND store_id = sqlc.arg(store_id) AND id = sqlc.arg(staff_id)
FOR SHARE;

-- name: GetQueuedOutbox :one
SELECT * FROM ai_outbox
WHERE store_id = sqlc.arg(store_id) AND recipient_id = sqlc.arg(recipient_id)
  AND subject_kind = sqlc.arg(subject_kind) AND subject_id = sqlc.arg(subject_id) AND status = 'queued'
ORDER BY queued_at DESC, id DESC
LIMIT 1;

-- name: ReplaceQueuedOutbox :one
UPDATE ai_outbox SET body = sqlc.arg(body), tier = sqlc.arg(tier), queued_at = sqlc.arg(queued_at)
WHERE id = sqlc.arg(id) AND store_id = sqlc.arg(store_id) AND status = 'queued'
RETURNING *;

-- name: GetSentOutboxParent :one
SELECT * FROM ai_outbox
WHERE store_id = sqlc.arg(store_id) AND recipient_id = sqlc.arg(recipient_id)
  AND subject_kind = sqlc.arg(subject_kind) AND subject_id = sqlc.arg(subject_id)
  AND status = 'sent' AND parent_id IS NULL
ORDER BY sent_at DESC, id DESC
LIMIT 1;

-- name: InsertOutbox :one
INSERT INTO ai_outbox (tenant_id, store_id, recipient_id, tier, subject_kind, subject_id, parent_id, body, status, session_id, queued_at)
VALUES (sqlc.arg(tenant_id), sqlc.arg(store_id), sqlc.arg(recipient_id), sqlc.arg(tier), sqlc.arg(subject_kind), sqlc.arg(subject_id),
        sqlc.narg(parent_id), sqlc.arg(body), 'queued', sqlc.narg(session_id), sqlc.arg(queued_at))
RETURNING *;

-- name: ListOutboxForRecipient :many
SELECT * FROM ai_outbox
WHERE store_id = sqlc.arg(store_id) AND recipient_id = sqlc.arg(recipient_id)
ORDER BY queued_at, id;

-- name: DispatchNowOutbox :exec
UPDATE ai_outbox SET status = 'sent', sent_at = sqlc.arg(now)
WHERE store_id = sqlc.arg(store_id) AND status = 'queued' AND tier = 'now';

-- name: WithdrawOutbox :exec
UPDATE ai_outbox SET status = 'withdrawn', withdrawn_at = sqlc.arg(now)
WHERE store_id = sqlc.arg(store_id) AND subject_kind = sqlc.arg(subject_kind) AND subject_id = sqlc.arg(subject_id)
  AND status <> 'withdrawn';

-- name: LockRequest :one
SELECT * FROM ai_requests WHERE id = sqlc.arg(id) FOR NO KEY UPDATE;

-- name: GetRequest :one
SELECT * FROM ai_requests WHERE id = sqlc.arg(id);

-- name: InsertRequest :one
INSERT INTO ai_requests (tenant_id, store_id, to_staff_id, kind, subject_kind, subject_id, options, due_at,
                         status, answer, answered_at, previous_id, created_at)
VALUES (sqlc.arg(tenant_id), sqlc.arg(store_id), sqlc.arg(to_staff_id), sqlc.arg(kind), sqlc.arg(subject_kind), sqlc.arg(subject_id),
        sqlc.arg(options)::jsonb, sqlc.narg(due_at), 'open', NULL, NULL, sqlc.narg(previous_id), sqlc.arg(created_at))
RETURNING *;

-- name: SetRequestAnswered :one
UPDATE ai_requests SET status = 'answered', answer = sqlc.arg(answer), answered_at = sqlc.arg(answered_at)
WHERE id = sqlc.arg(id) AND store_id = sqlc.arg(store_id)
RETURNING *;

-- name: SetRequestReassigned :one
UPDATE ai_requests SET status = 'reassigned'
WHERE id = sqlc.arg(id) AND store_id = sqlc.arg(store_id)
RETURNING *;

-- name: GetSuccessor :one
SELECT * FROM ai_requests WHERE previous_id = sqlc.arg(previous_id)
ORDER BY created_at DESC, id DESC LIMIT 1;

-- name: SetRequestCancelled :one
UPDATE ai_requests SET status = 'cancelled'
WHERE id = sqlc.arg(id) AND store_id = sqlc.arg(store_id)
RETURNING *;

-- name: ListMyReminderParents :many
SELECT p.*,
       COALESCE((SELECT max(r.sent_at) FROM ai_outbox r
                 WHERE r.tenant_id = p.tenant_id AND r.parent_id = p.id AND r.status = 'sent'),
                p.sent_at) AS last_sent_at
FROM ai_outbox p
WHERE p.store_id = sqlc.arg(store_id) AND p.recipient_id = sqlc.arg(recipient_id)
  AND p.status = 'sent' AND p.parent_id IS NULL
ORDER BY last_sent_at DESC, p.id DESC
LIMIT 50;

-- name: ListMyReminderReplies :many
SELECT * FROM ai_outbox
WHERE store_id = sqlc.arg(store_id) AND recipient_id = sqlc.arg(recipient_id)
  AND parent_id = ANY(sqlc.arg(parent_ids)::uuid[]) AND status = 'sent'
ORDER BY sent_at, id;

-- name: CountMyUnreadReminders :one
SELECT count(*)::bigint AS count FROM ai_outbox
WHERE store_id = sqlc.arg(store_id) AND recipient_id = sqlc.arg(recipient_id)
  AND status = 'sent' AND read_at IS NULL;

-- name: GetMyReminderParent :one
SELECT * FROM ai_outbox
WHERE id = sqlc.arg(id) AND store_id = sqlc.arg(store_id) AND recipient_id = sqlc.arg(recipient_id)
  AND status = 'sent' AND parent_id IS NULL
FOR NO KEY UPDATE;

-- name: GetInbox :one
SELECT * FROM ai_inboxes WHERE id = sqlc.arg(id);

-- name: MarkMyReminderRead :exec
UPDATE ai_outbox SET read_at = sqlc.arg(now)
WHERE store_id = sqlc.arg(store_id) AND recipient_id = sqlc.arg(recipient_id)
  AND status = 'sent' AND read_at IS NULL AND (id = sqlc.arg(id) OR parent_id = sqlc.arg(id));

-- name: CancelOpenRequestsForSubject :exec
UPDATE ai_requests SET status = 'cancelled'
WHERE store_id = sqlc.arg(store_id) AND subject_kind = sqlc.arg(subject_kind) AND subject_id = sqlc.arg(subject_id)
  AND status = 'open';

-- name: IncrementRequestAttempt :one
UPDATE ai_requests SET attempt = attempt + 1
WHERE id = sqlc.arg(id) AND store_id = sqlc.arg(store_id) AND status = 'open'
RETURNING *;

-- name: WithdrawOutboxForRecipient :exec
UPDATE ai_outbox SET status = 'withdrawn', withdrawn_at = sqlc.arg(now)
WHERE store_id = sqlc.arg(store_id) AND recipient_id = sqlc.arg(recipient_id)
  AND subject_kind = sqlc.arg(subject_kind) AND subject_id = sqlc.arg(subject_id)
  AND status <> 'withdrawn';

-- name: LockCouponRedemptionSubject :one
SELECT * FROM coupon_redemptions
WHERE store_id = sqlc.arg(store_id) AND id = sqlc.arg(id)
FOR SHARE;

-- name: GetOpenRequestForSubjectAndStaff :one
SELECT * FROM ai_requests
WHERE store_id = sqlc.arg(store_id) AND subject_kind = sqlc.arg(subject_kind) AND subject_id = sqlc.arg(subject_id)
  AND to_staff_id = sqlc.arg(to_staff_id) AND status = 'open'
ORDER BY created_at, id
LIMIT 1;

-- name: CancelFollowUpForRequest :exec
UPDATE ai_timers SET status = 'cancelled'
WHERE store_id = sqlc.arg(store_id) AND inbox_id = sqlc.arg(inbox_id) AND kind = 'follow_up' AND status = 'scheduled'
  AND subject_kind = sqlc.arg(subject_kind) AND subject_id = sqlc.arg(subject_id)
  AND payload->>'request_id' = sqlc.arg(request_id)::text;

-- name: InsertClassifierCheck :exec
INSERT INTO ai_classifier_checks (tenant_id, store_id, inbox_id, session_id, tool, args, decision, source, reason, model_reply, created_at)
VALUES (sqlc.arg(tenant_id), sqlc.arg(store_id), sqlc.arg(inbox_id), sqlc.narg(session_id), sqlc.arg(tool),
        sqlc.arg(args)::jsonb, sqlc.arg(decision), sqlc.arg(source), sqlc.narg(reason), sqlc.narg(model_reply),
        sqlc.arg(created_at));

-- name: ExistsReminderBody :one
SELECT EXISTS (
  SELECT 1 FROM ai_outbox
  WHERE store_id = sqlc.arg(store_id) AND recipient_id = sqlc.arg(recipient_id)
    AND subject_kind = sqlc.arg(subject_kind) AND subject_id = sqlc.arg(subject_id)
    AND body = sqlc.arg(body) AND status <> 'withdrawn'
) AS exists;

-- name: ListClassifierReminders :many
SELECT tier, body, status, COALESCE(sent_at, withdrawn_at, queued_at) AS at
FROM ai_outbox
WHERE store_id = sqlc.arg(store_id) AND recipient_id = sqlc.arg(recipient_id)
  AND subject_kind = sqlc.arg(subject_kind) AND subject_id = sqlc.arg(subject_id)
  AND status <> 'withdrawn'
ORDER BY queued_at DESC, id DESC
LIMIT 5;
