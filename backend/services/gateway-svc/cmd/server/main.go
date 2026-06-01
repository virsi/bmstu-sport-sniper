// Command server — точка входа gateway-svc.
//
// BFF для фронта: REST + SSE. JWT-middleware (валидация access через auth-svc),
// SSE-хаб (NATS subscribe alerts.<userID>), gRPC-клиенты ко всем внутренним
// сервисам. Слушает только HTTP_ADDR, gRPC не выставляет наружу.
//
// Жизненный цикл:
//
//  1. config + logger.
//  2. NATS (обязателен для SSE).
//  3. gRPC-клиенты ко всем 5 сервисам параллельно (errgroup).
//  4. SSE Hub (бэкенд /api/stream).
//  5. Handler + Router.
//  6. bootstrap.Run (graceful shutdown SIGINT/SIGTERM).
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	svccfg "github.com/fizcultor/backend/services/gateway-svc/internal/config"

	"github.com/fizcultor/backend/pkg/bootstrap"
	"github.com/fizcultor/backend/pkg/events"
	"github.com/fizcultor/backend/pkg/logger"
	"github.com/fizcultor/backend/pkg/metrics"

	"github.com/fizcultor/backend/services/gateway-svc/internal/clients"
	gwhttp "github.com/fizcultor/backend/services/gateway-svc/internal/http"
	"github.com/fizcultor/backend/services/gateway-svc/internal/http/handler"
	"github.com/fizcultor/backend/services/gateway-svc/internal/http/middleware"
	"github.com/fizcultor/backend/services/gateway-svc/internal/sse"
	"github.com/fizcultor/backend/services/gateway-svc/internal/ticket"
)

// errReadinessNATS — sentinel-ошибка для /readyz dep nats.
var errReadinessNATS = errReadiness("nats not connected")

// errReadiness — простая string-based ошибка, чтобы не тянуть errors.New
// для одного use-case.
type errReadiness string

// Error implements error.
func (e errReadiness) Error() string { return string(e) }

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", slog.Any("error", err))
		os.Exit(1)
	}
}

func run() error {
	cfg, err := svccfg.Load()
	if err != nil {
		return err
	}

	lg := logger.Init(logger.Env(cfg.Env), cfg.LogLevel, cfg.ServiceName)
	lg.Info("gateway-svc starting",
		slog.String("env", string(cfg.Env)),
		slog.String("http_addr", cfg.HTTPAddr),
	)

	ctx, cancel := bootstrap.SignalContext()
	defer cancel()

	// NATS — обязателен для SSE-хаба.
	bus, err := events.Connect(ctx, events.Config{
		URL:  cfg.NATSURL,
		Name: cfg.ServiceName,
	})
	if err != nil {
		return err
	}
	defer bus.Close()
	lg.Info("nats connected")

	// gRPC-клиенты — параллельно.
	cls, err := clients.New(ctx, clients.Addrs{
		Auth:     cfg.AuthGRPCAddr,
		Bmstu:    cfg.BmstuGRPCAddr,
		Filter:   cfg.FilterGRPCAddr,
		Notifier: cfg.NotifierGRPCAddr,
		Teachers: cfg.TeachersGRPCAddr,
	})
	if err != nil {
		return err
	}
	defer func() {
		if cerr := cls.Close(); cerr != nil {
			lg.Warn("clients: close", slog.Any("error", cerr))
		}
	}()
	lg.Info("grpc clients connected",
		slog.String("auth", cfg.AuthGRPCAddr),
		slog.String("bmstu", cfg.BmstuGRPCAddr),
		slog.String("filter", cfg.FilterGRPCAddr),
		slog.String("notifier", cfg.NotifierGRPCAddr),
		slog.String("teachers", cfg.TeachersGRPCAddr),
	)

	// Prometheus метрики.
	mreg := metrics.Init("gateway")
	gatewaySSEConnections := mreg.NewGauge(
		"sse_connections",
		"Currently open SSE connections (long-lived).",
	)
	_ = gatewaySSEConnections
	// sse.Hub можно расширить методом для инкремента/декремента (см.
	// internal/sse), пока экспортируем 0 на старте — это даёт точку
	// в Grafana для отрисовки графика после первой подписки.

	// SSE Hub.
	hub := sse.New(bus.Conn(), 0)

	// SSE-ticket store: одноразовые токены для безопасного query-auth в EventSource.
	// Фоновая cleanup-goroutine завершается с ctx.
	tickets := ticket.New(cfg.SSETicketTTL)
	go tickets.Cleanup(ctx)

	// Handler + Router.
	h := handler.New(handler.Deps{
		Clients:     cls,
		SSEHub:      hub,
		TicketStore: tickets,
		CookieConfig: handler.CookieConfig{
			Secure: cfg.CookieSecure,
			Domain: cfg.CookieDomain,
		},
		BotUsername:              cfg.BotUsername,
		SlotsEndpointEnabled:     cfg.SlotsEndpointEnabled,
		SlotsFetchTimeoutSeconds: int(cfg.SlotsFetchTimeout.Seconds()),
	})
	authMw := middleware.Auth(middleware.WrapAuthClient(cls.Auth))
	sseAuthMw := middleware.SSEAuth(tickets, authMw)

	health := bootstrap.NewHealthHandler()
	health.AttachMetrics(mreg.Handler())
	health.AddCheck("nats", func(_ context.Context) error {
		if !bus.Conn().IsConnected() {
			return errReadinessNATS
		}
		return nil
	})
	router := gwhttp.NewRouter(gwhttp.Config{
		CORSAllowedOrigins: cfg.CORSAllowedOrigins,
		RateLimitRPS:       cfg.RateLimitRPS,
		RateLimitBurst:     cfg.RateLimitBurst,
		HealthHandler:      health,
	}, h, authMw, sseAuthMw)

	httpSrv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		// Stream-эндпоинт может жить часами; не ставим WriteTimeout.
	}

	health.SetReady(true)

	return bootstrap.Run(ctx, bootstrap.RunOptions{
		HTTPServer:      httpSrv,
		ShutdownTimeout: 15 * time.Second,
		OnShutdown: func(_ context.Context) error {
			lg.Info("gateway-svc shutting down")
			return nil
		},
	})
}
