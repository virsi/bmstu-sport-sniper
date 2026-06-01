// Package config — env-конфигурация bmstu-svc.
package config

import (
	"errors"

	basecfg "github.com/fizcultor/backend/pkg/config"
	"github.com/fizcultor/backend/pkg/crypto"
)

// Config — параметры bmstu-svc.
type Config struct {
	basecfg.Base

	// AESMasterKeyHex — 64-символьная hex-строка (32 байта) AES-256 для шифра
	// BMSTU-кредов и cookie-jar at-rest.
	AESMasterKeyHex string `env:"AES_MASTER_KEY,required"`

	// SemesterUUID — UUID семестра, подставляется в LKS API URL.
	SemesterUUID string `env:"SEMESTER_UUID,required"`

	// LKSBaseURL — базовый URL LKS BMSTU.
	LKSBaseURL string `env:"LKS_BASE_URL" envDefault:"https://lks.bmstu.ru"`

	// OIDCUseBrowser — fallback на chromedp, если pure HTTP сломается.
	OIDCUseBrowser bool `env:"OIDC_USE_BROWSER" envDefault:"false"`

	// HTTPClientTimeoutSeconds — таймаут запросов к LKS.
	HTTPClientTimeoutSeconds int `env:"HTTP_CLIENT_TIMEOUT_SECONDS" envDefault:"15"`
}

// Load парсит env и валидирует.
func Load() (*Config, error) {
	cfg, err := basecfg.Load[Config]()
	if err != nil {
		return nil, err
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = "bmstu-svc"
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
	if _, err := crypto.KeyFromHex(c.AESMasterKeyHex); err != nil {
		return err
	}
	return nil
}
