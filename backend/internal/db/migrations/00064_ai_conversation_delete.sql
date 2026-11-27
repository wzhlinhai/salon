-- +goose Up
GRANT DELETE ON ai_conversation_messages, ai_conversations TO salon_app;

-- +goose Down
REVOKE DELETE ON ai_conversation_messages, ai_conversations FROM salon_app;
