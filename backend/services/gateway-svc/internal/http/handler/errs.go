// Package handler содержит HTTP-хэндлеры gateway-svc: тонкие адаптеры
// REST → gRPC → REST. Бизнес-логика вынесена в нижестоящие сервисы.
//
// Все ответы об ошибках единообразны (RFC 7807 problem+json).
package handler

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/fizcultor/backend/pkg/httpx"
)

// ProblemContentType — Content-Type для RFC 7807 problem+json ответов.
const ProblemContentType = "application/problem+json"

// JSONContentType — Content-Type для обычных JSON-ответов.
const JSONContentType = "application/json; charset=utf-8"

// errorTypePrefix — корень URI типов ошибок. В prod заменить на публичный
// домен. Сейчас фронт это поле не использует, поэтому KISS-плейсхолдер.
const errorTypePrefix = "https://fizcultor.example.com/errors/"

// Problem — структура RFC 7807 problem+json ответа.
//
// Поле Type — машинно-стабильный URI типа ошибки, фронт может маршрутизировать
// UX по нему. Status — HTTP-код, Detail — человеко-читаемое объяснение для
// разработчика/юзера. TraceID совпадает с X-Request-ID.
type Problem struct {
	// Type — URI типа ошибки.
	Type string `json:"type"`
	// Title — короткий заголовок типа ошибки.
	Title string `json:"title"`
	// Status — HTTP-статус.
	Status int `json:"status"`
	// Detail — детальное описание конкретного случая.
	Detail string `json:"detail,omitempty"`
	// TraceID — корреляционный id запроса (= X-Request-ID).
	TraceID string `json:"trace_id,omitempty"`
}

// WriteJSON сериализует v в JSON и пишет с указанным statusCode.
// При ошибке кодирования пишет 500 и логирует — caller не получает обратной связи,
// потому что тело ответа уже частично могло уйти.
func WriteJSON(w http.ResponseWriter, statusCode int, v any) {
	w.Header().Set("Content-Type", JSONContentType)
	w.WriteHeader(statusCode)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("handler: write json", slog.Any("error", err))
	}
}

// WriteNoContent отдаёт 204 без тела (для идемпотентных операций без полезной нагрузки).
func WriteNoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// WriteError маппит ошибку (gRPC status или любая Go ошибка) в RFC 7807 ответ.
//
// gRPC-коды → HTTP по таблице из docs/api.md:
//
//	InvalidArgument      → 400
//	Unauthenticated      → 401
//	PermissionDenied     → 403
//	NotFound             → 404
//	AlreadyExists        → 409
//	FailedPrecondition   → 422
//	ResourceExhausted    → 429
//	Unavailable          → 503
//	Internal             → 500
//
// 5xx-ошибки логируются на error-уровне с trace_id, 4xx — на debug
// (это норма флоу, не индикатор поломки).
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	if err == nil {
		return
	}
	p := errorToProblem(err)
	p.TraceID = httpx.RequestIDFrom(r.Context())

	if p.Status >= 500 {
		//nolint:gosec // G706: р.URL.Path помечается tainted, но это всё ещё типизированный
		// slog-атрибут без шелл-инъекций; путь нужен в логах для дебага.
		slog.Error("http: handler error",
			slog.Int("status", p.Status),
			slog.String("path", r.URL.Path),
			slog.String("trace_id", p.TraceID),
			slog.Any("error", err),
		)
	} else {
		//nolint:gosec // G706: см. выше.
		slog.Debug("http: handler 4xx",
			slog.Int("status", p.Status),
			slog.String("path", r.URL.Path),
			slog.String("trace_id", p.TraceID),
			slog.String("detail", p.Detail),
		)
	}

	w.Header().Set("Content-Type", ProblemContentType)
	w.WriteHeader(p.Status)
	if encErr := json.NewEncoder(w).Encode(p); encErr != nil {
		slog.Error("handler: encode problem", slog.Any("error", encErr))
	}
}

// errorToProblem строит Problem на основе error.
//
// Поддерживает:
//   - *handlerError (см. NewBadRequest и т.п.) — самый точный путь.
//   - gRPC-статусы (codes → http).
//   - io.EOF и json-syntax ошибки от decodeJSON — 400.
//   - Иначе 500 generic.
func errorToProblem(err error) Problem {
	var he *handlerError
	if errors.As(err, &he) {
		return Problem{
			Type:   errorTypeURI(he.kind),
			Title:  he.kind,
			Status: he.status,
			Detail: he.detail,
		}
	}

	if st, ok := status.FromError(err); ok {
		httpStatus, kind, title := grpcCodeToHTTP(st.Code())
		return Problem{
			Type:   errorTypeURI(kind),
			Title:  title,
			Status: httpStatus,
			Detail: st.Message(),
		}
	}

	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return Problem{
			Type:   errorTypeURI("bad_request"),
			Title:  "Bad Request",
			Status: http.StatusBadRequest,
			Detail: "request body is empty or truncated",
		}
	}

	return Problem{
		Type:   errorTypeURI("internal"),
		Title:  "Internal Server Error",
		Status: http.StatusInternalServerError,
		Detail: "unexpected error",
	}
}

