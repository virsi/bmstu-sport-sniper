// Package metrics предоставляет общую Prometheus-инструментацию для всех
// сервисов fizcultor-bot.
//
// Дизайн:
//
//   - Каждый сервис вызывает Init(name) на старте; функция возвращает
//     *Registry с предзарегистрированными общими метриками (gRPC, БД,
//     in-flight) и Go runtime collector'ом. Сервис может добавить
//     service-specific Counter/Gauge/Histogram через Registry.Custom().
//   - Метрики имеют префикс <service>_ (auth_grpc_requests_total и т.п.),
//     что упрощает Grafana-дашборды без relabel.
//   - /metrics экспонируется через bootstrap.HealthHandler (тот же HTTP-сервер,
//     что и /healthz, /readyz) — KISS, один порт на сервис.
//
// Не использует глобальный prometheus.DefaultRegisterer — это позволяет
// поднимать несколько Registry в юнит-тестах без коллизий.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry — обёртка над *prometheus.Registry с предсобранными общими
// метриками для одного сервиса.
//
// Поля общих метрик публичны, чтобы middleware и хуки могли инкрементить их
// напрямую без хелперов.
type Registry struct {
	// reg — нижележащий prometheus-регистр.
	reg *prometheus.Registry
	// service — имя сервиса (используется как префикс метрик).
	service string

	// GRPCRequestsTotal — кол-во gRPC-запросов по (method, code).
	GRPCRequestsTotal *prometheus.CounterVec
	// GRPCRequestDuration — гистограмма длительности gRPC-запросов в секундах.
	GRPCRequestDuration *prometheus.HistogramVec
	// GRPCInflight — количество одновременно выполняющихся gRPC-запросов.
	GRPCInflight prometheus.Gauge

	// HTTPRequestsTotal — кол-во HTTP-запросов по (route, status).
	HTTPRequestsTotal *prometheus.CounterVec
	// HTTPRequestDuration — гистограмма длительности HTTP-запросов в секундах.
	HTTPRequestDuration *prometheus.HistogramVec

	// DBQueriesTotal — кол-во SQL-запросов по (query, status).
	// query — это имя/тег запроса, который caller указывает явно (например,
	// "users.GetByEmail"); pgx hooks могут передавать SQL prefix, но в
	// прод-логах хочется именованных меток.
	DBQueriesTotal *prometheus.CounterVec
	// DBQueryDuration — длительность SQL-запросов в секундах.
	DBQueryDuration *prometheus.HistogramVec
}

// defaultBuckets — гистограмма-бакеты для request latency в секундах.
// Покрывает разумный диапазон от 5 ms до 10 s, что подходит для большинства
// внутренних gRPC-вызовов и БД-запросов; для SSE-стрима (часы) гистограмма
// не нужна — там работает GRPCInflight/HTTPRequestsTotal.
var defaultBuckets = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}

