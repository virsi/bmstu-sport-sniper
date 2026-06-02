-- name: UpsertCredentials :exec
INSERT INTO bmstu_credentials (
    user_id, enc_login, enc_password, nonce_login, nonce_password, last_login_at, health_group, created_at, updated_at
)
VALUES (
    $1, $2, $3, $4, $5, $6, $7, now(), now()
)
ON CONFLICT (user_id) DO UPDATE SET
    enc_login      = EXCLUDED.enc_login,
    enc_password   = EXCLUDED.enc_password,
    nonce_login    = EXCLUDED.nonce_login,
    nonce_password = EXCLUDED.nonce_password,
    last_login_at  = COALESCE(EXCLUDED.last_login_at, bmstu_credentials.last_login_at),
    health_group   = EXCLUDED.health_group,
    updated_at     = now();

-- name: GetCredentials :one
SELECT
    user_id,
    enc_login,
    enc_password,
    nonce_login,
    nonce_password,
    last_login_at,
    created_at,
    updated_at,
    health_group
FROM bmstu_credentials
WHERE user_id = $1;

-- name: DeleteCredentials :exec
DELETE FROM bmstu_credentials
WHERE user_id = $1;

-- name: GetCredentialsStatus :one
-- GetCredentialsStatus возвращает только статусные поля без расшифровки
-- секретов; используется в GetStatus RPC. health_group возвращается для
-- отображения badge группы здоровья в UI.
SELECT
    user_id,
    last_login_at,
    created_at,
    updated_at,
    health_group
FROM bmstu_credentials
WHERE user_id = $1;

-- name: TouchCredentialsLastLogin :exec
UPDATE bmstu_credentials
SET last_login_at = now(), updated_at = now()
WHERE user_id = $1;
