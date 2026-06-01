# filter-svc

CRUD пользовательских фильтров, дедупликация `known_slots`, чистая функция матчинга слотов.

## gRPC API

- `CreateFilter / GetFilter / ListFilters / UpdateFilter / DeleteFilter` — стандартный CRUD против `filter_db.filters`.
- `MatchSlots(userID, slots) → []MatchedSlot` — применяет enabled-фильтры юзера к набору слотов и возвращает только МАТЧНУВШИЕ с флагом `is_new`. **НЕ** пишет в `known_slots`.
- `MarkSeen(userID, slotIDs)` — вызывается poller-svc после успешной отправки алёрта через notifier. Записывает в `known_slots` (ON CONFLICT DO NOTHING) и в `alert_log`.
- `ResetKnown(userID)` — явная админ-операция, очищает `known_slots` пользователя.

## Архитектурные решения

### Разделение `MatchSlots` и `MarkSeen`

Делать матчинг и пометку seen разными RPC — это обязательное требование архитектуры. Если notifier упадёт между doc и MarkSeen, poller просто повторит цикл (дубль алёрта приемлем; потеря — нет). См. `architecture.md`, sequence «poll cycle».

### Фикс бага legacy_main.py:312

Legacy-код очищал `known_slots` при пустом ответе LKS:
```python
KNOWN_SLOTS.intersection_update(current_slots_map.keys())
```
Это давало повторные алёрты при первой же пустой выдаче. В filter-svc:
- `InsertKnownSlots` использует `ON CONFLICT DO NOTHING` — повторная пометка не меняет `first_seen`.
- НИКАКОЙ операции «удалить пропавшие слоты» нет.
- Только явный `ResetKnown` стирает `known_slots`.

### Чистая функция матчинга

`internal/match.Match` не делает I/O — это позволяет покрыть её детерминированными табличными тестами (см. `match_test.go`, 16+ кейсов). Сервисный слой загружает данные из БД и оборачивает чистую функцию.

## БД

`filter_db`:

| Таблица       | Описание |
|---------------|---------|
| `filters`     | `id, user_id, section, teacher_uid, day_of_week, time_from, time_to, min_rating, min_vacancy, enabled, created_at, updated_at` |
| `known_slots` | `user_id, slot_id, first_seen` (PK по `(user_id, slot_id)`) |
| `alert_log`   | `id, user_id, slot_id, channel, sent_at, payload (JSONB)` |

### GC старых known_slots

Таблица `known_slots` накапливает записи и должна периодически чиститься: `DELETE FROM known_slots WHERE first_seen < now() - interval '7 days'`. Сейчас CRON-задачи нет. Сделать в Wave 3 (отдельный воркер либо `pg_cron`). Helper `store.DeleteKnownSlotsOlderThan(cutoff)` уже доступен.

## Env vars

| Var            | Default          | Описание                                |
|----------------|------------------|-----------------------------------------|
| `APP_ENV`      | `dev`            | `dev` / `prod`                          |
| `SERVICE_NAME` | `filter-svc`     | для логов                               |
| `LOG_LEVEL`    | `info`           | debug/info/warn/error                   |
| `GRPC_ADDR`    | `:9090`          | gRPC listen                              |
| `HTTP_ADDR`    | `:8080`          | healthz/readyz HTTP                      |
| `POSTGRES_DSN` | (required)       | DSN `filter_db`                          |

## Локальный запуск

```sh
export POSTGRES_DSN="postgres://postgres:postgres@localhost:5432/filter_db?sslmode=disable"
goose -dir migrations/filter_db postgres "$POSTGRES_DSN" up
go run ./cmd/server
```

## Тесты

- `internal/match/match_test.go` — табличные тесты чистой функции матчинга (≥ 15 кейсов).
- `internal/service/service_test.go` — табличные тесты gRPC хэндлеров на mock-сторе.

```sh
go test ./...
go test -race -count=1 ./...
```
