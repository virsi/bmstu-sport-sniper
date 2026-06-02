# API Contract — gateway-svc

Публичный REST + SSE контракт, который потребляет фронт `frontend/apps/web`.
Внутренние сервисы общаются по gRPC (см. `backend/proto/`), здесь только
наружный периметр.

**Base URL:** `https://fizcultor.example.com/api`
**Content-Type:** `application/json; charset=utf-8`
**Auth:** `Authorization: Bearer <access_jwt>` — на всех эндпоинтах кроме
`POST /auth/register`, `POST /auth/login`, `POST /auth/refresh`,
`GET /healthz`, `GET /readyz`, `GET /metrics`.

JWT:
- `access` — HS256, TTL 15 мин, claims `sub` (user_id), `exp`, `iat`, `jti`.
- `refresh` — opaque строка (UUIDv7), TTL 30 дней, rotation on every refresh.

**Хранение токенов на клиенте:**
- `access` — `Authorization: Bearer` (любое локальное хранилище, короткоживущий).
- `refresh` — httpOnly cookie `rt` с атрибутами `HttpOnly; Secure; SameSite=Strict; Path=/api/auth; Max-Age=2592000`. Cookie ставится/удаляется бэком на login/refresh/logout; клиент его НЕ читает и НЕ передаёт явно — браузер шлёт автоматически на эндпоинты `/api/auth/*`. Защита от XSS-кражи долгоживущего токена (см. `docs/review-findings.md` #2).

## Соглашения по ошибкам

Любая ошибка — `application/problem+json`:

```json
{
  "type": "https://fizcultor.example.com/errors/invalid-credentials",
  "title": "Invalid credentials",
  "status": 401,
  "detail": "Email or password is incorrect",
  "trace_id": "01H6Q5...UUIDv7"
}
```

| HTTP | Когда |
|---|---|
| 400 | INVALID_ARGUMENT, валидация |
| 401 | UNAUTHENTICATED (нет/невалидный JWT, неверные креды) |
| 403 | PERMISSION_DENIED (чужой ресурс) |
| 404 | NOT_FOUND |
| 409 | ALREADY_EXISTS, conflict |
| 422 | semantically invalid (time_from > time_to и т.п.) |
| 429 | rate limit |
| 500 | INTERNAL |
| 503 | gRPC backend недоступен |

---

## 1. Authentication

### POST /api/auth/register

Создаёт нового пользователя. Сразу после этого вызовите login.

**Request:**
```json
{
  "email": "ivan@bmstu.ru",
  "password": "P@ssw0rd123"
}
```

**Response 201:**
```json
{
  "id": "01H6Q5...",
  "email": "ivan@bmstu.ru",
  "created_at": "2026-06-02T10:15:00Z",
  "last_seen_at": "2026-06-02T10:15:00Z"
}
```

**Errors:** 400 (invalid email/password), 409 (email taken).

---

### POST /api/auth/login

**Request:**
```json
{
  "email": "ivan@bmstu.ru",
  "password": "P@ssw0rd123"
}
```

**Response 200:**
```json
{
  "access_token": "eyJhbGciOiJIUzI1NiIs...",
  "access_expires_at": "2026-06-02T10:30:00Z",
  "refresh_expires_at": "2026-07-02T10:15:00Z"
}
```

Дополнительно сервер отдаёт `Set-Cookie: rt=<opaque>; Path=/api/auth; HttpOnly; Secure; SameSite=Strict; Max-Age=2592000`.

`refresh_token` в body НЕ возвращается (cookie-only).

**Errors:** 401 (любая причина — не различаем «нет email» vs «неверный пароль»).

---

### POST /api/auth/refresh

Refresh-token берётся из cookie `rt` (приоритет) или из body (DEPRECATED fallback).

**Request (cookie-based, рекомендуется):** пустое тело `{}`. Cookie `rt` уезжает автоматически (нужен `credentials: 'include'` / `withCredentials: true` на клиенте).

**Request (legacy, DEPRECATED):**
```json
{
  "refresh_token": "01H6Q5XK..."
}
```

**Response 200:** как у login, плюс новый `Set-Cookie: rt=<new-opaque>` (rotation также на cookie-уровне).

**Errors:** 401 (revoked / expired / reuse detected — в последнем случае ВСЕ токены юзера revoked; cookie очищается на 401 от cookie-источника).

---

### POST /api/auth/logout

Хедер с access обязателен.

**Request (cookie-based):** пустое тело `{}`. Refresh берётся из cookie `rt`.

**Request (legacy, DEPRECATED):**
```json
{
  "refresh_token": "01H6Q5XK..."
}
```

**Response 204:** No Content + `Set-Cookie: rt=; Max-Age=0` (удаление cookie). Идемпотентен.

---

## 2. Profile

### GET /api/me

**Response 200:**
```json
{
  "id": "01H6Q5...",
  "email": "ivan@bmstu.ru",
  "telegram_chat_id": 123456789,
  "created_at": "2026-06-02T10:15:00Z",
  "last_seen_at": "2026-06-02T11:00:00Z"
}
```

`telegram_chat_id` отсутствует если TG не привязан.

---

### POST /api/me/telegram/init

Возвращает deeplink для привязки TG. Код TTL 10 мин.

**Response 200:**
```json
{
  "deeplink": "https://t.me/FizcultorBot?start=ABC123XYZ",
  "code": "ABC123XYZ",
  "expires_at": "2026-06-02T10:25:00Z"
}
```

Сам факт привязки случится асинхронно, когда юзер нажмёт /start в TG.
Состояние привязки видно через `GET /api/me` (поле `telegram_chat_id`).

---

## 3. BMSTU credentials

### POST /api/bmstu/creds

Сохраняет BMSTU-креды (шифруются AES-GCM на bmstu-svc) и сразу делает
test-login. Если test-login не прошёл — креды не сохраняются.

**Request:**
```json
{
  "login": "ivanov_ii",
  "password": "BMSTU_password",
  "health_group": "BASIC"
}
```

| Поле | Тип | Описание |
|---|---|---|
| `login` | string | Логин LKS BMSTU. Обязателен. |
| `password` | string | Пароль LKS BMSTU. Обязателен; на бэке шифруется AES-256-GCM. |
| `health_group` | enum string | Группа здоровья студента. Одно из `BASIC`, `PREPARATORY`, `SPECIAL_MEDICAL`, `AFK`. Опционально на write — если опущено, bmstu-svc подставит `BASIC` (бэквард-совместимость). Определяет, какой `SEMESTER_UUID_*` пойдёт в LKS при FetchGroups. |

**Response 204:** No Content (успешный test-login, креды сохранены).

**Errors:**
- 400 — пустые login/password или невалидное значение `health_group`.
- 401 — креды отвергнуты Keycloak.
- 503 — LKS недоступен, попробуйте позже.

---

### GET /api/bmstu/status

**Response 200:**
```json
{
  "status": "VALID",
  "health_group": "BASIC",
  "last_login_at": "2026-06-02T10:18:30Z",
  "session_expires_at": "2026-06-02T18:18:30Z"
}
```

Возможные `status`: `NOT_LINKED`, `VALID`, `INVALID`, `EXPIRED`.

`health_group` — одно из `BASIC`, `PREPARATORY`, `SPECIAL_MEDICAL`, `AFK`.
Поле опускается, если креды не сохранены (`status == NOT_LINKED`).

При `INVALID` дополнительно:
```json
{
  "status": "INVALID",
  "health_group": "PREPARATORY",
  "last_error": "Keycloak returned 401: пароль устарел"
}
```

---

### DELETE /api/bmstu/creds

**Response 204:** No Content. Идемпотентен.

---

## 4. Filters

### GET /api/filters

Все фильтры юзера, отсортированы по created_at DESC.

**Query params:**
- `include_disabled` (bool) — включать ли enabled=false. По умолчанию true.

**Response 200:**
```json
{
  "filters": [
    {
      "id": "01H6Q5F1...",
      "user_id": "01H6Q5...",
      "section": "Аэробика",
      "teacher_uid": null,
      "day_of_week": "WEDNESDAY",
      "time_from": "18:00",
      "time_to": "21:00",
      "min_rating": 4.0,
      "enabled": true,
      "created_at": "2026-06-02T10:20:00Z",
      "updated_at": "2026-06-02T10:20:00Z"
    }
  ]
}
```

`day_of_week` — строка из `MONDAY`..`SUNDAY` или `ANY` (= UNSPECIFIED в proto).
`null` в опциональных полях означает «без ограничения».

---

### POST /api/filters

**Request:**
```json
{
  "section": "Аэробика",
  "day_of_week": "WEDNESDAY",
  "time_from": "18:00",
  "time_to": "21:00",
  "min_rating": 4.0,
  "enabled": true
}
```

Все поля кроме `enabled` опциональны. Пустые → «любое значение».

**Response 201:** созданный Filter (см. GET).

**Errors:**
- 422 — time_from > time_to, min_rating вне [0, 5].

---

### PATCH /api/filters/:id

Частичное обновление. Любое поле опционально.
`null` сбрасывает поле в «без ограничения».

**Request:**
```json
{
  "enabled": false
}
```

**Response 200:** обновлённый Filter.

---

### DELETE /api/filters/:id

**Response 204:** No Content.

**Errors:** 404 (не найдено), 403 (чужой фильтр).

---

## 5. Slots

### GET /api/slots

Снимок последних слотов, которые matched под фильтры юзера.
Без фильтров — пустой массив. Не делает live-запрос к LKS, читает кэш filter-svc.

**Query params:**
- `since` (RFC3339 timestamp) — только слоты, fetched_at >= since.

**Response 200:**
```json
{
  "slots": [
    {
      "id": "sha1:abc123...",
      "week": 14,
      "time": "18:00-19:30",
      "section": "Аэробика",
      "place": "СК «Дворец», зал 3",
      "teacher_name": "Иванова Анна Петровна",
      "teacher_uid": "uid_42",
      "teacher_rating": 4.5,
      "vacancy": 2,
      "semester_uuid": "f1d2...",
      "day_of_week": "WEDNESDAY",
      "matched_filter_ids": ["01H6Q5F1..."],
      "is_new": false
    }
  ],
  "fetched_at": "2026-06-02T11:00:00Z"
}
```

---

## 6. SSE — Real-time alerts

### POST /api/stream/ticket

Выпускает одноразовый short-lived (TTL 5 мин, конфигурируется `SSE_TICKET_TTL`) ticket для безопасного открытия SSE-стрима. Защищён обычной Auth (нужен `Authorization: Bearer <jwt>`).

**Response 200:**
```json
{
  "ticket": "Rk7p9Q8nZ8...base64url",
  "expires_at": "2026-06-02T11:05:00Z"
}
```

Ticket — одноразовый: после успешного открытия `/api/stream` он invalidated. На reconnect клиент должен запросить новый ticket.

**Зачем не JWT в query:** долгоживущий access-JWT попал бы в access-логи прокси/CDN/балансировщиков. Ticket — короткоживущий capability-токен; даже залогированный, повторно его использовать нельзя (см. `docs/review-findings.md` #3).

---

### GET /api/stream

Долгоживущий HTTP-стрим (`text/event-stream`). Аутентификация — в порядке приоритета:

1. `?ticket=<one-time-ticket>` — **рекомендуемый** способ для EventSource (см. `POST /api/stream/ticket`).
2. `Authorization: Bearer <jwt>` — для клиентов, умеющих ставить headers (e.g. fetch-streaming).
3. `?access=<jwt>` — **DEPRECATED**, оставлено для backward-compat. JWT в query попадает в access-логи.

Сервер шлёт heartbeat `: ping\n\n` каждые 25 сек, чтобы прокси не закрыли.

**Event: new-slot** — новый match для юзера.

```
event: new-slot
id: 01H6Q5E1...
data: {"slot": { ... как в GET /api/slots ... }, "matched_filter_ids": ["01H6Q5F1..."]}

```

**Event: status** — статусные изменения (BMSTU INVALID, фильтр выкл и т.п.).

```
event: status
id: 01H6Q5E2...
data: {"kind": "bmstu_invalid", "message": "Обнови BMSTU пароль"}

```

**Event: ping** — служебный keep-alive (можно игнорировать).

```
event: ping
data: 

```

**Закрытие:** клиент закрывает соединение → gateway снимает подписку из NATS.

---

## 7. Health & Ops

| Эндпоинт | Что |
|---|---|
| `GET /healthz` | Liveness, всегда 200 если процесс жив. |
| `GET /readyz` | Readiness, 200 если все gRPC бэкенды ответили на health-check, иначе 503. |
| `GET /metrics` | Prometheus exposition. |

---

## 8. Rate limiting (gateway-svc)

| Эндпоинт | Лимит |
|---|---|
| `/api/auth/login`, `/auth/register` | 5 req / 60s / IP |
| `/api/auth/refresh` | 30 req / 60s / user |
| Прочие | 60 req / 60s / user |

Превышение → 429 с `Retry-After`.

---

## 9. Маппинг gRPC → REST

| REST | gRPC |
|---|---|
| `POST /api/auth/register` | `AuthService.Register` |
| `POST /api/auth/login` | `AuthService.Login` |
| `POST /api/auth/refresh` | `AuthService.Refresh` |
| `POST /api/auth/logout` | `AuthService.Revoke` |
| `GET /api/me` | `AuthService.GetMe` |
| `POST /api/me/telegram/init` | `AuthService.LinkTelegramInit` |
| `POST /api/bmstu/creds` | `BmstuService.StoreCredentials` |
| `GET /api/bmstu/status` | `BmstuService.GetStatus` |
| `DELETE /api/bmstu/creds` | `BmstuService.DeleteCredentials` |
| `GET /api/filters` | `FilterService.ListFilters` |
| `POST /api/filters` | `FilterService.CreateFilter` |
| `PATCH /api/filters/:id` | `FilterService.UpdateFilter` |
| `DELETE /api/filters/:id` | `FilterService.DeleteFilter` |
| `GET /api/slots` | (gateway in-memory cache + опц. `filter.GetCachedSlots` в V2) |
| `POST /api/stream/ticket` | (in-memory `ticket.Store` в gateway) |
| `GET /api/stream` | NATS subscribe `alerts.<user_id>` |
