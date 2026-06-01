// Package errs определяет типизированные доменные ошибки и их маппинг в
// HTTP-статусы и gRPC-коды. Сервисы используют коды (errs.NotFound,
// errs.Unauthorized и т.д.), а gateway-слой конвертирует их в нужный
// протокольный код через HTTPStatus / GRPCCode.
package errs

import (
	"errors"
	"fmt"
	"net/http"

	"google.golang.org/grpc/codes"
)

// Code — машинно-читаемый код ошибки для маппинга в транспортный слой.
type Code string

const (
	// CodeUnknown — нераспознанная ошибка.
	CodeUnknown Code = "unknown"
	// CodeInvalidArgument — невалидный input от клиента.
	CodeInvalidArgument Code = "invalid_argument"
	// CodeUnauthorized — нет/невалидные креды.
	CodeUnauthorized Code = "unauthorized"
	// CodeForbidden — есть креды, но нет прав.
	CodeForbidden Code = "forbidden"
	// CodeNotFound — ресурс не найден.
	CodeNotFound Code = "not_found"
	// CodeAlreadyExists — конфликт уникальности.
	CodeAlreadyExists Code = "already_exists"
	// CodeRateLimit — превышен лимит.
	CodeRateLimit Code = "rate_limit"
	// CodeInternal — серверная ошибка.
	CodeInternal Code = "internal"
	// CodeUnavailable — апстрим/БД временно недоступны.
	CodeUnavailable Code = "unavailable"
)

// Error — типизированная доменная ошибка с кодом и опциональной обёрткой.
type Error struct {
	// Code — машинный код для маппинга.
	Code Code
	// Message — пользовательское сообщение (можно показывать клиенту).
	Message string
	// Err — внутренняя обёрнутая ошибка (НЕ показывать клиенту).
	Err error
}

// Error реализует интерфейс error.
func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap возвращает обёрнутую ошибку (для errors.Is / errors.As).
func (e *Error) Unwrap() error { return e.Err }

// New создаёт *Error с заданным кодом и сообщением.
func New(code Code, msg string) *Error {
	return &Error{Code: code, Message: msg}
}

// Wrap оборачивает ошибку в *Error с кодом и сообщением.
func Wrap(code Code, msg string, err error) *Error {
	return &Error{Code: code, Message: msg, Err: err}
}

// As извлекает *Error из цепочки errors.Wrap; bool=false если не найден.
func As(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// HTTPStatus возвращает HTTP-код для error. Не-*Error → 500.
func HTTPStatus(err error) int {
	e, ok := As(err)
	if !ok {
		return http.StatusInternalServerError
	}
	switch e.Code {
	case CodeInvalidArgument:
		return http.StatusBadRequest
	case CodeUnauthorized:
		return http.StatusUnauthorized
	case CodeForbidden:
		return http.StatusForbidden
	case CodeNotFound:
		return http.StatusNotFound
	case CodeAlreadyExists:
		return http.StatusConflict
	case CodeRateLimit:
		return http.StatusTooManyRequests
	case CodeUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// GRPCCode возвращает gRPC-код для error. Не-*Error → codes.Internal.
func GRPCCode(err error) codes.Code {
	e, ok := As(err)
	if !ok {
		return codes.Internal
	}
	switch e.Code {
	case CodeInvalidArgument:
		return codes.InvalidArgument
	case CodeUnauthorized:
		return codes.Unauthenticated
	case CodeForbidden:
		return codes.PermissionDenied
	case CodeNotFound:
		return codes.NotFound
	case CodeAlreadyExists:
		return codes.AlreadyExists
	case CodeRateLimit:
		return codes.ResourceExhausted
	case CodeUnavailable:
		return codes.Unavailable
	default:
		return codes.Internal
	}
}
