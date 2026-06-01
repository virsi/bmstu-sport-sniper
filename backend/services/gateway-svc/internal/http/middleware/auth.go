// Package middleware содержит HTTP-middleware специфичные для gateway-svc:
// аутентификация JWT (вызывает auth-svc), извлечение user_id в context.
//
// Кросс-сервисные общие middleware (recover, request-id, CORS, rate-limit)
// живут в pkg/httpx и подключаются в internal/http/router.go.
package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	authv1 "github.com/fizcultor/backend/gen/auth/v1"
	"github.com/fizcultor/backend/pkg/grpcx"
)

// ctxKey — приватный тип для ключей context.Value (избегает коллизий).
type ctxKey int

const (
	// ctxUserID — user_id, положенный AuthMiddleware после успешной валидации.
	ctxUserID ctxKey = iota
)

// HeaderAuthorization — стандартный HTTP-заголовок носителя токена.
const HeaderAuthorization = "Authorization"

// QueryAccessToken — query-параметр для access-токена. Используется только
// SSE-эндпоинтом (EventSource не умеет ставить headers). Не светим JWT
// в общий access-log → читаем только если нет header'а.
const QueryAccessToken = "access"

// bearerPrefix — обязательный префикс в Authorization-header.
const bearerPrefix = "Bearer "

// AuthVerifier — узкий интерфейс auth-svc.VerifyAccess. Сужен от полного
// AuthServiceClient, чтобы упростить мок в тестах middleware.
//
// Сигнатура совместима с authv1.AuthServiceClient.VerifyAccess: тот же
// метод того же name, последний аргумент — opaque grpc.CallOption-варианты,
// которые middleware никогда не передаёт.
type AuthVerifier interface {
	// VerifyAccess валидирует access-token (подпись, exp, revoked).
	VerifyAccess(ctx context.Context, in *authv1.VerifyAccessRequest) (*authv1.VerifyAccessResponse, error)
}

// authClientAdapter — адаптер authv1.AuthServiceClient под AuthVerifier.
type authClientAdapter struct {
	c authv1.AuthServiceClient
}

// VerifyAccess делегирует в обёрнутый клиент без дополнительных опций.
func (a *authClientAdapter) VerifyAccess(ctx context.Context, in *authv1.VerifyAccessRequest) (*authv1.VerifyAccessResponse, error) {
	return a.c.VerifyAccess(ctx, in)
}

// WrapAuthClient оборачивает оригинальный gRPC-клиент в AuthVerifier-интерфейс,
// удобный для DI и подмены моком в тестах.
func WrapAuthClient(c authv1.AuthServiceClient) AuthVerifier {
	return &authClientAdapter{c: c}
}

// UserIDFrom возвращает user_id, положенный AuthMiddleware. Пустая строка
// означает, что middleware не выполнялся (хэндлер не за auth).
func UserIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(ctxUserID).(string)
	return v
}

// withUserID кладёт user_id в context для дальнейших handler/clients-слоёв.
func withUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, ctxUserID, userID)
}

// Auth — middleware-обёртка, требующая валидный access-токен.
//
// Поиск токена:
//  1. Authorization: Bearer <token>
//  2. ?access=<token> в query (только если header пуст) — для SSE EventSource.
//
// При успехе:
//   - user_id кладётся в HTTP context (UserIDFrom).
//   - в outgoing gRPC metadata добавляется x-user-id (grpcx.WithUserID), чтобы
//     downstream-сервисы видели владельца запроса.
//
// При провале → 401 RFC 7807. Сообщения не разглашают причину (не различаем
// expired/invalid/revoked) — это не помогает атакующему.
func Auth(verifier AuthVerifier) func(http.Handler) http.Handler {
	if verifier == nil {
		panic("middleware.Auth: nil verifier")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractToken(r)
			if token == "" {
				writeUnauthorized(w, r, "missing access token")
				return
			}

			resp, err := verifier.VerifyAccess(r.Context(), &authv1.VerifyAccessRequest{AccessToken: token})
			if err != nil {
				st, ok := status.FromError(err)
				if ok && st.Code() == codes.Unauthenticated {
					writeUnauthorized(w, r, "invalid or expired token")
					return
				}
				// Прочие ошибки (Unavailable / Internal) — апстрим лёг.
				//nolint:gosec // G706: tainted path в типизированном slog-атрибуте безопасно.
				slog.Warn("auth: verify failed",
					slog.String("path", r.URL.Path),
					slog.Any("error", err),
				)
				writeUnauthorized(w, r, "auth backend unavailable")
				return
			}

			userID := resp.GetUserId()
			if userID == "" {
				writeUnauthorized(w, r, "invalid token claims")
				return
			}

			ctx := withUserID(r.Context(), userID)
			ctx = grpcx.WithUserID(ctx, userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// extractToken возвращает токен либо из header, либо из query-параметра.
// Query — fallback для SSE, не светим header в access-log.
func extractToken(r *http.Request) string {
	if h := r.Header.Get(HeaderAuthorization); h != "" {
		if strings.HasPrefix(h, bearerPrefix) {
			return strings.TrimSpace(h[len(bearerPrefix):])
		}
		return ""
	}
	if q := r.URL.Query().Get(QueryAccessToken); q != "" {
		return q
	}
	return ""
}

// writeUnauthorized пишет RFC 7807 401. Дублирует логику handler.WriteError,
// чтобы не создавать import cycle (handler → middleware).
func writeUnauthorized(w http.ResponseWriter, r *http.Request, detail string) {
	type problem struct {
		Type    string `json:"type"`
		Title   string `json:"title"`
		Status  int    `json:"status"`
		Detail  string `json:"detail,omitempty"`
		TraceID string `json:"trace_id,omitempty"`
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusUnauthorized)
	traceID := r.Header.Get("X-Request-ID")
	if err := json.NewEncoder(w).Encode(problem{
		Type:    "https://fizcultor.example.com/errors/unauthenticated",
		Title:   "Unauthorized",
		Status:  http.StatusUnauthorized,
		Detail:  detail,
		TraceID: traceID,
	}); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		slog.Error("middleware: write 401 body", slog.Any("error", err))
	}
}