// Init создаёт новый Registry для сервиса с указанным именем.
//
// service — короткое имя без суффикса -svc (например, "auth", "bmstu",
// "gateway"). Используется как префикс метрик.
//
// Метрики авто-регистрируются. Сервис может получить *prometheus.Registry
// через Registry.Reg() и доинициализировать service-specific метрики
// (см. Counter/Gauge/Histogram helpers).
func Init(service string) *Registry {
	reg := prometheus.NewRegistry()

	r := &Registry{
		reg:     reg,
		service: service,
		GRPCRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: service + "_grpc_requests_total",
				Help: "Total number of gRPC requests handled by " + service + ", labeled by method and gRPC status code.",
			},
			[]string{"method", "code"},
		),
		GRPCRequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    service + "_grpc_request_duration_seconds",
				Help:    "Duration of gRPC requests handled by " + service + ", labeled by method.",
				Buckets: defaultBuckets,
			},
			[]string{"method"},
		),
		GRPCInflight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: service + "_grpc_inflight_requests",
			Help: "Number of gRPC requests currently being processed by " + service + ".",
		}),
		HTTPRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: service + "_http_requests_total",
				Help: "Total HTTP requests handled by " + service + ", labeled by route and status.",
			},
			[]string{"route", "status"},
		),
		HTTPRequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    service + "_http_request_duration_seconds",
				Help:    "Duration of HTTP requests handled by " + service + ", labeled by route.",
				Buckets: defaultBuckets,
			},
			[]string{"route"},
		),
		DBQueriesTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: service + "_db_queries_total",
				Help: "Total number of DB queries issued by " + service + ", labeled by named query and status (ok|error).",
			},
			[]string{"query", "status"},
		),
		DBQueryDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    service + "_db_query_duration_seconds",
				Help:    "Duration of DB queries issued by " + service + ", labeled by named query.",
				Buckets: defaultBuckets,
			},
			[]string{"query"},
		),
	}

	reg.MustRegister(
		r.GRPCRequestsTotal,
		r.GRPCRequestDuration,
		r.GRPCInflight,
		r.HTTPRequestsTotal,
		r.HTTPRequestDuration,
		r.DBQueriesTotal,
		r.DBQueryDuration,
		// Go runtime + process metrics — полезно для алертов на FD/heap/GC.
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	return r
}

// Reg возвращает нижележащий *prometheus.Registry — полезно, если сервису
// нужно зарегистрировать собственные специфичные метрики.
func (r *Registry) Reg() *prometheus.Registry { return r.reg }

// Service возвращает имя сервиса, переданное в Init (используется для
// формирования имён service-specific метрик).
func (r *Registry) Service() string { return r.service }

// Handler возвращает http.Handler, экспонирующий /metrics в формате,
// совместимом с Prometheus scraper'ом.
func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.reg, promhttp.HandlerOpts{
		Registry:          r.reg,
		EnableOpenMetrics: true,
	})
}

// NewCounter регистрирует и возвращает service-specific Counter.
// Имя автоматически префиксуется именем сервиса.
//
// Паникует если метрика с таким именем уже зарегистрирована — это
// сигнализирует о баге в коде, а не runtime-ошибку.
func (r *Registry) NewCounter(name, help string) prometheus.Counter {
	c := prometheus.NewCounter(prometheus.CounterOpts{
		Name: r.service + "_" + name,
		Help: help,
	})
	r.reg.MustRegister(c)
	return c
}

// NewCounterVec регистрирует и возвращает service-specific CounterVec.
func (r *Registry) NewCounterVec(name, help string, labels []string) *prometheus.CounterVec {
	c := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: r.service + "_" + name,
		Help: help,
	}, labels)
	r.reg.MustRegister(c)
	return c
}

// NewGauge регистрирует и возвращает service-specific Gauge.
func (r *Registry) NewGauge(name, help string) prometheus.Gauge {
	g := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: r.service + "_" + name,
		Help: help,
	})
	r.reg.MustRegister(g)
	return g
}

// NewGaugeVec регистрирует и возвращает service-specific GaugeVec.
func (r *Registry) NewGaugeVec(name, help string, labels []string) *prometheus.GaugeVec {
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: r.service + "_" + name,
		Help: help,
	}, labels)
	r.reg.MustRegister(g)
	return g
}

// NewHistogram регистрирует Histogram с дефолтными бакетами.
func (r *Registry) NewHistogram(name, help string) prometheus.Histogram {
	h := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    r.service + "_" + name,
		Help:    help,
		Buckets: defaultBuckets,
	})
	r.reg.MustRegister(h)
	return h
}

// NewHistogramVec регистрирует HistogramVec с дефолтными бакетами.
func (r *Registry) NewHistogramVec(name, help string, labels []string) *prometheus.HistogramVec {
	h := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    r.service + "_" + name,
		Help:    help,
		Buckets: defaultBuckets,
	}, labels)
	r.reg.MustRegister(h)
	return h
}
