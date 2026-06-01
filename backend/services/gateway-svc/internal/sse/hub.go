// Package sse — мост NATS → Server-Sent Events.
//
// Hub управляет подписками per-connection: каждый /api/stream-запрос
// получает свою nats.Subscription на subject alerts.<user_id>, читает
// сообщения и пишет их в выделенный канал. При закрытии соединения —
// Unsubscribe + close канала, чтобы не утекали горутины и subject-buffers.
//
// Subject формат и payload — см. wave3-brief: subject `alerts.<user_id>`,
// payload JSON {"slot": Slot, "sent_at": ISO8601, "channel": "..."}.
package sse

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/nats-io/nats.go"
)

// SubjectPrefix — префикс NATS subject'а для алёртов конкретного юзера.
// Полный subject: `alerts.<user_id>`.
const SubjectPrefix = "alerts."

// defaultChanBuffer — буфер канала событий per-connection.
// 16 покрывает burst из poller-svc (≤ N matched slots сразу), при overflow
// сообщения дропаются с логом — мы не блокируем горутину reader'а NATS.
const defaultChanBuffer = 16

// NatsSubscriber — узкий интерфейс над nats.Conn (Subscribe). Сужен от
// nats.Conn чтобы упростить мок в тестах Hub.
type NatsSubscriber interface {
	// Subscribe регистрирует подписку с callback'ом, возвращает Subscription.
	Subscribe(subject string, cb nats.MsgHandler) (*nats.Subscription, error)
}

// Hub держит ссылку на NATS-соединение и параметры Subscribe.
//
// Один Hub на весь сервис (создаётся в main), потокобезопасен.
// Сам по себе состояния per-connection не держит — всё в возвращаемых каналах.
type Hub struct {
	nc      NatsSubscriber
	chanBuf int
}

// New создаёт Hub.
//
// chanBuf задаёт буфер каждого per-connection-канала (0 → default 16).
func New(nc NatsSubscriber, chanBuf int) *Hub {
	if chanBuf <= 0 {
		chanBuf = defaultChanBuffer
	}
	return &Hub{nc: nc, chanBuf: chanBuf}
}

// ErrEmptyUserID — попытка Subscribe без user_id, программерская ошибка caller'а.
var ErrEmptyUserID = errors.New("sse.Hub: empty userID")

// Subscribe регистрирует подписку на NATS subject `alerts.<userID>`.
//
// Возвращает:
//   - канал (<-chan []byte) с raw JSON-payload'ами сообщений (что пришло из NATS).
//   - cleanup func: вызвать на disconnect клиента или ctx.Done().
//     Идемпотентен (повторный вызов безопасен).
//   - ошибку (только при неверном userID или сбое nats.Subscribe).
//
// Контракт жизни:
//
//	ch, cleanup, err := hub.Subscribe(ctx, userID)
//	defer cleanup()
//	for { select { case msg := <-ch: ...; case <-ctx.Done(): return } }
//
// Buffer canal: при переполнении сообщение дропается + warn-лог. Это плата
// за non-blocking семантику — иначе NATS-горутина залипнет на slow consumer.
func (h *Hub) Subscribe(ctx context.Context, userID string) (events <-chan []byte, cleanup func(), err error) {
	if userID == "" {
		return nil, func() {}, ErrEmptyUserID
	}
	subject := SubjectPrefix + userID
	ch := make(chan []byte, h.chanBuf)

	sub, subErr := h.nc.Subscribe(subject, func(msg *nats.Msg) {
		// Защита от блокировки: если ch full — дропаем сообщение, логируем.
		// Альтернатива — блокировать NATS internal горутину, что хуже:
		// другие подписки тоже встанут.
		select {
		case ch <- msg.Data:
		default:
			slog.Warn("sse: slow consumer, dropping message",
				slog.String("subject", subject),
				slog.Int("dropped_bytes", len(msg.Data)),
			)
		}
	})
	if subErr != nil {
		close(ch)
		return nil, func() {}, fmt.Errorf("sse: subscribe %s: %w", subject, subErr)
	}

	// closed guard, чтобы повторный cleanup был безопасен.
	closed := make(chan struct{})
	doCleanup := func() {
		select {
		case <-closed:
			return
		default:
		}
		close(closed)
		// sub может быть nil в тестах с мок-NatsSubscriber'ом
		// (реальный nats.Conn никогда не возвращает (nil, nil)).
		if sub != nil {
			if unsubErr := sub.Unsubscribe(); unsubErr != nil {
				slog.Warn("sse: unsubscribe",
					slog.String("subject", subject),
					slog.Any("error", unsubErr),
				)
			}
		}
		close(ch)
	}

	// Авто-очистка по ctx.Done — на случай, если caller забыл defer.
	go func() {
		select {
		case <-ctx.Done():
			doCleanup()
		case <-closed:
		}
	}()

	return ch, doCleanup, nil
}
