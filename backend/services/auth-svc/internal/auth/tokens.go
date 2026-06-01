package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// refreshTokenRawBytes — длина «сырого» refresh-токена до base64-кодирования.
const refreshTokenRawBytes = 32

// GenerateRefreshToken генерирует случайный refresh-токен и его sha256-хеш.
//
// raw — base64 URL-safe строка (без padding) длиной 43 символа,
// hash — sha256(raw) в hex длиной 64 символа. raw отдаётся клиенту, hash в БД.
func GenerateRefreshToken() (raw, hash string, err error) {
	b := make([]byte, refreshTokenRawBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("auth: read refresh: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	hash = HashRefreshToken(raw)
	return raw, hash, nil
}

// HashRefreshToken возвращает hex-encoded sha256(raw). Детерминирован.
//
// Хешируется именно строковое представление raw (то, что лежит в куках/БД),
// чтобы lookup по hash в БД был стабилен.
func HashRefreshToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// GenerateOpaqueToken возвращает случайный URL-safe токен длиной size байт
// в base64 без padding. Используется для tg_link_token.
func GenerateOpaqueToken(size int) (string, error) {
	if size <= 0 {
		return "", fmt.Errorf("auth: bad token size %d", size)
	}
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: read token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
