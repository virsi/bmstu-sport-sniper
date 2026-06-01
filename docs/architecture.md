# Architecture — fizcultor-bot

Целевая архитектура переписанного BMSTU sport-sniper: Go-микросервисы за
gateway-svc, Vue 3 SPA фронт, NATS JetStream для алёртов, Postgres
(database-per-service).

См. также:
- [api.md](./api.md) — REST контракт gateway.
- [adr/](./adr/) — архитектурные решения с обоснованием.

---

## 1. C4 — System Context

Кто общается с системой и через что.

```mermaid
C4Context
    title System Context — fizcultor-bot

    Person(student, "Student", "Студент BMSTU, ищет свободные слоты записи на физкультуру")
    Person(admin, "Admin", "Оператор, импорт teachers.json, мониторинг")

    System(fizcultor, "fizcultor-bot", "Алёрты о свободных слотах записи + UI управления фильтрами")

    System_Ext(bmstu_lks, "BMSTU LKS", "Личный кабинет студента: Keycloak SSO + /lks-back/api/v1/fv/.../groups")
    System_Ext(telegram, "Telegram Bot API", "Доставка алёртов в личные сообщения")
    System_Ext(browser, "Browser", "Vue 3 SPA: REST + SSE")

    Rel(student, browser, "Использует")
    Rel(student, telegram, "Получает алёрты")
    Rel(admin, browser, "Управляет справочниками")
    Rel(browser, fizcultor, "HTTPS REST + SSE", "443")
    Rel(fizcultor, bmstu_lks, "HTTPS, OIDC + GET /groups", "443")
    Rel(fizcultor, telegram, "HTTPS Bot API", "443")
```

---

## 2. C4 — Container

Внутри fizcultor-bot: 7 Go-сервисов, 1 Vue SPA, Postgres, NATS, Caddy reverse-proxy.

```mermaid
C4Container
    title Container — fizcultor-bot

    Person(student, "Student")
    System_Ext(bmstu_lks, "BMSTU LKS")
    System_Ext(telegram_api, "Telegram Bot API")

    Container_Boundary(edge, "Edge") {
        Container(caddy, "Caddy", "Reverse proxy", "TLS termination, /api → gateway, / → static SPA")
    }

    Container_Boundary(frontend_b, "Frontend") {
        Container(spa, "web SPA", "Vue 3 + Vite + Pinia", "REST к /api, SSE к /api/stream")
    }

    Container_Boundary(backend, "Backend (Go)") {
        Container(gateway, "gateway-svc", "Go, chi", "REST+SSE, JWT middleware, gRPC clients ко всему")
        Container(auth, "auth-svc", "Go, gRPC", "users + JWT + argon2id")
        Container(bmstu, "bmstu-svc", "Go, gRPC", "Keycloak HTTP, BMSTU creds (AES-GCM)")
        Container(filter, "filter-svc", "Go, gRPC", "CRUD фильтров + дедуп")
        Container(poller, "poller-svc", "Go, без gRPC", "ticker 60s + jitter, orchestrator")
        Container(notifier, "notifier-svc", "Go, gRPC + telebot.v3", "TG bot, NATS publish")
        Container(teachers, "teachers-svc", "Go, gRPC", "справочник + рейтинги")
    }

    Container_Boundary(infra, "Infrastructure") {
        ContainerDb(pg, "Postgres", "PostgreSQL 16", "auth_db, bmstu_db, filter_db, teachers_db")
        ContainerQueue(nats, "NATS JetStream", "alerts.<user_id>, slots.updated")
    }

    Rel(student, caddy, "HTTPS", "443")
    Rel(caddy, spa, "static")
    Rel(caddy, gateway, "HTTP", ":8080")
    Rel(spa, gateway, "REST + SSE")

    Rel(gateway, auth, "gRPC")
    Rel(gateway, bmstu, "gRPC")
    Rel(gateway, filter, "gRPC")
    Rel(gateway, notifier, "gRPC SendDirect")
    Rel(gateway, teachers, "gRPC")
    Rel(gateway, nats, "subscribe alerts.<user_id>", "SSE bridge")

    Rel(poller, bmstu, "gRPC FetchGroups")
    Rel(poller, filter, "gRPC MatchSlots, MarkSeen")
    Rel(poller, notifier, "gRPC NotifyMatched")

    Rel(filter, teachers, "gRPC BatchGet", "обогащение рейтингом")
    Rel(notifier, auth, "gRPC LinkTelegramComplete")
    Rel(notifier, nats, "publish alerts.<user_id>")

    Rel(auth, pg, "auth_db")
    Rel(bmstu, pg, "bmstu_db")
    Rel(filter, pg, "filter_db")
    Rel(teachers, pg, "teachers_db")

    Rel(bmstu, bmstu_lks, "Keycloak OIDC + /groups")
    Rel(notifier, telegram_api, "Bot API long-poll")
```

**Ключевые наблюдения:**
- Никаких REST вызовов между Go-сервисами. Только gRPC и NATS.
- poller-svc — единственный источник тикеров, остальные сервисы реактивны.
- gateway-svc — единственная точка выхода во фронт; ни один внутренний сервис не имеет публичного HTTP.
- Postgres физически один инстанс; per-service — отдельные databases (логическая изоляция).

