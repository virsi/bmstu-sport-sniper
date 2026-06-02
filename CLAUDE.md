# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project shape

Monorepo с двумя top-level частями:

- `backend/` — Go 1.23 workspace (`go.work`) с **7 микросервисами** + общим `pkg/` + generated proto в `gen/`.
- `frontend/` — pnpm workspace, основное Vue 3 SPA в `frontend/apps/web/`.

Изначальный референс — Python+Selenium бот в branch `legacy-python` (тэги `0a2b929...`). Текущий `main` — полная переписка.

## Common commands

### Backend (cwd: `backend/`)

```sh
make build              # сборка всех 7 сервисов + pkg
make test               # unit-тесты во всех модулях
make test-race          # с -race -count=1
make test-integration   # testcontainers (требуется Docker)
make coverage           # per-service coverage, threshold 40% (pkg исключён)
make lint               # golangci-lint v2+ (config v2-формат)
make tidy               # go mod tidy во всех модулях + go work sync
make gen                # buf generate + sqlc generate (опц., gen/ закоммичен)
make migrate-up POSTGRES_DSN_BASE=postgres://user:pass@host:5432   # goose миграции
make up / make down     # docker compose dev стек
```

**Запуск одного сервиса/теста**:
```sh
cd backend/services/auth-svc && go test ./...                                  # все unit-тесты сервиса
cd backend/services/auth-svc && go test -run TestServiceRegister ./internal/auth/...
cd backend/services/auth-svc && go test -tags integration ./integration_test/...   # integration
cd backend/services/auth-svc && go build -o /tmp/auth-svc ./cmd/server          # бинарь
```

`go build ./...` из корня `backend/` **не работает** — go workspace требует per-module build. Всегда `cd services/X` или `make build`.

### Frontend (cwd: `frontend/`)

```sh
pnpm install
pnpm -C apps/web dev          # vite dev server на :5173 с proxy /api → :8080
pnpm -C apps/web typecheck    # vue-tsc strict
pnpm -C apps/web lint         # eslint --max-warnings 0
pnpm -C apps/web test         # vitest
pnpm -C apps/web build        # vue-tsc + vite build
pnpm -C apps/web e2e          # Playwright против vite preview (нужен e2e:install сначала)
```

### Local dev стек
```sh
cp .env.example .env          # заполнить JWT_SECRET, AES_MASTER_KEY (openssl rand -hex 32), SEMESTER_UUID, TG_BOT_TOKEN
docker compose -f backend/deploy/docker-compose.yaml up -d --build
```

## Architecture (big picture)

7 микросервисов, каждый = отдельный Go-модуль в `backend/services/<svc>/`. Связь:

- **gRPC** между сервисами (синхронные RPC, gateway → svc, poller → svc)
- **NATS JetStream** для алёртов (`alerts.<user_id>` subject per-user)
- **REST + SSE** только между browser и gateway-svc

Сервисы:
- **gateway-svc** — BFF: chi router, REST + SSE, JWT-middleware (через `auth.VerifyAccess`), NATS subscribe per-connection
- **auth-svc** — register/login/refresh с rotation+reuse-detection, argon2id passwords, JWT issuer
- **bmstu-svc** — **pure HTTP Keycloak OIDC** (без headless browser, см. ниже), AES-GCM шифрование LKS-кредов
- **filter-svc** — CRUD фильтров, чистая функция `Match()`, дедуп через `known_slots` (фикс stale-cache бага legacy)
- **poller-svc** — `time.Ticker` оркестратор: bmstu.FetchGroups → filter.MatchSlots → notifier.NotifyMatched → filter.MarkSeen
- **notifier-svc** — telebot.v3 для TG + NATS publish для SSE
- **teachers-svc** — справочник учителей+рейтингов, embedded `teachers.json`

Общий код в `backend/pkg/`:
- `bootstrap` (graceful shutdown, healthz/readyz/metrics)
- `crypto` (AES-256-GCM с `NonceSize` константой), `jwtx` (HS256), `logger` (slog JSON)
- `grpcx` (`DialInsecure` + `WithUserID` metadata helper)
- `events` (NATS wrapper), `pgxutil`, `httpx` (middleware), `errs`, `config`, `metrics` (Prometheus + gRPC interceptor)

**Никогда не дублируй** эти helper'ы в сервисах — расширяй `pkg/`.

### Database-per-service

4 БД на одном Postgres-инстансе: `auth_db`, `bmstu_db`, `filter_db`, `teachers_db`. Миграции независимы (`backend/migrations/<db>/` через goose). FK **только внутри** одной БД. Cross-svc id-связи логические (не enforced).

### Store layer: hand-written

