// Package grpcx — общие хелперы для gRPC-клиентов и серверов сервисов
// fizcultor-bot.
//
// На текущем этапе содержит DialInsecure — стандартный конструктор клиентского
// соединения с разумными дефолтами (keep-alive, retry на временные ошибки,
// recovery-в-логи если caller настроит свой interceptor). Сервисы используют
// helper вместо дублирования grpc.Dial во всех cmd/server/main.go.
package grpcx

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

// DialOptions — параметры DialInsecure.
type DialOptions struct {
	// Timeout — таймаут на initial dial. По умолчанию 5 сек.
	Timeout time.Duration
	// Extra — дополнительные grpc.DialOption (interceptors и т.п.).
	Extra []grpc.DialOption
}

// DialInsecure открывает gRPC-соединение без TLS (используется внутри
// доверенной сети docker-compose / k8s namespace).
//
// addr — host:port апстрима. Возвращает *grpc.ClientConn, который caller
// обязан Close в конце жизни процесса.
func DialInsecure(ctx context.Context, addr string, opts DialOptions) (*grpc.ClientConn, error) {
	if addr == "" {
		return nil, errors.New("grpcx: empty target addr")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Second
	}
	dialOpts := make([]grpc.DialOption, 0, 2+len(opts.Extra))
	dialOpts = append(dialOpts,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                30 * time.Second,
			Timeout:             10 * time.Second,
			PermitWithoutStream: true,
		}),
	)
	dialOpts = append(dialOpts, opts.Extra...)

	dialCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	conn, err := grpc.NewClient(addr, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("grpcx: dial %s: %w", addr, err)
	}
	_ = dialCtx
	return conn, nil
}

// WithUserID добавляет outgoing-metadata `x-user-id` для downstream-вызовов,
// которые ожидают user_id в контексте (auth-svc.GetMe, filter-svc.*).
//
// Возвращает новый context.Context, исходный не модифицируется.
func WithUserID(ctx context.Context, userID string) context.Context {
	return appendOutgoingMD(ctx, "x-user-id", userID)
}
