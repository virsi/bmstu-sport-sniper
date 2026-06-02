// Command server — точка входа bmstu-svc.
//
// Хранит BMSTU-кредсы (AES-256-GCM) и LKS-сессии (cookiejar + gob).
// Реализует Keycloak OIDC через pure HTTP (без браузера). gRPC API.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"google.golang.org/grpc"

	svccfg "github.com/fizcultor/backend/services/bmstu-svc/internal/config"

	bmstuv1 "github.com/fizcultor/backend/gen/bmstu/v1"

	"github.com/fizcultor/backend/pkg/bootstrap"
	"github.com/fizcultor/backend/pkg/crypto"
	"github.com/fizcultor/backend/pkg/events"
	"github.com/fizcultor/backend/pkg/logger"
	"github.com/fizcultor/backend/pkg/metrics"
	"github.com/fizcultor/backend/pkg/pgxutil"

	"github.com/fizcultor/backend/services/bmstu-svc/internal/groups"
	"github.com/fizcultor/backend/services/bmstu-svc/internal/oidc"
	"github.com/fizcultor/backend/services/bmstu-svc/internal/server"
	"github.com/fizcultor/backend/services/bmstu-svc/internal/session"
	"github.com/fizcultor/backend/services/bmstu-svc/internal/store"
)

// errReadinessNATS — sentinel для /readyz сообщения "dep nats: ...".
var errReadinessNATS = errors.New("nats not connected")

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
	lg.Info("bmstu-svc starting",
		slog.String("env", string(cfg.Env)),
		slog.String("grpc_addr", cfg.GRPCAddr),
		slog.Bool("oidc_use_browser", cfg.OIDCUseBrowser),
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

	var bus *events.Bus
	if cfg.NATSURL != "" {
		bus, err = events.Connect(ctx, events.Config{URL: cfg.NATSURL, Name: cfg.ServiceName})
		if err != nil {
			return err
		}
		defer bus.Close()
		lg.Info("nats connected")
	}

	// Decode master key (валидировано в Config.Validate, повторяем для безопасности).
	masterKey, err := crypto.KeyFromHex(cfg.AESMasterKeyHex)
	if err != nil {
		return err
	}

	queries := store.New(pool)

	oidcClient, err := oidc.New(
		oidc.WithBaseURL(cfg.LKSBaseURL),
		oidc.WithTimeout(time.Duration(cfg.HTTPClientTimeoutSeconds)*time.Second),
	)
	if err != nil {
		return err
	}

	manager, err := session.New(queries, oidcClient, session.Config{
		MasterKey:  masterKey,
		LKSBaseURL: cfg.LKSBaseURL,
	}, func() (*http.Client, error) {
		return &http.Client{Timeout: time.Duration(cfg.HTTPClientTimeoutSeconds) * time.Second}, nil
	})
	if err != nil {
		return err
	}

	groupsClient := groups.New(cfg.LKSBaseURL)

	bmstuSrv, err := server.New(queries, manager, oidcClient, groupsClient, server.Config{
		MasterKey:   masterKey,
		SemesterFor: cfg.SemesterUUIDFor,
		Logger:      lg,
	})
	if err != nil {
		return err
	}

	// Prometheus метрики + gRPC interceptor.
	mreg := metrics.Init("bmstu")

	grpcSrv := grpc.NewServer(
		grpc.UnaryInterceptor(mreg.UnaryServerInterceptor()),
	)
	bmstuv1.RegisterBmstuServiceServer(grpcSrv, bmstuSrv)

	health := bootstrap.NewHealthHandler()
	health.AttachMetrics(mreg.Handler())
	health.AddCheck("postgres", func(ctx context.Context) error { return pool.Ping(ctx) })
	if bus != nil {
		// NATS readiness: считаем, что Connect успешен ⇒ соединение установлено.
		// nats.Conn.IsConnected() — best-effort; при reconnect транзитно false.
		health.AddCheck("nats", func(_ context.Context) error {
			if !bus.Conn().IsConnected() {
				return errReadinessNATS
			}
			return nil
		})
	}
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
			lg.Info("bmstu-svc shutting down")
			return nil
		},
	})
}
