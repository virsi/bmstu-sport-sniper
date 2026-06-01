package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound — запись в БД не найдена. Сервисный слой маппит в errs.NotFound /
// gRPC NotFound.
var ErrNotFound = errors.New("store: not found")

// ErrAlreadyExists — нарушение уникальности (например email уже занят).
var ErrAlreadyExists = errors.New("store: already exists")

// pgUniqueViolation — SQLSTATE 23505 (unique_violation).
const pgUniqueViolation = "23505"

// Store — фасад над pgxpool.Pool с типизированными методами доступа к auth_db.
//
// Единственный интерфейс persistence-слоя, потребляемый internal/auth. Для
// тестов сервисного слоя используется мок этого Store через интерфейс
// auth.userStore + auth.refreshStore.
type Store struct {
	pool *pgxpool.Pool
}

// New создаёт Store с готовым pgxpool.Pool.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// ----------------------------------------------------------------------------
// users
// ----------------------------------------------------------------------------

const userColumns = "id, email, password_hash, tg_chat_id, tg_link_token, is_active, created_at, last_seen_at"

// GetUserByEmail возвращает пользователя по email. ErrNotFound если нет.
func (s *Store) GetUserByEmail(ctx context.Context, email string) (User, error) {
	const q = "SELECT " + userColumns + " FROM users WHERE email = $1"
	row := s.pool.QueryRow(ctx, q, email)
	return scanUser(row)
}

// GetUserByID возвращает пользователя по id. ErrNotFound если нет.
func (s *Store) GetUserByID(ctx context.Context, id int64) (User, error) {
	const q = "SELECT " + userColumns + " FROM users WHERE id = $1"
	row := s.pool.QueryRow(ctx, q, id)
	return scanUser(row)
}

// GetUserByTgLinkToken возвращает пользователя с заданным tg_link_token.
// ErrNotFound если код не найден (или уже использован).
func (s *Store) GetUserByTgLinkToken(ctx context.Context, token string) (User, error) {
	const q = "SELECT " + userColumns + " FROM users WHERE tg_link_token = $1"
	row := s.pool.QueryRow(ctx, q, token)
	return scanUser(row)
}

// CreateUser создаёт пользователя с email и password_hash.
// ErrAlreadyExists при дубликате email (23505).
func (s *Store) CreateUser(ctx context.Context, email, passwordHash string) (User, error) {
	const q = "INSERT INTO users (email, password_hash) VALUES ($1, $2) RETURNING " + userColumns
	row := s.pool.QueryRow(ctx, q, email, passwordHash)
	u, err := scanUser(row)
	if err != nil {
		if isUniqueViolation(err) {
			return User{}, ErrAlreadyExists
		}
		return User{}, fmt.Errorf("store: create user: %w", err)
	}
	return u, nil
}

// UpdateLastSeen обновляет last_seen_at = now() для пользователя id.
func (s *Store) UpdateLastSeen(ctx context.Context, id int64) error {
	const q = "UPDATE users SET last_seen_at = now() WHERE id = $1"
	if _, err := s.pool.Exec(ctx, q, id); err != nil {
		return fmt.Errorf("store: update last_seen: %w", err)
	}
	return nil
}

// SetTgChatID привязывает chat_id к пользователю и очищает tg_link_token.
func (s *Store) SetTgChatID(ctx context.Context, id, chatID int64) error {
	const q = "UPDATE users SET tg_chat_id = $2, tg_link_token = NULL WHERE id = $1"
	if _, err := s.pool.Exec(ctx, q, id, chatID); err != nil {
		return fmt.Errorf("store: set tg_chat_id: %w", err)
	}
	return nil
}

// SetTgLinkToken сохраняет одноразовый код привязки Telegram.
// Уникальный частичный индекс гарантирует, что код можно использовать только раз.
func (s *Store) SetTgLinkToken(ctx context.Context, id int64, token string) error {
	const q = "UPDATE users SET tg_link_token = $2 WHERE id = $1"
	if _, err := s.pool.Exec(ctx, q, id, token); err != nil {
		return fmt.Errorf("store: set tg_link_token: %w", err)
	}
	return nil
}

