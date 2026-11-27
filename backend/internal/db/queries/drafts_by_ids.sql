-- name: DraftsByIDs :many
SELECT * FROM ai_drafts WHERE store_id = $1 AND id = ANY($2::uuid[]) ORDER BY created_at, id;
