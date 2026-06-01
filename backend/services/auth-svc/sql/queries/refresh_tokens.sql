-- name: CreateRefreshToken :one
INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
VALUES ($1, $2, $3)
RETURNING id, user_id, token_hash, expires_at, revoked, replaced_by, created_at;

-- name: GetRefreshTokenByHash :one
SELECT id, user_id, token_hash, expires_at, revoked, replaced_by, created_at
FROM refresh_tokens
WHERE token_hash = $1;

-- name: RevokeRefreshToken :exec
UPDATE refresh_tokens SET revoked = TRUE WHERE id = $1;

-- name: MarkReplacedBy :exec
UPDATE refresh_tokens SET revoked = TRUE, replaced_by = $2 WHERE id = $1;

-- name: RevokeAllForUser :exec
UPDATE refresh_tokens SET revoked = TRUE WHERE user_id = $1 AND revoked = FALSE;
