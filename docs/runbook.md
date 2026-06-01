# Runbook — fizcultor-bot

Операционный гайд для on-call инженера.

## TL;DR

| Что | Где |
|---|---|
| Логи всех сервисов | `docker compose -f backend/deploy/docker-compose.prod.yaml logs -f <svc>` |
| Метрики | Grafana dashboard `fizcultor-overview` (https://<DOMAIN>/grafana/) |
| Prometheus UI | внутри сети, `kubectl port-forward` или `docker exec` |
| Healthchecks | `https://<DOMAIN>/healthz` / `/readyz` (наружу) |
| Deploy | push тэг `v*` в main → GitHub Actions release.yml |
| Rollback | `docker compose pull <svc>` с указанием предыдущего тэга + restart |

## Содержание

1. [Pre-deploy checklist](#pre-deploy-checklist)
2. [Production deploy](#production-deploy)
3. [Rollback](#rollback)
4. [Secrets generation](#secrets-generation)
5. [Backup и restore](#backup-и-restore)
6. [Key rotation](#key-rotation)
7. [Диагностика инцидентов](#диагностика-инцидентов)
8. [On-call cheatsheet](#on-call-cheatsheet)

---

## Pre-deploy checklist

Перед первым продом:

- [ ] Куплен домен, A-запись указывает на сервер.
- [ ] Открыты порты 80 (для ACME http-01) и 443 (HTTPS).
- [ ] Создан `.env.prod` (см. `backend/deploy/.env.prod.example`), все секреты сгенерированы.
- [ ] Получен `TG_BOT_TOKEN` от `@BotFather`.
- [ ] Запушены последние миграции через `make migrate-up` либо они применяются при первом старте сервиса (см. `backend/migrations/`).
- [ ] Bot `webhook` настроен на `https://<DOMAIN>/api/tg/webhook` (если `TG_USE_WEBHOOK=true`).
- [ ] Резервная копия Postgres настроена (см. ниже).

## Production deploy

### Первый запуск

```bash
# 1. Сгенерировать секреты и заполнить .env.prod
cp backend/deploy/.env.prod.example backend/.env.prod
$EDITOR backend/.env.prod

# 2. Пулл всех образов (или собрать локально --build)
cd backend/deploy
docker compose -f docker-compose.prod.yaml --env-file ../.env.prod pull

# 3. Применить миграции (можно повторно, идемпотентно)
docker compose -f docker-compose.prod.yaml --env-file ../.env.prod \
    run --rm postgres psql -h postgres -U fizcultor -d postgres -c "SELECT 1"
make -C ../ migrate-up POSTGRES_DSN_BASE=postgres://fizcultor:<password>@localhost:5432

# 4. Старт
docker compose -f docker-compose.prod.yaml --env-file ../.env.prod up -d

# 5. Проверить
docker compose -f docker-compose.prod.yaml ps
curl -fsS https://<DOMAIN>/healthz
```

### Обновление (после релиза через CI)

CI (`.github/workflows/release.yml`) при push тэга `v*`:

1. Собирает и пушит образы в `ghcr.io/<owner>/fizcultor-<svc>:<tag>`.
2. Создаёт GitHub Release с changelog'ом.

На production-хосте:

```bash
# Указываем VERSION в .env.prod либо передаём inline:
export VERSION=v1.2.3
docker compose -f backend/deploy/docker-compose.prod.yaml --env-file backend/.env.prod pull
docker compose -f backend/deploy/docker-compose.prod.yaml --env-file backend/.env.prod up -d
```

Compose дроп-в-replace выкатит новые контейнеры **поочерёдно** (за счёт `depends_on` + healthchecks). До передачи трафика gateway-svc контейнер не запустится, пока gRPC-cвязки готовы.

### Канарейка / blue-green

Сценарий с двумя compose-стэками (`docker-compose.prod.yaml` для blue, `docker-compose.prod.green.yaml` для green) на разных портах + переключение Caddy upstream — out-of-scope wave 5. Сейчас deploy — rolling через compose `up -d`.

## Rollback

Если новый релиз сломал прод:

```bash
# 1. Откатить версию в .env.prod
export VERSION=v1.2.2

# 2. Pull старых образов
docker compose -f backend/deploy/docker-compose.prod.yaml --env-file backend/.env.prod pull

# 3. Применить
docker compose -f backend/deploy/docker-compose.prod.yaml --env-file backend/.env.prod up -d

# 4. Проверить
curl -fsS https://<DOMAIN>/readyz
```

Если откат не помогает (миграция несовместима с предыдущей версией):

```bash
# Применить down-миграцию вручную:
goose -dir backend/migrations/<db> postgres "$DSN" down

# После down-миграции — снова rollback контейнеров.
```

**Никогда не делай `docker compose down -v`** — это удалит `postgres-data` и `nats-data` volume'ы (потеря всех данных).

## Secrets generation

| Переменная | Команда |
|---|---|
| `JWT_SECRET` | `openssl rand -base64 48 \| tr -d '\n'` |
| `AES_MASTER_KEY` | `openssl rand -hex 32` (ровно 64 hex-символа) |
| `POSTGRES_PASSWORD` | `openssl rand -base64 24 \| tr -d '\n/+='` |
| `GRAFANA_ADMIN_PASSWORD` | `openssl rand -base64 24 \| tr -d '\n/+='` |
| `TG_BOT_TOKEN` | через `@BotFather` в Telegram |

Все секреты хранятся в `backend/.env.prod` — этот файл **не коммитим**. Для multi-host setup используй Vault / 1Password CLI / Doppler.

## Backup и restore

### Daily Postgres backup

Cron-задача (на хосте, не в контейнере):

```bash
# /etc/cron.d/fizcultor-pg-backup — запускается ежедневно в 03:30
30 3 * * * fizcultor-ops \
  docker exec fizcultor-prod-postgres-1 \
    pg_dumpall -U fizcultor | \
    gzip > /var/backups/fizcultor/$(date +\%Y-\%m-\%d).sql.gz && \
  find /var/backups/fizcultor -name "*.sql.gz" -mtime +30 -delete
```

Альтернатива через `docker compose`-задачу (без cron):

```bash
docker exec -t $(docker compose -f backend/deploy/docker-compose.prod.yaml ps -q postgres) \
  pg_dumpall -U fizcultor | gzip > backup-$(date +%Y-%m-%d).sql.gz
```

### Restore

```bash
# 1. Остановить сервисы (но не postgres)
docker compose -f backend/deploy/docker-compose.prod.yaml \
  stop auth-svc bmstu-svc filter-svc gateway-svc notifier-svc poller-svc teachers-svc

# 2. Восстановить
gunzip -c backup-2026-06-01.sql.gz | \
  docker exec -i $(docker compose ps -q postgres) psql -U fizcultor postgres

# 3. Запустить сервисы
docker compose -f backend/deploy/docker-compose.prod.yaml up -d
```

### NATS JetStream backup

JetStream хранит state в `nats-data` volume. Снапшот:

```bash
docker run --rm \
  -v fizcultor-prod_nats-data:/data:ro \
  -v $(pwd):/backup \
  alpine tar czf /backup/nats-$(date +%Y-%m-%d).tar.gz -C /data .
```

Restore — обратная операция с остановленным `nats` контейнером.

## Key rotation

### JWT secret rotation

Сценарий: подозрение на компрометацию `JWT_SECRET`.

1. Сгенерировать новый секрет (`openssl rand -base64 48`).
2. Обновить `JWT_SECRET` в `.env.prod`.
3. Перезапустить **`auth-svc` и `gateway-svc` одновременно** (оба читают секрет):
   ```bash
   docker compose -f backend/deploy/docker-compose.prod.yaml restart auth-svc gateway-svc
   ```
4. Все существующие access/refresh токены инвалидируются → пользователи должны заново залогиниться.
5. Если нужно сохранить активные сессии — реализуй dual-key rotation (текущее и предыдущее) в `pkg/jwtx` (out-of-scope wave 5).

### Refresh-cookie rotation (incident response)

Сценарий: подозрение на массовую утечку refresh-токенов (например, XSS на стороннем CDN, который потенциально мог exfiltrate cookie до того, как мы переехали на `HttpOnly`).

Refresh живёт в httpOnly cookie `rt` (см. `docs/api.md` секция 1). Cookie не виден из JS, но если *база* refresh-токенов скомпрометирована — нужно revoke-нуть всё одним движением.

1. **Глобальный revoke в auth-svc:** напрямую в БД (helper RPC out-of-scope wave 5):
   ```sql
   -- В БД auth-svc:
   UPDATE refresh_tokens SET revoked_at = NOW() WHERE revoked_at IS NULL;
   ```
2. **Если нужно дополнительно поменять имя cookie** (заставить браузеры выкинуть старую):
   - Поменять константу `RefreshCookieName` в `backend/services/gateway-svc/internal/http/handler/cookies.go`.
   - Rebuild + redeploy gateway-svc.
   - Старая cookie остаётся в браузерах до своего TTL (30 дней), но проигнорирована бэком.
3. **Все пользователи получат 401 на следующем запросе** → axios-interceptor дёрнет `/auth/refresh`, получит 401, redirect на `/login`.
4. **Мониторить `auth_failures_total{reason="invalid_refresh"}` спайки** — это норма в первые часы после revoke.

### Cookie/AES master key rotation (`AES_MASTER_KEY`)

`AES_MASTER_KEY` шифрует BMSTU-credentials и LKS-session cookies в БД (`bmstu_credentials.enc_*` и `bmstu_sessions.session_blob`). Ротация требует re-encrypt всех записей.

Алгоритм (полу-ручной; helper utility не реализован в wave 5):

1. **Подготовить новый ключ:** `NEW_KEY=$(openssl rand -hex 32)`.
2. **Остановить `bmstu-svc` и `poller-svc`** (чтобы избежать гонок при перешифровке).
3. **Запустить миграционный helper** (TODO: реализовать в `cmd/rotate-aes/`):
   ```
   docker run --rm \
     -e POSTGRES_DSN="..." \
     -e OLD_KEY=<current AES_MASTER_KEY> \
     -e NEW_KEY=$NEW_KEY \
     ghcr.io/<owner>/fizcultor-bmstu-svc:<version> \
     /server --rotate-aes
   ```
   Helper читает все записи `bmstu_credentials` и `bmstu_sessions`, дешифрует OLD_KEY, шифрует NEW_KEY, апдейтит.
4. **Обновить `AES_MASTER_KEY` в `.env.prod`** на `$NEW_KEY`.
5. **Запустить сервисы:**
   ```bash
   docker compose -f backend/deploy/docker-compose.prod.yaml up -d
   ```
6. **Проверить:** smoke-тест — залогиниться, посмотреть `/api/bmstu/status` (должен вернуть `linked` без ошибок).

Если helper ещё не написан — alternative path: попросить пользователей re-link через `/api/bmstu/creds`, при этом сервис при логине пересохранит креды с новым ключом. Это менее транспарентно для юзера.

### Postgres password rotation

```bash
# 1. Залогиниться в Postgres под суперюзером
docker exec -it fizcultor-prod-postgres-1 psql -U fizcultor postgres

# 2. ALTER USER fizcultor WITH PASSWORD 'new-strong-password';
# 3. Обновить POSTGRES_PASSWORD в .env.prod
# 4. Перезапустить ВСЕ сервисы, использующие БД:
docker compose -f backend/deploy/docker-compose.prod.yaml restart \
  auth-svc bmstu-svc filter-svc teachers-svc
```

### TG bot token rotation

1. `@BotFather` → `/revoke` старого токена → `/newtoken` для бота.
2. Обновить `TG_BOT_TOKEN` в `.env.prod`.
3. `docker compose restart notifier-svc`.
4. Если использовался webhook — заново setWebhook (notifier-svc делает это при старте).

## Диагностика инцидентов

### 503 на /readyz одного сервиса

```bash
# Тело /readyz содержит причину
curl -i https://<DOMAIN>/readyz
# В логах:
docker compose -f backend/deploy/docker-compose.prod.yaml logs --tail 50 <svc>
```

Типичные причины:
- `dep postgres: ...` → проверь `docker compose logs postgres`, нет ли OOM kill / disk full.
- `dep nats: not connected` → `docker compose logs nats`, проверь `nats-data` volume не переполнен.

### Высокая latency (p95)

1. Открой Grafana dashboard `fizcultor-overview` → panel "gRPC latency p95".
2. Если выделяется один сервис — посмотри его лог + DB queries panel.
3. `docker stats` — проверь CPU/memory.

### Telegram уведомления не приходят

```bash
# 1. Логи notifier-svc
docker compose logs --tail 100 notifier-svc | grep -i telegram

# 2. Метрика sent_total — растёт ли счётчик
# В Prometheus: rate(notifier_sent_total{result="ok"}[5m])

# 3. Webhook health (если webhook mode)
curl https://api.telegram.org/bot<TOKEN>/getWebhookInfo
```

Если `last_error_message` в `getWebhookInfo` показывает 502/timeout — проблема с обратной связью Caddy → notifier-svc. Проверь `notifier-svc` `/readyz`.

### BMSTU OIDC сломался (логин не работает)

```bash
# Логи bmstu-svc — ищи "oidc: ..."
docker compose logs --tail 200 bmstu-svc | grep oidc

# Если LKS изменили auth-flow — bmstu-svc.internal.oidc нужно обновить.
# Workaround: OIDC_USE_BROWSER=true (chromedp fallback), но требует Chrome в image.
```

## On-call cheatsheet

```bash
# Статус всех сервисов
docker compose -f backend/deploy/docker-compose.prod.yaml ps

# Все логи (последние 5 минут)
docker compose -f backend/deploy/docker-compose.prod.yaml logs --since 5m

# Один сервис, follow
docker compose -f backend/deploy/docker-compose.prod.yaml logs -f gateway-svc

# Restart одного сервиса (без affect другим)
docker compose -f backend/deploy/docker-compose.prod.yaml restart bmstu-svc

# Полная остановка (НЕ удаляет volume)
docker compose -f backend/deploy/docker-compose.prod.yaml stop

# Полный teardown БЕЗ удаления данных
docker compose -f backend/deploy/docker-compose.prod.yaml down

# Полный teardown С удалением volume'ов (ОПАСНО — потеря данных)
docker compose -f backend/deploy/docker-compose.prod.yaml down -v   # НЕ ДЕЛАЙ В ПРОДЕ

# Применить миграции вручную
make -C backend migrate-up POSTGRES_DSN_BASE=postgres://...

# Запустить psql в postgres-контейнере
docker compose -f backend/deploy/docker-compose.prod.yaml exec postgres \
  psql -U fizcultor -d auth_db

# Тушить старые backup-файлы (>30 дней)
find /var/backups/fizcultor -name "*.sql.gz" -mtime +30 -delete
```

### Полезные Prometheus-queries (для on-call в Grafana Explore)

```promql
# Сервисы up (должно быть 7)
count(up{job="fizcultor-services"} == 1)

# Error rate всех сервисов (gRPC code != OK)
sum by (service) (rate({__name__=~".+_grpc_requests_total", code!="OK"}[5m]))

# p99 latency всех сервисов
histogram_quantile(0.99, sum by (le, service) (rate({__name__=~".+_grpc_request_duration_seconds_bucket"}[5m])))

# Сколько активных SSE-подключений
gateway_sse_connections

# Goroutine leak detector
go_goroutines > 1000
```

Куда смотреть после инцидента: `docs/architecture.md` для понимания зависимостей сервисов.
