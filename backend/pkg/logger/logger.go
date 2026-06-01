// Package logger предоставляет тонкую обёртку над log/slog для сервисов.
//
// В production-режиме (env="prod") пишет JSON в os.Stdout, что удобно для
// агрегаторов логов (Loki, Datadog). В dev-режиме пишет human-readable
// текстовый формат. Уровень логирования настраивается через параметр level
// ("debug", "info", "warn", "error"), по умолчанию — "info".
//
// Дополнительно: WithTraceID / WithUserID кладут typed-атрибуты в slog.Logger
// для cross-service correlation. Сервисы пробрасывают request-id и user-id
// через grpc metadata + httpx.RequestIDFrom и логируют через локальный
// instance логгера.
package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Env определяет режим работы логгера.
type Env string

const (
	// EnvDev — режим разработки: текстовый формат, уровень debug по умолчанию.
	EnvDev Env = "dev"
	// EnvProd — production-режим: JSON формат, уровень info по умолчанию.
	EnvProd Env = "prod"
)

// Init создаёт и устанавливает глобальный slog.Default логгер.
//
// env управляет форматом вывода (JSON для prod, text для dev), level задаёт
// минимальный уровень логирования ("debug", "info", "warn", "error").
// Параметр service добавляется ко всем записям как атрибут "service".
//
// Возвращает настроенный *slog.Logger; он же установлен как slog.Default.
func Init(env Env, level, service string) *slog.Logger {
	return InitWriter(env, level, service, os.Stdout)
}

// InitWriter — версия Init с произвольным io.Writer (полезно для тестов).
func InitWriter(env Env, level, service string, w io.Writer) *slog.Logger {
	lvl := parseLevel(level)
	opts := &slog.HandlerOptions{Level: lvl}

	var handler slog.Handler
	if env == EnvProd {
		handler = slog.NewJSONHandler(w, opts)
	} else {
		handler = slog.NewTextHandler(w, opts)
	}

	lg := slog.New(handler).With(slog.String("service", service))
	slog.SetDefault(lg)
	return lg
}

// parseLevel преобразует строку в slog.Level. Неизвестное значение → Info.
func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error", "err":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// ctxKey — приватный тип для logger-ключей в context.Context.
type ctxKey int

const (
	// ctxLogger — ключ для *slog.Logger в context.
	ctxLogger ctxKey = iota
)

// WithLogger кладёт *slog.Logger в context.Context. Используется HTTP/gRPC
// middleware'ом, чтобы хэндлеры могли логировать через FromContext без
// тащёщенного через все аргументы logger.
func WithLogger(ctx context.Context, lg *slog.Logger) context.Context {
	if lg == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxLogger, lg)
}

// FromContext возвращает *slog.Logger из контекста; если нет — slog.Default().
func FromContext(ctx context.Context) *slog.Logger {
	if v, ok := ctx.Value(ctxLogger).(*slog.Logger); ok && v != nil {
		return v
	}
	return slog.Default()
}

// WithTraceID возвращает дочерний логгер с attribute "trace_id".
//
// Используется для cross-service correlation: gateway-svc генерирует
// X-Request-ID, прокидывает в gRPC metadata, сервисы аттачат к своему
// логгеру и тегируют все записи в рамках запроса.
func WithTraceID(lg *slog.Logger, traceID string) *slog.Logger {
	if lg == nil {
		lg = slog.Default()
	}
	if traceID == "" {
		return lg
	}
	return lg.With(slog.String("trace_id", traceID))
}

// WithUserID возвращает дочерний логгер с attribute "user_id". Полезно для
// бизнес-логирования (например, "filter created" с user_id, чтобы найти все
// действия одного юзера в Loki).
func WithUserID(lg *slog.Logger, userID string) *slog.Logger {
	if lg == nil {
		lg = slog.Default()
	}
	if userID == "" {
		return lg
	}
	return lg.With(slog.String("user_id", userID))
}
