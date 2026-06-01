// Package config содержит общие части конфигурации, разделяемые между всеми
// сервисами: env (dev/prod), log-level, NATS URL, Postgres DSN, gRPC-порты.
// Per-service env-структуры эмбедят Base и добавляют свои поля.
package config

import (
	"errors"
	"fmt"

	"github.com/caarlos0/env/v11"
)

// Env — режим окружения.
type Env string

const (
	// EnvDev — локальная разработка.
	EnvDev Env = "dev"
	// EnvProd — production.
	EnvProd Env = "prod"
)

// Base — общие поля конфигурации для любого сервиса.
//
// Сервисная конфигурация эмбедит Base:
//
//	type Config struct {
//	    config.Base
//	    AuthGRPCAddr string `env:"AUTH_GRPC_ADDR"`
//	}
type Base struct {
	// Env — режим (dev/prod). Влияет на формат логов и debug-поведение.
	Env Env `env:"APP_ENV" envDefault:"dev"`
	// ServiceName — имя сервиса, попадает в логи как атрибут "service".
	ServiceName string `env:"SERVICE_NAME"`
	// LogLevel — debug/info/warn/error.
	LogLevel string `env:"LOG_LEVEL" envDefault:"info"`

	// PostgresDSN — DSN основной БД сервиса.
	PostgresDSN string `env:"POSTGRES_DSN"`
	// NATSURL — адрес NATS (пустой = без NATS).
	NATSURL string `env:"NATS_URL"`

	// GRPCAddr — адрес, на котором сервис слушает gRPC, например ":9091".
	GRPCAddr string `env:"GRPC_ADDR" envDefault:":9090"`
	// HTTPAddr — адрес HTTP-сервера (для gateway и healthz endpoints).
	HTTPAddr string `env:"HTTP_ADDR" envDefault:":8080"`
}

// Validate проверяет общие обязательные поля Base.
// Сервисы могут вызывать Base.Validate() и докинуть свои проверки.
func (b *Base) Validate() error {
	if b.ServiceName == "" {
		return errors.New("config: SERVICE_NAME is required")
	}
	if b.Env != EnvDev && b.Env != EnvProd {
		return fmt.Errorf("config: invalid APP_ENV %q (want dev|prod)", b.Env)
	}
	return nil
}

// Load — обобщённый загрузчик: парсит env-теги в *T и возвращает результат.
// Использует caarlos0/env/v11. Не вызывает Validate — это ответственность caller.
//
// Пример:
//
//	cfg, err := config.Load[Config]()
func Load[T any]() (*T, error) {
	var cfg T
	if err := env.Parse(&cfg); err != nil {
		return nil, fmt.Errorf("config: parse env: %w", err)
	}
	return &cfg, nil
}