// ----------------------------------------------------------------------------
// refresh_tokens
// ----------------------------------------------------------------------------

const refreshColumns = "id, user_id, token_hash, expires_at, revoked, replaced_by, created_at"

// CreateRefreshToken вставляет новый refresh-токен.
func (s *Store) CreateRefreshToken(ctx context.Context, userID int64, tokenHash string, expiresAt time.Time) (RefreshToken, error) {
	const q = "INSERT INTO refresh_tokens (user_id, token_hash, expires_at) VALUES ($1, $2, $3) RETURNING " + refreshColumns
	row := s.pool.QueryRow(ctx, q, userID, tokenHash, expiresAt)
	rt, err := scanRefresh(row)
	if err != nil {
		return RefreshToken{}, fmt.Errorf("store: create refresh: %w", err)
	}
	return rt, nil
}

// GetRefreshTokenByHash возвращает refresh-токен по hash. ErrNotFound если нет.
func (s *Store) GetRefreshTokenByHash(ctx context.Context, tokenHash string) (RefreshToken, error) {
	const q = "SELECT " + refreshColumns + " FROM refresh_tokens WHERE token_hash = $1"
	row := s.pool.QueryRow(ctx, q, tokenHash)
	return scanRefresh(row)
}

// RevokeRefreshToken помечает refresh-токен как revoked.
func (s *Store) RevokeRefreshToken(ctx context.Context, id int64) error {
	const q = "UPDATE refresh_tokens SET revoked = TRUE WHERE id = $1"
	if _, err := s.pool.Exec(ctx, q, id); err != nil {
		return fmt.Errorf("store: revoke refresh: %w", err)
	}
	return nil
}

// MarkReplacedBy выставляет revoked=TRUE и replaced_by=newID — применяется
// при rotation, чтобы reuse-detection мог отличить «отозванный из-за ротации»
// от «отозванный из-за logout».
func (s *Store) MarkReplacedBy(ctx context.Context, id, newID int64) error {
	const q = "UPDATE refresh_tokens SET revoked = TRUE, replaced_by = $2 WHERE id = $1"
	if _, err := s.pool.Exec(ctx, q, id, newID); err != nil {
		return fmt.Errorf("store: mark replaced_by: %w", err)
	}
	return nil
}

// RevokeAllForUser отзывает все активные refresh-токены пользователя.
// Используется при reuse-detection и при глобальном logout.
func (s *Store) RevokeAllForUser(ctx context.Context, userID int64) error {
	const q = "UPDATE refresh_tokens SET revoked = TRUE WHERE user_id = $1 AND revoked = FALSE"
	if _, err := s.pool.Exec(ctx, q, userID); err != nil {
		return fmt.Errorf("store: revoke all for user: %w", err)
	}
	return nil
}

// ----------------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------------

// scanUser считывает строку pgx.Row в User. pgx.ErrNoRows → ErrNotFound.
func scanUser(row pgx.Row) (User, error) {
	var u User
	if err := row.Scan(
		&u.ID,
		&u.Email,
		&u.PasswordHash,
		&u.TgChatID,
		&u.TgLinkToken,
		&u.IsActive,
		&u.CreatedAt,
		&u.LastSeenAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("store: scan user: %w", err)
	}
	return u, nil
}

// scanRefresh считывает строку pgx.Row в RefreshToken.
func scanRefresh(row pgx.Row) (RefreshToken, error) {
	var rt RefreshToken
	if err := row.Scan(
		&rt.ID,
		&rt.UserID,
		&rt.TokenHash,
		&rt.ExpiresAt,
		&rt.Revoked,
		&rt.ReplacedBy,
		&rt.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RefreshToken{}, ErrNotFound
		}
		return RefreshToken{}, fmt.Errorf("store: scan refresh: %w", err)
	}
	return rt, nil
}
