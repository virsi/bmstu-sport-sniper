//go:build integration

package testhelpers

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcnats "github.com/testcontainers/testcontainers-go/modules/nats"
	"github.com/testcontainers/testcontainers-go/wait"
)

// NATSContainer is a NATS server (JetStream-enabled) testcontainer with a
// ready connection bound to it.
type NATSContainer struct {
	// URL is the nats:// connection string.
	URL string
	// Conn is an already-connected *nats.Conn.
	Conn *nats.Conn
}

var (
	natsOnce sync.Once
	natsURL  string
	natsErr  error
)

// startSharedNATS boots one NATS container per test process; reused across
// tests for speed. Each test still gets its own *nats.Conn (multiple
// subjects don't collide because tests publish to user-scoped subjects).
func startSharedNATS(ctx context.Context) (string, error) {
	natsOnce.Do(func() {
		// notifier-svc публикует через core NATS (не JetStream) — флаг
		// `--jetstream` не нужен и в старых testcontainers-tcnats версиях
		// передаётся неправильно (контейнер падает с exit 1).
		c, err := tcnats.Run(ctx,
			"nats:2.10-alpine",
			testcontainers.WithWaitStrategy(
				wait.ForLog("Server is ready").
					WithStartupTimeout(30*time.Second),
			),
		)
		if err != nil {
			natsErr = err
			return
		}
		u, err := c.ConnectionString(ctx)
		if err != nil {
			natsErr = err
			return
		}
		natsURL = u
	})
	return natsURL, natsErr
}

// StartNATS returns a NATSContainer with a live connection. Both the
// connection and container teardown are wired into t.Cleanup.
func StartNATS(t *testing.T) *NATSContainer {
	t.Helper()
	ctx := context.Background()

	url, err := startSharedNATS(ctx)
	require.NoError(t, err, "start shared nats")

	conn, err := nats.Connect(url, nats.Timeout(3*time.Second))
	require.NoError(t, err, "connect to nats")

	t.Cleanup(func() {
		conn.Drain() //nolint:errcheck // best-effort drain at teardown
	})

	return &NATSContainer{URL: url, Conn: conn}
}
