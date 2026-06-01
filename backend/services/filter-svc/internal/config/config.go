// Package config — env-конфигурация filter-svc.
package config

import (
	"errors"

	basecfg "github.com/fizcultor/backend/pkg/config"
)

// Config — параметры filter-svc.
type Config struct {
	basecfg.Base
}

// Load парсит env и валидирует.
func Load() (*Config, error) {
	cfg, err := basecfg.Load[Config]()
	if err != nil {
		return nil, err
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = "filter-svc"
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
	if c.PostgresDSN == "" {
		return errors.New("config: POSTGRES_DSN is required")
	}
	return nil
}
