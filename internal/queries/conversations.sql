-- name: CreateConversation :one
INSERT INTO conversations (workspace_id, mode)
VALUES ($1, $2)
RETURNING *;

-- name: GetConversationByID :one
SELECT * FROM conversations WHERE id = $1;
