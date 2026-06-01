# Keycloak BMSTU OIDC Recon (2026-06-02)

## TL;DR

ROPC закрыт (`unauthorized_client`). Используем **эмуляцию Authorization Code через HTML-парс формы Keycloak**, без headless-браузера.

## Issuer

- Issuer: `https://sso.bmstu.ru/kc/realms/ph`
- Discovery: `https://sso.bmstu.ru/kc/realms/ph/.well-known/openid-configuration` (200 OK, публично)
- Keycloak base path: `/kc/` (не `/auth/` — Quarkus с `--http-relative-path=/kc`)

### Endpoints

| Цель | URL |
|---|---|
| authorization_endpoint | `https://sso.bmstu.ru/kc/realms/ph/protocol/openid-connect/auth` |
| token_endpoint | `https://sso.bmstu.ru/kc/realms/ph/protocol/openid-connect/token` |
| userinfo_endpoint | `https://sso.bmstu.ru/kc/realms/ph/protocol/openid-connect/userinfo` |
| end_session_endpoint | `https://sso.bmstu.ru/kc/realms/ph/protocol/openid-connect/logout` |
| jwks_uri | `https://sso.bmstu.ru/kc/realms/ph/protocol/openid-connect/certs` |
| login_action | `https://sso.bmstu.ru/kc/realms/ph/login-actions/authenticate` |

## Grant types для `client_id=sso`

| grant_type | Доступен | Решение |
|---|---|---|
| `authorization_code` | да | используем (через эмуляцию формы) |
| `refresh_token` | да | косвенно через portal4 |
| `password` (ROPC) | **нет** | `unauthorized_client` |
| `client_credentials` | n/a | нет secret |
| `device_code` | теоретически | не подходит |

## Архитектура auth

```
Browser → lks.bmstu.ru/portal4/cookie/login
        → 302 → sso.bmstu.ru/kc/realms/ph/protocol/openid-connect/auth?client_id=sso&...
        → user logs in form
        → 302 → lks.bmstu.ru/portal4/upstream/callback/kc?code=...&state=...
        → portal4 обменивает code на token, ставит p4sess (HttpOnly)
        → 302 → /profile
```

Фронт `lks.bmstu.ru` **не** OAuth-клиент. OAuth-handshake делает backend-прокси `portal4`. API `/lks-back/api/v1/...` валидирует **только** cookie `p4sess` (NestJS-стиль). Bearer не работает.

## Точная последовательность HTTP (имитируем в Go)

1. `GET https://lks.bmstu.ru/portal4/cookie/login?back=https://lks.bmstu.ru/profile&profile_any=1` (с cookiejar) → 302 → Keycloak auth с client_id=sso. Сохраняется cookie `p4sess` (Path=/portal4/).
2. Follow redirect → `GET sso.bmstu.ru/kc/.../auth?...` → 200 HTML с формой. Cookies: `AUTH_SESSION_ID`, `KC_RESTART`, `KC_AUTH_SESSION_HASH` (Max-Age=60).
3. Из HTML вытащить `<form id="kc-form-login" action="...login-actions/authenticate?session_code=X&execution=Y&client_id=sso&tab_id=Z&client_data=W">`.
4. `POST <form-action>` с `username=...&password=...&credentialId=` (Content-Type: application/x-www-form-urlencoded). Успех = 302 на `lks.bmstu.ru/portal4/upstream/callback/kc?code=...&state=...`. Ошибка = 200 HTML с тем же `id="kc-form-login"` и `class="alert-error"`.
5. Follow → portal4 обменивает code → ставит финальный `p4sess` → 302 на `/profile`. Готово.
6. `GET /lks-back/api/v1/fv/{SEMESTER_UUID}/groups` с тем же cookiejar.

## Подводные камни

1. **CSRF-токена в форме НЕТ.** Анти-replay в query (`session_code`, `execution`, `tab_id`, `client_data`).
2. **Один cookiejar на весь handshake.** Cookies `AUTH_SESSION_ID`+`KC_RESTART`+`KC_AUTH_SESSION_HASH`+`p4sess` обязаны долететь.
3. **`KC_AUTH_SESSION_HASH` живёт 60 с** — между GET формы и POST credentials не делать пауз.
4. **User-Agent обязателен** — `Mozilla/5.0 ...`, не `Go-http-client/1.1`.
5. **Парсить именно `<form id="kc-form-login">`**, не первый form (Keycloak умеет тогглить WebAuthn/OTP).
6. **Detect bad credentials** — 200 OK + `id="kc-form-login"` или `class="alert-error"` в body.
7. **Brute-force protection** Keycloak → exponential backoff, не ретраить `invalid_grant` авто.
8. **API только cookie**, Bearer не объявляется в `WWW-Authenticate`.
9. **Health-check сессии**: `GET portal4/cookie/watchdog` каждые 30 с — возвращает `{status:OK|FAIL, interval:30}`.
10. **`p4sess` TTL** — точно не измерено (нет кредов). При 401 → один полный re-login.

## Дизайн пакета `backend/services/bmstu-svc/internal/oidc/`

- `client.go` — `http.Client` + `cookiejar.Jar` + UA
- `login.go` — 4-шаговый handshake
- `parser.go` — извлечение `<form id="kc-form-login">` через `golang.org/x/net/html`
- `errors.go` — `ErrBadCredentials`, `ErrSessionExpired`, `ErrRateLimited`, `ErrCaptcha` (defensive)
- `watchdog.go` — health-check через portal4/cookie/watchdog
- **НЕ** использовать `golang.org/x/oauth2` или `coreos/go-oidc` — мы эмулятор браузера, не OAuth-клиент
- **НЕ** хранить access/refresh токены — только `p4sess` (+ остальные cookies из jar)
- Persist: gob-сериализация `[]*http.Cookie` → AES-GCM → `bmstu_sessions.cookies_blob`

Headless-fallback (chromedp) держать как `fallback.go.disabled` на случай добавления reCAPTCHA.

## Источники

- https://sso.bmstu.ru/kc/realms/ph/.well-known/openid-configuration (живой JSON)
- https://lks.bmstu.ru/portal4/cookie/login (наблюдаемый 302)
- Keycloak docs: Direct Access Grants, login-actions/authenticate
- https://wikival.bmstu.ru — упоминание realm `ph` и `ph-master` admin client
- /tmp/bmstu-sport-sniper/main.py:78-148, 245-292 — подтверждение cookie-only паттерна
