//go:build integration

package testhelpers

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// bufSize is the bufconn buffer size in bytes. 1 MiB is more than enough
// for any single gRPC message in this codebase.
const bufSize = 1024 * 1024

// GRPCServer wraps an in-process gRPC server backed by bufconn together
// with a ready Dial helper.
type GRPCServer struct {
	// Server is the underlying *grpc.Server (call Server.RegisterService).
	Server *grpc.Server
	// Listener is the bufconn listener used by the server.
	Listener *bufconn.Listener
}

// StartGRPCServer creates an in-process gRPC server backed by bufconn.
// Caller must register services on the returned Server before calling
// Serve.
//
// Lifecycle: t.Cleanup gracefully stops the server.
func StartGRPCServer(t *testing.T) *GRPCServer {
	t.Helper()

	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()

	t.Cleanup(func() {
		srv.GracefulStop()
		_ = lis.Close()
	})

	return &GRPCServer{Server: srv, Listener: lis}
}

// Serve starts the gRPC server in a background goroutine. Call after
// registering services on the underlying Server.
func (g *GRPCServer) Serve(t *testing.T) {
	t.Helper()
	go func() {
		// grpc.Server.Serve blocks until GracefulStop is called.
		if err := g.Server.Serve(g.Listener); err != nil {
			// Cannot fail the test from a goroutine after t.Cleanup may have
			// already started — log only.
			t.Logf("grpc server stopped: %v", err)
		}
	}()
}

// Dial returns a *grpc.ClientConn connected to the bufconn-backed server.
// The connection is closed in t.Cleanup.
func (g *GRPCServer) Dial(t *testing.T) *grpc.ClientConn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) {
			return g.Listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	require.NoError(t, err, "dial bufconn")

	t.Cleanup(func() {
		_ = conn.Close()
	})

	return conn
}
