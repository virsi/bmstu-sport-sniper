package store

import (
	"context"
	"time"
)

const upsertSession = `INSERT INTO bmstu_sessions (
    user_id, cookies_blob, nonce, expires_at, last_refresh_at
)
VALUES (
    $1, $2, $3, $4, now()
)
ON CONFLICT (user_id) DO UPDATE SET
    cookies_blob    = EXCLUDED.cookies_blob,
    nonce           = EXCLUDED.nonce,
    expires_at      = EXCLUDED.expires_at,
    last_refresh_at = now()`

// UpsertSessionParams — параметры UpsertSession.
//
// Nonce — первые crypto.NonceSize байт CookiesBlob (дубль для аудита, см.
// BmstuSession godoc). При расшифровке не используется.
type UpsertSessionParams struct {
	UserID      string
	CookiesBlob []byte
	Nonce       []byte
	ExpiresAt   *time.Time
}

// UpsertSession сохраняет/обновляет cookies-blob пользователя.
func (q *Queries) UpsertSession(ctx context.Context, arg UpsertSessionParams) error {
	_, err := q.db.Exec(ctx, upsertSession, arg.UserID, arg.CookiesBlob, arg.Nonce, arg.ExpiresAt)
	return err
}

const getSession = `SELECT user_id, cookies_blob, nonce, expires_at, last_refresh_at
FROM bmstu_sessions
WHERE user_id = $1`

// GetSession возвращает сессию пользователя или pgx.ErrNoRows.
func (q *Queries) GetSession(ctx context.Context, userID string) (BmstuSession, error) {
	row := q.db.QueryRow(ctx, getSession, userID)
	var s BmstuSession
	err := row.Scan(&s.UserID, &s.CookiesBlob, &s.Nonce, &s.ExpiresAt, &s.LastRefreshAt)
	return s, err
}

const deleteSession = `DELETE FROM bmstu_sessions WHERE user_id = $1`

// DeleteSession удаляет сессию пользователя. Идемпотентен.
func (q *Queries) DeleteSession(ctx context.Context, userID string) error {
	_, err := q.db.Exec(ctx, deleteSession, userID)
	return err
}

const touchSession = `UPDATE bmstu_sessions
SET last_refresh_at = now()
WHERE user_id = $1`

// TouchSession обновляет last_refresh_at; используется watchdog'ом.
func (q *Queries) TouchSession(ctx context.Context, userID string) error {
	_, err := q.db.Exec(ctx, touchSession, userID)
	return err
}