---

## 3. Sequence — Регистрация + линковка BMSTU + первый алёрт

End-to-end happy path для нового пользователя.

```mermaid
sequenceDiagram
    autonumber
    actor U as Student
    participant B as Browser (Vue SPA)
    participant GW as gateway-svc
    participant A as auth-svc
    participant BM as bmstu-svc
    participant LKS as BMSTU LKS
    participant FL as filter-svc
    participant P as poller-svc
    participant N as notifier-svc
    participant TG as Telegram API
    participant NATS as NATS JetStream

    rect rgba(180, 220, 255, 0.2)
    note over U,A: Этап 1: Регистрация
    U->>B: открывает /register
    B->>GW: POST /api/auth/register {email, password}
    GW->>A: AuthService.Register
    A->>A: argon2id(password), INSERT users
    A-->>GW: User
    GW-->>B: 201 Created
    B->>GW: POST /api/auth/login
    GW->>A: AuthService.Login
    A-->>GW: TokenPair (access+refresh)
    GW-->>B: 200 {access, refresh}
    end

    rect rgba(220, 255, 220, 0.2)
    note over U,LKS: Этап 2: Привязка BMSTU кредов
    U->>B: вводит BMSTU login/password
    B->>GW: POST /api/bmstu/creds (Authorization: Bearer)
    GW->>A: VerifyAccess(token)
    A-->>GW: {user_id}
    GW->>BM: StoreCredentials(user_id, login, password)
    BM->>BM: AES-GCM(password, master_key)
    BM->>LKS: GET /profile → 30x → Keycloak /auth
    LKS-->>BM: HTML формы (action + hidden inputs)
    BM->>LKS: POST username/password → set-cookie
    LKS-->>BM: 30x → /profile (success)
    BM->>BM: persist cookies в bmstu_sessions
    BM-->>GW: status=VALID, last_login_at
    GW-->>B: 204 No Content
    end

    rect rgba(255, 240, 200, 0.2)
    note over U,TG: Этап 3: Привязка Telegram
    U->>B: жмёт «Привязать TG»
    B->>GW: POST /api/me/telegram/init
    GW->>A: LinkTelegramInit(user_id)
    A-->>GW: {deeplink: t.me/BotName?start=ABC123, code}
    GW-->>B: {deeplink}
    B->>U: показывает кнопку «Открыть в TG»
    U->>TG: /start ABC123
    TG->>N: webhook update (или long-poll)
    N->>A: LinkTelegramComplete(code=ABC123, chat_id)
    A->>A: UPDATE users SET telegram_chat_id
    A-->>N: {user_id}
    N->>TG: sendMessage(chat_id, "Привязка завершена")
    end

    rect rgba(255, 220, 220, 0.2)
    note over U,FL: Этап 4: Создание фильтра
    U->>B: «Аэробика, пн-ср, 18:00-21:00»
    B->>GW: POST /api/filters
    GW->>A: VerifyAccess
    GW->>FL: CreateFilter(user_id, section="Аэробика", ...)
    FL-->>GW: Filter
    GW-->>B: 201 Filter
    end

    rect rgba(220, 220, 255, 0.2)
    note over P,B: Этап 5: Первый алёрт
    P->>P: tick (60s + jitter)
    P->>BM: FetchGroups(user_id)
    BM->>LKS: GET /lks-back/api/v1/fv/.../groups (cookies)
    LKS-->>BM: JSON слотов
    BM-->>P: []Slot
    P->>FL: MatchSlots(user_id, slots)
    FL->>FL: применяет фильтры, проверяет known_slots
    FL-->>P: []MatchedSlot (is_new=true)
    P->>N: NotifyMatched(user_id, matched, channels=[TG, SSE])
    N->>TG: sendMessage(chat_id, formatted_alert)
    N->>NATS: publish alerts.<user_id>
    GW->>NATS: subscribe alerts.<user_id>
    NATS-->>GW: event
    GW-->>B: SSE event: new-slot
    B-->>U: toast «Свободный слот: Аэробика, 18:00»
    P->>FL: MarkSeen(user_id, [slot_ids])
    end
```

---

## 4. Sequence — Poll cycle (один тик poller-svc)

Детально что происходит каждые 60s ± 15s jitter.

