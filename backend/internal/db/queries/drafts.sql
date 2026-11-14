-- name: CreateDraft :one
INSERT INTO ai_drafts (tenant_id,store_id,initiator,operation,utterance,params,original_params,summary,status,business_date,expires_at,created_at)
VALUES ($1,$2,$3,$4,$5,$6,$6,$7,'pending',$8,$9,$10) RETURNING *;

-- name: LockDraft :one
SELECT * FROM ai_drafts WHERE store_id=$1 AND id=$2 FOR NO KEY UPDATE;

-- name: GetDraft :one
SELECT * FROM ai_drafts WHERE store_id=$1 AND id=$2;

-- name: ListDrafts :many
SELECT * FROM ai_drafts WHERE store_id=$1 ORDER BY created_at, id;

-- name: SaveDraft :one
UPDATE ai_drafts SET params=$3,status=$4,decided_at=$5,decided_by=$6,reject_reason=$7
WHERE store_id=$1 AND id=$2 RETURNING *;

-- name: AddDraftEvent :exec
INSERT INTO ai_draft_events (tenant_id,store_id,draft_id,kind,before_params,after_params,actor_id,at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8);

-- name: DraftEvents :many
SELECT * FROM ai_draft_events WHERE store_id=$1 AND draft_id=$2 ORDER BY ordinal;

-- name: DraftAuditIDs :many
SELECT id FROM audit_log WHERE store_id=$1 AND draft_id=$2 ORDER BY at,id;

-- name: ExpireStoreDrafts :exec
WITH locked AS MATERIALIZED (
 SELECT x.id FROM ai_drafts x WHERE x.store_id= @store_id AND x.status='pending' AND x.expires_at<= @now::timestamptz
 ORDER BY x.id FOR NO KEY UPDATE
), expired AS (
 UPDATE ai_drafts d SET status='expired',decided_at= @now
 FROM locked l WHERE d.id=l.id
 RETURNING d.*
)
INSERT INTO ai_draft_events (tenant_id,store_id,draft_id,kind,before_params,after_params,actor_id,at)
SELECT tenant_id,store_id,id,'expired',params,params,@actor_id,@now FROM expired;
