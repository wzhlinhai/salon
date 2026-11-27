-- name: LockReminderStore :one
SELECT pg_try_advisory_xact_lock(hashtextextended(CAST(sqlc.arg(store_id)::uuid AS text) || '/ai_reminders', 0)) AS locked;

-- name: LockReminderRule :exec
SELECT pg_advisory_xact_lock(hashtextextended(CAST(sqlc.arg(store_id)::uuid AS text) || '/ai_reminder_rule/' || sqlc.arg(rule_key)::text, 0));

-- name: ListReminderRuleModes :many
SELECT * FROM ai_reminder_rules WHERE store_id = sqlc.arg(store_id) ORDER BY rule_key;

-- name: GetReminderRule :one
SELECT * FROM ai_reminder_rules WHERE store_id = sqlc.arg(store_id) AND rule_key = sqlc.arg(rule_key);

-- name: UpsertReminderRule :one
INSERT INTO ai_reminder_rules (tenant_id, store_id, rule_key, mode, updated_by)
VALUES (sqlc.arg(tenant_id), sqlc.arg(store_id), sqlc.arg(rule_key), sqlc.arg(mode), sqlc.arg(updated_by))
ON CONFLICT (tenant_id, store_id, rule_key) DO UPDATE
SET mode = EXCLUDED.mode, updated_by = EXCLUDED.updated_by, updated_at = clock_timestamp()
RETURNING *;

-- name: GetReminderRun :one
SELECT * FROM ai_reminder_runs
WHERE store_id = sqlc.arg(store_id) AND rule_key = sqlc.arg(rule_key)
  AND business_date = sqlc.arg(business_date) AND mode = sqlc.arg(mode);

-- name: UpsertReminderRun :one
INSERT INTO ai_reminder_runs (tenant_id, store_id, rule_key, business_date, mode, status, failures, last_error, matched)
VALUES (sqlc.arg(tenant_id), sqlc.arg(store_id), sqlc.arg(rule_key), sqlc.arg(business_date), sqlc.arg(mode), sqlc.arg(status), sqlc.arg(failures), sqlc.narg(last_error), sqlc.arg(matched))
ON CONFLICT (tenant_id, store_id, rule_key, business_date, mode) DO UPDATE
SET status = EXCLUDED.status, failures = EXCLUDED.failures, last_error = EXCLUDED.last_error,
    matched = EXCLUDED.matched, updated_at = clock_timestamp()
RETURNING *;

-- name: ReminderEmittedSameDay :one
SELECT EXISTS (
    SELECT 1 FROM ai_reminders
    WHERE store_id = sqlc.arg(store_id) AND rule_key = sqlc.arg(rule_key) AND mode = sqlc.arg(mode)
      AND recipient_staff_id = sqlc.arg(recipient_staff_id) AND subject_key = sqlc.arg(subject_key)
      AND business_date = sqlc.arg(business_date) AND outcome = 'emitted'
) AS exists;

-- name: ReminderEmittedInCooldown :one
SELECT EXISTS (
    SELECT 1 FROM ai_reminders
    WHERE store_id = sqlc.arg(store_id) AND rule_key = sqlc.arg(rule_key)
      AND mode = sqlc.arg(mode)
      AND recipient_staff_id = sqlc.arg(recipient_staff_id) AND subject_key = sqlc.arg(subject_key)
      AND business_date >= sqlc.arg(from_date) AND business_date < sqlc.arg(business_date)
      AND outcome = 'emitted'
) AS exists;

-- name: ReminderEmittedCount :one
SELECT count(*)::bigint AS count FROM ai_reminders
WHERE store_id = sqlc.arg(store_id) AND rule_key = sqlc.arg(rule_key)
  AND mode = sqlc.arg(mode)
  AND recipient_staff_id = sqlc.arg(recipient_staff_id) AND subject_key = sqlc.arg(subject_key)
  AND outcome = 'emitted';

-- name: ReminderDailyCount :one
SELECT count(*)::bigint AS count FROM ai_reminders
WHERE store_id = sqlc.arg(store_id) AND recipient_staff_id = sqlc.arg(recipient_staff_id)
  AND mode = sqlc.arg(mode) AND business_date = sqlc.arg(business_date)
  AND outcome = 'emitted' AND NOT urgent;

-- name: AddReminder :one
INSERT INTO ai_reminders (tenant_id, store_id, rule_key, rule_version, mode, recipient_staff_id, subject_key, business_date, urgent, facts, fallback_text, outcome)
VALUES (sqlc.arg(tenant_id), sqlc.arg(store_id), sqlc.arg(rule_key), sqlc.arg(rule_version), sqlc.arg(mode), sqlc.arg(recipient_staff_id), sqlc.arg(subject_key), sqlc.arg(business_date), sqlc.arg(urgent), sqlc.arg(facts)::jsonb, sqlc.arg(fallback_text), sqlc.arg(outcome))
RETURNING *;

-- name: SetReminderAIText :execrows
UPDATE ai_reminders SET ai_text = sqlc.arg(ai_text)
WHERE id = sqlc.arg(id) AND store_id = sqlc.arg(store_id) AND outcome = 'emitted';

