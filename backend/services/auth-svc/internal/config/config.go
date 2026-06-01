// Package config — env-конфигурация auth-svc.
package config

import (
	"errors"

	basecfg "github.com/fizcultor/backend/pkg/config"
)

// Config — параметры auth-svc.
type Config struct {
	basecfg.Base

	// JWTSecret — HMAC-ключ для подписи access/refresh JWT (HS256). >=32 байта.
	JWTSecret string `env:"JWT_SECRET,required"`
	// AccessTTLSeconds — TTL access-токена в секундах. По умолчанию 900 (15 мин).
	AccessTTLSeconds int `env:"JWT_ACCESS_TTL_SECONDS" envDefault:"900"`
	// RefreshTTLSeconds — TTL refresh-токена. По умолчанию 2592000 (30 дней).
	RefreshTTLSeconds int `env:"JWT_REFRESH_TTL_SECONDS" envDefault:"2592000"`

	// Argon2Memory — память argon2id в KiB.
	Argon2Memory uint32 `env:"ARGON2_MEMORY_KIB" envDefault:"65536"`
	// Argon2Iterations — iterations argon2id.
	Argon2Iterations uint32 `env:"ARGON2_ITERATIONS" envDefault:"3"`
	// Argon2Parallelism — параллелизм argon2id.
	Argon2Parallelism uint8 `env:"ARGON2_PARALLELISM" envDefault:"2"`
}

// Load парсит env в Config и валидирует.
func Load() (*Config, error) {
	cfg, err := basecfg.Load[Config]()
	if err != nil {
		return nil, err
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = "auth-svc"
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Validate валидирует Config (вызывает Base.Validate + свои проверки).
func (c *Config) Validate() error {
	if err := c.Base.Validate(); err != nil {
		return err
	}
	if len(c.JWTSecret) < 32 {
		return errors.New("config: JWT_SECRET must be >= 32 bytes")
	}
	if c.PostgresDSN == "" {
		return errors.New("config: POSTGRES_DSN is required")
	}
	return nil
}
