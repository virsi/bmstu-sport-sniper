// Package bootstrap содержит общую логику запуска сервисов: parsing signal
// контекста, healthz/readyz HTTP, graceful shutdown с таймаутом.
//
// Использование: в main.go сервис формирует SignalContext, инициализирует
// зависимости, передаёт http.Server и grpc.Server в Run, и ждёт.
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"google.golang.org/grpc"
)

// SignalContext возвращает context.Context, отменяемый при SIGINT/SIGTERM.
// Удобно вызывать в самом начале main().
func SignalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
}

// ReadinessCheck — проверка одной зависимости (DB ping, NATS connected и пр.).
//
// Возвращает nil если зависимость готова, ошибку — если нет. Время выполнения
// проверки ограничивается caller через context (по умолчанию 2 секунды).
//
// Конкретные адаптеры (для pgxpool, nats.Conn) сервисы инлайнят сами, чтобы
// pkg/bootstrap не тянул в зависимости pgx/nats.
type ReadinessCheck func(ctx context.Context) error

// HealthHandler возвращает http.Handler для /healthz, /readyz и опционально
// /metrics.
//
// /healthz — проверка живости (всегда 200, если процесс жив).
// /readyz  — проверка готовности: 503 пока ready == false ИЛИ хоть одна
// зависимость отвалилась. Использует ReadinessChecks, добавляемые AddCheck.
// /metrics — Prometheus expose; включается через AttachMetrics.
type HealthHandler struct {
	ready          atomic.Bool
	checks         atomic.Pointer[[]namedCheck]
	metricsHandler atomic.Pointer[http.Handler]
}

// namedCheck — readiness-check с именем для диагностики в /readyz body.
type namedCheck struct {
	name  string
	check ReadinessCheck
}

// NewHealthHandler создаёт хэндлер с ready=false и без зависимых проверок.
func NewHealthHandler() *HealthHandler { return &HealthHandler{} }

// SetReady помечает сервис как готовый к приёму трафика. После SetReady(true)
// /readyz начинает реально выполнять зарегистрированные ReadinessCheck'и; до
// этого момента сервис отдаёт 503 даже если все зависимости в порядке (это
// удобно для startup probe в k8s/compose, когда мы хотим не ловить трафик до
// окончания инициализации).
func (h *HealthHandler) SetReady(v bool) { h.ready.Store(v) }

// AddCheck регистрирует ReadinessCheck. Безопасно вызывать в любой момент
// (например, после успешного pool.Ping и connect к NATS).
//
// На каждый вызов /readyz будут запускаться все зарегистрированные проверки
// с таймаутом 2 секунды (общим, не на каждую). Если хоть одна вернула
// ошибку, /readyz отвечает 503 с телом "dep <name>: <error>".
func (h *HealthHandler) AddCheck(name string, c ReadinessCheck) {
	for {
		old := h.checks.Load()
		var current []namedCheck
		if old != nil {
			current = *old
		}
		next := make([]namedCheck, 0, len(current)+1)
		next = append(next, current...)
		next = append(next, namedCheck{name: name, check: c})
		if h.checks.CompareAndSwap(old, &next) {
			return
		}
	}
}

// AttachMetrics монтирует Prometheus-handler на /metrics в Mux'е этого
// health-handler'а. Передавать nil чтобы убрать.
//
// Сделано через atomic.Pointer, чтобы Mux() можно было вызывать один раз
// в начале (handler передан как http.Handler в http.Server), а
// AttachMetrics — позже, без рестарта сервера.
func (h *HealthHandler) AttachMetrics(handler http.Handler) {
	if handler == nil {
		h.metricsHandler.Store(nil)
		return
	}
	h.metricsHandler.Store(&handler)
}

// Mux возвращает *http.ServeMux с /healthz, /readyz и (если задан) /metrics.
//
// Возвращаемый mux держит ссылку на HealthHandler — повторные вызовы Mux()
// безопасны, но каждый создаёт новый ServeMux. Обычно сервис вызывает Mux()
// ровно один раз в main().
func (h *HealthHandler) Mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", h.handleReadyz)
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		hp := h.metricsHandler.Load()
		if hp == nil {
			http.NotFound(w, r)
			return
		}
		(*hp).ServeHTTP(w, r)
	})
	return mux
}

// handleReadyz реализует /readyz: 200 только если ready=true и все
// ReadinessCheck'и прошли.
func (h *HealthHandler) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if !h.ready.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("not ready"))
		return
	}
	checksPtr := h.checks.Load()
	if checksPtr == nil || len(*checksPtr) == 0 {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	for _, c := range *checksPtr {
		if err := c.check(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprintf(w, "dep %s: %v", c.name, err)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready"))
}

// RunOptions — параметры Run.
type RunOptions struct {
	// HTTPServer — HTTP-сервер (может быть nil если сервис чисто gRPC).
	HTTPServer *http.Server
	// GRPCServer — gRPC-сервер (может быть nil).
	GRPCServer *grpc.Server
	// GRPCAddr — адрес для gRPC (если GRPCServer != nil).
	GRPCAddr string
	// ShutdownTimeout — макс. время на graceful shutdown.
	ShutdownTimeout time.Duration
	// OnShutdown — пользовательский cleanup, вызывается до закрытия серверов
	// (закрытие БД-пулов, дрейн NATS и т.п.).
	OnShutdown func(context.Context) error
}

// Run запускает HTTP и/или gRPC серверы, ждёт отмены ctx и выполняет
// graceful shutdown. Логирует через slog.Default().
func Run(ctx context.Context, opts RunOptions) error {
	if opts.ShutdownTimeout <= 0 {
		opts.ShutdownTimeout = 15 * time.Second
	}

	errCh := make(chan error, 2)

	if opts.HTTPServer != nil {
		go func() {
			slog.Info("http: starting", slog.String("addr", opts.HTTPServer.Addr))
			if err := opts.HTTPServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("http: serve: %w", err)
				return
			}
			errCh <- nil
		}()
	}

	if opts.GRPCServer != nil {
		lis, err := net.Listen("tcp", opts.GRPCAddr)
		if err != nil {
			return fmt.Errorf("bootstrap: grpc listen %s: %w", opts.GRPCAddr, err)
		}
		go func() {
			slog.Info("grpc: starting", slog.String("addr", opts.GRPCAddr))
			if err := opts.GRPCServer.Serve(lis); err != nil {
				errCh <- fmt.Errorf("grpc: serve: %w", err)
				return
			}
			errCh <- nil
		}()
	}

	select {
	case <-ctx.Done():
		slog.Info("shutdown: signal received")
	case err := <-errCh:
		if err != nil {
			slog.Error("shutdown: server failed", slog.Any("error", err))
			return err
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), opts.ShutdownTimeout)
	defer cancel()

	if opts.OnShutdown != nil {
		if err := opts.OnShutdown(shutdownCtx); err != nil {
			slog.Error("shutdown: cleanup failed", slog.Any("error", err))
		}
	}

	if opts.GRPCServer != nil {
		done := make(chan struct{})
		go func() {
			opts.GRPCServer.GracefulStop()
			close(done)
		}()
		select {
		case <-done:
		case <-shutdownCtx.Done():
			opts.GRPCServer.Stop()
		}
	}

	if opts.HTTPServer != nil {
		if err := opts.HTTPServer.Shutdown(shutdownCtx); err != nil {
			slog.Error("http: shutdown failed", slog.Any("error", err))
		}
	}

	slog.Info("shutdown: complete")
	return nil
}

// Hostname возвращает имя хоста или "unknown" если os.Hostname вернул ошибку.
func Hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}
