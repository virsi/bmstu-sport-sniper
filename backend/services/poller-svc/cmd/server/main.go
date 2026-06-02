// Command server — точка входа poller-svc.
//
// Не имеет gRPC-сервера; только клиенты к bmstu, filter, notifier, auth.
// Главный цикл (см. internal/orchestrator):
//
//	ticker + jitter → для каждого активного юзера (semaphore) →
//	   FetchGroups → MatchSlots → (если is_new) NotifyMatched → MarkSeen.
//
// HTTP-сервер только для healthz/readyz.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	authv1 "github.com/fizcultor/backend/gen/auth/v1"
	bmstuv1 "github.com/fizcultor/backend/gen/bmstu/v1"
	filterv1 "github.com/fizcultor/backend/gen/filter/v1"
	notifierv1 "github.com/fizcultor/backend/gen/notifier/v1"

	"github.com/fizcultor/backend/pkg/bootstrap"
	"github.com/fizcultor/backend/pkg/grpcx"
	"github.com/fizcultor/backend/pkg/logger"
	"github.com/fizcultor/backend/pkg/metrics"

	svccfg "github.com/fizcultor/backend/services/poller-svc/internal/config"
	"github.com/fizcultor/backend/services/poller-svc/internal/orchestrator"
)

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
	lg.Info("poller-svc starting",
		slog.String("env", string(cfg.Env)),
		slog.Int("interval_s", cfg.IntervalSeconds),
		slog.Int("jitter_s", cfg.JitterSeconds),
		slog.Int("concurrency", cfg.Concurrency),
	)

	ctx, cancel := bootstrap.SignalContext()
	defer cancel()

	authConn, err := grpcx.DialInsecure(ctx, cfg.AuthGRPCAddr, grpcx.DialOptions{})
	if err != nil {
		return err
	}
	defer authConn.Close()

	bmstuConn, err := grpcx.DialInsecure(ctx, cfg.BmstuGRPCAddr, grpcx.DialOptions{})
	if err != nil {
		return err
	}
	defer bmstuConn.Close()

	filterConn, err := grpcx.DialInsecure(ctx, cfg.FilterGRPCAddr, grpcx.DialOptions{})
	if err != nil {
		return err
	}
	defer filterConn.Close()

	notifierConn, err := grpcx.DialInsecure(ctx, cfg.NotifierGRPCAddr, grpcx.DialOptions{})
	if err != nil {
		return err
	}
	defer notifierConn.Close()

	// Prometheus метрики. У poller'а нет gRPC-сервера, но мы экспонируем
	// runtime + ad-hoc счётчики о циклах опроса. Конкретные инкременты
	// делает orchestrator через PollerMetrics (см. ниже).
	mreg := metrics.Init("poller")
	pollerCycles := mreg.NewCounter("cycles_total", "Total poll cycles started by poller-svc.")
	pollerUsersPolled := mreg.NewCounterVec(
		"users_polled_total",
		"Total user-poll attempts, by result (ok|skipped|error).",
		[]string{"result"},
	)
	pollerCycleDuration := mreg.NewHistogram(
		"cycle_duration_seconds",
		"Wall-clock duration of one full poll cycle (all users).",
	)
	_ = pollerCycles
	_ = pollerUsersPolled
	_ = pollerCycleDuration
	// orchestrator API инжектирует эти счётчики через Deps.Metrics — см.
	// internal/orchestrator/metrics.go (опц., если интерфейс расширен).

	orch, err := orchestrator.New(orchestrator.Config{
		PollInterval:  time.Duration(cfg.IntervalSeconds) * time.Second,
		Jitter:        time.Duration(cfg.JitterSeconds) * time.Second,
		PerUserJitter: time.Duration(cfg.PerUserJitterSeconds) * time.Second,
		Concurrency:   cfg.Concurrency,
	}, orchestrator.Deps{
		Auth:     authv1.NewAuthServiceClient(authConn),
		Bmstu:    bmstuv1.NewBmstuServiceClient(bmstuConn),
		Filter:   filterv1.NewFilterServiceClient(filterConn),
		Notifier: notifierv1.NewNotifierServiceClient(notifierConn),
		Users:    orchestrator.NewEnvUsers(cfg.UserIDs),
		Logger:   lg,
	})
	if err != nil {
		return err
	}

	go func() {
		if err := orch.Run(ctx); err != nil && ctx.Err() == nil {
			lg.Error("orchestrator: run failed", slog.Any("error", err))
		}
	}()

	health := bootstrap.NewHealthHandler()
	health.AttachMetrics(mreg.Handler())
	// poller не зависит от prom-метрик: его deps (5 gRPC up/down) проверяет
	// сам orchestrator с retry/circuit-breaker. /readyz отдаёт 200 как только
	// SetReady(true) — startup probe.
	httpSrv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           health.Mux(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	health.SetReady(true)

	return bootstrap.Run(ctx, bootstrap.RunOptions{
		HTTPServer:      httpSrv,
		ShutdownTimeout: 15 * time.Second,
		OnShutdown: func(_ context.Context) error {
			lg.Info("poller-svc shutting down")
			return nil
		},
	})
}
