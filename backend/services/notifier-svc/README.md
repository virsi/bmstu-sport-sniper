# notifier-svc

Доставка алертов пользователю: Telegram (telebot.v3) + NATS publish для
SSE-bridge в gateway-svc.

## Архитектура

```
poller-svc ─gRPC─▶ notifier-svc ─┬─▶ Telegram Bot API (HTML, parse_mode=HTML)
                                  └─▶ NATS subject alerts.<user_id>
                                          └─▶ gateway-svc ─SSE─▶ Browser
```

- `tg_chat_id` юзера получается через `auth-svc.GetMe` (proto уже отдает
  `User.telegram_chat_id`). notifier-svc не хранит своего маппинга.
- Рейтинги преподавателей — `teachers-svc.BatchGet` (с fallback на рейтинг,
  если `MatchedSlot.teacher_rating` уже заполнен).
- `/start <token>` обрабатывается TG-handler-ом и делегируется в
  `auth-svc.LinkTelegramComplete` через bot.LinkCompleter.

## gRPC API

- `NotifyMatched(user_id, slots[], channels?)` — пушит TG + NATS,
  возвращает delivered_by/failed_by/errors per-channel.
- `SendDirect(user_id, text)` — произвольный HTML в TG, опционально в SSE.
- `RegisterTelegramChat(code, chat_id)` — программный аналог `/start <code>`
  для интеграционных тестов.

## Формат TG-сообщения

Портирован из legacy_main.py:209-242 без изменений эмодзи. Один MatchedSlot
рендерится как:

```html
<b>🔥 ДОСТУПНЫ НОВЫЕ СЛОТЫ!</b>

🏟 <b>Аэробика</b>
🗓 Пн | ⏰ 10:00-11:30
📍 ОФП-1
👨‍🏫 Иванов И.И.
⭐️ Рейтинг: <b>4.7</b> (<a href='...'>Studizba</a>)
🟢 Свободно мест: <b>3</b>

<a href='https://lks.bmstu.ru/fv/new-record'><b>✍️ ЗАПИСАТЬСЯ</b></a>
```

Если рейтинг не найден — строка `ℹ️ Рейтинг: <i>не найден</i>`.
Большие batch'и режутся на сообщения ≤4000 символов (с запасом до TG-лимита 4096).

## NATS event format (subject `alerts.<user_id>`)

```json
{
  "slot": {
    "id": "...",
    "week": 1,
    "time": "10:00-11:30",
    "section": "Аэробика",
    "place": "...",
    "teacher_name": "...",
    "teacher_uid": "T-1",
    "vacancy": 3,
    "teacher_rating": 4.7
  },
  "matched_filter_ids": ["..."],
  "sent_at": "2026-06-01T12:00:00Z",
  "channel": "sse"
}
```

## БД

Нет (alert_log хранится в filter_db, если потребуется).

## Env vars

| Var | Default | Описание |
|---|---|---|
| `APP_ENV` | `dev` | dev/prod |
| `SERVICE_NAME` | `notifier-svc` | для логов |
| `GRPC_ADDR` | `:9090` | gRPC listen |
| `HTTP_ADDR` | `:8080` | healthz/readyz |
| `TG_BOT_TOKEN` | (required) | Токен бота от @BotFather |
| `TG_USE_WEBHOOK` | `false` | true → webhook, false → long-poll |
| `TG_WEBHOOK_URL` | `""` | публичный URL (если `TG_USE_WEBHOOK=true`) |
| `NATS_URL` | (required) | для SSE-bridge |
| `AUTH_GRPC_ADDR` | `auth-svc:9090` | для GetMe / LinkTelegramComplete |
| `TEACHERS_GRPC_ADDR` | `teachers-svc:9090` | для BatchGet рейтингов |

## Локальный запуск

```sh
export TG_BOT_TOKEN="..." NATS_URL="nats://localhost:4222"
export AUTH_GRPC_ADDR=":9001" TEACHERS_GRPC_ADDR=":9002"
go run ./cmd/server
```

## Тесты

- `internal/format` — табличные тесты рендера TG (с/без рейтинга, экранирование,
  splitting в batch'е).
- `internal/server` — gRPC сервер на моках auth/teachers/sender/publisher.
- Реальный TG-бот в тестах НЕ запускается (Sender — интерфейс).
