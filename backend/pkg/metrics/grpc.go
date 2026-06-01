// gRPC unary interceptor для Prometheus-инструментации.
//
// Покрывает все методы AuthService/BmstuService/FilterService/NotifierService/
// TeachersService без необходимости править их код: достаточно один раз
// передать interceptor в grpc.NewServer(...).
//
// Делает три вещи:
//
//  1. Инкрементит GRPCInflight на старте, декрементит в defer.
//  2. Меряет длительность через GRPCRequestDuration{method}.
//  3. Считает успех/ошибки в GRPCRequestsTotal{method, code} — code это
//     строковое имя grpc.codes.Code (например, "OK", "InvalidArgument").

package metrics

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// UnaryServerInterceptor возвращает grpc.UnaryServerInterceptor, который
// инструментирует все unary-вызовы метриками r.
//
// Использование:
//
//	r := metrics.Init("auth")
//	srv := grpc.NewServer(grpc.UnaryInterceptor(metrics.UnaryServerInterceptor(r)))
//
// Для chain-interceptor'ов используйте grpc.ChainUnaryInterceptor.
func (r *Registry) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		r.GRPCInflight.Inc()
		defer r.GRPCInflight.Dec()

		start := time.Now()
		resp, err := handler(ctx, req)
		dur := time.Since(start).Seconds()

		method := info.FullMethod
		code := codes.OK.String()
		if err != nil {
			code = status.Code(err).String()
		}
		r.GRPCRequestsTotal.WithLabelValues(method, code).Inc()
		r.GRPCRequestDuration.WithLabelValues(method).Observe(dur)
		return resp, err
	}
}
