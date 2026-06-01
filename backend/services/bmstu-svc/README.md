# bmstu-svc

Хранит BMSTU-креды пользователей (AES-256-GCM at-rest), управляет LKS-сессиями
(cookiejar + gob persist, AES-GCM), реализует Keycloak OIDC через **pure HTTP**
(replacement для Selenium из старого Python-репо).

## gRPC API

| RPC | Семантика |
|---|---|
| `StoreCredentials(user_id, login, password)` | test-login → AES-encrypt → upsert |
| `DeleteCredentials(user_id)` | каскад: creds + sessions |
| `GetStatus(user_id)` | NOT_LINKED / VALID / EXPIRED |
| `FetchGroups(user_id)` | Acquire(jar) → GET /groups; один retry на 401/403 |
| `RefreshSession(user_id)` | форс-relogin |

Маппинг ошибок:

| gRPC code | Когда |
|---|---|
| `Unauthenticated` | bad login/password |
| `Unavailable` | сессия истекла / 5xx |
| `ResourceExhausted` | Keycloak brute-force protection |
| `FailedPrecondition` | CAPTCHA в форме / нет линковки кредов |
| `InvalidArgument` | пустые поля запроса |
| `Internal` | форма поменялась / crypto / БД |

## БД (bmstu_db)

- `bmstu_credentials(user_id PK, enc_login, enc_password, nonce_*, last_login_at, ...)`
- `bmstu_sessions(user_id PK→creds CASCADE, cookies_blob, nonce, expires_at, last_refresh_at)`

`user_id` — TEXT (UUIDv7 строка из auth_db), без физического FK
(databases isolation).

### Nonce layout

`enc_*` / `cookies_blob` создаются `pkg/crypto.Encrypt` и имеют формат
`nonce(NonceSize) || ciphertext || tag(16)`, где `NonceSize = 12` —
exported-константа `crypto.NonceSize`. Колонки `nonce_login`,
`nonce_password`, `nonce` хранят **дубль** первых `NonceSize` байт
соответствующего blob'а и используются **только** для аудита /
наблюдаемости (отчёты, ad-hoc grep'ы). При расшифровке они не нужны —
`crypto.Decrypt` нарезает blob по `NonceSize` самостоятельно. Дубль
оставлен сознательно: цена 12 байт на строку незначительна, а отдельная
колонка упрощает SQL-запросы типа «у скольких пользователей nonce
коллидирует» (теоретически — 0 при правильно работающем
`crypto/rand.Reader`).

Миграции — goose:
```sh
make migrate-up POSTGRES_DSN_BASE=postgres://postgres:postgres@localhost:5432
```

## Pure HTTP Keycloak (4 шага)

Подробности — `docs/research/keycloak-oidc-bmstu.md`.

```
1. GET https://lks.bmstu.ru/portal4/cookie/login?back=/profile&profile_any=1
   → 302 на Keycloak (cookies: p4sess_intermediate)
2. Follow → GET sso.bmstu.ru/kc/realms/ph/.../auth?client_id=sso
   → 200 HTML формы (cookies: AUTH_SESSION_ID, KC_RESTART, KC_AUTH_SESSION_HASH)
3. Извлекаем <form id="kc-form-login" action=...> через golang.org/x/net/html
4. POST <form-action> с username/password/credentialId=""
   → 302 → portal4/upstream/callback/kc?code=...
   → 302 → /profile (cookie p4sess)
```

После 4-го шага в `http.CookieJar` лежат cookies со всех origin'ов
(`lks.bmstu.ru`, `sso.bmstu.ru`). Они сериализуются через **gob**,
шифруются AES-256-GCM мастер-ключом и пишутся в `bmstu_sessions.cookies_blob`.
При последующих опросах jar восстанавливается из БД, и поход в `/groups`
идёт без re-login'а.

### Cost vs Selenium

| | pure HTTP | Selenium |
|---|---|---|
| Latency login | ~200 ms | 30–50 s |
| RAM | 0 (только Go heap) | 200+ MB (Chromium) |
| Зависимости | net/http, x/net | chromium, chromedriver |

### Что НЕ обрабатывается

- **CAPTCHA**: Keycloak пока не показывает, но детектор есть
  (`ErrCaptcha → FailedPrecondition`). Headless fallback оставлен
  плейсхолдером: `internal/oidc/fallback.go.disabled` (нужно будет добавить
  chromedp; держим в режиме «активируем при необходимости»).
- **WebAuthn/OTP**: парс ищет именно `id="kc-form-login"`, альтернативные
  формы игнорируются — будет `ErrLoginFormNotFound`.

## Env vars

| Var | Default | Описание |
|---|---|---|
| `APP_ENV` | `dev` | dev/prod, влияет на формат логов |
| `SERVICE_NAME` | `bmstu-svc` | имя сервиса в логах |
| `LOG_LEVEL` | `info` | debug/info/warn/error |
| `GRPC_ADDR` | `:9090` | listen-адрес gRPC |
| `HTTP_ADDR` | `:8080` | listen-адрес healthz/readyz |
| `POSTGRES_DSN` | **required** | DSN bmstu_db |
| `AES_MASTER_KEY` | **required** | 64 hex chars = 32 bytes AES-256 |
| `SEMESTER_UUID` | **required** | UUID семестра LKS |
| `LKS_BASE_URL` | `https://lks.bmstu.ru` | можно подменить для тестов |
| `OIDC_USE_BROWSER` | `false` | placeholder для future chromedp fallback |
| `HTTP_CLIENT_TIMEOUT_SECONDS` | `15` | таймаут запроса к LKS |
| `BMSTU_HEALTH_INTERVAL` | `30s` | (зарезервировано, пока неиспользуется) |
| `NATS_URL` | (опц.) | для publish `bmstu.creds.invalid` (V2) |

Сгенерировать мастер-ключ:
```sh
openssl rand -hex 32
```

## Локальный запуск

```sh
export AES_MASTER_KEY="$(openssl rand -hex 32)"
export SEMESTER_UUID="<UUID-семестра-из-LKS>"
export POSTGRES_DSN="postgres://postgres:postgres@localhost:5432/bmstu_db?sslmode=disable"
cd services/bmstu-svc
go run ./cmd/server
```

## Безопасность

- Пароль/логин в логах **никогда** (только `user_id` + `result`).
- Cookie blob не логируется (только размер при debug).
- AES-256-GCM с уникальным `crypto.NonceSize`-байтовым nonce на каждый
  Encrypt (см. pkg/crypto). Магических литералов `12` в коде нет —
  везде ссылка на константу.
- Один мастер-ключ на креды и cookies — DRY-выбор; для отдельной
  ротации добавить параметр SubKey в `crypto.Encrypt` (V2).

## Архитектурные заметки

- **Cookies для нескольких origins**: jar.Cookies(URL) спрашивает по одному
  origin; мы группируем cookies при persist по полю `Domain` и при load
  раскладываем обратно по соответствующим URL (см.
  `internal/session/cookiebox.go:LoadJar`).
- **slot.id — детерминированный sha1** (`sha1(semester|week|time|section|teacher_uid)`,
  не используем API id) — стабильный ключ для дедупа в filter-svc.
- **Один retry** в FetchGroups: если `/groups` вернул 401/403 — мы
  Invalidate + Refresh + повтор. Повторный 401 → `Unavailable`,
  poller должен сам решить ретраить позже.
- **DRY**: один cipher (`pkg/crypto`), одна фабрика cookiejar
  (`session.LoadJar`), один HTTP-клиент фабрика (`session.newClient`).
