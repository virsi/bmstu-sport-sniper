package middleware

import (
	"log/slog"
	"net/http"

	"github.com/fizcultor/backend/pkg/grpcx"
)

// QueryTicket — query-параметр с одноразовым ticket для SSE-подключения.
// Альтернатива небезопасному `?access=<JWT>` (см. QueryAccessToken).
const QueryTicket = "ticket"

// TicketRedeemer — узкий интерфейс ticket.Store.Redeem. Сужен от полной
// реализации, чтобы упростить мок в тестах middleware.
//
// Сигнатура совместима с *ticket.Store: тот же метод того же name, опции
// не нужны.
type TicketRedeemer interface {
	// Redeem атомарно проверяет и удаляет ticket, возвращает userID или ошибку.
	Redeem(ticket string) (userID string, err error)
}

// SSEAuth — middleware-обёртка для SSE-эндпоинтов. Поддерживает 3 способа
// аутентификации, в порядке приоритета:
//
//  1. `?ticket=<X>` — one-time ticket, безопасно для query (короткий TTL,
//     одноразовый). Рекомендуемый путь.
//  2. `Authorization: Bearer <JWT>` — заголовок, если клиент умеет.
//  3. `?access=<JWT>` — DEPRECATED, JWT в query попадает в access-логи.
//     Оставлено для backward-compat; будет удалено после миграции фронта.
//
// Если redeemer == nil, ticket-путь отключён (только JWT через delegate).
// delegate — обычная Auth middleware, вызывается как fallback.
//
// На успех: user_id кладётся в context (UserIDFrom + grpcx.WithUserID).
// На провал: 401 RFC 7807.
func SSEAuth(redeemer TicketRedeemer, delegate func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	if delegate == nil {
		panic("middleware.SSEAuth: nil delegate")
	}
	return func(next http.Handler) http.Handler {
		// Делегат для JWT-пути собираем один раз, в замыкании.
		jwtHandler := delegate(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 1. Пытаемся ticket из query — если redeemer есть и ticket указан.
			if redeemer != nil {
				if tk := r.URL.Query().Get(QueryTicket); tk != "" {
					userID, err := redeemer.Redeem(tk)
					if err == nil && userID != "" {
						ctx := withUserID(r.Context(), userID)
						ctx = grpcx.WithUserID(ctx, userID)
						next.ServeHTTP(w, r.WithContext(ctx))
						return
					}
					// Ticket был, но redeem не прошёл — это намеренная попытка
					// доступа, не fallthrough. Сразу 401.
					//nolint:gosec // G706: р.URL.Path помечается tainted, но это типизированный
					// slog-атрибут без шелл-инъекций; путь нужен в логах для дебага.
					slog.Debug("sse: ticket redeem failed",
						slog.String("path", r.URL.Path),
						slog.Any("error", err),
					)
					writeUnauthorized(w, r, "invalid or expired ticket")
					return
				}
			}
			// 2-3. Делегируем обычной JWT-middleware (header / ?access= fallback).
			jwtHandler.ServeHTTP(w, r)
		})
	}
}
