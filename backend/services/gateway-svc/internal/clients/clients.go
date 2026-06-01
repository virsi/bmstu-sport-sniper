// Package clients агрегирует gRPC-клиенты ко всем внутренним сервисам
// fizcultor-bot. Используется HTTP-хэндлерами gateway-svc как единая точка
// доступа: один Clients-объект инжектится в хэндлеры, не нужно тащить пять
// разных аргументов.
package clients

import (
	"context"
	"fmt"
	"io"

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	authv1 "github.com/fizcultor/backend/gen/auth/v1"
	bmstuv1 "github.com/fizcultor/backend/gen/bmstu/v1"
	filterv1 "github.com/fizcultor/backend/gen/filter/v1"
	notifierv1 "github.com/fizcultor/backend/gen/notifier/v1"
	teachersv1 "github.com/fizcultor/backend/gen/teachers/v1"
	"github.com/fizcultor/backend/pkg/grpcx"
)

// Addrs — адреса нижестоящих gRPC-сервисов (host:port).
type Addrs struct {
	// Auth — auth-svc.
	Auth string
	// Bmstu — bmstu-svc.
	Bmstu string
	// Filter — filter-svc.
	Filter string
	// Notifier — notifier-svc.
	Notifier string
	// Teachers — teachers-svc.
	Teachers string
}

// Clients — набор инициализированных gRPC-клиентов + соединения, которые
// нужно Close при shutdown. Не safe-for-concurrent-mutation после New;
// читать поля можно из любых горутин (gRPC-клиенты thread-safe).
type Clients struct {
	// Auth — клиент AuthService.
	Auth authv1.AuthServiceClient
	// Bmstu — клиент BmstuService.
	Bmstu bmstuv1.BmstuServiceClient
	// Filter — клиент FilterService.
	Filter filterv1.FilterServiceClient
	// Notifier — клиент NotifierService.
	Notifier notifierv1.NotifierServiceClient
	// Teachers — клиент TeachersService.
	Teachers teachersv1.TeachersServiceClient

	conns []io.Closer
}

// New открывает gRPC-соединения ко всем сервисам параллельно (errgroup).
// При ошибке хотя бы одного — закрывает уже открытые и возвращает ошибку.
//
// Соединения lazy: NewClient не блокирует на TCP-handshake, но возвращает
// ошибку при некорректном target.
func New(ctx context.Context, addrs Addrs) (*Clients, error) {
	type dialResult struct {
		name string
		conn *grpc.ClientConn
	}

	targets := []struct {
		name string
		addr string
	}{
		{"auth", addrs.Auth},
		{"bmstu", addrs.Bmstu},
		{"filter", addrs.Filter},
		{"notifier", addrs.Notifier},
		{"teachers", addrs.Teachers},
	}

	results := make([]dialResult, len(targets))
	g, gctx := errgroup.WithContext(ctx)
	for i, t := range targets {
		i, t := i, t
		g.Go(func() error {
			conn, err := grpcx.DialInsecure(gctx, t.addr, grpcx.DialOptions{})
			if err != nil {
				return fmt.Errorf("clients: dial %s (%s): %w", t.name, t.addr, err)
			}
			results[i] = dialResult{name: t.name, conn: conn}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		// На ошибке — закрыть успешно открытые, не оставлять висеть.
		for _, r := range results {
			if r.conn != nil {
				_ = r.conn.Close()
			}
		}
		return nil, err
	}

	c := &Clients{
		Auth:     authv1.NewAuthServiceClient(results[0].conn),
		Bmstu:    bmstuv1.NewBmstuServiceClient(results[1].conn),
		Filter:   filterv1.NewFilterServiceClient(results[2].conn),
		Notifier: notifierv1.NewNotifierServiceClient(results[3].conn),
		Teachers: teachersv1.NewTeachersServiceClient(results[4].conn),
		conns: []io.Closer{
			results[0].conn, results[1].conn, results[2].conn,
			results[3].conn, results[4].conn,
		},
	}
	return c, nil
}

// Close закрывает все gRPC-соединения. Возвращает первую возникшую ошибку,
// остальные логируются на уровне caller через slog при необходимости.
// Идемпотентен: повторный вызов на закрытом — no-op.
func (c *Clients) Close() error {
	if c == nil {
		return nil
	}
	var firstErr error
	for _, conn := range c.conns {
		if err := conn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	c.conns = nil
	return firstErr
}
