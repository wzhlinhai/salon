-- name: CreateAIConversation :one
INSERT INTO ai_conversations (tenant_id, store_id, staff_id, title, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $5) RETURNING *;

-- name: GetAIConversation :one
SELECT * FROM ai_conversations WHERE store_id = $1 AND staff_id = $2 AND id = $3;

-- name: TouchAIConversation :one
UPDATE ai_conversations SET updated_at = GREATEST(updated_at, $4), archived_at = NULL
WHERE store_id = $1 AND staff_id = $2 AND id = $3 RETURNING *;

-- name: SetAIConversationArchived :one
UPDATE ai_conversations SET archived_at = $4
WHERE store_id = $1 AND staff_id = $2 AND id = $3 RETURNING *;

-- name: SetAIConversationUnarchived :one
UPDATE ai_conversations SET archived_at = NULL
WHERE store_id = $1 AND staff_id = $2 AND id = $3 RETURNING *;

-- name: ListAIConversations :many
SELECT c.* FROM ai_conversations c
WHERE c.store_id = @store_id AND c.staff_id = @staff_id
  AND ((@archived::bool AND c.archived_at IS NOT NULL) OR (NOT @archived::bool AND c.archived_at IS NULL))
  AND (@query::text = '' OR c.title ILIKE @pattern OR EXISTS (
    SELECT 1 FROM ai_conversation_messages m
    WHERE m.tenant_id = c.tenant_id AND m.conversation_id = c.id AND m.content ILIKE @pattern
  ))
ORDER BY c.updated_at DESC, c.id
LIMIT 100;

-- name: ListRecentAIConversationMessages :many
SELECT * FROM (
  SELECT * FROM ai_conversation_messages
  WHERE store_id = $1 AND conversation_id = $2
  ORDER BY ordinal DESC
  LIMIT $3
) recent
ORDER BY ordinal;

-- name: ListAIConversationMessages :many
SELECT * FROM ai_conversation_messages
WHERE store_id = $1 AND conversation_id = $2
ORDER BY ordinal;

-- name: AddAIConversationMessage :one
INSERT INTO ai_conversation_messages (tenant_id, store_id, conversation_id, role, content, sent, editing, draft_ids, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING *;
