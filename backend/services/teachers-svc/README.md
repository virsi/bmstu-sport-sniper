# teachers-svc

Справочник учителей физкультуры BMSTU с агрегированными рейтингами. Источник — embedded `teachers.json` (импорт при первом запуске).

## gRPC API

- `Get(uid) → Teacher` — поиск по детерминированному UID. NOT_FOUND если нет.
- `BatchGet(uids) → []Teacher` — батч-поиск (filter-svc обогащает `MatchedSlot.teacher_rating`). Отсутствующие uid'ы молча пропускаются.
- `List(name_query?, page) → []Teacher` — paginate + опц. case-insensitive substring-search по нормализованному имени.
- `Refresh(inline_json?) → ImportStats` — повторный импорт. По умолчанию из embedded JSON; для тестов можно передать `inline_json`. Идемпотентен (upsert по uid).

## Архитектурные решения

### Детерминированный UID

UID = `sha1(normalize(full_name))[:16]` (hex). Тот же UID для того же входного имени между запусками — это позволяет `Slot.teacher_uid` от bmstu-svc матчиться с `teachers.uid` после нескольких циклов импорта.

Реализация:
- `NormalizeName(name)` — lower-case + trim + сжать повторные пробелы.
- `GenerateUID(normalized)` — sha1 первые 16 hex символов.

Обе функции — чистые, покрыты тестами в `import_test.go`.

### Bootstrap import

При старте сервиса:
1. `store.Count()` — если есть хоть одна запись → пропуск.
2. Иначе → `teachers.Import(embeddedJSON)` (parse + upsert).

`BOOTSTRAP_IMPORT=false` пропустит шаг — для тестов / прод-restarts с уже залитой таблицей.

## БД

`teachers_db`:

| Колонка           | Тип             | Описание                                          |
|-------------------|-----------------|---------------------------------------------------|
| `uid`             | TEXT PK         | sha1 от `name_normalized`, первые 16 символов     |
| `name`            | TEXT NOT NULL   | оригинальное ФИО как в источнике                  |
| `name_normalized` | TEXT NOT NULL   | lower + trim + сжатые пробелы                     |
| `rating`          | NUMERIC(3,2)    | 0..5, может быть NULL                             |
| `source_url`      | TEXT            | URL источника рейтинга (V1 = NULL)                |
| `imported_at`     | TIMESTAMPTZ     | последний `upsert`                                |

Индекс: `(name_normalized)` — для substring-поиска.

## Обновление teachers.json

1. Положить новый JSON в `services/teachers-svc/internal/teachers/teachers.json` (та же папка, что и embed-директива).
2. Пересобрать бинарь (embedded — встроен в binary).
3. На запущенном сервисе: либо вызвать `Refresh()` через grpcurl, либо `DROP TABLE teachers; CREATE TABLE ...; restart` для гарантированно чистого импорта.

Формат `teachers.json`:
```json
{
  "<полное имя в lowercase>": {"rating": "4.85"}
}
```
- Ключ — имя как есть; на парсе нормализуется через `NormalizeName`.
- `rating` — строка; нечисловые / out-of-range значения сохраняются как `NULL`.

## Env vars

| Var               | Default        | Описание                                |
|-------------------|----------------|-----------------------------------------|
| `APP_ENV`         | `dev`          | `dev` / `prod`                          |
| `SERVICE_NAME`    | `teachers-svc` | для логов                               |
| `LOG_LEVEL`       | `info`         | debug/info/warn/error                   |
| `GRPC_ADDR`       | `:9090`        | gRPC listen                              |
| `HTTP_ADDR`       | `:8080`        | healthz/readyz HTTP                      |
| `POSTGRES_DSN`    | (required)     | DSN `teachers_db`                        |
| `BOOTSTRAP_IMPORT`| `true`         | импорт embedded JSON при пустой таблице  |

## Локальный запуск

```sh
export POSTGRES_DSN="postgres://postgres:postgres@localhost:5432/teachers_db?sslmode=disable"
goose -dir migrations/teachers_db postgres "$POSTGRES_DSN" up
go run ./cmd/server
```

## Тесты

```sh
go test ./...
go test -race -count=1 ./...
```

Покрытие:
- `import_test.go` — golden JSON парс, нормализация имён (Unicode), детерминизм UID, парсинг embedded JSON целиком.
- `service_test.go` — gRPC хэндлеры на in-memory mock-сторе (Get/BatchGet/List/Refresh/Bootstrap).
