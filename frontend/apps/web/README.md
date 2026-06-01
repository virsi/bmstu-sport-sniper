# @fizcultor/web

Vue 3 SPA для `fizcultor-bot`. Общается с `backend/services/gateway-svc` через REST + SSE.

## Стек

- Vue 3.5 + `<script setup lang="ts">` everywhere
- Vite 5 + TypeScript 5 (strict)
- Vue Router 4 + Pinia 2 (setup-store syntax)
- TailwindCSS 3 (без сторонней UI-библиотеки — KISS)
- axios + `@vueuse/core`
- VeeValidate 4 + zod (формы)
- Vitest + `@vue/test-utils` (unit)
- Playwright (e2e — конфиг есть, сценарии в Wave 4)

## Запуск

Скрипты запускаются из workspace-рута (`frontend/`):

```bash
# из репо
cd frontend
pnpm install                         # один раз
pnpm dev                             # dev-сервер на :5173 c proxy /api → :8080
pnpm -C apps/web typecheck
pnpm -C apps/web lint
pnpm -C apps/web test
pnpm -C apps/web build
```

В директории `apps/web` (этой) — те же скрипты без префикса `-C apps/web`.

### Связка с backend gateway

`vite.config.ts` проксирует:

- `/api/stream` → `http://localhost:8080/api/stream` (SSE, без буферизации)
- `/api` → `http://localhost:8080/api`

Поднять backend локально:

```bash
# из репо-рута
cd backend
docker compose -f deploy/docker-compose.yaml up gateway-svc nats postgres
```

После — `pnpm dev` в `frontend/apps/web` (или `pnpm dev` из `frontend/`).

## Переменные окружения

| Var | Default | Что |
|---|---|---|
| `VITE_API_BASE_URL` | `/api` | Базовый URL REST. В dev — относительный через vite-proxy. |
| `VITE_SSE_URL` | `/api/stream` | SSE endpoint. |
| `VITE_BOT_USERNAME` | `FizcultorBot` | Username бота для UI-подписей. Сам deeplink приходит из gateway. |

См. `.env.example`. Для локала — скопируй в `.env.local`.

## Структура

```
src/
├── api/
│   ├── client.ts        # axios + JWT-refresh + RFC7807 → toast (единственный HTTP-клиент)
│   ├── client.spec.ts   # тесты 401-retry / refresh dedupe / error parsing
│   └── sse.ts           # EventSource wrapper c reconnect/backoff
├── components/          # base UI: BaseButton, BaseInput, BaseSelect, SlotCard, Spinner, ToastContainer
├── composables/
│   └── useToast.ts      # singleton-нотифаер
├── router/
│   └── index.ts         # маршруты + auth-guard + redirect после login
├── stores/              # Pinia per domain
│   ├── auth.ts          # register/login/refresh/logout/linkTelegram
│   ├── slots.ts         # GET /slots + SSE event:new-slot
│   ├── slots.spec.ts
│   ├── filters.ts       # CRUD /filters (+ toggleEnabled)
│   ├── filters.spec.ts
│   └── bmstu.ts         # creds + status (NOT_LINKED|VALID|INVALID|EXPIRED)
├── types/
│   └── api.ts           # DTO REST API, snake_case 1:1 с backend/proto
├── views/               # страницы: Login, Register, Dashboard, Settings, Filters
├── styles/
│   └── main.css         # Tailwind + component-классы
├── App.vue
└── main.ts
```

## Конвенции

- **TSDoc на каждый exported символ** (на русском, кратко).
- **`<script setup lang="ts">`** обязателен.
- **Pinia per domain** — отдельный стор на каждую сущность.
- **Никаких сырых `fetch`/`axios`** в компонентах — только `@/api/client`.
- **Никаких хардкод-строк UI** в TS-логике — `Intl.*` или константы.
- ESLint + Prettier, `vue-tsc` в strict mode.
- Все ошибки идут через RFC7807 → `extractErrorMessage` → toast.

## Контракт с backend

Источник правды:

- `docs/api.md` — REST + SSE
- `backend/proto/common/v1/common.proto` — общие DTO (User, Slot, Filter)
- `backend/proto/auth/v1/auth.proto` — auth-флоу

Соответствие:

| Domain | REST | gRPC backend |
|---|---|---|
| Auth | `POST /api/auth/register\|login\|refresh\|logout` | `AuthService` |
| Profile | `GET /api/me`, `POST /api/me/telegram/init` | `AuthService.GetMe`, `LinkTelegramInit` |
| BMSTU | `POST\|DELETE /api/bmstu/creds`, `GET /api/bmstu/status` | `BmstuService` |
| Filters | `GET\|POST /api/filters`, `PATCH\|DELETE /api/filters/:id` | `FilterService` |
| Slots | `GET /api/slots` | gateway in-mem cache |
| Stream | `GET /api/stream?access_token=...` (SSE) | NATS `alerts.<user_id>` |

## Известные ограничения V1 (см. `docs/wave3-brief.md`)

- **`min_rating` дисабл-нут в UI** — filter-svc пока не вызывает teachers-svc, поэтому
  фильтр с рейтингом фактически не сработает. Поле помечено «coming soon».
- **`section` / `teacher_uid`** — singular в proto, на UI одно значение. V2: `repeated`.
- **JWT в SSE через `?access_token=`** — стандартная боль EventSource. V2: эфемерный
  one-time stream-token через `POST /api/stream/ticket`.
- **Refresh-токен в `localStorage`** — пометка в `api/client.ts`. V2: httpOnly cookie.

## TODO Wave 4

- Playwright E2E (auth + filters flow + dashboard)
- Web Push (VAPID)
- Locale i18n (multi-language ru/en)
- Авто-запись на слот
