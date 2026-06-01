package handler

import (
	"time"

	"github.com/fizcultor/backend/services/gateway-svc/internal/clients"
	"github.com/fizcultor/backend/services/gateway-svc/internal/sse"
)

// Deps — зависимости HTTP-хэндлеров. Создаются в main, передаются один раз
// в New, дальше живут весь жизненный цикл сервиса. Все поля thread-safe.
type Deps struct {
	// Clients — gRPC-клиенты ко всем внутренним сервисам.
	Clients *clients.Clients
	// SSEHub — NATS↔SSE мост. Может быть nil — тогда /api/stream вернёт 400.
	SSEHub *sse.Hub
	// TicketStore — выпуск/проверка one-time ticket для SSE. nil ⇒ ручка
	// /api/stream/ticket вернёт 503, SSE-middleware разрешит только JWT.
	TicketStore TicketIssuer
	// CookieConfig — параметры refresh-token cookie (Secure / Domain).
	CookieConfig CookieConfig
	// BotUsername — Telegram bot username (без `@`), используется для
	// rewrite tg://start-deeplink в https://t.me/<bot>?start=<code>.
	BotUsername string
	// SlotsEndpointEnabled — true, если /api/slots делает live-запрос
	// к bmstu-svc; false → возвращает пустой массив (KISS V1).
	SlotsEndpointEnabled bool
	// SlotsFetchTimeoutSeconds — таймаут на live-запрос к bmstu-svc, секунды.
	SlotsFetchTimeoutSeconds int
}

// TicketIssuer — узкий интерфейс ticket-store: только Issue (Redeem нужен
// в middleware, отдельный интерфейс). Сужено, чтобы упростить мок в тестах.
type TicketIssuer interface {
	// Issue выпускает одноразовый ticket для пользователя userID.
	// Возвращает сам ticket и момент истечения.
	Issue(userID string) (ticket string, expiresAt time.Time)
}

// Handler — общий контейнер всех HTTP-обработчиков gateway-svc.
//
// Хэндлеры — методы Handler.*: коротки, без бизнес-логики, маппят REST↔gRPC.
// Регистрация в роутере — в internal/http/router.go.
type Handler struct {
	deps          Deps
	streamHandler *StreamHandler
}

// New создаёт Handler с переданными зависимостями.
//
// Если deps.SSEHub != nil — создаётся StreamHandler для /api/stream;
// иначе Stream-метод вернёт 400 (SSE отключён).
func New(deps Deps) *Handler {
	h := &Handler{deps: deps}
	if deps.SSEHub != nil {
		h.streamHandler = NewStreamHandler(deps.SSEHub)
	}
	return h
}
