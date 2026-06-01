# Testing Strategy — fizcultor-bot

Wave 4 QA layer for the 7 Go micro-services + Vue 3 SPA. This document
describes the test pyramid, what each layer covers, and how to run it
locally and in CI.

```
                ┌──────────────────────┐
                │   Playwright E2E     │   4 specs, ~30s
                │  (mocked gateway)    │
                ├──────────────────────┤
                │  Go integration      │   35 tests across 5 svc
                │  (testcontainers)    │   Postgres / NATS real
                ├──────────────────────┤
                │  Frontend Vitest     │   14 tests
                │ (jsdom + axios mock) │
                ├──────────────────────┤
                │  Go unit tests       │   185+ tests across 7 svc
                │  (mocks, no I/O)     │
                └──────────────────────┘
```

## 1. Unit tests (Go)

Location: `backend/services/<svc>/internal/.../*_test.go`.

Properties:
- No external dependencies (no Postgres, no Docker, no network).
- All collaborators mocked via small in-package interfaces.
- Each test runs in milliseconds; whole suite < 5 s per service.

Coverage status as of Wave 4:

| Service        | Tests | Focus                                                  |
|----------------|-------|--------------------------------------------------------|
| `auth-svc`     | 15    | argon2id, JWT, refresh rotation, reuse-detection       |
| `bmstu-svc`    | 54    | Keycloak HTML OIDC mock, cookie encrypt, groups, mgr   |
| `filter-svc`   | 29    | `match.Match` pure fn, gRPC CRUD, time/section filters |
| `gateway-svc`  | 30+   | REST handlers, auth middleware, SSE hub                |
| `notifier-svc` | 23    | Telegram format, bot, server (NATS publish)            |
| `poller-svc`   | 16    | orchestrator, exponential backoff                      |
| `teachers-svc` | 18    | JSON import, lookup, bootstrap                         |

Run them:

```sh
cd backend
make test            # all unit tests
make test-race       # with race detector
```

## 2. Integration tests (Go + testcontainers)

Location: `backend/services/<svc>/integration_test/*_test.go`.

Build tag: `integration` (so unit `go test` does not pick them up).

