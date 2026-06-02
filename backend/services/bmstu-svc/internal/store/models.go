// Package store — слой доступа к bmstu_db (Postgres через pgx/v5).
//
// Структуры и методы повторяют контракт sql/queries/*.sql и
// предназначены для последующей замены на sqlc-generated код без
// изменения вызывающего кода. До установки sqlc в окружении сборки
// реализация ведётся вручную, но семантика (одни и те же query-имена,
// одни и те же rowscan-структуры) сохраняется.
package store

import (
	"time"
)

// BmstuCredential — строка таблицы bmstu_credentials.
//
// NonceLogin / NoncePassword — дубль первых crypto.NonceSize байт
// EncLogin / EncPassword соответственно (blob, который кладёт pkg/crypto,
// уже начинается с nonce). Колонки оставлены для аудита/наблюдаемости и
// симметрии со схемой; при расшифровке НЕ используются — pkg/crypto.Decrypt
// сам нарезает blob по NonceSize.
//
// HealthGroup — строковое значение группы здоровья (одно из 4-х; см.
// CHECK в migrations/bmstu_db/00003_health_group.sql). bmstu-svc мапит на
// common.v1.HealthGroup через internal/health.
type BmstuCredential struct {
	UserID        string     `json:"user_id"`
	EncLogin      []byte     `json:"enc_login"`
	EncPassword   []byte     `json:"enc_password"`
	NonceLogin    []byte     `json:"nonce_login"`
	NoncePassword []byte     `json:"nonce_password"`
	LastLoginAt   *time.Time `json:"last_login_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	HealthGroup   string     `json:"health_group"`
}

// BmstuCredentialStatus — компактный snapshot без секретов.
//
// HealthGroup — строковое значение группы здоровья (см. BmstuCredential).
type BmstuCredentialStatus struct {
	UserID      string     `json:"user_id"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	HealthGroup string     `json:"health_group"`
}

// BmstuSession — строка таблицы bmstu_sessions.
//
// Nonce — дубль первых crypto.NonceSize байт CookiesBlob. blob (созданный
// pkg/crypto.Encrypt) уже содержит nonce внутри себя; отдельное поле
// хранится для аудита/наблюдаемости и НЕ используется при расшифровке —
// pkg/crypto.Decrypt сам нарезает blob по NonceSize.
type BmstuSession struct {
	UserID        string     `json:"user_id"`
	CookiesBlob   []byte     `json:"cookies_blob"`
	Nonce         []byte     `json:"nonce"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	LastRefreshAt time.Time  `json:"last_refresh_at"`
}
