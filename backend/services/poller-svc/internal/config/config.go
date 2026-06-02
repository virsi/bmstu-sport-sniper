// Package config — env-конфигурация poller-svc.
package config

import (
	basecfg "github.com/fizcultor/backend/pkg/config"
)

// Config — параметры poller-svc.
type Config struct {
	basecfg.Base

	// IntervalSeconds — интервал основного тикера, по умолчанию 60s.
	IntervalSeconds int `env:"POLL_INTERVAL_SECONDS" envDefault:"60"`
	// JitterSeconds — случайный jitter ±N сек, антибан.
	JitterSeconds int `env:"POLL_JITTER_SECONDS" envDefault:"15"`
	// PerUserJitterSeconds — случайная задержка для каждого юзера 0..N сек.
	PerUserJitterSeconds int `env:"POLL_PER_USER_JITTER_SECONDS" envDefault:"3"`
	// Concurrency — лимит параллельных опросов юзеров.
	Concurrency int `env:"POLL_CONCURRENCY" envDefault:"10"`
	// ActiveUserDays — юзер считается активным если LastSeen<N дней.
	ActiveUserDays int `env:"ACTIVE_USER_DAYS" envDefault:"7"`

	// UserIDs — comma-separated user_id'ы для опроса. Временный stub до
	// появления filter-svc.ListActiveUsers RPC.
	UserIDs string `env:"POLL_USER_IDS"`

	// Адреса gRPC-сервисов (клиенты).
	BmstuGRPCAddr    string `env:"BMSTU_GRPC_ADDR" envDefault:"bmstu-svc:9090"`
	FilterGRPCAddr   string `env:"FILTER_GRPC_ADDR" envDefault:"filter-svc:9090"`
	NotifierGRPCAddr string `env:"NOTIFIER_GRPC_ADDR" envDefault:"notifier-svc:9090"`
	AuthGRPCAddr     string `env:"AUTH_GRPC_ADDR" envDefault:"auth-svc:9090"`
}

// Load парсит env и валидирует.
func Load() (*Config, error) {
	cfg, err := basecfg.Load[Config]()
	if err != nil {
		return nil, err
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = "poller-svc"
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Validate валидирует Config.
func (c *Config) Validate() error {
	return c.Base.Validate()
}
