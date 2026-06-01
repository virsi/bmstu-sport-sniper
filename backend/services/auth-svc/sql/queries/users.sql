-- name: GetUserByEmail :one
SELECT id, email, password_hash, tg_chat_id, tg_link_token, is_active, created_at, last_seen_at
FROM users
WHERE email = $1;

-- name: GetUserByID :one
SELECT id, email, password_hash, tg_chat_id, tg_link_token, is_active, created_at, last_seen_at
FROM users
WHERE id = $1;

-- name: GetUserByTgLinkToken :one
SELECT id, email, password_hash, tg_chat_id, tg_link_token, is_active, created_at, last_seen_at
FROM users
WHERE tg_link_token = $1;

-- name: CreateUser :one
INSERT INTO users (email, password_hash)
VALUES ($1, $2)
RETURNING id, email, password_hash, tg_chat_id, tg_link_token, is_active, created_at, last_seen_at;

-- name: UpdateLastSeen :exec
UPDATE users SET last_seen_at = now() WHERE id = $1;

-- name: SetTgChatID :exec
UPDATE users SET tg_chat_id = $2, tg_link_token = NULL WHERE id = $1;

-- name: SetTgLinkToken :exec
UPDATE users SET tg_link_token = $2 WHERE id = $1;
