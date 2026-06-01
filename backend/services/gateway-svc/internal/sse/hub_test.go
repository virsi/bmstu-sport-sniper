package sse_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fizcultor/backend/services/gateway-svc/internal/sse"
)

// fakeSubscriber — мок NatsSubscriber, эмулирует поведение nats.Conn.Subscribe
// без поднятия реального NATS-сервера. Сохраняет последнюю handler-функцию,
// чтобы тест мог дергать её напрямую (== «сервер отправил сообщение»).
type fakeSubscriber struct {
	subscribed      string
	cb              nats.MsgHandler
	subscribeErr    error
	subscribeCalled bool
}

func (f *fakeSubscriber) Subscribe(subject string, cb nats.MsgHandler) (*nats.Subscription, error) {
	f.subscribeCalled = true
	f.subscribed = subject
	f.cb = cb
	if f.subscribeErr != nil {
		return nil, f.subscribeErr
	}
	// Реальный nats.Subscription — приватный type без exported конструктора.
	// Возвращаем nil; в Hub.Subscribe мы вызываем sub.Unsubscribe(), которая
	// для nil-Subscription корректно вернёт ErrBadSubscription. Покрываем
	// это отдельным тестом ниже (sub-error не валит cleanup).
	return nil, nil
}

func TestHub_Subscribe_EmptyUserID(t *testing.T) {
	t.Parallel()

	hub := sse.New(&fakeSubscriber{}, 0)
	ch, cleanup, err := hub.Subscribe(context.Background(), "")
	assert.Nil(t, ch)
	assert.NotNil(t, cleanup) // defer-friendly
	cleanup()                 // должно быть no-op без паники
	assert.ErrorIs(t, err, sse.ErrEmptyUserID)
}

func TestHub_Subscribe_ProducesSubject(t *testing.T) {
	t.Parallel()

	fs := &fakeSubscriber{}
	hub := sse.New(fs, 0)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, cleanup, err := hub.Subscribe(ctx, "user-123")
	require.NoError(t, err)
	defer cleanup()

	assert.True(t, fs.subscribeCalled)
	assert.Equal(t, "alerts.user-123", fs.subscribed)
	assert.NotNil(t, ch)
}

func TestHub_Subscribe_DeliversMessages(t *testing.T) {
	t.Parallel()

	fs := &fakeSubscriber{}
	hub := sse.New(fs, 4)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, cleanup, err := hub.Subscribe(ctx, "u1")
	require.NoError(t, err)
	defer cleanup()

	// «NATS push» — вызываем cb напрямую.
	payload := []byte(`{"slot": {}, "sent_at": "2026-06-02T10:00:00Z", "channel": "sse"}`)
	fs.cb(&nats.Msg{Subject: "alerts.u1", Data: payload})

	select {
	case got := <-ch:
		assert.Equal(t, payload, got)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for message")
	}
}

func TestHub_Subscribe_SlowConsumerDropsMessages(t *testing.T) {
	t.Parallel()

	fs := &fakeSubscriber{}
	hub := sse.New(fs, 1) // тесная буферизация

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, cleanup, err := hub.Subscribe(ctx, "u-slow")
	require.NoError(t, err)
	defer cleanup()

	// Заливаем 5 сообщений подряд, никто не читает.
	// Первое влезет в буфер, остальные дропнутся без блокировки.
	for i := 0; i < 5; i++ {
		fs.cb(&nats.Msg{Subject: "alerts.u-slow", Data: []byte("x")})
	}
	// Если бы select{} в Subscribe был блокирующим — этот тест бы зависал
	// или нам пришлось бы делать timeout. Дошли сюда без зависания = ок.
}

func TestHub_Subscribe_CleanupIsIdempotent(t *testing.T) {
	t.Parallel()

	fs := &fakeSubscriber{}
	hub := sse.New(fs, 0)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, cleanup, err := hub.Subscribe(ctx, "u-idem")
	require.NoError(t, err)

	// Двойной вызов cleanup не должен паниковать (close на nil channel).
	cleanup()
	cleanup()

	// Канал должен быть закрыт (читается с ok=false).
	select {
	case _, ok := <-ch:
		assert.False(t, ok)
	case <-time.After(time.Second):
		t.Fatal("ch was not closed")
	}
}

func TestHub_Subscribe_ContextCancelTriggersCleanup(t *testing.T) {
	t.Parallel()

	fs := &fakeSubscriber{}
	hub := sse.New(fs, 0)

	ctx, cancel := context.WithCancel(context.Background())

	ch, _, err := hub.Subscribe(ctx, "u-cancel")
	require.NoError(t, err)

	cancel()

	// После cancel() авто-cleanup должен закрыть канал — проверяем с таймаутом.
	select {
	case _, ok := <-ch:
		assert.False(t, ok)
	case <-time.After(2 * time.Second):
		t.Fatal("ch was not closed after ctx cancel")
	}
}

func TestHub_Subscribe_SubscribeError(t *testing.T) {
	t.Parallel()

	fs := &fakeSubscriber{subscribeErr: errors.New("nats down")}
	hub := sse.New(fs, 0)

	ch, cleanup, err := hub.Subscribe(context.Background(), "u-err")
	assert.Nil(t, ch)
	assert.NotNil(t, cleanup)
	cleanup() // no-op
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nats down")
}
