# Final Code Review Findings (2026-06-02)

## Готовность к production: 6/10

### Сильные стороны
- argon2id параметры корректны (OWASP 2024)
- AES-256-GCM правильно: random nonce, Seal/Open с auth-tag
- JWT rotation + reuse-detection реализовано
- gRPC error codes правильно выбраны (Unauthenticated/AlreadyExists/FailedPrecondition/ResourceExhausted/Unavailable)
- Нет утечек passwords/tokens/cookies в логах (проверено grep'ом)
- Concurrency safe: sync.Map + per-entry mutex в backoff, SSE cleanup идемпотентен
- Error wrapping `%w` consistently
- TSDoc + godoc на exported symbols
- ~220 unit + 35 integration + 13 e2e тестов

### Топ-5 issues перед production

| # | Severity | Location | Issue | Fix | Status |
|---|---|---|---|---|---|
| 1 | MED | `backend/services/bmstu-svc/internal/server/server.go:124-125` | Nonce hardcode `encL[:12]` — предполагает что crypto.Encrypt всегда выдаёт 12-byte nonce. Сломается молча при изменении. | Использовать константу `crypto.NonceSize` или явный split-helper. | OPEN |
| 2 | MED | `frontend/apps/web/src/api/client.ts:16-17` | Refresh-token в `localStorage` → XSS-уязвимо | Миграция на `httpOnly; Secure; SameSite=Strict` cookie. Влияет на `/auth/refresh` (без body). | **RESOLVED 2026-06-02** — refresh переехал в httpOnly cookie `rt` (Path=/api/auth, HttpOnly, Secure, SameSite=Strict, Max-Age=30d). Body fallback оставлен на transition. См. `backend/services/gateway-svc/internal/http/handler/auth.go`, `cookies.go`. |
| 3 | LOW | `backend/services/gateway-svc/internal/http/middleware/auth.go:145` | JWT в `?access=` query попадёт в access-logs | Tested что query-token не логируется, либо переход на one-time ticket. | **RESOLVED 2026-06-02** — введён endpoint `POST /api/stream/ticket` + `SSEAuth` middleware. Фронт `sse.ts` использует ticket (TTL 5 мин, one-shot). Legacy `?access=` оставлен как DEPRECATED. См. `backend/services/gateway-svc/internal/ticket/store.go`, `internal/http/middleware/sse.go`. |
| 4 | LOW | `backend/services/bmstu-svc/internal/session/manager.go:170` | nonce дублируется отдельным полем в БД при том что он внутри blob (первые 12 байт) | Документировать что nonce-колонка — для аудита, при decrypt не нужна. | OPEN |
| 5 | LOW | docs/* | DRY паттерны (`pkg/grpcx`, `pkg/config`, `pkg/bootstrap`) не задокументированы в overview | Добавить в `docs/architecture.md` секцию «Shared libraries» с описанием каждого pkg. | OPEN |

### Не-критичные наблюдения

- argon2id, AES-GCM, JWT — крипто-fundamentals корректны, не править
- Валидация форм через VeeValidate+zod — все async actions имеют loading/error states
- gRPC codes правильно
- Refresh dedupe в `client.ts:107-110` race-safe (JS single-threaded)

### Что нужно операционно перед production

1. Метрики + Prometheus + Grafana (Wave 5 devops в работе)
2. mTLS между gRPC сервисами (V2)
3. WAF rules в Caddy (rate-limit per-IP, geo-block если нужно)
4. Logrotation + backup strategy для Postgres
5. Penetration testing external team
6. Canary deployment / blue-green
7. Alerting на Telegram (auth_failures spike, bmstu_login_attempts {result=banned}, cycles_failed_total spike)
