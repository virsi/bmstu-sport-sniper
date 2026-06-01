// Package config — env-конфигурация notifier-svc.
package config

import (
	"errors"

	basecfg "github.com/fizcultor/backend/pkg/config"
)

// Config — параметры notifier-svc.
type Config struct {
	basecfg.Base

	// TGBotToken — токен Telegram-бота.
	TGBotToken string `env:"TG_BOT_TOKEN,required"`
	// TGUseWebhook — webhook (true) или long-poll (false). По умолчанию long-poll.
	TGUseWebhook bool `env:"TG_USE_WEBHOOK" envDefault:"false"`
	// TGWebhookURL — публичный URL для webhook (если TGUseWebhook).
	TGWebhookURL string `env:"TG_WEBHOOK_URL"`

	// AuthGRPCAddr — адрес auth-svc для GetMe / LinkTelegramComplete.
	AuthGRPCAddr string `env:"AUTH_GRPC_ADDR" envDefault:"auth-svc:9090"`
	// TeachersGRPCAddr — адрес teachers-svc для BatchGet рейтингов.
	TeachersGRPCAddr string `env:"TEACHERS_GRPC_ADDR" envDefault:"teachers-svc:9090"`
}

// Load парсит env и валидирует.
func Load() (*Config, error) {
	cfg, err := basecfg.Load[Config]()
	if err != nil {
		return nil, err
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = "notifier-svc"
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Validate валидирует Config.
func (c *Config) Validate() error {
	if err := c.Base.Validate(); err != nil {
		return err
	}
	if c.TGUseWebhook && c.TGWebhookURL == "" {
		return errors.New("config: TG_WEBHOOK_URL is required when TG_USE_WEBHOOK=true")
	}
	if c.NATSURL == "" {
		return errors.New("config: NATS_URL is required for SSE bridge")
	}
	return nil
}
