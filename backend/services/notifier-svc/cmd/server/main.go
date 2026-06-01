// Command server — точка входа notifier-svc.
//
// Bootstrap:
//   - load config (env)
//   - init logger
//   - dial gRPC clients (auth-svc, teachers-svc)
//   - connect NATS
//   - init TG bot (telebot.v3, без linker'а)
//   - init gRPC Server (NotifierService), реализует bot.LinkCompleter
//   - привязка linker'а к боту (SetLinker)
//   - start gRPC + HTTP healthz/readyz, TG-handler в фоне
//   - graceful shutdown по SIGINT/SIGTERM
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"google.golang.org/grpc"

	authv1 "github.com/fizcultor/backend/gen/auth/v1"
	notifierv1 "github.com/fizcultor/backend/gen/notifier/v1"
	teachersv1 "github.com/fizcultor/backend/gen/teachers/v1"

	"github.com/fizcultor/backend/pkg/bootstrap"
	"github.com/fizcultor/backend/pkg/events"
	"github.com/fizcultor/backend/pkg/grpcx"
	"github.com/fizcultor/backend/pkg/logger"
	"github.com/fizcultor/backend/pkg/metrics"

	"github.com/fizcultor/backend/services/notifier-svc/internal/bot"
	svccfg "github.com/fizcultor/backend/services/notifier-svc/internal/config"
	"github.com/fizcultor/backend/services/notifier-svc/internal/server"
)

// errReadinessNATS — sentinel-ошибка для /readyz dep nats.
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
	lg.Info("notifier-svc starting",
		slog.String("env", string(cfg.Env)),
		slog.Bool("tg_webhook", cfg.TGUseWebhook),
	)

	ctx, cancel := bootstrap.SignalContext()
	defer cancel()

	bus, err := events.Connect(ctx, events.Config{URL: cfg.NATSURL, Name: cfg.ServiceName})
	if err != nil {
		return err
	}
	defer bus.Close()
	lg.Info("nats connected")

	authConn, err := grpcx.DialInsecure(ctx, cfg.AuthGRPCAddr, grpcx.DialOptions{})
	if err != nil {
		return err
	}
	defer authConn.Close()

	teachersConn, err := grpcx.DialInsecure(ctx, cfg.TeachersGRPCAddr, grpcx.DialOptions{})
	if err != nil {
		return err
	}
	defer teachersConn.Close()

	authClient := authv1.NewAuthServiceClient(authConn)
	teachersClient := teachersv1.NewTeachersServiceClient(teachersConn)

	// Telegram bot создаётся без linker'а: Server-у нужен Bot как sender,
	// а Bot'у нужен Server как LinkCompleter — circular DI, решается SetLinker.
	tgBot, err := bot.New(bot.Config{
		Token:      cfg.TGBotToken,
		UseWebhook: cfg.TGUseWebhook,
		WebhookURL: cfg.TGWebhookURL,
	}, nil, lg)
	if err != nil {
		return err
	}

	srv, err := server.New(server.Deps{
		Auth:      authClient,
		Teachers:  teachersClient,
		Sender:    tgBot,
		Publisher: bus,
		Logger:    lg,
	})
	if err != nil {
		return err
	}
	tgBot.SetLinker(srv)

	// Prometheus метрики.
	mreg := metrics.Init("notifier")
	notifierSent := mreg.NewCounterVec(
		"sent_total",
		"Total notifications sent, by channel (telegram|sse) and result (ok|error).",
		[]string{"channel", "result"},
	)
	_ = notifierSent
	// server.Server инкрементит notifierSent через будущий Server.SetMetrics
	// hook; пока KISS — счётчик зарегистрирован, инкременты добавим по мере
	// необходимости. Главный поток инструментации — UnaryServerInterceptor.

	grpcSrv := grpc.NewServer(
		grpc.UnaryInterceptor(mreg.UnaryServerInterceptor()),
	)
	notifierv1.RegisterNotifierServiceServer(grpcSrv, srv)

	go tgBot.Start(ctx)
	lg.Info("telegram bot started", slog.Bool("webhook", cfg.TGUseWebhook))

	health := bootstrap.NewHealthHandler()
	health.AttachMetrics(mreg.Handler())
	health.AddCheck("nats", func(_ context.Context) error {
		if !bus.Conn().IsConnected() {
			return errReadinessNATS
		}
		return nil
	})
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
			lg.Info("notifier-svc shutting down")
			return nil
		},
	})
}
