package oidc

import (
	"net/http"
	"net/http/cookiejar"
	"time"
)

// Константы Keycloak/LKS, вынесенные сюда, чтобы у тестов был
// один источник истины. Реальные URL зашиты намеренно: эмулируем
// конкретного апстрима, абстракция «провайдер OIDC» здесь излишня.
const (
	// DefaultUserAgent — заголовок User-Agent для имитации браузера.
	// Keycloak отказывается отдавать форму на Go-http-client/1.1.
	DefaultUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

	// DefaultLKSBaseURL — основной фронт BMSTU (portal4 + UI).
	DefaultLKSBaseURL = "https://lks.bmstu.ru"

	// LoginInitPath — стартовая точка флоу portal4; редиректит на Keycloak.
	LoginInitPath = "/portal4/cookie/login"

	// WatchdogPath — health-check сессии portal4.
	WatchdogPath = "/portal4/cookie/watchdog"

	// defaultMaxRedirects — Go-дефолт; явно для документации.
	defaultMaxRedirects = 10
)

// ClientOption — функциональная опция конструктора Client.
type ClientOption func(*Client)

// WithBaseURL переопределяет LKS base URL (нужно тестам).
func WithBaseURL(base string) ClientOption {
	return func(c *Client) { c.baseURL = base }
}

// WithHTTPClient подменяет http.Client (например для пользовательского Transport).
func WithHTTPClient(h *http.Client) ClientOption {
	return func(c *Client) { c.http = h }
}

// WithUserAgent подменяет UA.
func WithUserAgent(ua string) ClientOption {
	return func(c *Client) { c.userAgent = ua }
}

// WithTimeout задаёт таймаут на один HTTP-запрос.
// 0 → без таймаута (только context.Context).
func WithTimeout(d time.Duration) ClientOption {
	return func(c *Client) { c.timeout = d }
}

// Client — pure-HTTP клиент BMSTU SSO.
//
// Хранит свой cookiejar на одну OIDC-сессию. После Login можно дёргать
// Jar() для получения cookies, сохранения и реюза.
type Client struct {
	http      *http.Client
	baseURL   string
	userAgent string
	timeout   time.Duration
}

// New строит Client с пустым cookiejar, дефолтным UA и таймаутом 15s.
//
// Если *http.Client не передан явно, создаётся со своим Jar и
// CheckRedirect, ограничивающим цепочку 10 переходами.
func New(opts ...ClientOption) (*Client, error) {
	c := &Client{
		baseURL:   DefaultLKSBaseURL,
		userAgent: DefaultUserAgent,
		timeout:   15 * time.Second,
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.http == nil {
		jar, err := cookiejar.New(nil)
		if err != nil {
			return nil, err
		}
		c.http = &http.Client{
			Jar:           jar,
			Timeout:       c.timeout,
			CheckRedirect: limitRedirects(defaultMaxRedirects),
		}
	} else if c.http.Jar == nil {
		jar, err := cookiejar.New(nil)
		if err != nil {
			return nil, err
		}
		c.http.Jar = jar
	}
	return c, nil
}

// HTTP возвращает внутренний *http.Client (с тем же Jar).
// Полезно для последующих API-запросов с теми же cookies (groups-клиент).
func (c *Client) HTTP() *http.Client { return c.http }

// Jar возвращает cookiejar клиента.
func (c *Client) Jar() http.CookieJar { return c.http.Jar }

// BaseURL возвращает текущий base URL.
func (c *Client) BaseURL() string { return c.baseURL }

// UserAgent возвращает строку UA.
func (c *Client) UserAgent() string { return c.userAgent }

// limitRedirects возвращает CheckRedirect, обрывающий цепочку после n переходов.
func limitRedirects(n int) func(req *http.Request, via []*http.Request) error {
	return func(_ *http.Request, via []*http.Request) error {
		if len(via) >= n {
			return http.ErrUseLastResponse
		}
		return nil
	}
}
