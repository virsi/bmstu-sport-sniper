// Package httpx предоставляет переиспользуемые HTTP-middleware: восстановление
// после паник, CORS, request-id, in-memory rate-limiter. Все middleware имеют
// сигнатуру func(http.Handler) http.Handler и совместимы с net/http и chi.
package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// ctxKey — тип для ключей контекста, чтобы избежать коллизий.
type ctxKey int

const (
	// ctxRequestID — ключ для request-id в context.Context.
	ctxRequestID ctxKey = iota
)

// HeaderRequestID — имя HTTP-заголовка с идентификатором запроса.
const HeaderRequestID = "X-Request-ID"

// RequestIDFrom возвращает request-id из контекста; пустая строка если не задан.
func RequestIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(ctxRequestID).(string)
	return v
}

// RequestID — middleware, генерирующий X-Request-ID если клиент не прислал.
// Сохраняет id в контексте (RequestIDFrom) и отдаёт обратно в response-header.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(HeaderRequestID)
		if id == "" {
			id = newID()
		}
		w.Header().Set(HeaderRequestID, id)
		ctx := context.WithValue(r.Context(), ctxRequestID, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// newID генерирует 16-символьный hex-id из crypto/rand.
func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Recover — middleware, перехватывающий panic, логирующий stack trace через
// slog.Default() и возвращающий 500 клиенту. Должен быть первым в цепочке.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				//nolint:gosec // G706: путь tainted, но это вся ценность recovery-лога,
				// никакой SQL/shell-инъекции тут нет — только slog с typed attrs.
				slog.Error("http: panic recovered",
					slog.Any("panic", rec),
					slog.String("path", r.URL.Path),
					slog.String("stack", string(debug.Stack())),
				)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// CORSConfig — параметры CORS-middleware.
type CORSConfig struct {
	// AllowedOrigins — список origin'ов. "*" разрешает все (не для credentialed-запросов).
	AllowedOrigins []string
	// AllowedMethods — методы для preflight, например []string{"GET","POST","PATCH"}.
	AllowedMethods []string
	// AllowedHeaders — заголовки, разрешённые в preflight.
	AllowedHeaders []string
	// AllowCredentials — разрешить cookies/Authorization в cross-origin запросах.
	AllowCredentials bool
	// MaxAge — кэширование preflight в секундах.
	MaxAge int
}

// CORS возвращает middleware с настройками cfg. На OPTIONS запросы отвечает 204.
func CORS(cfg CORSConfig) func(http.Handler) http.Handler {
	allowAll := len(cfg.AllowedOrigins) == 1 && cfg.AllowedOrigins[0] == "*"
	originSet := make(map[string]struct{}, len(cfg.AllowedOrigins))
	for _, o := range cfg.AllowedOrigins {
		originSet[o] = struct{}{}
	}
	methods := strings.Join(cfg.AllowedMethods, ", ")
	headers := strings.Join(cfg.AllowedHeaders, ", ")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				if allowAll {
					w.Header().Set("Access-Control-Allow-Origin", "*")
				} else if _, ok := originSet[origin]; ok {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Add("Vary", "Origin")
				}
			}
			if cfg.AllowCredentials {
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}

			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				if methods != "" {
					w.Header().Set("Access-Control-Allow-Methods", methods)
				}
				if headers != "" {
					w.Header().Set("Access-Control-Allow-Headers", headers)
				}
				if cfg.MaxAge > 0 {
					w.Header().Set("Access-Control-Max-Age", itoa(cfg.MaxAge))
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// itoa — без strconv чтобы не тянуть пакет в горячий путь; для маленьких int.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// RateLimitConfig — параметры лимитера.
type RateLimitConfig struct {
	// RPS — допустимое число запросов в секунду на ключ.
	RPS float64
	// Burst — размер всплеска (token-bucket capacity).
	Burst int
	// KeyFunc — функция, выделяющая ключ из запроса (IP, user-id, etc).
	//          Если nil — используется r.RemoteAddr.
	KeyFunc func(r *http.Request) string
	// CleanupInterval — как часто чистить устаревшие лимитеры. <=0 → 10 минут.
	CleanupInterval time.Duration
	// IdleTTL — TTL неактивного лимитера. <=0 → 15 минут.
	IdleTTL time.Duration
}

// RateLimit — middleware token-bucket per-key. Возвращает 429 при превышении.
// Для prod-нагрузки замените на Redis-backed лимитер.
func RateLimit(cfg RateLimitConfig) func(http.Handler) http.Handler {
	if cfg.KeyFunc == nil {
		cfg.KeyFunc = func(r *http.Request) string { return r.RemoteAddr }
	}
	if cfg.CleanupInterval <= 0 {
		cfg.CleanupInterval = 10 * time.Minute
	}
	if cfg.IdleTTL <= 0 {
		cfg.IdleTTL = 15 * time.Minute
	}
	l := newLimiterStore(cfg)
	go l.runCleanup()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := cfg.KeyFunc(r)
			if !l.allow(key) {
				http.Error(w, "too many requests", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// limiterStore — внутренняя in-memory мапа ключ→лимитер с TTL-cleanup.
type limiterStore struct {
	cfg RateLimitConfig
	mu  sync.Mutex
	m   map[string]*limiterEntry
}

type limiterEntry struct {
	lim  *rate.Limiter
	seen time.Time
}

func newLimiterStore(cfg RateLimitConfig) *limiterStore {
	return &limiterStore{cfg: cfg, m: make(map[string]*limiterEntry)}
}

func (s *limiterStore) allow(key string) bool {
	s.mu.Lock()
	e, ok := s.m[key]
	if !ok {
		e = &limiterEntry{
			lim: rate.NewLimiter(rate.Limit(s.cfg.RPS), s.cfg.Burst),
		}
		s.m[key] = e
	}
	e.seen = time.Now()
	s.mu.Unlock()
	return e.lim.Allow()
}

func (s *limiterStore) runCleanup() {
	t := time.NewTicker(s.cfg.CleanupInterval)
	defer t.Stop()
	for range t.C {
		cutoff := time.Now().Add(-s.cfg.IdleTTL)
		s.mu.Lock()
		for k, e := range s.m {
			if e.seen.Before(cutoff) {
				delete(s.m, k)
			}
		}
		s.mu.Unlock()
	}
}
