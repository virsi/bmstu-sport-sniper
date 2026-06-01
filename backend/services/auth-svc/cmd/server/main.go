// Command server — точка входа auth-svc.
//
// Bootstrap:
//   - загрузка config из env (pkg/config + svc/config).
//   - init slog (pkg/logger).
//   - подключение Postgres (pkg/pgxutil) с retry/backoff.
//   - регистрация authv1.AuthServiceServer на gRPC.
//   - запуск HTTP healthz/readyz и graceful shutdown через pkg/bootstrap.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"google.golang.org/grpc"

	authv1 "github.com/fizcultor/backend/gen/auth/v1"
	"github.com/fizcultor/backend/pkg/bootstrap"
	"github.com/fizcultor/backend/pkg/jwtx"
	"github.com/fizcultor/backend/pkg/logger"
	"github.com/fizcultor/backend/pkg/metrics"
	"github.com/fizcultor/backend/pkg/pgxutil"
	"github.com/fizcultor/backend/services/auth-svc/internal/auth"
	svccfg "github.com/fizcultor/backend/services/auth-svc/internal/config"
	"github.com/fizcultor/backend/services/auth-svc/internal/store"
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
	lg.Info("auth-svc starting",
		slog.String("env", string(cfg.Env)),
		slog.String("grpc_addr", cfg.GRPCAddr),
		slog.String("http_addr", cfg.HTTPAddr),
	)

	ctx, cancel := bootstrap.SignalContext()
	defer cancel()

	// Postgres pool.
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

	// JWT signer/verifier (HS256).
	signer := jwtx.NewSigner([]byte(cfg.JWTSecret), auth.JWTIssuer)
	verifier := jwtx.NewVerifier([]byte(cfg.JWTSecret))

	// Store + Service.
	st := store.New(pool)
	svc, err := auth.NewService(st, auth.Config{
		Signer:     signer,
		Verifier:   verifier,
		AccessTTL:  time.Duration(cfg.AccessTTLSeconds) * time.Second,
		RefreshTTL: time.Duration(cfg.RefreshTTLSeconds) * time.Second,
		Argon2: auth.Argon2Params{
			MemoryKiB:   cfg.Argon2Memory,
			Iterations:  cfg.Argon2Iterations,
			Parallelism: cfg.Argon2Parallelism,
		},
		Logger: lg,
	})
	if err != nil {
		return err
	}

	// Prometheus-метрики: общие gRPC/DB-латенси даёт UnaryServerInterceptor,
	// сервис-специфичные counters (auth_logins_total{result}, auth_register_total{result})
	// инкрементятся самим Service.NewCounterVec через коллекторов в Registry.
	mreg := metrics.Init("auth")

	// gRPC server c metrics interceptor.
	grpcSrv := grpc.NewServer(
		grpc.UnaryInterceptor(mreg.UnaryServerInterceptor()),
	)
	authv1.RegisterAuthServiceServer(grpcSrv, svc)

	// HTTP server (healthz/readyz/metrics).
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
			lg.Info("auth-svc shutting down")
			return nil
		},
	})
}
