// Command server — точка входа teachers-svc.
//
// Bootstrap:
//   - load config
//   - init logger
//   - connect Postgres pool (teachers_db)
//   - run teachers.Bootstrap (импорт embedded teachers.json при первом запуске)
//   - build store + service
//   - register gRPC TeachersService
//   - serve gRPC + healthz/readyz
//   - graceful shutdown
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"google.golang.org/grpc"

	teachersv1 "github.com/fizcultor/backend/gen/teachers/v1"
	"github.com/fizcultor/backend/pkg/bootstrap"
	"github.com/fizcultor/backend/pkg/logger"
	"github.com/fizcultor/backend/pkg/metrics"
	"github.com/fizcultor/backend/pkg/pgxutil"
	svccfg "github.com/fizcultor/backend/services/teachers-svc/internal/config"
	"github.com/fizcultor/backend/services/teachers-svc/internal/store"
	"github.com/fizcultor/backend/services/teachers-svc/internal/teachers"
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
	lg.Info("teachers-svc starting",
		slog.String("env", string(cfg.Env)),
		slog.Bool("bootstrap_import", cfg.BootstrapImport),
		slog.String("grpc_addr", cfg.GRPCAddr),
		slog.String("http_addr", cfg.HTTPAddr),
	)

	ctx, cancel := bootstrap.SignalContext()
	defer cancel()

	pool, err := pgxutil.NewPool(ctx, pgxutil.PoolConfig{
		DSN:            cfg.PostgresDSN,
		MaxConns:       5,
		ConnectTimeout: 5 * time.Second,
		MaxAttempts:    5,
	})
	if err != nil {
		return err
	}
	defer pool.Close()
	lg.Info("postgres connected")

	st := store.New(pool)

	// One-shot bootstrap import.
	if cfg.BootstrapImport {
		if err := teachers.Bootstrap(ctx, st); err != nil {
			return err
		}
	}

	svc := teachers.New(st)

	// Prometheus метрики + gRPC interceptor.
	mreg := metrics.Init("teachers")

	grpcSrv := grpc.NewServer(
		grpc.UnaryInterceptor(mreg.UnaryServerInterceptor()),
	)
	teachersv1.RegisterTeachersServiceServer(grpcSrv, svc)

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
			lg.Info("teachers-svc shutting down")
			return nil
		},
	})
}
