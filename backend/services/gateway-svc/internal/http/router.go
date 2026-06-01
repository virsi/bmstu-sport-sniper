// Package http собирает HTTP-роутер gateway-svc.
//
// Цепочка middleware (порядок важен):
//
//	Recover → RequestID → CORS → RateLimit → [Auth?] → handler
//
// Группа /api/* монтируется с Auth-middleware кроме открытых ручек
// (register/login/refresh). Stream имеет свой обработчик query-fallback'а.
package http

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/fizcultor/backend/pkg/bootstrap"
	"github.com/fizcultor/backend/pkg/httpx"

	"github.com/fizcultor/backend/services/gateway-svc/internal/http/handler"
	"github.com/fizcultor/backend/services/gateway-svc/internal/http/middleware"
)

// Config — параметры роутера.
type Config struct {
	// CORSAllowedOrigins — список разрешённых origin'ов (через запятую в env).
	CORSAllowedOrigins []string
	// RateLimitRPS — допустимых запросов в секунду на ключ.
	RateLimitRPS float64
	// RateLimitBurst — пик token-bucket.
	RateLimitBurst int
	// HealthHandler — общий /healthz и /readyz из pkg/bootstrap.
	HealthHandler *bootstrap.HealthHandler
}

// NewRouter собирает chi-роутер с подключёнными middleware и хэндлерами.
//
// auth — middleware-фабрика; используется как mw в защищённых группах.
// sseAuth — middleware для /api/stream, допускает ticket ИЛИ JWT.
// Если sseAuth == nil — fallback на обычный auth (старое поведение).
// h — собранный набор хэндлеров (handler.New).
func NewRouter(cfg Config, h *handler.Handler, auth, sseAuth func(http.Handler) http.Handler) http.Handler {
	r := chi.NewRouter()

	// Глобальные cross-cutting middleware.
	r.Use(httpx.Recover)
	r.Use(httpx.RequestID)
	r.Use(httpx.CORS(httpx.CORSConfig{
		AllowedOrigins:   cfg.CORSAllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Authorization", "Content-Type", "X-Request-ID"},
		AllowCredentials: true,
		MaxAge:           600,
	}))
	r.Use(httpx.RateLimit(httpx.RateLimitConfig{
		RPS:     cfg.RateLimitRPS,
		Burst:   cfg.RateLimitBurst,
		KeyFunc: rateLimitKey,
	}))
	r.Use(accessLog)

	// Health + readiness + metrics — без middleware-логирования секретов.
	// Все три ручки разделяют один HealthHandler.Mux(), который маршрутит по
	// path внутри себя.
	if cfg.HealthHandler != nil {
		mux := cfg.HealthHandler.Mux()
		r.Handle("/healthz", mux)
		r.Handle("/readyz", mux)
		r.Handle("/metrics", mux)
	}

	// Fallback: если caller не передал sseAuth — используем обычный auth.
	// Это сохраняет backward-compat при упрощённой инициализации (тесты, минимум).
	if sseAuth == nil {
		sseAuth = auth
	}

	// API group.
	r.Route("/api", func(api chi.Router) {
		// Открытые ручки — без Auth, но с rate-limit (наследуется).
		api.Route("/auth", func(a chi.Router) {
			a.Post("/register", h.Register)
			a.Post("/login", h.Login)
			a.Post("/refresh", h.Refresh)
			// Logout требует Auth — отдельный protected обработчик ниже.
		})

		// Защищённые ручки — обычная Auth middleware (JWT only).
		api.Group(func(p chi.Router) {
			p.Use(auth)

			p.Post("/auth/logout", h.Logout)

			p.Get("/me", h.Me)
			p.Post("/me/telegram/init", h.TelegramInit)

			p.Post("/bmstu/creds", h.BmstuStoreCreds)
			p.Get("/bmstu/status", h.BmstuStatus)
			p.Delete("/bmstu/creds", h.BmstuDeleteCreds)

			p.Get("/filters", h.ListFilters)
			p.Post("/filters", h.CreateFilter)
			p.Patch("/filters/{id}", h.UpdateFilter)
			p.Delete("/filters/{id}", h.DeleteFilter)

			p.Get("/slots", h.Slots)

			// Выпуск одноразового ticket для последующего открытия /api/stream.
			p.Post("/stream/ticket", h.IssueStreamTicket)
		})

		// SSE-ручка отдельная: ticket-OR-JWT middleware.
		api.Group(func(s chi.Router) {
			s.Use(sseAuth)
			s.Get("/stream", h.Stream)
		})
	})

	return r
}

// rateLimitKey — ключ для лимитера. Приоритет: Authorization-header (user-scoped),
// иначе X-Forwarded-For / RemoteAddr (IP-scoped).
//
// На SSE-стрим (долгоживущий) лимитер срабатывает только на initial connect.
func rateLimitKey(r *http.Request) string {
	if h := r.Header.Get(middleware.HeaderAuthorization); h != "" {
		// Используем сам заголовок как ключ — равные токены = равные ключи,
		// разные токены = разные ключи. Достаточно для KISS.
		return "auth:" + h
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return "ip:" + xff
	}
	return "ip:" + r.RemoteAddr
}

// accessLog — минималистичный access-логгер. Не пишет body/headers/токены.
// Логирует: method, path, status, длительность.
func accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		// Не логируем `/healthz` и SSE keep-alive — низкий signal/noise.
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			return
		}
		//nolint:gosec // G706: tainted path в типизированном slog-атрибуте безопасно.
		slog.Info("http",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rw.status),
			slog.Duration("dur", time.Since(start)),
			slog.String("trace_id", httpx.RequestIDFrom(r.Context())),
		)
	})
}

// statusRecorder — http.ResponseWriter wrapper для захвата записанного status.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

// WriteHeader — кэширует первый статус (chi/middleware ниже могут писать ещё раз).
func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Flush проксирует Flush к нижележащему writer'у (нужно для SSE).
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
