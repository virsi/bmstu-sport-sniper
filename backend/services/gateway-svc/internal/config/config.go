// Package config — env-конфигурация gateway-svc.
package config

import (
	"errors"
	"time"

	basecfg "github.com/fizcultor/backend/pkg/config"
)

// Config — параметры gateway-svc.
type Config struct {
	basecfg.Base

	// JWTSecret — секрет для верификации JWT, выпущенных auth-svc.
	// Используется для локальной верификации подписи в SSE-эндпоинте
	// (быстрый путь, без сетевого вызова auth-svc). Полные защищённые ручки
	// дополнительно вызывают AuthService.VerifyAccess для проверки revoke.
	JWTSecret string `env:"JWT_SECRET,required"`

	// CORSAllowedOrigins — список разрешённых origin'ов (через запятую).
	CORSAllowedOrigins []string `env:"CORS_ALLOWED_ORIGINS" envSeparator:"," envDefault:"http://localhost:5173"`

	// RateLimitRPS — лимит запросов в секунду на IP.
	RateLimitRPS float64 `env:"RATE_LIMIT_RPS" envDefault:"10"`
	// RateLimitBurst — размер всплеска.
	RateLimitBurst int `env:"RATE_LIMIT_BURST" envDefault:"20"`

	// BotUsername — Telegram bot username (без `@`), используется для
	// rewrite deeplink из tg://start?token=X в https://t.me/<bot>?start=X.
	BotUsername string `env:"BOT_USERNAME" envDefault:"FizcultorBot"`

	// SlotsEndpointEnabled — включает онлайн-запрос /api/slots напрямую в bmstu-svc.
	// Если false (KISS-дефолт), эндпоинт возвращает пустой массив.
	SlotsEndpointEnabled bool `env:"SLOTS_ENDPOINT_ENABLED" envDefault:"false"`

	// SlotsFetchTimeout — таймаут запроса к bmstu-svc.FetchGroups.
	SlotsFetchTimeout time.Duration `env:"SLOTS_FETCH_TIMEOUT" envDefault:"5s"`

	// Адреса нижестоящих gRPC-сервисов.
	AuthGRPCAddr     string `env:"AUTH_GRPC_ADDR" envDefault:"auth-svc:9090"`
	BmstuGRPCAddr    string `env:"BMSTU_GRPC_ADDR" envDefault:"bmstu-svc:9090"`
	FilterGRPCAddr   string `env:"FILTER_GRPC_ADDR" envDefault:"filter-svc:9090"`
	NotifierGRPCAddr string `env:"NOTIFIER_GRPC_ADDR" envDefault:"notifier-svc:9090"`
	TeachersGRPCAddr string `env:"TEACHERS_GRPC_ADDR" envDefault:"teachers-svc:9090"`

	// CookieSecure — выставлять ли флаг Secure на refresh-token cookie.
	// В prod ОБЯЗАТЕЛЬНО true (cookie ходит только по HTTPS). В dev (http://localhost)
	// браузер игнорирует Secure-cookie → ставить false, иначе фронт не получит refresh.
	CookieSecure bool `env:"COOKIE_SECURE" envDefault:"false"`

	// CookieDomain — Domain-атрибут refresh-token cookie. Пустая строка ⇒ cookie
	// привязан к origin, который выдал её (host-only). Для prod, где фронт и API
	// делят один apex-домен, можно оставить пустым; для cross-subdomain (api.example.com
	// + app.example.com) — выставить ".example.com".
	CookieDomain string `env:"COOKIE_DOMAIN" envDefault:""`

	// SSETicketTTL — время жизни one-time ticket для SSE-подключения.
	// 5 минут — компромисс: достаточно для медленных мобильных клиентов,
	// мало для случайной утечки через access-log/screen-share.
	SSETicketTTL time.Duration `env:"SSE_TICKET_TTL" envDefault:"5m"`
}

// Load парсит env и валидирует.
func Load() (*Config, error) {
	cfg, err := basecfg.Load[Config]()
	if err != nil {
		return nil, err
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = "gateway-svc"
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
	if len(c.JWTSecret) < 32 {
		return errors.New("config: JWT_SECRET must be >= 32 bytes")
	}
	if c.NATSURL == "" {
		return errors.New("config: NATS_URL is required for SSE bridge")
	}
	return nil
}