```mermaid
sequenceDiagram
    autonumber
    participant T as Ticker (60s)
    participant P as poller-svc
    participant FL as filter-svc
    participant BM as bmstu-svc
    participant LKS as BMSTU LKS
    participant TC as teachers-svc
    participant N as notifier-svc
    participant TG as Telegram API
    participant NATS as NATS JetStream
    participant GW as gateway-svc

    T->>P: tick
    P->>FL: ListActiveUsers() (внутр. метод или БД-запрос)
    note right of P: фильтр: last_seen < 7d, есть creds, есть фильтры
    FL-->>P: [user_id_1, user_id_2, ...]
    P->>P: shuffle (anti-ban)

    loop для каждого user_id (последовательно или ограниченным пулом)
        P->>BM: FetchGroups(user_id)
        alt cookies валидны
            BM->>LKS: GET /groups (with cookies)
            LKS-->>BM: 200 JSON
            BM-->>P: []Slot
        else 401/403 от LKS
            BM->>BM: try RefreshSession()
            alt reauth ok
                BM->>LKS: GET /groups retry
                LKS-->>BM: 200 JSON
                BM-->>P: []Slot
            else reauth fail
                BM-->>P: gRPC error FAILED_PRECONDITION
                P->>N: SendDirect(user_id, "Обнови BMSTU пароль")
                Note over P: skip rest для этого user, переходим к следующему
            end
        end

        P->>FL: MatchSlots(user_id, slots)
        FL->>FL: применяет enabled-фильтры
        FL->>TC: BatchGet([teacher_uids])
        TC-->>FL: [Teacher]
        FL->>FL: dedup vs known_slots, обогащает rating
        FL-->>P: []MatchedSlot (только is_new=true)

        opt matched непусто
            P->>N: NotifyMatched(user_id, matched)
            par отправка
                N->>TG: sendMessage(chat_id, formatted)
            and
                N->>NATS: publish alerts.<user_id>
                NATS->>GW: event
                GW->>GW: push в SSE-канал юзера (если онлайн)
            end
            N-->>P: delivered_by=[TG,SSE]
            P->>FL: MarkSeen(user_id, slot_ids)
        end
    end

    P->>P: sleep until next tick + jitter
```

**Резильентность:**
- Если bmstu-svc отвечает FAILED_PRECONDITION → poller НЕ вызывает MarkSeen → при следующем тике повторит.
- Если notifier упал между TG и MarkSeen → дубль алёрта на следующем цикле (приемлемо, лучше дубль чем потеря).
- circuit-breaker (`sony/gobreaker`) на LKS — при N подряд ошибках LKS приостанавливаем опросы на M минут.
- `known_slots` НЕ очищается при ошибке/пустом ответе LKS — фикс бага main.py:312.

---

## 5. Развёртывание (deployment view)

```mermaid
graph TB
    subgraph VPS["Single VPS / dev box"]
        subgraph Docker["docker-compose"]
            caddy[Caddy<br/>:443/:80<br/>auto-TLS]
            spa_static[Vue SPA<br/>статика, /usr/share/nginx/html]
            gateway[gateway-svc<br/>:8080]
            auth[auth-svc<br/>:9001]
            bmstu[bmstu-svc<br/>:9002]
            filter[filter-svc<br/>:9003]
            notifier[notifier-svc<br/>:9004]
            teachers[teachers-svc<br/>:9005]
            poller[poller-svc<br/>no port]
            pg[(Postgres :5432)]
            nats[(NATS :4222)]
        end
    end

    Internet[Internet]
    BMSTU[lks.bmstu.ru]
    TG[api.telegram.org]

    Internet -->|443| caddy
    caddy --> spa_static
    caddy -->|/api/*| gateway
    gateway --> auth & bmstu & filter & notifier & teachers
    gateway -.subscribe.-> nats
    poller --> bmstu & filter & notifier
    notifier -.publish.-> nats
    auth & bmstu & filter & teachers --> pg
    bmstu --> BMSTU
    notifier --> TG
```

В prod каждый сервис — distroless-образ ~30 MB. Можно горизонтально масштабировать stateless-сервисы (auth, gateway, filter, teachers). bmstu-svc — карефул, cookiejar в памяти (если хотим scale-out — нужен sticky или session store в Redis, V2). poller — ровно 1 instance (выбор лидера если scale).

---

## 6. Cross-cutting

| Тема | Решение |
|---|---|
| Логирование | `slog` JSON в prod, текст в dev. Корреляция через `X-Request-ID` (генерится в gateway, прокидывается в gRPC metadata). |
| Трейсинг | OpenTelemetry, gRPC + HTTP middleware, экспорт в OTLP (опц. в V2). |
| Метрики | `/metrics` Prometheus в каждом сервисе (RED + business: алёрты доставлено/упало). |
| Healthchecks | `/healthz` (liveness) и `/readyz` (зависимости готовы) в gateway; gRPC health protocol во внутренних. |
| Конфиг | `caarlos0/env` из ENV; секреты — Docker secrets / `.env` (gitignored). |
| Шифрование при хранении | AES-256-GCM для BMSTU паролей, master-key из env `BMSTU_CREDS_KEY` (32 байта base64). |
| Транспортная безопасность | mTLS между gRPC-сервисами в prod (V2); в dev — plain text внутри docker network. |
| Идентификаторы | UUIDv7 для users/filters (лексикографически = по времени, индексы дружелюбнее), детерминированный sha1 для Slot.id. |

---

## 7. Что НЕ в scope этой версии

- Авто-запись на слот (реверс POST `/fv/new-record`) — V2.
- Web Push с VAPID — V2.
- Email через SMTP — V2.
- Admin UI — V2.
- Multi-tenant (несколько вузов) — V3.
- Grafana dashboards — настраиваются devops отдельно.

См. [adr/](./adr/) для архитектурных решений с обоснованием.
