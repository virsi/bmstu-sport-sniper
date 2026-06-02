# k8s deploy для fizcultor

Это каталог Kustomize-манифестов для деплоя стека в k0s-кластер `twc-brainy-crane` (Timeweb Cloud).

## Что внутри

```
base/                                # переиспользуемые манифесты
  namespace.yaml
  configmap-shared.yaml              # неcекретные env
  secrets.template.yaml              # справочник полей Secret (НЕ применяется)
  traefik.yaml                       # свой Traefik в fizcultor ns + LoadBalancer
  postgres.yaml                      # StatefulSet + 4 БД init script
  nats.yaml                          # JetStream
  auth-svc.yaml … gateway-svc.yaml   # 7 Go-сервисов
  frontend-nginx.yaml                # Vue SPA в nginx
  migrations-job.yaml                # goose-up Job
  ingressroute.yaml                  # Traefik routes fizcultor.ru
  observability/
    prometheus.yaml
    grafana.yaml
    exporters.yaml                   # node + cAdvisor (DaemonSet) + pg + nats
    dashboards/                      # копия из backend/deploy/grafana/dashboards/
    kustomization.yaml
overlays/prod/
  kustomization.yaml                 # image-теги :latest, imagePullPolicy: Always
```

## Подготовка

1. **kubeconfig** уже в корне проекта как `k8s.cluster.yaml` (gitignored).
2. **kubectl** + **kustomize** должны быть в PATH.

## Деплой

```sh
# Применить полный стек:
make -C _/k8s apply

# или вручную:
kubectl --kubeconfig=k8s.cluster.yaml apply -k _/k8s/overlays/prod/
```

## Секреты

Не коммитятся, не генерируются Kustomize. Создаются вручную **один раз**:

```sh
# Вариант 1 — из локального backend/.env (он gitignored):
make -C _/k8s create-secrets

# Вариант 2 — руками:
kubectl -n fizcultor create secret generic fizcultor-secrets \
  --from-literal=JWT_SECRET="$(openssl rand -hex 32)" \
  --from-literal=AES_MASTER_KEY="$(openssl rand -hex 32)" \
  --from-literal=POSTGRES_PASSWORD="$(openssl rand -hex 24)" \
  --from-literal=TG_BOT_TOKEN="<bot-token>" \
  --from-literal=SEMESTER_UUID_BASIC="<uuid>" \
  --from-literal=SEMESTER_UUID_PREPARATORY="<uuid>" \
  --from-literal=SEMESTER_UUID_SPECIAL_MEDICAL="<uuid>" \
  --from-literal=SEMESTER_UUID_AFK="<uuid>" \
  --from-literal=GRAFANA_ADMIN_PASSWORD="$(openssl rand -hex 12)"
```

Обновить уже существующий Secret — `make create-secrets` ещё раз (он
выполняет `apply`, идемпотентен).

## DNS

`fizcultor.ru` → `72.56.93.229` (LoadBalancer IP, выдан TWC). A-record TTL 300.

## Образы

Билдятся GitHub Actions `.github/workflows/deploy-images.yml` при push в `main`:
- `ghcr.io/virsi/fizcultor-bot/auth-svc:latest` (+ `:sha-<short>`, `:main`)
- … 6 других Go-сервисов
- `ghcr.io/virsi/fizcultor-bot/frontend:latest`
- `ghcr.io/virsi/fizcultor-bot/migrations:latest`

Для отката — поменять тэг в `overlays/prod/kustomization.yaml` на `sha-XXXX`.

## Полезные команды

```sh
# Что развёрнуто:
kubectl -n fizcultor get all

# Логи сервиса:
kubectl -n fizcultor logs deploy/gateway-svc --tail=200 -f

# Применить миграции вручную:
kubectl -n fizcultor delete job/migrations
kubectl apply -k _/k8s/overlays/prod/    # пересоздаст Job

# Получить публичный LB IP:
kubectl -n fizcultor get svc traefik -o jsonpath='{.status.loadBalancer.ingress[0].ip}'

# Перезапустить gateway (после push нового образа):
kubectl -n fizcultor rollout restart deploy/gateway-svc

# Открыть Grafana локально (если ещё нет TLS):
kubectl -n fizcultor port-forward svc/grafana 3000:3000
```

## ACME / TLS

Traefik в `fizcultor` ns настроен с certResolver `letsencrypt` (email
egrvrb@gmail.com, TLS-ALPN-01 challenge). Сертификат выдаётся при первом
HTTPS-запросе на `fizcultor.ru`. Хранится в `emptyDir` пода Traefik —
**теряется при рестарте**, перевыпускается автоматически (но 1 раз
попадает в rate-limit LE: 5 дублей/неделя). Для prod — заменить emptyDir
на PVC 100Mi.

## Replicas / масштабирование

- gateway-svc: горизонтально OK, replicas=1 в overlay
- poller-svc, notifier-svc: **только 1 реплика** (стейтфул long-poll)
- auth/bmstu/filter/teachers: горизонтально OK, замораживаем в 1 пока
  кластер single-node
