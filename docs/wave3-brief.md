# Wave 3 Integration Brief

Wave 2 закрыт. 7 сервисов: build OK, 148 тестов pass, golangci-lint clean per-svc.

## Контракты установлены (использовать)

- **gRPC metadata user-id**: ключ `x-user-id` (lowercase). Use `pkg/grpcx.WithUserID(ctx, userID)`.
- **gRPC dial**: `pkg/grpcx.DialInsecure(ctx, addr, opts...)`.
- **gRPC error mapping** (см. `docs/api.md`):
  - `InvalidArgument` → 400
  - `Unauthenticated` → 401
  - `NotFound` → 404
  - `AlreadyExists` → 409
  - `FailedPrecondition` → 422 (юзер должен что-то сделать)
  - `ResourceExhausted` → 429
  - `Unavailable` → 503
  - `Internal` → 500
- **User.id**: `string` (auth-svc strconv-форматирует BIGSERIAL). Gateway пробрасывает как есть.
- **NATS subject**: `alerts.<user_id>`, JSON payload `{"slot": Slot, "sent_at": ISO8601, "channel": "telegram"|"sse"}`.
- **Telegram deeplink**: auth-svc возвращает `tg://start?token=<code>`. Gateway переписывает на `https://t.me/${BOT_USERNAME}?start=<code>`.

## Открытые вопросы (для V2)

1. **`ListActiveUsers` RPC** — нет в `filter/v1`. Poller сейчас использует env-stub `POLL_USER_IDS`. Wave 4/5: добавить proto + RPC.
2. **Rating enrichment**: `filter.MatchSlots` сейчас не вызывает teachers. Либо poller обогащает и передаёт `map<uid, rating>` в Match, либо filter-svc держит teachers gRPC client. **Сейчас**: фильтры с `min_rating > 0` не пройдут (filter-svc не знает рейтинг). Можно временно дисабл `min_rating` в UI с пометкой «coming soon».
3. **`alert_log.channel`** хардкод `"telegram"`. Wave 4: notifier возвращает delivered channels, MarkSeen принимает их.
4. **`Filter.section`/`teacher_uid`** — proto singular (`optional string`). Frontend парсит CSV. Wave 4: миграция на `repeated string` (breaking).
5. **`tg_link_token` TTL** не enforced в БД. Wave 4: добавить `expires_at` колонку.
6. **GC `known_slots`** — нет автомат. Wave 4: cron-задача.
7. **JetStream durability** для `alerts.*` — Wave 4 если нужно offline replay.

## Не трогать в Wave 3 (KISS)

- Авто-запись — V2
- Rating-фильтр — отключить в UI
- Multi-language i18n
- Web Push
