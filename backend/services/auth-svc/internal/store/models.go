// Package store — слой доступа к БД auth_db.
//
// Реализация повторяет API, который сгенерирует sqlc по queries из
// services/auth-svc/sql/queries. Если sqlc установлен — можно перегенерить
// этот пакет через `make sqlc`; ручная реализация эквивалентна по сигнатурам.
package store

import "time"

// User — строка таблицы users.
type User struct {
	// ID — внутренний BIGSERIAL ключ.
	ID int64 `json:"id"`
	// Email — нормализованный (lowercase, trimmed) email пользователя.
	Email string `json:"email"`
	// PasswordHash — argon2id-хеш пароля в формате PHC.
	PasswordHash string `json:"password_hash"`
	// TgChatID — Telegram chat_id, если привязан, иначе nil.
	TgChatID *int64 `json:"tg_chat_id,omitempty"`
	// TgLinkToken — одноразовый код привязки Telegram, NULL после Complete.
	TgLinkToken *string `json:"tg_link_token,omitempty"`
	// IsActive — флаг активного аккаунта (soft-delete за пределами V1).
	IsActive bool `json:"is_active"`
	// CreatedAt — момент регистрации, UTC.
	CreatedAt time.Time `json:"created_at"`
	// LastSeenAt — последний успешный запрос пользователя, UTC.
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
}

// RefreshToken — строка таблицы refresh_tokens.
type RefreshToken struct {
	// ID — внутренний BIGSERIAL ключ.
	ID int64 `json:"id"`
	// UserID — владелец токена, FK на users.id.
	UserID int64 `json:"user_id"`
	// TokenHash — sha256(raw_token) в hex. raw_token не хранится.
	TokenHash string `json:"token_hash"`
	// ExpiresAt — момент истечения refresh, UTC.
	ExpiresAt time.Time `json:"expires_at"`
	// Revoked — признак отзыва (logout / rotation / reuse).
	Revoked bool `json:"revoked"`
	// ReplacedBy — ID нового refresh, выпущенного в результате rotation.
	// nil, если токен ещё активен либо отозван не через rotation.
	ReplacedBy *int64 `json:"replaced_by,omitempty"`
	// CreatedAt — момент выпуска, UTC.
	CreatedAt time.Time `json:"created_at"`
}
