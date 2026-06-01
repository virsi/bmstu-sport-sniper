# ADR 0001 — Микросервисы vs монолит

**Дата:** 2026-06-02
**Статус:** Accepted
**Контекст:** переписываем bmstu-sport-sniper с Python-монолита на Go.

## Контекст

Текущая Python-версия — однопроцессный монолит `main.py`. Это работает на
single-user MVP, но мы переходим в:
- Multi-user сайт с регистрацией, фильтрами на юзера, web UI.
- Замена Selenium на pure HTTP Keycloak — сильно меняет шаблон работы со
  «внешним вендором» (cookiejar persist, retry, encryption).
- Цель: ≥1000 одновременных юзеров, p95 REST < 50ms, доставка алёрта < 3s
  от обнаружения слота в LKS.

Варианты:
1. **Go monolith** — один бинарь, один процесс, всё в pkg/.
2. **Modular monolith** — один бинарь, чёткие пакеты-модули, явные границы
   через интерфейсы внутри процесса.
3. **Microservices** — 7 отдельных сервисов, gRPC + NATS, database-per-service.

## Решение

Выбираем **microservices**.

## Обоснование

Хотя сложность развёртывания растёт, конкретно эти сервисы имеют
радикально разные операционные профили, и это даёт реальный выигрыш:

| Сервис | Профиль | Почему отдельный |
|---|---|---|
| `bmstu-svc` | I/O bound, cookiejar в памяти, риск ban от LKS | Изолируем blast radius при rate-limit от BMSTU; можем горизонтально масштабировать с sticky cookies (V2). |
| `poller-svc` | Ровно 1 instance (singleton ticker) | Несовместим с горизонтальным масштабированием stateless-сервисов; если в монолите — придётся ставить распределённый лок. |
| `notifier-svc` | TG long-poll держит постоянное HTTP-соединение | Перезапуск не должен ронять SSE-хаб или auth. |
| `gateway-svc` | Stateful SSE-хаб + JWT validation hot path | Можно скейлить независимо при росте онлайнов. |
| `auth-svc`, `filter-svc`, `teachers-svc` | CRUD на Postgres, stateless | Можно скейлить независимо. |

Кроме операционных причин:
- **Чёткие контракты proto** заставляют команду явно проектировать границы.
  В modular monolith эти границы постепенно эрозируют.
- **Database-per-service** избавляет от cross-domain транзакций и
  предотвращает анти-паттерн «один большой SQL JOIN через всю систему».
- **Постепенный rewrite** возможен: можно сначала вынести `bmstu-svc` за
  Python-фасад и проверить parity, потом дописать остальное.

## Цена

Принимаем издержки:
- Сложность local dev → решается docker-compose + Makefile.
- Distributed tracing → решается OTel (V2), пока — request-id correlation.
- Сетевой overhead gRPC → внутри docker network < 1ms, не проблема.
- Code reuse → решается `backend/pkg/` для общего (logger, crypto, jwtx).

## Альтернативы

**Modular monolith** был бы дешевле в dev, но:
- poller-singleton в одном процессе с gateway — рискованно (panic в poller роняет API).
- Невозможно скейлить независимо при росте онлайнов.
- Границы пакетов в Go легко нарушить (нет физической изоляции).

**Полный monolith** не рассматриваем — слишком монолитная исходная Python-версия породила баги типа stale-cache.

## Последствия

- Нужна dev-инфра (docker-compose, Makefile, sqlc per-svc).
- Каждый сервис имеет свой README, свои env, свои миграции.
- Поломка контракта в proto — coordinated change (но это feature, не bug).
- Можно безопасно переписать один сервис без касания остальных.

## Связанные

- [0002 — NATS vs Redis Streams](./0002-nats-vs-redis-streams.md)
- [0004 — Database per service](./0004-database-per-service.md)
