// HTTP middleware для Prometheus-инструментации.
//
// Используется gateway-svc как middleware в chi: подсчитывает HTTP-запросы
// и длительность по (route, status). Route — это chi.RouteContext.RoutePattern()
// либо r.URL.Path как fallback (чтобы не было cardinality-взрыва от id'шек).

package metrics

import (
	"net/http"
	"strconv"
	"time"
)

// HTTPMiddleware возвращает middleware, считающий HTTPRequestsTotal и
// HTTPRequestDuration.
//
// routeFn — функция, возвращающая «нормализованный» путь для метки route
// (например, chi.RouteContext(r.Context()).RoutePattern()). Если nil или
// возвращает пустую строку — используется r.URL.Path (потенциальная
// высокая cardinality, использовать с осторожностью).
//
// /metrics, /healthz, /readyz исключены: их вызывает Prometheus каждые
// несколько секунд, шумят в дашбордах и не несут полезной телеметрии о
// бизнес-трафике.
func (r *Registry) HTTPMiddleware(routeFn func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			path := req.URL.Path
			switch path {
			case "/metrics", "/healthz", "/readyz":
				next.ServeHTTP(w, req)
				return
			}

			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, req)
			dur := time.Since(start).Seconds()

			route := path
			if routeFn != nil {
				if p := routeFn(req); p != "" {
					route = p
				}
			}
			r.HTTPRequestsTotal.WithLabelValues(route, strconv.Itoa(rec.status)).Inc()
			r.HTTPRequestDuration.WithLabelValues(route).Observe(dur)
		})
	}
}

// statusRecorder — приватный http.ResponseWriter wrapper для захвата
// финального HTTP-статуса.
//
// Дублирует gateway-svc/internal/http.statusRecorder намеренно: мы не хотим
// тянуть зависимость pkg → service (только service → pkg), а вынести оба в
// pkg/httpx — отдельный refactor вне scope wave 5.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

// WriteHeader кэширует первый статус.
func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Flush проксирует Flush к нижележащему writer (нужно для SSE).
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
