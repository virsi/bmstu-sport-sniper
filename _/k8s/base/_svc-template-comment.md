# Шаблон Deployment+Service для Go-сервиса (справочно)

Все 7 сервисов следуют одной структуре. Различия:
- `SERVICE` (имя контейнера, env SERVICE_NAME, image)
- `POSTGRES_DSN` env (auth/bmstu/filter/teachers — разные БД; gateway/notifier/poller — без БД)
- `BOOT_ORDER` (через initContainers `wait-for`)

Не пишем helm/jsonnet шаблоны (KISS), пишем 7 явных манифестов. Каждый
~70-90 строк. Изменения общей структуры — каждый файл вручную.

Поля общие:
- `envFrom`: ConfigMap fizcultor-config + Secret fizcultor-secrets
- HTTP /metrics+/healthz+/readyz на :8080
- gRPC на :9090 (auth/bmstu/filter/notifier/teachers; poller — клиент-only;
  gateway — REST/SSE на :8080)
- imagePullPolicy: IfNotPresent (на prod overlays — Always)
- securityContext: nonroot, no-new-priv, drop ALL caps
- resources: requests 25m CPU / 32Mi RAM, limits 200m / 128Mi
