package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	authv1 "github.com/fizcultor/backend/gen/auth/v1"
	commonv1 "github.com/fizcultor/backend/gen/common/v1"

	"github.com/fizcultor/backend/services/gateway-svc/internal/http/middleware"
)

// withUserCtx добавляет user_id в HTTP-контекст (имитирует Auth middleware).
//
// Использует через рефлексию приватный ключ middleware-пакета — пакет
// открывает только UserIDFrom, но writer (withUserID) приватный. Вместо
// reflection мы вызываем настоящий middleware.Auth с мок-verifier'ом, который
// всегда отдаёт указанный user_id.
func withUserCtx(_ *testing.T, userID string) func(http.Handler) http.Handler {
	mockVerifier := &fakeVerifierForCtx{userID: userID}
	return middleware.Auth(mockVerifier)
}

type fakeVerifierForCtx struct{ userID string }

func (f *fakeVerifierForCtx) VerifyAccess(_ context.Context, _ *authv1.VerifyAccessRequest) (*authv1.VerifyAccessResponse, error) {
	return &authv1.VerifyAccessResponse{UserId: f.userID}, nil
}

func TestHandler_Me(t *testing.T) {
	t.Parallel()

	tc := int64(1234567)
	mock := &mockAuthClient{
		GetMeFn: func(_ context.Context, in *authv1.GetMeRequest) (*commonv1.User, error) {
			assert.Equal(t, "u-42", in.GetUserId())
			return &commonv1.User{
				Id:             "u-42",
				Email:          "ivan@bmstu.ru",
				TelegramChatId: &tc,
				CreatedAt:      timestamppb.New(fixedTime),
				LastSeenAt:     timestamppb.New(fixedTime),
			}, nil
		},
	}
	h := newHandler(t, withAuth(mock))

	// Имитируем auth middleware с фиксированным user_id.
	mw := withUserCtx(t, "u-42")
	protected := mw(http.HandlerFunc(h.Me))

	req := httptest.NewRequest(http.MethodGet, "/api/me", http.NoBody)
	req.Header.Set("Authorization", "Bearer dummy")
	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.Equal(t, "u-42", body["id"])
	assert.Equal(t, "ivan@bmstu.ru", body["email"])
	assert.EqualValues(t, tc, body["telegram_chat_id"])
}

func TestHandler_Me_NoTelegramChatID(t *testing.T) {
	t.Parallel()

	mock := &mockAuthClient{
		GetMeFn: func(_ context.Context, _ *authv1.GetMeRequest) (*commonv1.User, error) {
			return &commonv1.User{
				Id:         "u-1",
				Email:      "x@y.z",
				CreatedAt:  timestamppb.New(fixedTime),
				LastSeenAt: timestamppb.New(fixedTime),
			}, nil
		},
	}
	h := newHandler(t, withAuth(mock))
	mw := withUserCtx(t, "u-1")
	protected := mw(http.HandlerFunc(h.Me))

	req := httptest.NewRequest(http.MethodGet, "/api/me", http.NoBody)
	req.Header.Set("Authorization", "Bearer dummy")
	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	_, has := body["telegram_chat_id"]
	assert.False(t, has, "telegram_chat_id must be omitted if nil")
}

func TestHandler_Me_NoUserInContext(t *testing.T) {
	t.Parallel()

	h := newHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/me", http.NoBody)
	rec := httptest.NewRecorder()
	h.Me(rec, req) // без auth middleware → нет user_id в ctx

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHandler_TelegramInit_RewritesDeeplink(t *testing.T) {
	t.Parallel()

	mock := &mockAuthClient{
		LinkTelegramInitFn: func(_ context.Context, _ *authv1.LinkTelegramInitRequest) (*authv1.LinkTelegramInitResponse, error) {
			return &authv1.LinkTelegramInitResponse{
				// auth-svc возвращает tg://, gateway должен переписать.
				Deeplink:  "tg://start?token=ABC123XYZ",
				Code:      "ABC123XYZ",
				ExpiresAt: timestamppb.New(fixedTime),
			}, nil
		},
	}
	h := newHandler(t, withAuth(mock))
	mw := withUserCtx(t, "u-1")
	protected := mw(http.HandlerFunc(h.TelegramInit))

	req := httptest.NewRequest(http.MethodPost, "/api/me/telegram/init", http.NoBody)
	req.Header.Set("Authorization", "Bearer dummy")
	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.Equal(t, "https://t.me/FizcultorBot?start=ABC123XYZ", body["deeplink"])
	assert.Equal(t, "ABC123XYZ", body["code"])
}

func TestHandler_TelegramInit_PreservesHTTPSDeeplink(t *testing.T) {
	t.Parallel()

	mock := &mockAuthClient{
		LinkTelegramInitFn: func(_ context.Context, _ *authv1.LinkTelegramInitRequest) (*authv1.LinkTelegramInitResponse, error) {
			return &authv1.LinkTelegramInitResponse{
				Deeplink:  "https://t.me/SomeOtherBot?start=XXX",
				Code:      "XXX",
				ExpiresAt: timestamppb.New(fixedTime),
			}, nil
		},
	}
	h := newHandler(t, withAuth(mock))
	mw := withUserCtx(t, "u-1")
	protected := mw(http.HandlerFunc(h.TelegramInit))

	req := httptest.NewRequest(http.MethodPost, "/api/me/telegram/init", http.NoBody)
	req.Header.Set("Authorization", "Bearer dummy")
	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	// Уже https://t.me/ — gateway не должен трогать.
	assert.Equal(t, "https://t.me/SomeOtherBot?start=XXX", body["deeplink"])
}
