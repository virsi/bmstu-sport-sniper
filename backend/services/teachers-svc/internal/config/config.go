// Package config — env-конфигурация teachers-svc.
package config

import (
	"errors"

	basecfg "github.com/fizcultor/backend/pkg/config"
)

// Config — параметры teachers-svc.
type Config struct {
	basecfg.Base

	// BootstrapImport — импортировать embedded teachers.json при первом запуске
	// (если таблица teachers пуста).
	BootstrapImport bool `env:"BOOTSTRAP_IMPORT" envDefault:"true"`
}

// Load парсит env и валидирует.
func Load() (*Config, error) {
	cfg, err := basecfg.Load[Config]()
	if err != nil {
		return nil, err
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = "teachers-svc"
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
