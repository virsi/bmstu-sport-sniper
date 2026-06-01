package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	authv1 "github.com/fizcultor/backend/gen/auth/v1"
	commonv1 "github.com/fizcultor/backend/gen/common/v1"

	"github.com/fizcultor/backend/services/gateway-svc/internal/clients"
	"github.com/fizcultor/backend/services/gateway-svc/internal/http/handler"
)

// newHandler собирает handler.Handler с замоканными gRPC-клиентами.
//
// Передавать nil-моки безопасно: их методы вернут errUnused, любая попытка
// дёрнуть не сконфигурированный метод даст 500 + явное сообщение в логе.
func newHandler(t *testing.T, opts ...mockOpt) *handler.Handler {
	t.Helper()
	cls := &clients.Clients{
		Auth:     &mockAuthClient{},
		Bmstu:    &mockBmstuClient{},
		Filter:   &mockFilterClient{},
		Notifier: &mockNotifierClient{},
		Teachers: &mockTeachersClient{},
	}
	for _, o := range opts {
		o(cls)
	}
	return handler.New(handler.Deps{
		Clients:     cls,
		BotUsername: "FizcultorBot",
	})
}

// mockOpt — функциональная опция для конфигурации моков.
type mockOpt func(*clients.Clients)

func withAuth(m *mockAuthClient) mockOpt {
	return func(c *clients.Clients) { c.Auth = m }
}

func withBmstu(m *mockBmstuClient) mockOpt {
	return func(c *clients.Clients) { c.Bmstu = m }
}

func withFilter(m *mockFilterClient) mockOpt {
	return func(c *clients.Clients) { c.Filter = m }
}

// fixedTime — стабильный timestamp для тестов.
var fixedTime = time.Date(2026, time.June, 2, 10, 15, 0, 0, time.UTC)

func TestHandler_Register(t *testing.T) {
	t.Parallel()

	type tc struct {
		name       string
		body       string
		mockFn     func(ctx context.Context, in *authv1.RegisterRequest) (*authv1.RegisterResponse, error)
		wantStatus int
		check      func(*testing.T, *httptest.ResponseRecorder)
	}

	cases := []tc{
		{
			name: "happy path",
			body: `{"email":"ivan@bmstu.ru","password":"P@ssw0rd123"}`,
			mockFn: func(_ context.Context, in *authv1.RegisterRequest) (*authv1.RegisterResponse, error) {
				assert.Equal(t, "ivan@bmstu.ru", in.GetEmail())
				return &authv1.RegisterResponse{
					User: &commonv1.User{
						Id:         "42",
						Email:      "ivan@bmstu.ru",
						CreatedAt:  timestamppb.New(fixedTime),
						LastSeenAt: timestamppb.New(fixedTime),
					},
				}, nil
			},
			wantStatus: http.StatusCreated,
			check: func(t *testing.T, rec *httptest.ResponseRecorder) {
				var body map[string]any
				require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
				assert.Equal(t, "42", body["id"])
				assert.Equal(t, "ivan@bmstu.ru", body["email"])
			},
		},
		{
			name:       "invalid body",
			body:       `not-json`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "email taken (gRPC AlreadyExists)",
			body: `{"email":"x@y.z","password":"12345678"}`,
			mockFn: func(_ context.Context, _ *authv1.RegisterRequest) (*authv1.RegisterResponse, error) {
				return nil, status.Error(codes.AlreadyExists, "email taken")
			},
			wantStatus: http.StatusConflict,
			check: func(t *testing.T, rec *httptest.ResponseRecorder) {
				assert.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
			},
		},
		{
			name: "invalid arg (gRPC InvalidArgument)",
			body: `{"email":"bad","password":"short"}`,
			mockFn: func(_ context.Context, _ *authv1.RegisterRequest) (*authv1.RegisterResponse, error) {
				return nil, status.Error(codes.InvalidArgument, "invalid email")
			},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			mock := &mockAuthClient{RegisterFn: c.mockFn}
			h := newHandler(t, withAuth(mock))

			req := httptest.NewRequest(http.MethodPost, "/api/auth/register",
				strings.NewReader(c.body))
			rec := httptest.NewRecorder()
			h.Register(rec, req)

			assert.Equal(t, c.wantStatus, rec.Code)
			if c.check != nil {
				c.check(t, rec)
			}
		})
	}
}

