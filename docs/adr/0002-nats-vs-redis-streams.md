# ADR 0002 — NATS JetStream vs Redis Streams для алёртов

**Дата:** 2026-06-02
**Статус:** Accepted

## Контекст

Нужен брокер сообщений для:
1. **Алёрт fan-out**: `notifier-svc` публикует событие, gateway-svc-инстансы
   подписываются и пушат в SSE подключённых браузеров.
2. **Bridging Telegram ↔ web**: пользователь привязал TG в одном tab, нужно
   обновить статус в другом tab без перезагрузки.
3. (V2) **Offline buffering**: если юзер был оффлайн, при подключении SSE
   получить пропущенные алёрты.

Требования:
- Topic per user (`alerts.<user_id>`) — высокая кардинальность subject'ов.
- Persistence on-disk (durable) для V2 offline buffer.
- Минимум RAM (целимся в ≤100 MB на всю систему).
- Single-binary в dev (запуск без k8s).
- Go-клиент production-ready.

## Решение

Выбираем **NATS JetStream**.

## Обоснование

| Критерий | NATS JetStream | Redis Streams | Kafka |
|---|---|---|---|
| Single-binary в dev | Да, ~15 MB | Да, ~6 MB | Нет, нужен ZK/KRaft, ~500 MB+ |
| Wildcards subject (alerts.*) | Да, нативно | Нет, нужно знать ключ или KEYS-скан | Да (через consumer groups) |
| Persistence | Да, file/memory | Да (AOF/RDB) | Да |
| Latency p99 | < 5ms | < 2ms | 5–20ms |
| RAM idle | ~20 MB | ~10 MB | 200+ MB JVM |
| Native fan-out | Да | Нужны consumer groups вручную | Да |
| Lightweight Go client | nats.go, очень зрелый | go-redis, тоже хорош | sarama/kgo, тяжелее |
| Управление subject'ами per-user | Естественно (subject = key) | Через streams + consumer groups, неудобно | Через partitions, требует ребаланса |

Решающий фактор — **subject-per-user pattern**. У нас тысячи пользователей,
и нам нужно подписать gateway-svc на `alerts.<его_user_id>` для каждого
SSE-коннекта. В NATS это однострочник `nc.Subscribe("alerts.UUID", ...)`.
В Redis Streams пришлось бы либо вести один общий stream и фильтровать на
клиенте (тратит CPU), либо порождать stream per user (микро-объекты в Redis,
плохой паттерн).

Дополнительно:
- NATS Core (без JetStream) даёт fan-out в RAM с минимальной задержкой,
  включаем JetStream только для тех subject'ов, где нужен durable
  (`alerts.>` с retention 24h для V2 offline replay).
- Удобный operator-режим (NATS Operator JWT) для multi-tenant в будущем.

## Цена

- NATS меньше «модный» чем Kafka — меньше специалистов на рынке. Принимаем,
  команда быстро освоит за неделю.
- JetStream «новее» чем Streams (Streams с Redis 5.0, JetStream с NATS 2.2).
  Оба production-ready на 2026.

## Альтернативы

**Redis Streams** отвергли из-за плохого fit для subject-per-user.

**Kafka** отвергли:
- Тяжёлый в dev (даже с KRaft без ZK — 500+ MB RAM, отдельный процесс).
- Партиции — не subject-per-user; для алёртов кому-то одному пришлось бы
  ставить ключ-партицию по user_id, и при rebalance ловить лаг.
- Overkill для нашего объёма (≤ 10k событий/сек на пике).

**RabbitMQ** отвергли — между Kafka и NATS по сложности ближе к Kafka,
без явных преимуществ перед NATS для этого юзкейса.

## Последствия

- Развёртывание: `nats:2.10-alpine` в docker-compose, один порт `:4222`.
- В коде: общий wrapper в `backend/pkg/events/` — `Publish(subj, data)` и
  `Subscribe(subj, handler) -> unsubscribe`. Не утекать `*nats.Conn` в бизнес-логику.
- Конфиг JetStream stream `ALERTS`:
  - subjects: `alerts.>`
  - retention: WorkQueue (consumer ack-it → delete) для core flow,
    или Limits (24h) для V2 offline replay.
- Tests: testcontainers `nats:2.10-alpine` в integration тестах.

## Связанные

- [0001 — Microservices vs monolith](./0001-microservices-vs-monolith.md)
