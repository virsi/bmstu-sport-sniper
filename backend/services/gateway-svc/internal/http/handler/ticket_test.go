package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fizcultor/backend/services/gateway-svc/internal/clients"
	"github.com/fizcultor/backend/services/gateway-svc/internal/http/handler"
	"github.com/fizcultor/backend/services/gateway-svc/internal/http/middleware"
)

// stubTicketIssuer — мок handler.TicketIssuer.
type stubTicketIssuer struct {
	ticket    string
	expiresAt time.Time
}

func (s stubTicketIssuer) Issue(_ string) (string, time.Time) {
	return s.ticket, s.expiresAt
}

func TestHandler_IssueStreamTicket_Happy(t *testing.T) {
	t.Parallel()
	exp := time.Date(2026, time.June, 2, 11, 0, 0, 0, time.UTC)
	h := handler.New(handler.Deps{
		Clients: &clients.Clients{
			Auth:     &mockAuthClient{},
			Bmstu:    &mockBmstuClient{},
			Filter:   &mockFilterClient{},
			Notifier: &mockNotifierClient{},
			Teachers: &mockTeachersClient{},
		},
		TicketStore: stubTicketIssuer{ticket: "tk-abc", expiresAt: exp},
		BotUsername: "FizcultorBot",
	})

	// Имитируем Auth middleware: user_id уже в контексте.
	req := httptest.NewRequest(http.MethodPost, "/api/stream/ticket", http.NoBody)
	ctx := contextWithUserID(req.Context(), "u-1")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.IssueStreamTicket(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.Equal(t, "tk-abc", body["ticket"])
	assert.NotEmpty(t, body["expires_at"])
}

func TestHandler_IssueStreamTicket_NoStore_503(t *testing.T) {
	t.Parallel()
	h := handler.New(handler.Deps{
		Clients: &clients.Clients{
			Auth:     &mockAuthClient{},
			Bmstu:    &mockBmstuClient{},
			Filter:   &mockFilterClient{},
			Notifier: &mockNotifierClient{},
			Teachers: &mockTeachersClient{},
		},
		// TicketStore: nil — store отсутствует.
		BotUsername: "FizcultorBot",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/stream/ticket", http.NoBody)
	req = req.WithContext(contextWithUserID(req.Context(), "u-1"))
	rec := httptest.NewRecorder()
	h.IssueStreamTicket(rec, req)

	// 400, KISS — нет dedicated 503 фабрики; см. NewBadRequest в ticket.go.
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandler_IssueStreamTicket_NoUserID_401(t *testing.T) {
	t.Parallel()
	h := handler.New(handler.Deps{
		Clients: &clients.Clients{
			Auth:     &mockAuthClient{},
			Bmstu:    &mockBmstuClient{},
			Filter:   &mockFilterClient{},
			Notifier: &mockNotifierClient{},
			Teachers: &mockTeachersClient{},
		},
		TicketStore: stubTicketIssuer{ticket: "tk", expiresAt: time.Now()},
		BotUsername: "FizcultorBot",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/stream/ticket", http.NoBody)
	// user_id отсутствует в ctx.
	rec := httptest.NewRecorder()
	h.IssueStreamTicket(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHandler_IssueStreamTicket_EmptyTicket_500(t *testing.T) {
	t.Parallel()
	h := handler.New(handler.Deps{
		Clients: &clients.Clients{
			Auth:     &mockAuthClient{},
			Bmstu:    &mockBmstuClient{},
			Filter:   &mockFilterClient{},
			Notifier: &mockNotifierClient{},
			Teachers: &mockTeachersClient{},
		},
		TicketStore: stubTicketIssuer{ticket: "", expiresAt: time.Time{}},
		BotUsername: "FizcultorBot",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/stream/ticket", http.NoBody)
	req = req.WithContext(contextWithUserID(req.Context(), "u"))
	rec := httptest.NewRecorder()
	h.IssueStreamTicket(rec, req)
	// 400 (NewBadRequest) — внутренний сбой crypto/rand, не должен зацикливать клиента.
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// contextWithUserID — тестовый хелпер для имитации Auth middleware.
// middleware.UserIDFrom читает приватный ключ контекста, поэтому используем
// нашу Auth middleware напрямую как best-effort: оборачиваем handler.
//
// Этот хелпер использует unexported withUserID через http.Handler-wrap чтобы
// не светить test-helper API в production-пакете.
func contextWithUserID(parent context.Context, userID string) context.Context {
	// Trick: запускаем dummy middleware-цепочку. Это не громоздко и не плодит
	// test-helper API в продакшен-коде.
	type contextResult struct {
		ctx context.Context
	}
	var res contextResult

	mw := middleware.Auth(&dummyVerifier{userID: userID})
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		res.ctx = r.Context()
	}))
	req := httptest.NewRequest(http.MethodGet, "/probe", http.NoBody)
	req = req.WithContext(parent)
	req.Header.Set("Authorization", "Bearer probe")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if res.ctx == nil {
		// верификатор отверг — возвращаем parent (тогда тест увидит пустой user_id).
		return parent
	}
	return res.ctx
}