Each `integration_test` package:
- Spins up a real Postgres 16 (or NATS 2) container via
  [testcontainers-go](https://golang.testcontainers.org/).
- Applies the production goose migrations from `backend/migrations/<dbName>/`.
- Starts the real service implementation behind a `bufconn` in-process gRPC
  server. The test client dials that server, end-to-end.
- Shares one container per `go test` process (each test gets its own
  `CREATE DATABASE` to stay isolated, ~5x faster than booting a fresh
  container per test).

Shared fixtures live at `backend/tests/testhelpers/`:
- `pg.go` — `StartPostgres(t, "auth_db")` returns a ready `*pgxpool.Pool`.
- `nats.go` — `StartNATS(t)` returns a live `*nats.Conn`.
- `grpc.go` — `StartGRPCServer(t)` + `Dial(t)` wiring for bufconn.

### What's covered

| Service        | Tests | Scenarios                                                                                                  |
|----------------|-------|------------------------------------------------------------------------------------------------------------|
| `auth-svc`     | 9     | Register → Login → GetMe, refresh rotation, reuse-detection, Revoke (idempotent), Telegram link single-use |
| `filter-svc`   | 9     | CRUD round-trip, MatchSlots → MarkSeen → IsNew=false → ResetKnown, alert_log persistence, cross-user 403   |
| `teachers-svc` | 7     | Bootstrap from embedded JSON, Refresh idempotent, BatchGet mixed existing/unknown, pagination, name search |
| `notifier-svc` | 4     | NATS publish of `alerts.<user>` payload, Telegram delivery, empty match no-op, TG-not-linked failure       |
| `bmstu-svc`    | 6     | Keycloak HTML mock (no real lks.bmstu.ru), credentials persisted encrypted, upsert keeps single row       |

### Running integration tests

Prereqs: Docker daemon running.

```sh
cd backend
make test-integration         # all services
# or per-service:
cd services/auth-svc
go test -tags integration -count=1 -timeout 120s -v ./integration_test/...
```

Typical wall time: 30-90 seconds per service (first call boots the
container; subsequent tests in the process share it).

## 3. Frontend unit tests (Vitest)

Location: `frontend/apps/web/src/**/*.spec.ts`.

Runner: Vitest (jsdom environment).

Coverage gate: 50 % lines / functions / statements, 40 % branches —
configured in `vitest.config.ts`. Source files under
`src/views/` and `src/components/` are excluded from coverage because
those Vue SFC templates are covered by Playwright E2E; vitest covers
composables, stores, axios client, router.

```sh
cd frontend
pnpm install
pnpm test                # vitest run
pnpm -C apps/web test:coverage   # with coverage gate
```

## 4. E2E tests (Playwright)

Location: `frontend/apps/web/tests/e2e/*.spec.ts`.

Backend strategy: **mocked via `page.route()`** (see
`tests/e2e/fixtures/mocks.ts`). The mocks emulate gateway-svc REST
endpoints and let the test side script state changes. No real Postgres,
NATS, or Go service is required. The real backend is covered by the Go
unit + integration layers above.

For SSE we shim `window.EventSource` in `slots-sse.spec.ts` so the
test can dispatch `new-slot` events deterministically.

### Specs

| File                  | Scenarios                                                              |
|-----------------------|------------------------------------------------------------------------|
| `auth.spec.ts`        | Register → auto-login → dashboard; login existing user; bad creds 401; |
|                       | unauthenticated /dashboard → /login; logout clears tokens              |
| `bmstu-link.spec.ts`  | Status NOT_LINKED → POST creds → VALID; delete reverts; empty form 400 |
| `filters.spec.ts`     | Create filter shows in list; delete with confirm dialog                |
| `slots-sse.spec.ts`   | "Live-канал активен" indicator after open; new-slot event renders card;|
|                       | duplicate id push deduplicates                                         |

### Running E2E

```sh
cd frontend
pnpm install
pnpm -C apps/web e2e:install    # download Playwright browsers
pnpm -C apps/web build           # build SPA
pnpm -C apps/web e2e             # run all specs
```

Playwright auto-starts the SPA via `pnpm preview` (see
`playwright.config.ts`).

Artifacts (HTML report, traces, screenshots on failure) land in
`frontend/apps/web/playwright-report/` and `test-results/`.

## 5. CI pipeline

Defined in `.github/workflows/ci.yml`. Jobs (ordering enforced by GHA
default DAG):

1. **`go-lint`** — golangci-lint on pkg + each service.
2. **`go-test`** — `go test -race` for pkg + each service with
   `-coverprofile`, then a coverage gate (`60%` threshold by default).
   Artifacts: per-service `coverage/*.out`.
3. **`go-build`** — `make build`.
4. **`buf`** — proto lint + breaking-change check against `main`.
5. **`sqlc`** — `sqlc diff` (no-op until queries land).
6. **`go-integration`** — runs on push (and PRs labelled `integration`)
   only; spins up testcontainers via the ambient Docker daemon. Uploads
   logs on failure.
7. **`frontend`** — lint, typecheck, vitest with coverage gate (50%),
   build.
8. **`e2e`** — Playwright against the built SPA + mocked backend.
   Uploads HTML report + screenshots on failure.

## 6. Local-developer quick reference

```sh
# Backend unit (fast, no Docker)
cd backend && make test

# Backend integration (Docker required)
cd backend && make test-integration

# Coverage
cd backend && make coverage

# Frontend unit
cd frontend && pnpm install && pnpm test

# Frontend coverage
cd frontend && pnpm -C apps/web test:coverage

# E2E (Docker NOT required — mocks handle the backend)
cd frontend
pnpm -C apps/web e2e:install   # one-time
pnpm -C apps/web build
pnpm -C apps/web e2e
```

## 7. What's intentionally NOT covered (yet)

- **Load / performance tests (k6, vegeta)** — Wave 5 (devops).
- **Real `lks.bmstu.ru` integration** — production-only; bmstu-svc
  integration tests use a mocked Keycloak / portal4.
- **Real Telegram bot delivery** — covered by unit tests with a
  `bot.Sender` mock; nothing in CI talks to api.telegram.org.
- **Cross-browser E2E (Firefox, WebKit)** — Wave 4 ships chromium only.
- **Contract tests against pact / oasdiff** — out of scope for Wave 4;
  buf-breaking already gates proto changes.

## 8. Adding new tests

- New unit test for service X: drop a `_test.go` next to the code under
  `services/X/internal/...`. Use existing mocks; do not pull in
  testhelpers.
- New integration test for service X: create
  `services/X/integration_test/<name>_test.go` with the
  `//go:build integration` tag. Use `testhelpers.StartPostgres` /
  `StartNATS` / `StartGRPCServer`. Each test must do its own setup.
- New E2E spec: add `tests/e2e/<name>.spec.ts`, install backend mocks via
  `installGatewayMocks(page, state)`, drive the UI.
