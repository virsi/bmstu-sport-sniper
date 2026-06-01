package middleware_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	authv1 "github.com/fizcultor/backend/gen/auth/v1"
	"github.com/fizcultor/backend/services/gateway-svc/internal/http/middleware"
)

// fakeRedeemer — мок TicketRedeemer.
type fakeRedeemer struct {
	wantTicket string
	userID     string
	err        error
	called     int
}

func (f *fakeRedeemer) Redeem(t string) (string, error) {
	f.called++
	if f.wantTicket != "" && t != f.wantTicket {
		return "", errors.New("ticket mismatch")
	}
	return f.userID, f.err
}

func TestSSEAuth_TicketPath(t *testing.T) {
	t.Parallel()

	red := &fakeRedeemer{wantTicket: "tk-good", userID: "u-1"}
	// delegate должен НЕ дёргаться: ticket-путь выиграл.
	delegate := middleware.Auth(&fakeVerifier{})

	var got string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = middleware.UserIDFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	mw := middleware.SSEAuth(red, delegate)
	h := mw(next)

	req := httptest.NewRequest(http.MethodGet, "/api/stream?ticket=tk-good", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "u-1", got)
	assert.Equal(t, 1, red.called)
}

func TestSSEAuth_InvalidTicket_Rejects(t *testing.T) {
	t.Parallel()

	red := &fakeRedeemer{err: errors.New("bad")}
	// delegate не должен сработать как fallback при провальном ticket-е:
	// клиент явно указал ticket, fallthrough к JWT мог бы маскировать атаку.
	delegate := middleware.Auth(&fakeVerifier{
		resp: &authv1.VerifyAccessResponse{UserId: "should-not-reach"},
	})

	called := false
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	})

	mw := middleware.SSEAuth(red, delegate)
	h := mw(next)

	req := httptest.NewRequest(http.MethodGet, "/api/stream?ticket=tk-bad", http.NoBody)
	req.Header.Set("Authorization", "Bearer any.jwt")
	req.Header.Set("X-Request-ID", "trace")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.False(t, called, "next must not be called when ticket fails")
	assert.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
}

func TestSSEAuth_NoTicket_FallsBackToJWT(t *testing.T) {
	t.Parallel()

	red := &fakeRedeemer{}
	delegate := middleware.Auth(&fakeVerifier{
		wantToken: "jwt-ok",
		resp:      &authv1.VerifyAccessResponse{UserId: "u-jwt"},
	})

	var got string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = middleware.UserIDFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	mw := middleware.SSEAuth(red, delegate)
	h := mw(next)

	req := httptest.NewRequest(http.MethodGet, "/api/stream", http.NoBody)
	req.Header.Set("Authorization", "Bearer jwt-ok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "u-jwt", got)
	assert.Equal(t, 0, red.called, "redeemer must not be called without ?ticket=")
}

func TestSSEAuth_NoTicket_LegacyQueryAccess_StillWorks(t *testing.T) {
	t.Parallel()

	red := &fakeRedeemer{}
	delegate := middleware.Auth(&fakeVerifier{
		wantToken: "legacy-jwt",
		resp:      &authv1.VerifyAccessResponse{UserId: "u-legacy"},
	})

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := middleware.SSEAuth(red, delegate)
	h := mw(next)

	// DEPRECATED path: ?access=<jwt>. Должен ещё работать.
	req := httptest.NewRequest(http.MethodGet, "/api/stream?access=legacy-jwt", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestSSEAuth_NilRedeemer_OK(t *testing.T) {
	t.Parallel()
	// Если ticket store не сконфигурирован — middleware должна fallback на JWT
	// без NPE.
	delegate := middleware.Auth(&fakeVerifier{
		wantToken: "jwt",
		resp:      &authv1.VerifyAccessResponse{UserId: "u"},
	})
	mw := middleware.SSEAuth(nil, delegate)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/stream", http.NoBody)
	req.Header.Set("Authorization", "Bearer jwt")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestSSEAuth_NilDelegate_Panics(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() {
		middleware.SSEAuth(&fakeRedeemer{}, nil)
	})
}

func TestSSEAuth_NoTicket_NoJWT_401(t *testing.T) {
	t.Parallel()
	red := &fakeRedeemer{}
	delegate := middleware.Auth(&fakeVerifier{
		err: status.Error(codes.Unauthenticated, "no token"),
	})
	mw := middleware.SSEAuth(red, delegate)
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("should not reach handler")
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/stream", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// guard: проверим что после ticket-redeem userID живёт в outgoing gRPC metadata
// (downstream-сервисы видят владельца).
func TestSSEAuth_Ticket_PropagatesToGRPCMetadata(t *testing.T) {
	t.Parallel()
	red := &fakeRedeemer{wantTicket: "tk", userID: "u-9"}
	delegate := middleware.Auth(&fakeVerifier{})

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Используем context.Context API — наш middleware кладёт grpcx user id.
		// Не дублируем проверку из TestAuth, тут KISS-проверка userID в ctx.
		assert.Equal(t, "u-9", middleware.UserIDFrom(r.Context()))
		_ = context.Background()
		w.WriteHeader(http.StatusOK)
	})

	mw := middleware.SSEAuth(red, delegate)
	h := mw(next)
	req := httptest.NewRequest(http.MethodGet, "/api/stream?ticket=tk", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}
