package handler

import (
	"net/http"
	"time"

	"github.com/fizcultor/backend/services/gateway-svc/internal/http/middleware"
)

// streamTicketResponse — публичный ответ POST /api/stream/ticket.
//
// Контракт: см. docs/api.md секция 6 (SSE).
type streamTicketResponse struct {
	// Ticket — одноразовый base64url-токен. Передавать в EventSource
	// как `?ticket=<value>`.
	Ticket string `json:"ticket"`
	// ExpiresAt — ISO-8601, когда ticket станет невалиден (если не redeem-нут раньше).
	ExpiresAt time.Time `json:"expires_at"`
}

// IssueStreamTicket — POST /api/stream/ticket.
//
// Требует Auth middleware (user_id берём из контекста). Выпускает одноразовый
// ticket с TTL 5 мин для последующего открытия `/api/stream?ticket=<X>`.
//
// Зачем: EventSource не умеет ставить header'ы, но передавать долгоживущий
// JWT в query небезопасно (попадает в access-логи). One-time ticket — это
// короткоживущая capability-строка: даже если ticket залогируется, повторно
// его использовать нельзя.
//
// 503 если ticket store не сконфигурирован (стартап без SSE).
func (h *Handler) IssueStreamTicket(w http.ResponseWriter, r *http.Request) {
	if h.deps.TicketStore == nil {
		WriteError(w, r, NewBadRequest("SSE ticket store not configured"))
		return
	}
	userID := middleware.UserIDFrom(r.Context())
	if userID == "" {
		// Auth middleware гарантирует userID, но защищаем инвариант явно.
		WriteError(w, r, NewUnauthorized("missing user_id in context"))
		return
	}
	tk, exp := h.deps.TicketStore.Issue(userID)
	if tk == "" {
		// crypto/rand упал — критическая системная проблема.
		WriteError(w, r, NewBadRequest("failed to issue ticket"))
		return
	}
	WriteJSON(w, http.StatusOK, streamTicketResponse{
		Ticket:    tk,
		ExpiresAt: exp,
	})
}
