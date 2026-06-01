// Хелперы для gRPC outgoing metadata.

package grpcx

import (
	"context"

	"google.golang.org/grpc/metadata"
)

// appendOutgoingMD добавляет пару ключ-значение к outgoing-метаданным.
// Сохраняет существующие пары, не перезаписывает их.
func appendOutgoingMD(ctx context.Context, key, value string) context.Context {
	if value == "" {
		return ctx
	}
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		md = metadata.New(nil)
	} else {
		md = md.Copy()
	}
	md.Set(key, value)
	return metadata.NewOutgoingContext(ctx, md)
}
