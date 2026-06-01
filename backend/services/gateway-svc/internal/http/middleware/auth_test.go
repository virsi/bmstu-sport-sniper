package middleware_test

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
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	authv1 "github.com/fizcultor/backend/gen/auth/v1"
	"github.com/fizcultor/backend/services/gateway-svc/internal/http/middleware"
)

// fakeVerifier — мок AuthVerifier для табличных тестов Auth middleware.
type fakeVerifier struct {
	wantToken string
	resp      *authv1.VerifyAccessResponse
	err       error
}

func (f *fakeVerifier) VerifyAccess(_ context.Context, in *authv1.VerifyAccessRequest) (*authv1.VerifyAccessResponse, error) {
	if f.wantToken != "" && in.GetAccessToken() != f.wantToken {
		return nil, status.Error(codes.Unauthenticated, "token mismatch")
	}
	return f.resp, f.err
}

func TestAuth(t *testing.T) {
	t.Parallel()

	const validToken = "valid.jwt.token"
	const userID = "42"

	type tc struct {
		name        string
		setHeader   string // Authorization header value
		setQuery    string // ?access=<...>
		verifier    *fakeVerifier
		wantStatus  int
		wantUserID  string // checked in protected handler
		wantOutMD   bool   // expect x-user-id in outgoing gRPC metadata
		wantProblem bool   // 401-body is RFC 7807
	}

	cases := []tc{
		{
			name:      "happy path: bearer header",
			setHeader: "Bearer " + validToken,
			verifier: &fakeVerifier{
				wantToken: validToken,
				resp:      &authv1.VerifyAccessResponse{UserId: userID},
			},
			wantStatus: http.StatusOK,
			wantUserID: userID,
			wantOutMD:  true,
		},
		{
			name:     "happy path: query fallback (SSE)",
			setQuery: validToken,
			verifier: &fakeVerifier{
				wantToken: validToken,
				resp:      &authv1.VerifyAccessResponse{UserId: userID},
			},
			wantStatus: http.StatusOK,
			wantUserID: userID,
			wantOutMD:  true,
		},
		{
			name:        "missing token",
			verifier:    &fakeVerifier{},
			wantStatus:  http.StatusUnauthorized,
			wantProblem: true,
		},
		{
			name:      "malformed header (no Bearer prefix)",
			setHeader: "Basic abcdef",
			verifier:  &fakeVerifier{},
			// extractToken вернёт "", → missing token → 401.
			wantStatus:  http.StatusUnauthorized,
			wantProblem: true,
		},
		{
			name:      "invalid token (auth-svc returns Unauthenticated)",
			setHeader: "Bearer wrong.token",
			verifier: &fakeVerifier{
				err: status.Error(codes.Unauthenticated, "token expired"),
			},
			wantStatus:  http.StatusUnauthorized,
			wantProblem: true,
		},
		{
			name:      "auth backend unavailable (Internal)",
			setHeader: "Bearer " + validToken,
			verifier: &fakeVerifier{
				err: status.Error(codes.Internal, "boom"),
			},
			wantStatus:  http.StatusUnauthorized,
			wantProblem: true,
		},
		{
			name:      "empty user_id in claims",
			setHeader: "Bearer " + validToken,
			verifier: &fakeVerifier{
				resp: &authv1.VerifyAccessResponse{UserId: ""},
			},
			wantStatus:  http.StatusUnauthorized,
			wantProblem: true,
		},
		{
			name:      "header wins over query when both set",
			setHeader: "Bearer " + validToken,
			setQuery:  "some.other.token",
			verifier: &fakeVerifier{
				wantToken: validToken,
				resp:      &authv1.VerifyAccessResponse{UserId: userID},
			},
			wantStatus: http.StatusOK,
			wantUserID: userID,
			wantOutMD:  true,
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var capturedUserID string
			var capturedOutMD metadata.MD

			downstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedUserID = middleware.UserIDFrom(r.Context())
				if md, ok := metadata.FromOutgoingContext(r.Context()); ok {
					capturedOutMD = md
				}
				w.WriteHeader(http.StatusOK)
			})

			mw := middleware.Auth(c.verifier)
			h := mw(downstream)

			url := "/api/me"
			if c.setQuery != "" {
				url += "?access=" + c.setQuery
			}
			req := httptest.NewRequest(http.MethodGet, url, http.NoBody)
			if c.setHeader != "" {
				req.Header.Set("Authorization", c.setHeader)
			}
			req.Header.Set("X-Request-ID", "test-trace-id")

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			assert.Equal(t, c.wantStatus, rec.Code, "status")

			if c.wantStatus == http.StatusOK {
				assert.Equal(t, c.wantUserID, capturedUserID, "user_id in ctx")
				if c.wantOutMD {
					require.NotNil(t, capturedOutMD, "expected outgoing metadata")
					values := capturedOutMD.Get("x-user-id")
					require.Len(t, values, 1, "x-user-id metadata")
					assert.Equal(t, c.wantUserID, values[0])
				}
			}

			if c.wantProblem {
				assert.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
				var p map[string]any
				require.NoError(t, json.NewDecoder(rec.Body).Decode(&p))
				assert.Equal(t, float64(http.StatusUnauthorized), p["status"])
				assert.NotEmpty(t, p["title"])
				assert.Equal(t, "test-trace-id", p["trace_id"])
			}
		})
	}
}

func TestAuth_NilVerifier_Panics(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() {
		middleware.Auth(nil)
	})
}

func TestUserIDFrom_AbsentContext(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "", middleware.UserIDFrom(context.Background()))
}

func TestAuth_TokenWithWhitespace(t *testing.T) {
	t.Parallel()

	// `Bearer   token  ` → trim whitespace, valid.
	verifier := &fakeVerifier{
		wantToken: "valid.token",
		resp:      &authv1.VerifyAccessResponse{UserId: "1"},
	}
	mw := middleware.Auth(verifier)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/me", http.NoBody)
	req.Header.Set("Authorization", "Bearer   "+strings.TrimSpace("  valid.token  ")+"  ")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}
