# ADR 0004 — Database per service vs shared database

**Дата:** 2026-06-02
**Статус:** Accepted

## Контекст

7 микросервисов, у некоторых из них есть состояние:
- `auth-svc`: users, refresh_tokens.
- `bmstu-svc`: bmstu_credentials (encrypted), bmstu_sessions (cookies).
- `filter-svc`: filters, known_slots, alert_log.
- `teachers-svc`: teachers.

Варианты:
1. **Один shared `fizcultor_db`** со всеми таблицами и FK между ними.
2. **Database per service** — `auth_db`, `bmstu_db`, `filter_db`, `teachers_db`,
   каждая со своим owner-role и миграциями. Один Postgres-инстанс, разные
   логические databases.
3. **Schema per service** — один `fizcultor_db`, разные schemas
   (`auth.*`, `bmstu.*`).

## Решение

**Database per service** (вариант 2), один Postgres-инстанс.

## Обоснование

Главный аргумент — **независимость миграций и право собственности**.
Каждый сервис имеет:
- Свою `migrations/` папку (golang-migrate).
- Свой role в БД с ограниченными правами (`auth_user` владеет только
  `auth_db`, не может SELECT из `filter_db`).
- Свой sqlc.yaml для генерации type-safe SQL.

Это устраняет анти-паттерн «общая БД как канал коммуникации»:
если filter-svc хочет user.email — он не делает JOIN на `users`, а
вызывает `auth-svc.GetMe(user_id)`. Связь по `user_id` (FK на уровне типа,
не на уровне БД).

| Критерий | Database per service | Shared DB | Schema per service |
|---|---|---|---|
| Изоляция миграций | Полная | Нулевая (lock contention) | Частичная |
| Независимая эволюция схемы | Да | Нет, любая миграция трогает чужие табл | Да (если строго по schema) |
| Возможность JOIN между сервисами | Нет (правильно) | Да (легко скатиться к monolith) | Да (искушение) |
| Backup/restore per service | Возможно | Только всё сразу | По схеме (`pg_dump --schema`) |
| Cross-service транзакции | Невозможны (надо saga) | Возможны (но плохо) | Возможны |
| Отделение в физически разные кластеры (V2) | Тривиально | Нет | Нет |

«Невозможность cross-service транзакций» — это feature, не bug.
Заставляет проектировать eventually-consistent саги (например, регистрация
+ выпуск токенов — это две операции, мы делаем их в auth-svc внутри одной
транзакции; cross-svc сага в V2 если понадобится).

## Цена

- Нет referential integrity на уровне БД между сервисами (`filter.user_id`
  не FK на `auth_db.users.id`). Митигация:
  - Soft-delete вместо hard-delete пользователей (если уж удаление — отдельный
    хук во все сервисы).
  - При cascade delete юзера запускается «cleanup job» в каждом сервисе.
- Не можем сделать атомарный backup всей системы. Митигация: snapshot
  Postgres-инстанса целиком (pg_basebackup) — всё ещё атомарно по
  WAL-position.
- Чуть больше connection pool'ов (по одному на сервис), но это копейки.

## Как мигрировать (с зеро)

Это новый проект, ничего мигрировать не надо. Структура с нуля:

```
backend/migrations/
├── auth/
│   ├── 0001_init.up.sql
│   ├── 0001_init.down.sql
│   ├── 0002_refresh_tokens.up.sql
│   └── ...
├── bmstu/
│   ├── 0001_init.up.sql
│   └── ...
├── filter/
│   ├── 0001_init.up.sql
│   └── ...
└── teachers/
    ├── 0001_init.up.sql
    └── ...
```

Каждая папка применяется отдельной командой `migrate -path ./migrations/auth
-database $AUTH_DB_DSN up` при старте контейнера через `entrypoint.sh`.

В docker-compose:
```yaml
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_MULTIPLE_DATABASES: auth_db,bmstu_db,filter_db,teachers_db
    volumes:
      - ./deploy/postgres-init.sh:/docker-entrypoint-initdb.d/init.sh
```

Скрипт `postgres-init.sh` создаёт databases и роли с правами только на свою:
```sql
CREATE DATABASE auth_db;
CREATE ROLE auth_user LOGIN PASSWORD '...';
GRANT ALL PRIVILEGES ON DATABASE auth_db TO auth_user;
```

## Альтернативы

**Shared DB** отвергнут: ведёт к JOIN-ам между сервисами, миграции
блокируют друг друга, нарушает принцип «у каждого сервиса свои данные».

**Schema per service** — компромисс, но соблазн сделать JOIN между схемами
слишком велик; нет реальной изоляции прав без сложной настройки. Без явной
блокировки cross-schema SELECT'ов команда легко сделает короткое замыкание
в обход gRPC.

## Последствия

- 4 отдельные миграционные папки в `backend/migrations/`.
- 4 env-переменные DSN: `AUTH_DB_DSN`, `BMSTU_DB_DSN`, `FILTER_DB_DSN`,
  `TEACHERS_DB_DSN`.
- 4 `sqlc.yaml` (или один с 4 packages — sqlc это умеет).
- pgxpool инстансы — отдельные per-service (общий wrapper в `pkg/pgxutil`).
- Запрет в code review: «не делать SELECT из чужой БД». Линтер `sqlc lint`
  не поймает, но визуально DSN не тот.

## Связанные

- [0001 — Microservices vs monolith](./0001-microservices-vs-monolith.md)
- [0002 — NATS vs Redis Streams](./0002-nats-vs-redis-streams.md)
