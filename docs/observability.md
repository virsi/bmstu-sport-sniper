# Observability — fizcultor-bot

Production-стэк: **Prometheus** (метрики) + **Grafana** (дашборды) + **slog JSON** в stdout (логи). Tracing (Jaeger/Tempo) пока не подключено — V2.

## Стэк

```
              ┌───────────────────────────────────┐
              │  https://<DOMAIN>/grafana/        │
              │  Grafana UI (только админ)        │
              └───────────────┬───────────────────┘
                              │ datasource
                              ▼
              ┌───────────────────────────────────┐
              │  Prometheus :9090                 │
              │  scrape 15s, retention 30d        │
              └───────────────┬───────────────────┘
                              │ /metrics
            ┌─────────────────┼─────────────────┐
            ▼                 ▼                 ▼
   ┌────────────────┐ ┌────────────────┐ ┌────────────────┐
   │  auth-svc      │ │  bmstu-svc     │ │  gateway-svc   │
   │  :8080/metrics │ │  :8080/metrics │ │  :8080/metrics │
   └────────────────┘ └────────────────┘ └────────────────┘
   (+ filter-svc, teachers-svc, notifier-svc, poller-svc)
```

Логи: `docker logs <svc>` → stdout (JSON в prod) → можно собирать через
Loki / Promtail (V2).

## Метрики

Все имена префиксованы именем сервиса (`auth_`, `bmstu_` и т.д.). Это даёт
читаемые названия в Grafana без relabel.

### Общие для всех сервисов

| Имя | Тип | Описание |
|---|---|---|
| `<svc>_grpc_requests_total{method, code}` | counter | Все gRPC-запросы по методу и коду ответа (`OK`, `InvalidArgument`, ...). |
| `<svc>_grpc_request_duration_seconds{method}` | histogram | Латенси gRPC по методу. Бакеты: 5ms..10s. |
| `<svc>_grpc_inflight_requests` | gauge | Сколько одновременных gRPC-запросов в обработке. |
| `<svc>_db_queries_total{query, status}` | counter | SQL-запросы по имени и статусу (`ok`/`error`). |
| `<svc>_db_query_duration_seconds{query}` | histogram | Латенси SQL-запросов. |
| `<svc>_http_requests_total{route, status}` | counter | HTTP-запросы (только в gateway-svc). |
| `<svc>_http_request_duration_seconds{route}` | histogram | HTTP latency. |
| `go_goroutines` | gauge | Стандартный Go runtime — следить на leak. |
| `go_memstats_heap_inuse_bytes` | gauge | Heap, для OOM detection. |
| `process_cpu_seconds_total` | counter | CPU usage. |

`<svc>` — короткое имя без `-svc`: `auth`, `bmstu`, `filter`, `gateway`, `notifier`, `poller`, `teachers`.

### Service-specific (по типу сервиса)

| Сервис | Метрика | Описание |
|---|---|---|
| `auth` | `auth_logins_total{result}` | Login attempts: `ok` / `wrong_password` / `not_found` / `error` |
| `auth` | `auth_register_total{result}` | Регистрация: `ok` / `email_exists` / `weak_password` / `error` |
| `bmstu` | `bmstu_login_attempts_total{result}` | LKS-логин попытки |
| `bmstu` | `bmstu_session_age_seconds` | Histogram возраста активных LKS-сессий |
| `bmstu` | `bmstu_api_requests_total{status}` | Запросы к LKS-API |
| `poller` | `poller_cycles_total` | Сколько циклов опроса запущено |
| `poller` | `poller_users_polled_total{result}` | Пер-юзерные poll-результаты |
| `poller` | `poller_cycle_duration_seconds` | Длительность одного полного цикла |
| `notifier` | `notifier_sent_total{channel, result}` | Уведомления отправлены, по каналу и результату |
| `gateway` | `gateway_sse_connections` | Активные SSE-подключения (gauge) |

**Текущий статус инкрементов** (wave 5 baseline): общие метрики (gRPC, DB, HTTP) собираются автоматически через interceptor / middleware. Service-specific counters **зарегистрированы** в Registry, но конкретные `Inc()` вызовы из бизнес-логики добавляются по мере необходимости. Это даёт нулевую базовую точку для алертов; реальные значения появятся, когда сервис расширят hooks (см. TODO в каждом `cmd/server/main.go`).

