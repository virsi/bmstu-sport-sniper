# poller-svc

Оркестратор: ticker + ротация запросов к LKS, обвязка bmstu → filter → notifier.

## Архитектура

- Без gRPC-сервера. Только клиенты к `bmstu-svc`, `filter-svc`, `notifier-svc`, `auth-svc`.
- Без БД (state дедупа — в filter-svc).
- Resilience:
  - per-user exponential backoff (30s → 30m, factor 2.0).
  - global circuit-breaker (`sony/gobreaker`) на bmstu: 5 подряд-фейлов → open
    на 2 минуты, half-open пропускает 3 пробных запроса.
- Concurrency: `POLL_CONCURRENCY` (по умолчанию 10) параллельных опросов,
  ограничены семафором (`chan struct{}`).

## Главный цикл (каждые `POLL_INTERVAL_SECONDS` + jitter `POLL_JITTER_SECONDS`)

См. `docs/architecture.md` §4. Для каждого активного юзера в горутине:

1. Per-user jitter `0..POLL_PER_USER_JITTER_SECONDS` (антибан LKS).
2. `bmstu.FetchGroups(user_id)` через circuit-breaker.
   - При gRPC `FAILED_PRECONDITION` → `notifier.SendDirect("Обнови BMSTU пароль")`.
3. `filter.MatchSlots(user_id, slots)`.
4. Фильтр `is_new=true` локально (это безопаснее, чем полагаться на
   filter-svc returning только new).
5. `notifier.NotifyMatched(user_id, matched_new)`.
6. **`filter.MarkSeen` вызывается ТОЛЬКО если notifier вернул ≥1 delivered_by.**
   При полном фейле notify — не помечаем; следующий тик повторит (дубль приемлем,
   потеря — нет; легаси-баг `KNOWN_SLOTS.clear()` исправлен на уровне filter-svc).

## Источник активных юзеров

Текущая итерация: статический env-список `POLL_USER_IDS=uuid1,uuid2,...`
(интерфейс `orchestrator.ActiveUsers`). Когда в proto filter-svc появится
`ListActiveUsers` RPC — реализация поменяется без изменения orchestrator'а.

## Env vars

| Var | Default | Описание |
|---|---|---|
| `APP_ENV` | `dev` | |
| `SERVICE_NAME` | `poller-svc` | |
| `HTTP_ADDR` | `:8080` | healthz/readyz |
| `POLL_INTERVAL_SECONDS` | `60` | базовый интервал тикера |
| `POLL_JITTER_SECONDS` | `15` | ±N сек jitter между тиками |
| `POLL_PER_USER_JITTER_SECONDS` | `3` | задержка перед запросом каждого юзера |
| `POLL_CONCURRENCY` | `10` | максимум параллельных опросов |
| `POLL_USER_IDS` | `""` | список user_id'ов через запятую (stub) |
| `ACTIVE_USER_DAYS` | `7` | (зарезервировано для будущего RPC) |
| `SEMESTER_UUID` | `""` | (диагностика; bmstu-svc сам читает из env) |
| `BMSTU_GRPC_ADDR` | `bmstu-svc:9090` | |
| `FILTER_GRPC_ADDR` | `filter-svc:9090` | |
| `NOTIFIER_GRPC_ADDR` | `notifier-svc:9090` | |
| `AUTH_GRPC_ADDR` | `auth-svc:9090` | |

## Локальный запуск

```sh
export POLL_USER_IDS="01H6...,01H7..."
export POLL_INTERVAL_SECONDS=60
go run ./cmd/server
```

## Наблюдаемость

- Лог уровня INFO в конце каждого цикла: длительность, кол-во юзеров.
- Лог уровня INFO на успешный poll юзера: slots_total, matched_new,
  delivered, failed_channels.
- Лог уровня WARN на каждый сетевой фейл / FAILED_PRECONDITION / open breaker.

## Тесты

- `internal/orchestrator/backoff_test.go` — параллельная корректность,
  экспоненциальный рост, Reset.
- `internal/orchestrator/orchestrator_test.go` — табличные тесты `runCycle`:
  - happy path,
  - bmstu fail → нет MarkSeen,
  - notifier fail → нет MarkSeen,
  - delivered_by == 0 → нет MarkSeen,
  - empty matched → нет notify,
  - empty slots → нет filter,
  - FAILED_PRECONDITION → SendDirect,
  - MarkSeen fail не валит цикл,
  - semaphore не превышает `POLL_CONCURRENCY`,
  - backoff скипает повторный фейл.
