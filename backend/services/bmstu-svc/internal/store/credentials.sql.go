package store

import (
	"context"
	"time"
)

// upsertCredentials повторяет ON CONFLICT-логику sql/queries/credentials.sql.
//
//nolint:gosec // G101 false-positive: SQL literal с колонками `enc_password`/`password`, реальных секретов нет.
const upsertCredentials = `INSERT INTO bmstu_credentials (
    user_id, enc_login, enc_password, nonce_login, nonce_password, last_login_at, created_at, updated_at
)
VALUES (
    $1, $2, $3, $4, $5, $6, now(), now()
)
ON CONFLICT (user_id) DO UPDATE SET
    enc_login      = EXCLUDED.enc_login,
    enc_password   = EXCLUDED.enc_password,
    nonce_login    = EXCLUDED.nonce_login,
    nonce_password = EXCLUDED.nonce_password,
    last_login_at  = COALESCE(EXCLUDED.last_login_at, bmstu_credentials.last_login_at),
    updated_at     = now()`

// UpsertCredentialsParams — параметры UpsertCredentials.
//
// NonceLogin / NoncePassword — первые crypto.NonceSize байт EncLogin /
// EncPassword. Заполняются вызывающим кодом как дубль исключительно для
// аудита/наблюдаемости (см. BmstuCredential godoc), при decrypt не нужны.
type UpsertCredentialsParams struct {
	UserID        string
	EncLogin      []byte
	EncPassword   []byte
	NonceLogin    []byte
	NoncePassword []byte
	LastLoginAt   *time.Time
}

// UpsertCredentials вставляет или обновляет креды пользователя.
func (q *Queries) UpsertCredentials(ctx context.Context, arg UpsertCredentialsParams) error {
	_, err := q.db.Exec(ctx, upsertCredentials,
		arg.UserID,
		arg.EncLogin,
		arg.EncPassword,
		arg.NonceLogin,
		arg.NoncePassword,
		arg.LastLoginAt,
	)
	return err
}

//nolint:gosec // G101 false-positive: SQL SELECT с колонкой `enc_password`, не секрет.
const getCredentials = `SELECT user_id, enc_login, enc_password, nonce_login, nonce_password, last_login_at, created_at, updated_at
FROM bmstu_credentials
WHERE user_id = $1`

// GetCredentials возвращает креды пользователя или pgx.ErrNoRows.
func (q *Queries) GetCredentials(ctx context.Context, userID string) (BmstuCredential, error) {
	row := q.db.QueryRow(ctx, getCredentials, userID)
	var c BmstuCredential
	err := row.Scan(
		&c.UserID,
		&c.EncLogin,
		&c.EncPassword,
		&c.NonceLogin,
		&c.NoncePassword,
		&c.LastLoginAt,
		&c.CreatedAt,
		&c.UpdatedAt,
	)
	return c, err
}

//nolint:gosec // G101 false-positive: SQL DELETE для таблицы кредов, не секрет.
const deleteCredentials = `DELETE FROM bmstu_credentials WHERE user_id = $1`

// DeleteCredentials удаляет креды пользователя; CASCADE удалит и сессию.
// Идемпотентен.
func (q *Queries) DeleteCredentials(ctx context.Context, userID string) error {
	_, err := q.db.Exec(ctx, deleteCredentials, userID)
	return err
}

//nolint:gosec // G101 false-positive: SELECT статус-снэпшота из таблицы кредов, не секрет.
const getCredentialsStatus = `SELECT user_id, last_login_at, created_at, updated_at
FROM bmstu_credentials
WHERE user_id = $1`

// GetCredentialsStatus возвращает статус-snapshot без расшифровки полей.
func (q *Queries) GetCredentialsStatus(ctx context.Context, userID string) (BmstuCredentialStatus, error) {
	row := q.db.QueryRow(ctx, getCredentialsStatus, userID)
	var s BmstuCredentialStatus
	err := row.Scan(&s.UserID, &s.LastLoginAt, &s.CreatedAt, &s.UpdatedAt)
	return s, err
}

//nolint:gosec // G101 false-positive: SQL UPDATE для таблицы кредов, не секрет.
const touchCredentialsLastLogin = `UPDATE bmstu_credentials
SET last_login_at = now(), updated_at = now()
WHERE user_id = $1`

// TouchCredentialsLastLogin отмечает время последнего успешного логина.
func (q *Queries) TouchCredentialsLastLogin(ctx context.Context, userID string) error {
	_, err := q.db.Exec(ctx, touchCredentialsLastLogin, userID)
	return err
}
