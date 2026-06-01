// Package events — тонкая обёртка над nats.go для publish/subscribe и
// JetStream-поддержки. Используется для алертов (alerts.<userID>), событий
// изменения слотов (slots.updated) и невалидных кредов (creds.invalid).
package events

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
)

// Bus — обёртка над nats.Conn с удобными методами Publish/Subscribe.
type Bus struct {
	nc *nats.Conn
}

// Config — параметры подключения к NATS.
type Config struct {
	// URL — адрес сервера, например nats://nats:4222 (может быть список через запятую).
	URL string
	// Name — имя клиента для мониторинга в nats-server.
	Name string
	// MaxReconnects — кол-во попыток reconnect. <0 → бесконечно.
	MaxReconnects int
	// ReconnectWait — пауза между попытками reconnect.
	ReconnectWait time.Duration
	// ConnectTimeout — таймаут на initial connect.
	ConnectTimeout time.Duration
}

// Connect подключается к NATS с retry-логикой.
func Connect(_ context.Context, cfg Config) (*Bus, error) {
	if cfg.URL == "" {
		return nil, errors.New("events: empty NATS URL")
	}
	if cfg.ReconnectWait <= 0 {
		cfg.ReconnectWait = 2 * time.Second
	}
	if cfg.MaxReconnects == 0 {
		cfg.MaxReconnects = -1 // infinite
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 5 * time.Second
	}
	opts := []nats.Option{
		nats.Name(cfg.Name),
		nats.MaxReconnects(cfg.MaxReconnects),
		nats.ReconnectWait(cfg.ReconnectWait),
		nats.Timeout(cfg.ConnectTimeout),
	}
	nc, err := nats.Connect(cfg.URL, opts...)
	if err != nil {
		return nil, fmt.Errorf("events: nats connect: %w", err)
	}
	return &Bus{nc: nc}, nil
}

// Close корректно закрывает соединение с дрейном in-flight сообщений.
func (b *Bus) Close() {
	if b == nil || b.nc == nil {
		return
	}
	_ = b.nc.Drain()
}

// Conn возвращает нижележащее nats.Conn для редких случаев (JetStream и пр.).
func (b *Bus) Conn() *nats.Conn { return b.nc }

// Publish публикует raw-байты в subject. Не дожидается ack от сервера.
func (b *Bus) Publish(subject string, data []byte) error {
	if err := b.nc.Publish(subject, data); err != nil {
		return fmt.Errorf("events: publish %s: %w", subject, err)
	}
	return nil
}

// Handler — callback подписки. data — payload сообщения.
type Handler func(ctx context.Context, subject string, data []byte) error

// Subscribe регистрирует асинхронную подписку. Возвращает Subscription для
// последующего Unsubscribe. Ошибки handler логируются через slog.Default().
func (b *Bus) Subscribe(subject string, h Handler) (*nats.Subscription, error) {
	sub, err := b.nc.Subscribe(subject, func(msg *nats.Msg) {
		_ = h(context.Background(), msg.Subject, msg.Data)
	})
	if err != nil {
		return nil, fmt.Errorf("events: subscribe %s: %w", subject, err)
	}
	return sub, nil
}

// SubscribeQueue — групповая подписка (work-queue semantics).
// Сообщение получает только один из подписчиков с одинаковым queue.
func (b *Bus) SubscribeQueue(subject, queue string, h Handler) (*nats.Subscription, error) {
	sub, err := b.nc.QueueSubscribe(subject, queue, func(msg *nats.Msg) {
		_ = h(context.Background(), msg.Subject, msg.Data)
	})
	if err != nil {
		return nil, fmt.Errorf("events: queue-subscribe %s: %w", subject, err)
	}
	return sub, nil
}