## Логи

`pkg/logger.Init(env, level, service)` создаёт slog-логгер:

- `env=prod`: **JSON** в `os.Stdout`. Caller-логи и stack trace включены через `slog.HandlerOptions{AddSource: true}` если нужно (по умолчанию off для меньшего overhead).
- `env=dev`: **text** в `os.Stdout`, level=info.
- Все записи имеют атрибут `service=<svc>` (auth-svc, bmstu-svc, ...).

Дополнительные helper'ы:

```go
// Аттач trace_id к логгеру (использовать в HTTP middleware)
lg := logger.WithTraceID(slog.Default(), requestID)

// Аттач user_id (в защищённых ручках после Auth middleware)
lg = logger.WithUserID(lg, userID)

// В контексте
ctx = logger.WithLogger(ctx, lg)
// ...
lg = logger.FromContext(ctx)  // в downstream-коде
```

### Пример лога (prod, JSON)

```json
{"time":"2026-06-01T18:24:15.234Z","level":"INFO","msg":"http","service":"gateway-svc","method":"POST","path":"/api/filters","status":200,"dur":47000000,"trace_id":"a7b2..."}
```

### Loki / Datadog agent

Stdout-JSON формат совместим с любым агентом. Минимальный setup с Loki + Promtail:

```yaml
# promtail.yaml — pseudo-config, выходит за scope wave 5
scrape_configs:
  - job_name: fizcultor
    docker_sd_configs:
      - host: unix:///var/run/docker.sock
    pipeline_stages:
      - docker: {}
      - json:
          expressions:
            level: level
            service: service
            trace_id: trace_id
      - labels:
          service:
          level:
```

## Алерты

V2. Заготовка для Alertmanager:

```yaml
# backend/deploy/prometheus/rules/services.yml — пока не созданы
groups:
  - name: services
    rules:
      - alert: ServiceDown
        expr: up{job="fizcultor-services"} == 0
        for: 2m
        labels: {severity: page}
        annotations:
          summary: "{{ $labels.service }} is down"

      - alert: HighErrorRate
        expr: sum by (service) (rate({__name__=~".+_grpc_requests_total", code!="OK"}[5m]))
              / sum by (service) (rate({__name__=~".+_grpc_requests_total"}[5m])) > 0.05
        for: 5m
        labels: {severity: warning}
        annotations:
          summary: "{{ $labels.service }} error rate > 5%"

      - alert: HighLatencyP99
        expr: histogram_quantile(0.99, sum by (le, service) (rate({__name__=~".+_grpc_request_duration_seconds_bucket"}[5m]))) > 2
        for: 10m
        labels: {severity: warning}

      - alert: PostgresDown
        expr: up{service="postgres"} == 0   # требует postgres_exporter
        for: 1m
        labels: {severity: page}
```

## Dashboards

Provisioned автоматически из `backend/deploy/grafana/dashboards/`:

| UID | Имя | Что показывает |
|---|---|---|
| `fizcultor-overview` | fizcultor — overview | Per-svc request rate, latency p95/p99, error rate, BMSTU auth, poller cycles, Go runtime |

Свои дашборды можно создавать в Grafana UI и экспортировать в JSON, затем коммитить в `dashboards/`.

URL pattern (если Caddyfile проксирует Grafana):
```
https://<DOMAIN>/grafana/d/fizcultor-overview/fizcultor-overview
```

## Tracing (V2)

Не реализовано в wave 5. План:

1. Добавить `pkg/tracing` с OpenTelemetry SDK (Jaeger или Tempo backend).
2. Унифицировать `trace_id` в slog с OTEL TraceID (вместо своего request-id).
3. gRPC interceptor через `otelgrpc.UnaryServerInterceptor`.
4. Sampling: 100% errors + 5% successful (rate-based).

## Quick links для on-call

- Prometheus targets: `http://<host>:9090/targets`
- Grafana login: `https://<DOMAIN>/grafana/login`
- См. также: [runbook.md](runbook.md) для типичных инцидентов.
