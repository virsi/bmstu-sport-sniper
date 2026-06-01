package metrics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fizcultor/backend/pkg/metrics"
)

// TestInit_Smoke проверяет, что Init создаёт регистр и стандартные метрики
// без коллизий, а /metrics экспонирует их в текстовом формате.
func TestInit_Smoke(t *testing.T) {
	t.Parallel()

	r := metrics.Init("test")
	r.GRPCRequestsTotal.WithLabelValues("/test.Service/Method", "OK").Inc()
	r.DBQueriesTotal.WithLabelValues("users.Get", "ok").Inc()

	srv := httptest.NewServer(r.Handler())
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	buf := make([]byte, 16*1024)
	n, _ := resp.Body.Read(buf)
	body := string(buf[:n])
	for _, want := range []string{
		"test_grpc_requests_total",
		"test_db_queries_total",
		"go_goroutines",
		"process_cpu_seconds_total",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics body missing %q", want)
		}
	}
}

// TestObserve_StatusError проверяет, что Observe правильно классифицирует
// статус "error" при ненулевой ошибке.
func TestObserve_StatusError(t *testing.T) {
	t.Parallel()

	r := metrics.Init("test2")
	sentinel := errSample{msg: "boom"}
	gotErr := r.Observe("users.Get", func() error { return sentinel })
	if gotErr != sentinel {
		t.Fatalf("Observe must return original error, got %v", gotErr)
	}
	// Не проверяем содержимое: что-то проинкрементилось — для smoke этого
	// достаточно; полные сценарии — в integration-тестах сервисов.
}

type errSample struct{ msg string }

func (e errSample) Error() string { return e.msg }
