package bootstrap_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fizcultor/backend/pkg/bootstrap"
)

func TestHealthHandler_Healthz_AlwaysOK(t *testing.T) {
	t.Parallel()

	h := bootstrap.NewHealthHandler()
	srv := httptest.NewServer(h.Mux())
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz=%d, want 200", resp.StatusCode)
	}
}

func TestHealthHandler_Readyz_503UntilReady(t *testing.T) {
	t.Parallel()

	h := bootstrap.NewHealthHandler()
	srv := httptest.NewServer(h.Mux())
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("readyz before SetReady = %d, want 503", resp.StatusCode)
	}
	_ = resp.Body.Close()

	h.SetReady(true)
	resp, err = http.Get(srv.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("readyz after SetReady = %d, want 200", resp.StatusCode)
	}
}

func TestHealthHandler_Readyz_RunsChecks(t *testing.T) {
	t.Parallel()

	h := bootstrap.NewHealthHandler()
	h.SetReady(true)

	h.AddCheck("db", func(_ context.Context) error { return nil })
	h.AddCheck("nats", func(_ context.Context) error { return errors.New("disconnected") })

	srv := httptest.NewServer(h.Mux())
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("readyz with failed dep = %d, want 503", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !contains(string(body), "nats") {
		t.Fatalf("readyz body should mention failed dep, got %q", string(body))
	}
}

func TestHealthHandler_Metrics_AttachAndServe(t *testing.T) {
	t.Parallel()

	h := bootstrap.NewHealthHandler()
	srv := httptest.NewServer(h.Mux())
	t.Cleanup(srv.Close)

	// Before AttachMetrics — 404.
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("metrics before Attach = %d, want 404", resp.StatusCode)
	}
	_ = resp.Body.Close()

	h.AttachMetrics(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("# HELP test\n"))
	}))

	resp, err = http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("metrics after Attach = %d, want 200", resp.StatusCode)
	}
}

// contains — простой strings.Contains, продублирован чтобы не тянуть импорт
// в маленьком тесте.
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