-- name: ReminderAuditActions :many
SELECT action, at FROM audit_log
WHERE store_id = sqlc.arg(store_id) AND action = ANY(sqlc.arg(actions)::text[])
  AND at >= sqlc.arg(window_from) AND at < sqlc.arg(window_to);

-- name: ReminderSummaryCounts :one
SELECT
    (SELECT count(DISTINCT r.business_date)::int FROM ai_reminder_runs r
     WHERE r.store_id = sqlc.arg(store_id) AND r.rule_key = sqlc.arg(rule_key)
       AND r.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date) AS ran_days,
    (SELECT count(DISTINCT r.business_date)::int FROM ai_reminder_runs r
     WHERE r.store_id = sqlc.arg(store_id) AND r.rule_key = sqlc.arg(rule_key)
       AND r.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date AND r.matched > 0) AS matched_days,
    (SELECT count(DISTINCT r.business_date)::int FROM ai_reminder_runs r
     WHERE r.store_id = sqlc.arg(store_id) AND r.rule_key = sqlc.arg(rule_key)
       AND r.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date AND r.status = 'error') AS error_days,
    (SELECT COALESCE(max(r.business_date)::text, '')::text FROM ai_reminder_runs r
     WHERE r.store_id = sqlc.arg(store_id) AND r.rule_key = sqlc.arg(rule_key)
       AND r.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date AND r.status = 'error') AS last_error_date,
    (SELECT count(*)::bigint FROM ai_reminders r
     WHERE r.store_id = sqlc.arg(store_id) AND r.rule_key = sqlc.arg(rule_key)
       AND r.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date AND r.outcome = 'emitted') AS emitted,
    (SELECT count(*)::bigint FROM ai_reminders r
     WHERE r.store_id = sqlc.arg(store_id) AND r.rule_key = sqlc.arg(rule_key)
       AND r.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date AND r.outcome = 'deduped') AS deduped,
    (SELECT count(*)::bigint FROM ai_reminders r
     WHERE r.store_id = sqlc.arg(store_id) AND r.rule_key = sqlc.arg(rule_key)
       AND r.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date AND r.outcome = 'cooldown') AS cooldown,
    (SELECT count(*)::bigint FROM ai_reminders r
     WHERE r.store_id = sqlc.arg(store_id) AND r.rule_key = sqlc.arg(rule_key)
       AND r.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date AND r.outcome = 'max_times') AS max_times,
    (SELECT count(*)::bigint FROM ai_reminders r
     WHERE r.store_id = sqlc.arg(store_id) AND r.rule_key = sqlc.arg(rule_key)
       AND r.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date AND r.outcome = 'expired') AS expired,
    (SELECT count(*)::bigint FROM ai_reminders r
     WHERE r.store_id = sqlc.arg(store_id) AND r.rule_key = sqlc.arg(rule_key)
       AND r.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date AND r.outcome = 'daily_cap') AS daily_cap,
    (SELECT count(*)::bigint FROM ai_reminders r
     WHERE r.store_id = sqlc.arg(store_id) AND r.rule_key = sqlc.arg(rule_key)
       AND r.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date AND r.feedback = 'helpful') AS helpful,
    (SELECT count(*)::bigint FROM ai_reminders r
     WHERE r.store_id = sqlc.arg(store_id) AND r.rule_key = sqlc.arg(rule_key)
       AND r.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date AND r.feedback = 'not_helpful') AS not_helpful,
    (SELECT count(DISTINCT (r.recipient_staff_id, r.business_date))::bigint FROM ai_reminders r
     WHERE r.store_id = sqlc.arg(store_id) AND r.rule_key = sqlc.arg(rule_key)
       AND r.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date AND r.outcome = 'emitted') AS recipient_days;

-- name: ListReminderSamples :many
SELECT r.id, r.business_date, s.name AS recipient_name,
       COALESCE(r.ai_text, r.fallback_text)::text AS text,
       (r.ai_text IS NOT NULL)::boolean AS ai_written,
       r.facts, r.feedback, r.recipient_staff_id
FROM ai_reminders r
JOIN staff s ON s.tenant_id = r.tenant_id AND s.id = r.recipient_staff_id
WHERE r.store_id = sqlc.arg(store_id) AND r.rule_key = sqlc.arg(rule_key)
  AND r.business_date BETWEEN sqlc.arg(date_from)::date AND sqlc.arg(date_to)::date
  AND r.outcome = 'emitted'
ORDER BY r.business_date DESC, r.created_at DESC, r.id DESC
LIMIT 10;

-- name: GetReminderForFeedback :one
SELECT * FROM ai_reminders
WHERE id = sqlc.arg(id) AND store_id = sqlc.arg(store_id) AND outcome = 'emitted'
FOR NO KEY UPDATE;

-- name: SetReminderFeedback :execrows
UPDATE ai_reminders
SET feedback = sqlc.narg(feedback), feedback_by = sqlc.narg(feedback_by), feedback_at = sqlc.narg(feedback_at)
WHERE id = sqlc.arg(id) AND store_id = sqlc.arg(store_id);

-- name: SavepointReminderRule :exec
SAVEPOINT reminder_rule;

-- name: RollbackReminderRule :exec
ROLLBACK TO SAVEPOINT reminder_rule;

-- name: ReleaseReminderRule :exec
RELEASE SAVEPOINT reminder_rule;
