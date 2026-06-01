-- name: UpsertSession :exec
INSERT INTO bmstu_sessions (
    user_id, cookies_blob, nonce, expires_at, last_refresh_at
)
VALUES (
    $1, $2, $3, $4, now()
)
ON CONFLICT (user_id) DO UPDATE SET
    cookies_blob    = EXCLUDED.cookies_blob,
    nonce           = EXCLUDED.nonce,
    expires_at      = EXCLUDED.expires_at,
    last_refresh_at = now();

-- name: GetSession :one
SELECT
    user_id,
    cookies_blob,
    nonce,
    expires_at,
    last_refresh_at
FROM bmstu_sessions
WHERE user_id = $1;

-- name: DeleteSession :exec
DELETE FROM bmstu_sessions
WHERE user_id = $1;

-- name: TouchSession :exec
UPDATE bmstu_sessions
SET last_refresh_at = now()
WHERE user_id = $1;
