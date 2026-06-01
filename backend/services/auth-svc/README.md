# auth-svc

Сервис аутентификации пользователей сайта fizcultor-bot.

Зона ответственности:
- Хранение пользователей (argon2id хеш паролей).
- Выпуск/обновление/отзыв JWT (access HS256 15 мин, refresh opaque 30 дней).
- Refresh-token rotation с **reuse-detection**: при повторном предъявлении
  уже-revoked токена отзываются ВСЕ активные refresh-токены пользователя.
- Привязка Telegram chat_id через одноразовый deeplink-код.

Что НЕ делает: не знает про BMSTU-креды, фильтры или слоты. Связь с ними —
только по `user_id`.

## gRPC API

Полный контракт: [`backend/proto/auth/v1/auth.proto`](../../proto/auth/v1/auth.proto).

| RPC | Кто вызывает | Краткое описание |
|---|---|---|
| `Register` | gateway-svc | Регистрация (email+password). |
| `Login` | gateway-svc | Логин, возвращает `TokenPair`. |
| `Refresh` | gateway-svc | Ротация refresh+access. Reuse-detection. |
| `Revoke` | gateway-svc | Logout: revoke текущий + все активные refresh. |
| `GetMe` | gateway-svc | Профиль (берёт user_id из metadata `x-user-id`). |
| `VerifyAccess` | gateway-svc | Stateless валидация JWT для защищённых endpoint. |
| `LinkTelegramInit` | gateway-svc | Генерация одноразового кода для привязки TG. |
| `LinkTelegramComplete` | notifier-svc | Завершение привязки по коду + chat_id. |

### Соглашения

- **`x-user-id`** — gRPC-metadata ключ, через который gateway-svc передаёт
  user_id вниз по стеку после `VerifyAccess`. Соответствует
  [`pkg/grpcx.WithUserID`](../../pkg/grpcx/dial.go).
- **Anti-enumeration**: `Login` отвечает одинаковым `UNAUTHENTICATED` для
  «email не найден» и «неверный пароль» — нельзя различить эти случаи.
- **Reuse-detection**: попытка использовать уже-revoked refresh приводит к
  отзыву ВСЕХ refresh-токенов пользователя + `UNAUTHENTICATED`.
- **Token format**:
  - access — JWT HS256, claims `sub` (user_id), `exp`, `iat`, `nbf`, `jti`, `iss=fizcultor-bot-auth`, `kind=access`.
  - refresh — `base64.RawURLEncoding(rand 32 bytes)`, в БД хранится `hex(sha256(raw))`.

### gRPC-коды ошибок

| Код | Условие |
|---|---|
| `InvalidArgument` | Невалидный email/пароль/`telegram_chat_id`. |
| `Unauthenticated` | Любая ошибка проверки (Login, Refresh, VerifyAccess). |
| `AlreadyExists` | Email занят (Register). |
| `NotFound` | `GetMe`/`LinkTelegramComplete`: пользователь/код не найден. |
| `Internal` | Падение БД/argon2/JWT-подписи. |

## База данных

DB: `auth_db` (отдельная PostgreSQL-БД).

Таблицы:
- `users(id BIGSERIAL, email UNIQUE, password_hash, tg_chat_id, tg_link_token, is_active, created_at, last_seen_at)`
- `refresh_tokens(id BIGSERIAL, user_id FK→users, token_hash UNIQUE, expires_at, revoked, replaced_by FK→refresh_tokens, created_at)`

Миграции: [`backend/migrations/auth_db/`](../../migrations/auth_db/) — формат
goose. Прогон:
```sh
make migrate-up   # требует POSTGRES_DSN_BASE и установленный goose
```

SQL-queries для sqlc: [`sql/queries/`](./sql/queries). Перегенерация
типизированного слоя:
```sh
make sqlc         # требует установленный sqlc
```
Ручная реализация в `internal/store/` повторяет сигнатуры, которые
сгенерирует sqlc — обе версии взаимозаменяемы.

## Env vars

| Var | Default | Описание |
|---|---|---|
| `APP_ENV` | `dev` | `dev` или `prod` |
| `SERVICE_NAME` | `auth-svc` | Имя сервиса для логов |
| `LOG_LEVEL` | `info` | debug/info/warn/error |
| `GRPC_ADDR` | `:9090` | Адрес gRPC сервера |
| `HTTP_ADDR` | `:8080` | Адрес healthz/readyz |
| `POSTGRES_DSN` | (required) | `postgres://user:pass@postgres:5432/auth_db?sslmode=disable` |
| `JWT_SECRET` | (required, ≥32 байт) | HMAC-секрет HS256 |
| `JWT_ACCESS_TTL_SECONDS` | `900` | TTL access (15 мин) |
| `JWT_REFRESH_TTL_SECONDS` | `2592000` | TTL refresh (30 дней) |
| `ARGON2_MEMORY_KIB` | `65536` | Память argon2id (64 MiB) |
| `ARGON2_ITERATIONS` | `3` | Итераций argon2id |
| `ARGON2_PARALLELISM` | `2` | Параллелизм argon2id |

## Локальный запуск

```sh
cd backend/services/auth-svc
export POSTGRES_DSN="postgres://fizcultor:fizcultor@localhost:5432/auth_db?sslmode=disable"
export JWT_SECRET="$(head -c 64 /dev/urandom | base64)"
go run ./cmd/server
```

Через docker-compose из корня:
```sh
cd backend/deploy && docker compose up auth-svc
```

## Тесты

```sh
cd backend/services/auth-svc && go test ./...
```

Покрытие:
- `internal/auth/password_test.go` — argon2id round-trip, формат PHC, wrong-password, разные salt.
- `internal/auth/tokens_test.go` — формат refresh, детерминизм hash, uniqueness.
- `internal/auth/service_test.go` — table-driven Register/Login, rotation + reuse-detection Refresh, Revoke, VerifyAccess, GetMe, LinkTelegram.

Persistence-слой замокан in-memory `fakeStore`, реализующим тот же `auth.Store`
интерфейс что и `*store.Store`. Интеграционных тестов с реальным Postgres нет
(они в задаче wave 4 qa-engineer).
