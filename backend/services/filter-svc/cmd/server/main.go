// Command server — точка входа filter-svc.
//
// Bootstrap:
//   - load config (env)
//   - init logger
//   - connect Postgres pool (filter_db)
//   - build store + service
//   - register gRPC FilterService
//   - serve gRPC + healthz/readyz
//   - graceful shutdown по SIGINT/SIGTERM
//
// CRUD фильтров, дедуп known_slots, match слотов. Чистая функция match.Match —
// легко юнит-тестится. Фикс бага старого репо: не очищать known_slots при
// пустом / ошибочном ответе LKS (см. internal/store.InsertKnownSlots).
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"google.golang.org/grpc"

	filterv1 "github.com/fizcultor/backend/gen/filter/v1"
	"github.com/fizcultor/backend/pkg/bootstrap"
	"github.com/fizcultor/backend/pkg/logger"
	"github.com/fizcultor/backend/pkg/metrics"
	"github.com/fizcultor/backend/pkg/pgxutil"
	svccfg "github.com/fizcultor/backend/services/filter-svc/internal/config"
	"github.com/fizcultor/backend/services/filter-svc/internal/service"
	"github.com/fizcultor/backend/services/filter-svc/internal/store"
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
	lg.Info("filter-svc starting",
		slog.String("env", string(cfg.Env)),
		slog.String("grpc_addr", cfg.GRPCAddr),
		slog.String("http_addr", cfg.HTTPAddr),
	)

	ctx, cancel := bootstrap.SignalContext()
	defer cancel()

	pool, err := pgxutil.NewPool(ctx, pgxutil.PoolConfig{
		DSN:            cfg.PostgresDSN,
		MaxConns:       10,
		ConnectTimeout: 5 * time.Second,
		MaxAttempts:    5,
	})
	if err != nil {
		return err
	}
	defer pool.Close()
	lg.Info("postgres connected")

	st := store.New(pool)
	svc := service.New(st)

	// Prometheus метрики + gRPC interceptor.
	mreg := metrics.Init("filter")

	grpcSrv := grpc.NewServer(
		grpc.UnaryInterceptor(mreg.UnaryServerInterceptor()),
	)
	filterv1.RegisterFilterServiceServer(grpcSrv, svc)

	health := bootstrap.NewHealthHandler()
	health.AttachMetrics(mreg.Handler())
	health.AddCheck("postgres", func(ctx context.Context) error { return pool.Ping(ctx) })
	httpSrv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           health.Mux(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	health.SetReady(true)

	return bootstrap.Run(ctx, bootstrap.RunOptions{
		HTTPServer:      httpSrv,
		GRPCServer:      grpcSrv,
		GRPCAddr:        cfg.GRPCAddr,
		ShutdownTimeout: 15 * time.Second,
		OnShutdown: func(_ context.Context) error {
			lg.Info("filter-svc shutting down")
			return nil
		},
	})
}
