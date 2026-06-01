// Package jwtx предоставляет тонкую обёртку над golang-jwt/jwt/v5 для
// HS256-токенов: подпись access/refresh, верификация, типизированные claims
// с UserID и TokenID для refresh-rotation/reuse-detection.
package jwtx

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ErrInvalidToken возвращается при структурно невалидном токене.
var ErrInvalidToken = errors.New("jwtx: invalid token")

// ErrExpired возвращается, когда токен истёк.
var ErrExpired = errors.New("jwtx: token expired")

// TokenKind различает access и refresh токены (хранится в claim "kind").
type TokenKind string

const (
	// TokenAccess — короткоживущий access-токен (15 мин).
	TokenAccess TokenKind = "access"
	// TokenRefresh — refresh-токен (30 дней) с rotation/reuse-detection.
	TokenRefresh TokenKind = "refresh"
)

// Claims — payload JWT-токена. TokenID нужен для refresh-rotation:
// при каждом refresh выпускается новый TokenID, старый помечается revoked,
// повторное использование = reuse-attack → revoke всей сессии.
type Claims struct {
	// UserID — UUID пользователя (subject).
	UserID string `json:"uid"`
	// Kind — access или refresh.
	Kind TokenKind `json:"kind"`
	// TokenID — уникальный id токена (jti), для rotation/revocation.
	TokenID string `json:"tid,omitempty"`
	jwt.RegisteredClaims
}

// Signer — подписывает токены с фиксированным HS256-секретом.
type Signer struct {
	secret []byte
	issuer string
}

// NewSigner создаёт Signer. secret — HMAC-ключ (любой длины ≥32 байт),
// issuer кладётся в claim "iss" для аудита.
func NewSigner(secret []byte, issuer string) *Signer {
	return &Signer{secret: secret, issuer: issuer}
}

// Sign подписывает claims методом HS256.
func (s *Signer) Sign(c Claims) (string, error) {
	if c.Issuer == "" {
		c.Issuer = s.issuer
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	signed, err := tok.SignedString(s.secret)
	if err != nil {
		return "", fmt.Errorf("jwtx: sign: %w", err)
	}
	return signed, nil
}

// NewClaims — конструктор Claims с заполнением exp/iat/nbf.
func NewClaims(userID string, kind TokenKind, tokenID string, ttl time.Duration) Claims {
	now := time.Now()
	return Claims{
		UserID:  userID,
		Kind:    kind,
		TokenID: tokenID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			ID:        tokenID,
		},
	}
}

// Verifier — валидирует HS256-токены тем же секретом.
type Verifier struct {
	secret []byte
}

// NewVerifier создаёт Verifier.
func NewVerifier(secret []byte) *Verifier {
	return &Verifier{secret: secret}
}

// Verify парсит и проверяет токен. Возвращает Claims при успехе.
// Преобразует библиотечные ошибки в ErrExpired / ErrInvalidToken.
func (v *Verifier) Verify(token string) (*Claims, error) {
	parsed, err := jwt.ParseWithClaims(token, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("jwtx: unexpected signing method %v", t.Header["alg"])
		}
		return v.secret, nil
	})
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrExpired
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	c, ok := parsed.Claims.(*Claims)
	if !ok || !parsed.Valid {
		return nil, ErrInvalidToken
	}
	return c, nil
}
