# gateway-svc

BFF (Backend-For-Frontend) — единственный сервис, доступный для браузера.
REST + SSE наружу, gRPC-клиенты внутрь.

## Архитектура

```
браузер ──HTTPS──▶ Caddy ──HTTP──▶ gateway-svc :8080
                                          │
                                          ├── gRPC ─▶ auth-svc      :9090
                                          ├── gRPC ─▶ bmstu-svc     :9090
                                          ├── gRPC ─▶ filter-svc    :9090
                                          ├── gRPC ─▶ notifier-svc  :9090
                                          ├── gRPC ─▶ teachers-svc  :9090
                                          └── NATS sub `alerts.<uid>` ──▶ SSE
```

gateway-svc:

- Маппит REST → gRPC и обратно (см. `docs/api.md`).
- Валидирует access JWT через `AuthService.VerifyAccess` (auth-svc — источник
  истины: знает про revoke).
- Прокидывает `user_id` в downstream gRPC через metadata `x-user-id`
  (`pkg/grpcx.WithUserID`).
- SSE-мост `/api/stream`: одна подписка NATS на коннект, cleanup на disconnect.
- Никакой БД. Stateless, горизонтально масштабируется (при необходимости
  поставьте лимитер не in-memory, а Redis-backed — TODO V2).

## REST API

Полный контракт — `docs/api.md`. Кратко:

```
POST   /api/auth/register     {email, password}                  → 201 User
POST   /api/auth/login        {email, password}                  → 200 TokenPair
POST   /api/auth/refresh      {refresh_token}                    → 200 TokenPair
POST   /api/auth/logout       {refresh_token?}                   → 204
GET    /api/me                                                    → 200 User
POST   /api/me/telegram/init                                      → 200 {deeplink, code, expires_at}
POST   /api/bmstu/creds       {login, password}                  → 204
GET    /api/bmstu/status                                          → 200 {status, last_login_at?, ...}
DELETE /api/bmstu/creds                                           → 204
GET    /api/filters?include_disabled=true                         → 200 {filters: [...]}
POST   /api/filters           Filter                              → 201 Filter
PATCH  /api/filters/:id       partial Filter                      → 200 Filter
DELETE /api/filters/:id                                           → 204
GET    /api/slots                                                 → 200 {slots: [...], fetched_at}
GET    /api/stream            (SSE)                               → text/event-stream
GET    /healthz | /readyz | /metrics
```

### Аутентификация

- `Authorization: Bearer <access>` — на всех ручках кроме открытых
  (`register`, `login`, `refresh`, `healthz`, `readyz`, `metrics`).
- Access JWT, HS256, TTL 15 минут.
- Refresh — opaque UUIDv7, TTL 30 дней, rotation на каждом `/refresh`.

### SSE (`/api/stream`)

- Авторизация: `Authorization` header **или** `?access=<token>` query
  (EventSource не умеет ставить headers).
- Каждые 25 секунд сервер шлёт `: ping\n\n` keepalive.
- Каждое сообщение из NATS `alerts.<user_id>` транслируется как
  `event: new-slot\ndata: <json>\n\n`.
- На disconnect клиента (`r.Context().Done()`) — `nats.Subscription.Unsubscribe()`,
  канал закрывается, горутины stage out.

### Формат ошибок (RFC 7807)

```json
{
  "type": "https://fizcultor.example.com/errors/unauthenticated",
  "title": "Unauthorized",
  "status": 401,
  "detail": "invalid or expired token",
  "trace_id": "01H6Q5...UUIDv7"
}
```

`trace_id` равен `X-Request-ID` (запрос: входящий header или сгенерированный).

Маппинг gRPC-кодов → HTTP см. `docs/api.md` §9.

## Env vars

| Var | Default | Описание |
|---|---|---|
| `APP_ENV` | `dev` | `dev` (text logs) или `prod` (JSON logs) |
| `LOG_LEVEL` | `info` | debug / info / warn / error |
| `SERVICE_NAME` | `gateway-svc` | Атрибут `service` в slog |
| `HTTP_ADDR` | `:8080` | Внешний listener (за Caddy) |
| `JWT_SECRET` | **required** | HMAC-секрет, тот же что у auth-svc (≥32 байт) |
| `NATS_URL` | **required** | `nats://nats:4222` |
| `CORS_ALLOWED_ORIGINS` | `http://localhost:5173` | Через запятую |
| `RATE_LIMIT_RPS` | `10` | Запросов в секунду на IP/user |
| `RATE_LIMIT_BURST` | `20` | Размер всплеска |
| `BOT_USERNAME` | `FizcultorBot` | Telegram bot username без `@` |
| `SLOTS_ENDPOINT_ENABLED` | `false` | true → `/api/slots` live-запрос к bmstu-svc (медленно); false → пустой массив |
| `SLOTS_FETCH_TIMEOUT` | `5s` | Таймаут на live-запрос (если включён) |
| `AUTH_GRPC_ADDR` | `auth-svc:9090` | |
| `BMSTU_GRPC_ADDR` | `bmstu-svc:9090` | |
| `FILTER_GRPC_ADDR` | `filter-svc:9090` | |
| `NOTIFIER_GRPC_ADDR` | `notifier-svc:9090` | |
| `TEACHERS_GRPC_ADDR` | `teachers-svc:9090` | |

## Локальный запуск

```sh
cd backend/services/gateway-svc
export JWT_SECRET="$(head -c 32 /dev/urandom | base64)"
export NATS_URL="nats://localhost:4222"
export AUTH_GRPC_ADDR="localhost:9001"   # пример из compose
export BMSTU_GRPC_ADDR="localhost:9002"
export FILTER_GRPC_ADDR="localhost:9003"
export NOTIFIER_GRPC_ADDR="localhost:9004"
export TEACHERS_GRPC_ADDR="localhost:9005"
go run ./cmd/server
```

В докер-композе (см. `backend/deploy/docker-compose.yaml`) все адреса
выставляются через service-discovery, ничего вручную задавать не надо.

## Тесты

```sh
go test ./...           # unit
go test -race ./...     # с детектором гонок
golangci-lint run ./... # лит
```

Покрытие:

- `internal/http/middleware/auth_test.go` — JWT валидация, header + query
  fallback, RFC 7807 на 401.
- `internal/http/handler/*_test.go` — табличные тесты всех ручек с моками
  gRPC-клиентов. Проверяют маппинг gRPC-кодов → HTTP, форматы ответов,
  PATCH-семантику для фильтров, deeplink rewrite.
- `internal/sse/hub_test.go` — подписка/cleanup/idempotency/slow-consumer.

## Что НЕ сделано (Wave 3 KISS)

- **frontend static embed** (`/internal/static/embed.go`) — TODO. Когда фронт
  собирается в `frontend/apps/web/dist`, добавить `//go:embed dist` и mount
  `/` → SPA fallback на `index.html`. Сейчас фронт раздаётся отдельно
  (vite dev server).
- **min_rating филтр** — filter-svc не знает рейтинг (см. open-questions).
  Фронт временно дисаблит поле.
- **/api/slots реальная отдача** — по умолчанию пустой массив. Live-режим
  включается флагом `SLOTS_ENDPOINT_ENABLED=true`, но идёт напрямую в
  bmstu-svc без матчинга и кэша. Кэширование cross-poll-результатов — V2
  (filter.GetCachedSlots).
- **Ticket-based SSE auth** — сейчас передаём JWT в query как fallback.
  Альтернатива (`POST /api/stream/ticket` → одноразовый короткий код) — V2,
  безопаснее (JWT не попадает в access-логи прокси).
- **httpOnly cookie для refresh** — V2 security review.