`internal/store/` в каждом svc написан **вручную**, эквивалентен sqlc-генерации (одинаковые сигнатуры, `pgx/v5`). `sqlc.yaml` существует для запасного path через `make sqlc`. CI запускает `sqlc vet` (синтаксис queries), **не** `sqlc diff` (diff всегда расходится по дизайну).

### Inter-service auth

Gateway после `auth.VerifyAccess(jwt)` кладёт `user_id` в gRPC metadata через `pkg/grpcx.WithUserID(ctx, userID)`. Все сервисы читают metadata ключ **`x-user-id`** (lowercase). Stateless.

### gRPC error → HTTP mapping (gateway)

`InvalidArgument`→400, `Unauthenticated`→401, `NotFound`→404, `AlreadyExists`→409, `FailedPrecondition`→422, `ResourceExhausted`→429, `Unavailable`→503, `Internal`→500. Body — RFC 7807 problem+json.

## Non-obvious gotchas

1. **`go.work` replace для `genproto`**: `telebot.v3` транзитивно тянет старый `google.golang.org/genproto` без сплита `googleapis/rpc/status`, что создаёт ambiguous import. Workspace-level `replace` форсирует новую версию. **Не удаляй** этот блок в `backend/go.work` — без него ни один сервис не соберётся.

2. **`backend/gen/` закоммичен** (proto-generated). Это нарушает обычное правило «generated не в git», но без этого `go.work` не подцепляет module `./gen` без локального `protoc`. Регенерация через `make gen` (нужны `buf` + `protoc-gen-go-grpc`).

3. **Per-service `replace ../../pkg`**: каждый `services/<svc>/go.mod` имеет relative `replace github.com/fizcultor/backend/pkg => ../../pkg` (и аналогично `tests/testhelpers`). Это намеренно — для possible независимых релизов.

4. **Pure HTTP Keycloak OIDC (bmstu-svc)**: НЕ используется `chromedp`/`playwright`/`golang.org/x/oauth2`. Реализован 4-шаговый HTML-form scrape Keycloak (`sso.bmstu.ru/kc/realms/ph`). Подробности и rationale в `docs/research/keycloak-oidc-bmstu.md`. ROPC недоступен (`unauthorized_client`). API LKS принимает **только** session cookie `p4sess`, Bearer не работает.

5. **`Slot.id` детерминированный**: `sha1(semester|week|time|section|teacher_uid)` — не auto-increment, не UUID. Это позволяет cross-service сравнение без координации. См. ADR в `docs/adr/`.

6. **`Match` + `MarkSeen` разделены** (фикс legacy-бага main.py:312). `MatchSlots` возвращает с `is_new`, **не** записывает в `known_slots`. Poller вызывает `MarkSeen` **только** после успешного `notifier.NotifyMatched` — потеря алёрта при сбое notifier ведёт к повтору на следующем цикле, не к тихой пропаже.

7. **`AES_MASTER_KEY`** — один и тот же ключ для BMSTU creds (login/password) И сессионных cookies. 32 байта в hex (64 chars). Изменение требует re-encryption миграции (есть sketch в `docs/runbook.md`).

8. **Refresh-token в httpOnly cookie** (`Path=/api/auth; HttpOnly; Secure; SameSite=Strict`), НЕ в localStorage. Login response = `{access_token, expires_at}` без refresh. SSE auth — через **one-time ticket** (`POST /api/stream/ticket` → `?ticket=<X>`), не JWT в query.

9. **golangci-lint v2-формат config**. CI использует `v2.1.6`. Локально проверь `golangci-lint --version`; v1 не пройдёт.

10. **Frontend i18n не настроен** — все строки на русском инлайнятся в SFC. E2E тесты ищут по русскому тексту (`getByText(/Live-канал активен/i)`) — если меняешь UI label, обновляй `frontend/apps/web/tests/e2e/*.spec.ts`.

11. **`backend/refs/`** — игнорируется в .gitignore. Локально содержит legacy `main.py` + `teachers.json` для справки. Не пытайся коммитить.

## Documentation

- `docs/architecture.md` — C4 + sequence-диаграммы
- `docs/api.md` — REST контракт gateway + SSE format
- `docs/runbook.md` — operations (deploy, backups, key rotation, oncall)
- `docs/observability.md` — Prometheus метрики per-service
- `docs/testing.md` — стратегия unit/integration/e2e
- `docs/adr/` — architectural decisions (микросервисы, NATS, OIDC, DB-per-svc)
- `docs/research/keycloak-oidc-bmstu.md` — recon OIDC флоу BMSTU
- `docs/review-findings.md` — production-readiness гэпы

## Commits and PRs

- Conventional Commits (`feat:`, `fix:`, `ci:`, `docs:`, `refactor:`)
- `Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>` в коммитах от Claude
- Сообщения в основном на русском
- Branch `legacy-python` — снапшот старого Python-кода, не удалять
