// Package oidc — pure-HTTP клиент Keycloak SSO для BMSTU LKS.
//
// Имитирует браузер: cookiejar + HTML-парсинг формы Keycloak.
// Не использует OAuth-протокол как клиент — `lks.bmstu.ru/portal4` сам
// обменивает code на токен, а наружу видны только cookies (`p4sess`).
//
// Подробности — `docs/research/keycloak-oidc-bmstu.md`.
package oidc

import "errors"

// Sentinel-ошибки сценариев OIDC-флоу.
//
// Маппинг в gRPC-коды (см. internal/server):
//   - ErrBadCredentials       → Unauthenticated.
//   - ErrSessionExpired       → Unavailable (poller может ретраить).
//   - ErrRateLimited          → ResourceExhausted.
//   - ErrCaptcha              → FailedPrecondition (нужно вмешательство).
//   - ErrLoginFormNotFound    → Internal (Keycloak поменял HTML).
//   - ErrUnexpectedResponse   → Internal.
var (
	// ErrBadCredentials — Keycloak отклонил username/password.
	ErrBadCredentials = errors.New("oidc: bad credentials")
	// ErrSessionExpired — cookies перестали приниматься LKS API
	// (часто 401/403 в /lks-back/...).
	ErrSessionExpired = errors.New("oidc: session expired")
	// ErrRateLimited — Keycloak brute-force protection включился.
	ErrRateLimited = errors.New("oidc: rate limited")
	// ErrCaptcha — в HTML-форме обнаружена reCAPTCHA / иная challenge.
	ErrCaptcha = errors.New("oidc: captcha required")
	// ErrLoginFormNotFound — HTML не содержит <form id="kc-form-login">.
	ErrLoginFormNotFound = errors.New("oidc: login form not found")
	// ErrUnexpectedResponse — неожиданный код/тело ответа upstream'а.
	ErrUnexpectedResponse = errors.New("oidc: unexpected response")
)
