# fizcultor-bot — Frontend

pnpm-monorepo с Vue 3 SPA.

## Структура

```
frontend/
├── apps/
│   └── web/            # Vue 3 + Vite + TS SPA (главный фронт)
├── packages/           # shared библиотеки (UI-kit, utils) — добавляются по мере роста
├── package.json
└── pnpm-workspace.yaml
```

## Требования

- Node.js >= 20.10
- pnpm >= 9 (`npm i -g pnpm`)

## Быстрый старт

```bash
pnpm install
pnpm dev        # запустит apps/web на http://localhost:5173 (proxy /api → :8080)
```

## Скрипты

| Команда | Что делает |
|---|---|
| `pnpm dev` | Dev-сервер Vite для `apps/web` |
| `pnpm build` | Production-сборка |
| `pnpm preview` | Локальный preview production-сборки |
| `pnpm typecheck` | `vue-tsc --noEmit` по всем пакетам |
| `pnpm lint` | ESLint по всем пакетам |
| `pnpm test` | Vitest по всем пакетам |
| `pnpm format` | Prettier на всё |

## Backend dev-proxy

Vite в `apps/web` проксирует `/api` и `/api/stream` (SSE) на `http://localhost:8080`
— это `backend/services/gateway-svc`. Поднимай его параллельно через
`backend/deploy/docker-compose.yaml`.

## Packages

`packages/` пустая на старте. Сюда переедет shared UI-kit, когда появится второй app
(например, admin-panel в V2) — раньше выносить нет смысла (KISS).