// grpcCodeToHTTP — таблица из wave3-brief + docs/api.md.
// Возвращает (http_status, kind, title).
func grpcCodeToHTTP(c codes.Code) (httpStatus int, kind, title string) {
	switch c {
	case codes.OK:
		return http.StatusOK, "ok", "OK"
	case codes.InvalidArgument:
		return http.StatusBadRequest, "invalid_argument", "Bad Request"
	case codes.Unauthenticated:
		return http.StatusUnauthorized, "unauthenticated", "Unauthorized"
	case codes.PermissionDenied:
		return http.StatusForbidden, "permission_denied", "Forbidden"
	case codes.NotFound:
		return http.StatusNotFound, "not_found", "Not Found"
	case codes.AlreadyExists:
		return http.StatusConflict, "already_exists", "Conflict"
	case codes.FailedPrecondition:
		return http.StatusUnprocessableEntity, "failed_precondition", "Unprocessable Entity"
	case codes.ResourceExhausted:
		return http.StatusTooManyRequests, "rate_limit", "Too Many Requests"
	case codes.Unavailable:
		return http.StatusServiceUnavailable, "unavailable", "Service Unavailable"
	case codes.DeadlineExceeded:
		return http.StatusGatewayTimeout, "timeout", "Gateway Timeout"
	case codes.Canceled:
		return 499, "canceled", "Client Closed Request"
	default:
		return http.StatusInternalServerError, "internal", "Internal Server Error"
	}
}

// errorTypeURI собирает stable URI типа.
func errorTypeURI(kind string) string {
	return errorTypePrefix + strings.ReplaceAll(kind, "_", "-")
}

// handlerError — внутренняя ошибка, которую хэндлер строит сам
// (валидация, missing fields и т.п.), чтобы не делать gRPC-ходку
// ради 400. NewBadRequest, NewUnauthorized — фабрики.
type handlerError struct {
	status int
	kind   string
	detail string
}

func (e *handlerError) Error() string {
	return e.kind + ": " + e.detail
}

// NewBadRequest строит handlerError со статусом 400.
func NewBadRequest(detail string) error {
	return &handlerError{status: http.StatusBadRequest, kind: "invalid_argument", detail: detail}
}

// NewUnauthorized строит handlerError со статусом 401.
func NewUnauthorized(detail string) error {
	return &handlerError{status: http.StatusUnauthorized, kind: "unauthenticated", detail: detail}
}

// NewForbidden строит handlerError со статусом 403.
func NewForbidden(detail string) error {
	return &handlerError{status: http.StatusForbidden, kind: "permission_denied", detail: detail}
}

// NewNotFound строит handlerError со статусом 404.
func NewNotFound(detail string) error {
	return &handlerError{status: http.StatusNotFound, kind: "not_found", detail: detail}
}

// DecodeJSON парсит тело запроса в dst, ограничивая размер тела maxBytes.
// Возвращает NewBadRequest при ошибке парсинга — caller передаёт в WriteError.
//
// Включает strict-mode (DisallowUnknownFields): незнакомые поля в JSON-body
// дают 400, чтобы клиент быстрее замечал расхождения контракта.
func DecodeJSON(r *http.Request, dst any, maxBytes int64) error {
	if maxBytes <= 0 {
		maxBytes = 1 << 20 // 1 MiB
	}
	r.Body = http.MaxBytesReader(nil, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return NewBadRequest("invalid request body: " + err.Error())
	}
	return nil
}

// DecodeJSONInto — версия DecodeJSON без DisallowUnknownFields, для PATCH-флоу
// где нужно различать «поле есть и null» vs «поля нет» (тело в map[string]any).
//
// Лимит по умолчанию 1 MiB.
func DecodeJSONInto(r *http.Request, dst any, maxBytes int64) error {
	if maxBytes <= 0 {
		maxBytes = 1 << 20
	}
	r.Body = http.MaxBytesReader(nil, r.Body, maxBytes)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		return NewBadRequest("invalid request body: " + err.Error())
	}
	return nil
}
