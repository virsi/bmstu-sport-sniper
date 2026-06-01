package grpcx

import (
	"context"
	"testing"

	"google.golang.org/grpc/metadata"
)

func TestDialInsecure_EmptyAddr(t *testing.T) {
	_, err := DialInsecure(context.Background(), "", DialOptions{})
	if err == nil {
		t.Fatal("ожидаем ошибку для пустого addr")
	}
}

func TestDialInsecure_Smoke(t *testing.T) {
	// grpc.NewClient ленивый — реальное TCP-подключение не открывается.
	conn, err := DialInsecure(context.Background(), "127.0.0.1:0", DialOptions{})
	if err != nil {
		t.Fatalf("неожиданная ошибка dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if conn == nil {
		t.Fatal("conn == nil")
	}
}

func TestWithUserID_AddsMetadata(t *testing.T) {
	ctx := WithUserID(context.Background(), "user-1")
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		t.Fatal("ожидаем outgoing metadata")
	}
	vals := md.Get("x-user-id")
	if len(vals) != 1 || vals[0] != "user-1" {
		t.Fatalf("ожидаем x-user-id=user-1, получили %v", vals)
	}
}

func TestWithUserID_PreservesExisting(t *testing.T) {
	base := metadata.New(map[string]string{"x-other": "v"})
	ctx := metadata.NewOutgoingContext(context.Background(), base)
	ctx = WithUserID(ctx, "user-1")
	md, _ := metadata.FromOutgoingContext(ctx)
	if md.Get("x-other")[0] != "v" {
		t.Fatal("исходный ключ потерян")
	}
	if md.Get("x-user-id")[0] != "user-1" {
		t.Fatal("новый ключ не добавлен")
	}
}

func TestWithUserID_EmptyNoop(t *testing.T) {
	ctx := WithUserID(context.Background(), "")
	if _, ok := metadata.FromOutgoingContext(ctx); ok {
		t.Fatal("пустой user_id не должен добавлять metadata")
	}
}
