# fizcultor-bot

Go-микросервисная система мониторинга свободных слотов BMSTU физкультуры
+ Vue 3 SPA. Замена монолитного Python-репо `bmstu-sport-sniper`.

## Стек

- **Backend:** Go 1.23, gRPC + chi (REST), Postgres 16, NATS 2 (JetStream).
- **Frontend:** Vue 3 + TS + Vite + Pinia + Tailwind.
- **Infra:** Docker Compose, Caddy 2 (auto-HTTPS в prod), GitHub Actions CI/CD.
- **Observability:** Prometheus + Grafana, slog JSON в stdout.

## Архитектура

Подробности — в [docs/architecture.md](docs/architecture.md) (C4 + sequence diagrams).

```
                ┌──────────┐       ┌──────────┐
                │  Caddy   │ ----► │ gateway- │
HTTPS  ────────►│ + auto-  │       │  svc     │
                │  HTTPS   │       │ (BFF)    │
                └──────────┘       └─┬────────┘
                                     │ gRPC
       ┌─────────┬─────────┬─────────┴──────────┬─────────┐
       ▼         ▼         ▼                    ▼         ▼
   ┌──────┐ ┌───────┐ ┌────────┐         ┌──────────┐ ┌──────────┐
   │ auth │ │ bmstu │ │ filter │   poll  │ notifier │ │ teachers │
   │ -svc │ │  -svc │ │  -svc  │ ◄───── │  -svc    │ │  -svc    │
   └──────┘ └───────┘ └────────┘ poller- └──────────┘ └──────────┘
       │         │         │       svc        │
       ▼         ▼         ▼                  ▼
   ┌───────────────────────────────┐    ┌──────────┐
   │       Postgres 16             │    │  NATS 2  │
   │  (auth/bmstu/filter/teachers) │    │JetStream │
   └───────────────────────────────┘    └──────────┘
```

## Структура

```
fizcultor-bot/
├── backend/
│   ├── services/        # 7 микросервисов
│   ├── pkg/             # shared Go-библиотеки (logger, metrics, bootstrap, ...)
│   ├── proto/           # protobuf-контракты
│   ├── migrations/      # per-svc БД миграции
│   ├── deploy/
│   │   ├── docker-compose.yaml         # dev
│   │   ├── docker-compose.prod.yaml    # prod (read-only fs, resource limits)
│   │   ├── Dockerfile.svc              # multi-stage, distroless
│   │   ├── caddy/Caddyfile             # dev (no TLS)
│   │   ├── caddy/Caddyfile.prod        # prod (Let's Encrypt)
│   │   ├── prometheus/prometheus.yml   # scrape config
│   │   ├── grafana/                    # provisioned dashboards
│   │   └── .env.prod.example
│   ├── Makefile
│   └── go.work
├── frontend/            # Vue 3 SPA + Dockerfile (nginx)
├── docs/                # architecture.md, api.md, runbook.md, observability.md
├── .github/workflows/   # ci.yml, release.yml
└── .env.example
```

## Quick start (dev)

```sh
cp .env.example .env
# отредактируй .env:
#   JWT_SECRET, AES_MASTER_KEY (openssl rand -hex 32),
#   SEMESTER_UUID_BASIC / _PREPARATORY / _SPECIAL_MEDICAL / _AFK, TG_BOT_TOKEN

# Одна команда — старт всей инфры (Postgres + NATS + Caddy + 7 svc):
docker compose -f backend/deploy/docker-compose.yaml up -d --build

# Логи:
docker compose -f backend/deploy/docker-compose.yaml logs -f gateway-svc

# Остановка:
docker compose -f backend/deploy/docker-compose.yaml down
```

После запуска:
- Gateway HTTP: <http://localhost:8080> (через Caddy proxy на :80)
- Postgres: localhost:5432 (user/pass `fizcultor`/`fizcultor`)
- NATS monitoring: <http://localhost:8222>

## Production deploy

```sh
# 1. Сгенерировать секреты + заполнить .env.prod
cp backend/deploy/.env.prod.example backend/.env.prod
$EDITOR backend/.env.prod   # JWT_SECRET, AES_MASTER_KEY, POSTGRES_PASSWORD, DOMAIN, ...

# 2. Старт (Caddy сам получит Let's Encrypt cert при доступности порта 80)
docker compose -f backend/deploy/docker-compose.prod.yaml --env-file backend/.env.prod up -d

# 3. Проверка
curl https://<DOMAIN>/healthz
docker compose -f backend/deploy/docker-compose.prod.yaml ps
```

Полный гайд — в [docs/runbook.md](docs/runbook.md) (deploy, rollback, backups,
key rotation, on-call cheatsheet).

## Локальная Go-сборка без Docker

```sh
cd backend
make tidy
make build
make test
```

## Тестирование

Test-пирамида: unit (Go + Vitest) → integration (testcontainers
Postgres/NATS) → E2E (Playwright с моками gateway-svc через `page.route`).
Подробности и команды — в [docs/testing.md](docs/testing.md).

```sh
# unit
cd backend && make test
cd frontend && pnpm -C apps/web test

# integration (нужен Docker)
cd backend && make test-integration

# E2E (Docker НЕ нужен — backend замокан)
cd frontend
pnpm -C apps/web e2e:install
pnpm -C apps/web build
pnpm -C apps/web e2e
```

## Документация

- [docs/architecture.md](docs/architecture.md) — C4 + sequence diagrams
- [docs/api.md](docs/api.md) — REST + gRPC контракты
- [docs/runbook.md](docs/runbook.md) — операционка: deploy, rollback, backups, key rotation, on-call
- [docs/observability.md](docs/observability.md) — Prometheus метрики, Grafana dashboards, slog logs
- [docs/testing.md](docs/testing.md) — тест-стратегия (unit / integration / E2E)

## Сервисы

| Service | Зачем | gRPC :9090 | HTTP :8080 |
|---|---|---|---|
| `gateway-svc` | BFF: REST + SSE для фронта | – | да (наружу через Caddy) |
| `auth-svc` | Регистрация/логин/JWT | да | healthz, readyz, metrics |
| `bmstu-svc` | Keycloak OIDC + LKS-сессии | да | healthz, readyz, metrics |
| `filter-svc` | CRUD фильтров + дедуп слотов | да | healthz, readyz, metrics |
| `notifier-svc` | Telegram + NATS publish (SSE) | да | healthz, readyz, metrics |
| `poller-svc` | Ticker-оркестратор | – | healthz, readyz, metrics |
| `teachers-svc` | Справочник учителей | да | healthz, readyz, metrics |

Все сервисы экспонируют:
- `GET /healthz` — liveness (200, пока процесс жив)
- `GET /readyz` — readiness (200 только если все зависимости в порядке: pg ping, nats connected)
- `GET /metrics` — Prometheus метрики (`<svc>_grpc_requests_total`, `<svc>_db_queries_total`, Go runtime, ...)

## Release pipeline

CI: `.github/workflows/ci.yml` — lint, test, integration, E2E на каждый push/PR.

Release: `.github/workflows/release.yml` — на push тэга `v*`:
1. Build Docker-образов всех 7 сервисов + frontend.
2. Push в `ghcr.io/<owner>/fizcultor-<svc>:<tag>`.
3. Генерация SBOM (syft) + trivy-скан → артефакты к Release.
4. Auto-changelog по git log → GitHub Release.
