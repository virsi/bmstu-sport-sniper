package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	bmstuv1 "github.com/fizcultor/backend/gen/bmstu/v1"
	commonv1 "github.com/fizcultor/backend/gen/common/v1"
)

func TestHandler_BmstuStoreCreds(t *testing.T) {
	t.Parallel()

	type tc struct {
		name       string
		body       string
		mockFn     func(ctx context.Context, in *bmstuv1.StoreCredentialsRequest) (*bmstuv1.StoreCredentialsResponse, error)
		wantStatus int
	}

	cases := []tc{
		{
			name: "happy → 204 (with health_group)",
			body: `{"login":"ivanov_ii","password":"secret","health_group":"PREPARATORY"}`,
			mockFn: func(_ context.Context, in *bmstuv1.StoreCredentialsRequest) (*bmstuv1.StoreCredentialsResponse, error) {
				assert.Equal(t, "u-1", in.GetUserId())
				assert.Equal(t, "ivanov_ii", in.GetLogin())
				assert.Equal(t, commonv1.HealthGroup_HEALTH_GROUP_PREPARATORY, in.GetHealthGroup())
				return &bmstuv1.StoreCredentialsResponse{
					Status: commonv1.BmstuLinkStatus_BMSTU_LINK_STATUS_VALID,
				}, nil
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name: "happy → 204 (empty health_group → UNSPECIFIED, bmstu-svc дефолтит на BASIC)",
			body: `{"login":"ivanov_ii","password":"secret"}`,
			mockFn: func(_ context.Context, in *bmstuv1.StoreCredentialsRequest) (*bmstuv1.StoreCredentialsResponse, error) {
				assert.Equal(t, commonv1.HealthGroup_HEALTH_GROUP_UNSPECIFIED, in.GetHealthGroup())
				return &bmstuv1.StoreCredentialsResponse{
					Status: commonv1.BmstuLinkStatus_BMSTU_LINK_STATUS_VALID,
				}, nil
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "invalid health_group → 400",
			body:       `{"login":"x","password":"y","health_group":"OLYMPIC"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "missing login → 400",
			body:       `{"password":"x"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "missing password → 400",
			body:       `{"login":"x"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Keycloak rejects → 401",
			body: `{"login":"x","password":"y"}`,
			mockFn: func(_ context.Context, _ *bmstuv1.StoreCredentialsRequest) (*bmstuv1.StoreCredentialsResponse, error) {
				return nil, status.Error(codes.Unauthenticated, "bad creds")
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "LKS down → 503",
			body: `{"login":"x","password":"y"}`,
			mockFn: func(_ context.Context, _ *bmstuv1.StoreCredentialsRequest) (*bmstuv1.StoreCredentialsResponse, error) {
				return nil, status.Error(codes.Unavailable, "LKS down")
			},
			wantStatus: http.StatusServiceUnavailable,
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			mock := &mockBmstuClient{StoreCredentialsFn: c.mockFn}
			h := newHandler(t, withBmstu(mock))

			mw := withUserCtx(t, "u-1")
			protected := mw(http.HandlerFunc(h.BmstuStoreCreds))

			req := httptest.NewRequest(http.MethodPost, "/api/bmstu/creds",
				strings.NewReader(c.body))
			req.Header.Set("Authorization", "Bearer dummy")
			rec := httptest.NewRecorder()
			protected.ServeHTTP(rec, req)

			assert.Equal(t, c.wantStatus, rec.Code)
		})
	}
}

func TestHandler_BmstuStatus(t *testing.T) {
	t.Parallel()

	mock := &mockBmstuClient{
		GetStatusFn: func(_ context.Context, in *bmstuv1.GetStatusRequest) (*bmstuv1.GetStatusResponse, error) {
			assert.Equal(t, "u-1", in.GetUserId())
			lastErr := "Keycloak returned 401"
			return &bmstuv1.GetStatusResponse{
				Status:      commonv1.BmstuLinkStatus_BMSTU_LINK_STATUS_INVALID,
				LastLoginAt: timestamppb.New(fixedTime),
				LastError:   &lastErr,
				HealthGroup: commonv1.HealthGroup_HEALTH_GROUP_SPECIAL_MEDICAL,
			}, nil
		},
	}
	h := newHandler(t, withBmstu(mock))
	mw := withUserCtx(t, "u-1")
	protected := mw(http.HandlerFunc(h.BmstuStatus))

	req := httptest.NewRequest(http.MethodGet, "/api/bmstu/status", http.NoBody)
	req.Header.Set("Authorization", "Bearer dummy")
	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.Equal(t, "INVALID", body["status"])
	assert.Equal(t, "Keycloak returned 401", body["last_error"])
	assert.Equal(t, "SPECIAL_MEDICAL", body["health_group"])
	assert.Contains(t, body, "last_login_at")
}

func TestHandler_BmstuStatus_NotLinked(t *testing.T) {
	t.Parallel()

	mock := &mockBmstuClient{
		GetStatusFn: func(context.Context, *bmstuv1.GetStatusRequest) (*bmstuv1.GetStatusResponse, error) {
			return &bmstuv1.GetStatusResponse{
				Status: commonv1.BmstuLinkStatus_BMSTU_LINK_STATUS_NOT_LINKED,
			}, nil
		},
	}
	h := newHandler(t, withBmstu(mock))
	mw := withUserCtx(t, "u-1")
	protected := mw(http.HandlerFunc(h.BmstuStatus))

	req := httptest.NewRequest(http.MethodGet, "/api/bmstu/status", http.NoBody)
	req.Header.Set("Authorization", "Bearer dummy")
	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.Equal(t, "NOT_LINKED", body["status"])
	_, hasLogin := body["last_login_at"]
	assert.False(t, hasLogin)
}

func TestHandler_BmstuDeleteCreds(t *testing.T) {
	t.Parallel()

	var called bool
	mock := &mockBmstuClient{
		DeleteCredentialsFn: func(_ context.Context, in *bmstuv1.DeleteCredentialsRequest) (*bmstuv1.DeleteCredentialsResponse, error) {
			called = true
			assert.Equal(t, "u-1", in.GetUserId())
			return &bmstuv1.DeleteCredentialsResponse{}, nil
		},
	}
	h := newHandler(t, withBmstu(mock))
	mw := withUserCtx(t, "u-1")
	protected := mw(http.HandlerFunc(h.BmstuDeleteCreds))

	req := httptest.NewRequest(http.MethodDelete, "/api/bmstu/creds", http.NoBody)
	req.Header.Set("Authorization", "Bearer dummy")
	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.True(t, called)
}