func TestHandler_Login(t *testing.T) {
	t.Parallel()

	mock := &mockAuthClient{
		LoginFn: func(_ context.Context, in *authv1.LoginRequest) (*authv1.TokenPair, error) {
			if in.GetEmail() == "bad@x.z" {
				return nil, status.Error(codes.Unauthenticated, "bad creds")
			}
			return &authv1.TokenPair{
				AccessToken:      "access.jwt",
				RefreshToken:     "refresh.uuid",
				AccessExpiresAt:  timestamppb.New(fixedTime.Add(15 * time.Minute)),
				RefreshExpiresAt: timestamppb.New(fixedTime.Add(30 * 24 * time.Hour)),
			}, nil
		},
	}
	h := newHandler(t, withAuth(mock))

	t.Run("happy: refresh in Set-Cookie, not in body", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
			strings.NewReader(`{"email":"x@y.z","password":"123"}`))
		rec := httptest.NewRecorder()
		h.Login(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		var body map[string]any
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
		assert.Equal(t, "access.jwt", body["access_token"])
		// Refresh-token НЕ должен попадать в body.
		_, exists := body["refresh_token"]
		assert.False(t, exists, "refresh_token must not appear in login response body")

		// Set-Cookie должен содержать rt=...
		cookies := rec.Result().Cookies()
		require.Len(t, cookies, 1)
		rt := cookies[0]
		assert.Equal(t, "rt", rt.Name)
		assert.Equal(t, "refresh.uuid", rt.Value)
		assert.True(t, rt.HttpOnly, "rt cookie must be HttpOnly")
		assert.Equal(t, http.SameSiteStrictMode, rt.SameSite)
		assert.Equal(t, "/api/auth", rt.Path)
	})

	t.Run("bad creds → 401, no cookie set", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
			strings.NewReader(`{"email":"bad@x.z","password":"x"}`))
		rec := httptest.NewRecorder()
		h.Login(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.Empty(t, rec.Result().Cookies(), "no cookie on failed login")
	})

	t.Run("malformed body", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
			strings.NewReader(`{`))
		rec := httptest.NewRecorder()
		h.Login(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestHandler_Refresh(t *testing.T) {
	t.Parallel()

	makeMock := func() *mockAuthClient {
		return &mockAuthClient{
			RefreshFn: func(_ context.Context, in *authv1.RefreshRequest) (*authv1.TokenPair, error) {
				if in.GetRefreshToken() == "revoked" {
					return nil, status.Error(codes.Unauthenticated, "revoked")
				}
				return &authv1.TokenPair{
					AccessToken:      "new-access",
					RefreshToken:     "new-refresh",
					AccessExpiresAt:  timestamppb.New(fixedTime),
					RefreshExpiresAt: timestamppb.New(fixedTime),
				}, nil
			},
		}
	}

	t.Run("happy: refresh from cookie", func(t *testing.T) {
		t.Parallel()
		h := newHandler(t, withAuth(makeMock()))
		req := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", http.NoBody)
		req.AddCookie(&http.Cookie{Name: "rt", Value: "old-refresh"})
		rec := httptest.NewRecorder()
		h.Refresh(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		// Должен прийти НОВЫЙ Set-Cookie с rotated refresh.
		cookies := rec.Result().Cookies()
		require.Len(t, cookies, 1)
		assert.Equal(t, "rt", cookies[0].Name)
		assert.Equal(t, "new-refresh", cookies[0].Value)
		// Тело без refresh_token.
		var body map[string]any
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
		_, exists := body["refresh_token"]
		assert.False(t, exists)
	})

	t.Run("happy: refresh from body (fallback)", func(t *testing.T) {
		t.Parallel()
		h := newHandler(t, withAuth(makeMock()))
		req := httptest.NewRequest(http.MethodPost, "/api/auth/refresh",
			strings.NewReader(`{"refresh_token":"old-refresh"}`))
		rec := httptest.NewRecorder()
		h.Refresh(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("missing refresh: no cookie, no body → 401", func(t *testing.T) {
		t.Parallel()
		h := newHandler(t, withAuth(makeMock()))
		req := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", http.NoBody)
		rec := httptest.NewRecorder()
		h.Refresh(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("revoked cookie → 401 + clear cookie", func(t *testing.T) {
		t.Parallel()
		h := newHandler(t, withAuth(makeMock()))
		req := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", http.NoBody)
		req.AddCookie(&http.Cookie{Name: "rt", Value: "revoked"})
		rec := httptest.NewRecorder()
		h.Refresh(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
		cookies := rec.Result().Cookies()
		require.Len(t, cookies, 1)
		assert.Equal(t, "rt", cookies[0].Name)
		assert.Equal(t, "", cookies[0].Value)
		assert.Less(t, cookies[0].MaxAge, 0, "cookie must be deleted (MaxAge<0)")
	})

	t.Run("cookie wins over body", func(t *testing.T) {
		t.Parallel()
		var got string
		mock := &mockAuthClient{
			RefreshFn: func(_ context.Context, in *authv1.RefreshRequest) (*authv1.TokenPair, error) {
				got = in.GetRefreshToken()
				return &authv1.TokenPair{
					AccessToken:      "a",
					RefreshToken:     "r",
					AccessExpiresAt:  timestamppb.New(fixedTime),
					RefreshExpiresAt: timestamppb.New(fixedTime),
				}, nil
			},
		}
		h := newHandler(t, withAuth(mock))
		req := httptest.NewRequest(http.MethodPost, "/api/auth/refresh",
			strings.NewReader(`{"refresh_token":"from-body"}`))
		req.AddCookie(&http.Cookie{Name: "rt", Value: "from-cookie"})
		rec := httptest.NewRecorder()
		h.Refresh(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "from-cookie", got, "cookie has priority over body")
	})
}

func TestHandler_Logout(t *testing.T) {
	t.Parallel()

	t.Run("cookie present → Revoke called + cookie cleared + 204", func(t *testing.T) {
		t.Parallel()
		var called bool
		mock := &mockAuthClient{
			RevokeFn: func(_ context.Context, in *authv1.RevokeRequest) (*authv1.RevokeResponse, error) {
				called = true
				assert.Equal(t, "ck-refresh", in.GetRefreshToken())
				return &authv1.RevokeResponse{}, nil
			},
		}
		h := newHandler(t, withAuth(mock))
		req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", http.NoBody)
		req.AddCookie(&http.Cookie{Name: "rt", Value: "ck-refresh"})
		rec := httptest.NewRecorder()
		h.Logout(rec, req)
		assert.Equal(t, http.StatusNoContent, rec.Code)
		assert.True(t, called)
		cookies := rec.Result().Cookies()
		require.Len(t, cookies, 1)
		assert.Equal(t, "rt", cookies[0].Name)
		assert.Less(t, cookies[0].MaxAge, 0)
	})

	t.Run("body fallback → Revoke called + 204", func(t *testing.T) {
		t.Parallel()
		var called bool
		mock := &mockAuthClient{
			RevokeFn: func(_ context.Context, in *authv1.RevokeRequest) (*authv1.RevokeResponse, error) {
				called = true
				assert.Equal(t, "x", in.GetRefreshToken())
				return &authv1.RevokeResponse{}, nil
			},
		}
		h := newHandler(t, withAuth(mock))
		req := httptest.NewRequest(http.MethodPost, "/api/auth/logout",
			strings.NewReader(`{"refresh_token":"x"}`))
		rec := httptest.NewRecorder()
		h.Logout(rec, req)
		assert.Equal(t, http.StatusNoContent, rec.Code)
		assert.True(t, called)
	})

	t.Run("empty body, no cookie → 204, Revoke not called, cookie cleared", func(t *testing.T) {
		t.Parallel()
		mock := &mockAuthClient{
			RevokeFn: func(context.Context, *authv1.RevokeRequest) (*authv1.RevokeResponse, error) {
				t.Fatal("Revoke must not be called with empty body and no cookie")
				return nil, nil
			},
		}
		h := newHandler(t, withAuth(mock))
		req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", http.NoBody)
		rec := httptest.NewRecorder()
		h.Logout(rec, req)
		assert.Equal(t, http.StatusNoContent, rec.Code)
		// Cookie всё равно чистится — идемпотентно.
		cookies := rec.Result().Cookies()
		require.Len(t, cookies, 1)
		assert.Less(t, cookies[0].MaxAge, 0)
	})

	t.Run("Revoke error → propagated", func(t *testing.T) {
		t.Parallel()
		mock := &mockAuthClient{
			RevokeFn: func(context.Context, *authv1.RevokeRequest) (*authv1.RevokeResponse, error) {
				return nil, status.Error(codes.Internal, "boom")
			},
		}
		h := newHandler(t, withAuth(mock))
		req := httptest.NewRequest(http.MethodPost, "/api/auth/logout",
			strings.NewReader(`{"refresh_token":"x"}`))
		rec := httptest.NewRecorder()
		h.Logout(rec, req)
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}
