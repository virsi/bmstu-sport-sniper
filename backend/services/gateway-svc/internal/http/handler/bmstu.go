package handler

import (
	"net/http"
	"time"

	bmstuv1 "github.com/fizcultor/backend/gen/bmstu/v1"
	commonv1 "github.com/fizcultor/backend/gen/common/v1"
	"github.com/fizcultor/backend/services/gateway-svc/internal/http/middleware"
)

// bmstuCredsRequest — тело POST /api/bmstu/creds.
//
// ВАЖНО: эти поля логировать НЕЛЬЗЯ — bmstu-svc сам редактирует логи.
// Gateway не должен класть password в slog.
type bmstuCredsRequest struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

// bmstuStatusResponse — ответ GET /api/bmstu/status (api.md §3).
//
// Поля LastLoginAt, SessionExpiresAt, LastError опциональны.
type bmstuStatusResponse struct {
	Status           string     `json:"status"`
	LastLoginAt      *time.Time `json:"last_login_at,omitempty"`
	SessionExpiresAt *time.Time `json:"session_expires_at,omitempty"`
	LastError        string     `json:"last_error,omitempty"`
}

// BmstuStoreCreds — POST /api/bmstu/creds. Требует Auth middleware.
//
// bmstu-svc делает test-login. При провале — UNAUTHENTICATED → 401.
// При недоступности LKS — UNAVAILABLE → 503.
func (h *Handler) BmstuStoreCreds(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFrom(r.Context())
	if userID == "" {
		WriteError(w, r, NewUnauthorized("missing user_id in context"))
		return
	}

	var body bmstuCredsRequest
	if err := DecodeJSON(r, &body, 0); err != nil {
		WriteError(w, r, err)
		return
	}
	if body.Login == "" || body.Password == "" {
		WriteError(w, r, NewBadRequest("login and password are required"))
		return
	}

	if _, err := h.deps.Clients.Bmstu.StoreCredentials(r.Context(), &bmstuv1.StoreCredentialsRequest{
		UserId:   userID,
		Login:    body.Login,
		Password: body.Password,
	}); err != nil {
		WriteError(w, r, err)
		return
	}
	WriteNoContent(w)
}

// BmstuStatus — GET /api/bmstu/status. Требует Auth middleware.
func (h *Handler) BmstuStatus(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFrom(r.Context())
	if userID == "" {
		WriteError(w, r, NewUnauthorized("missing user_id in context"))
		return
	}

	resp, err := h.deps.Clients.Bmstu.GetStatus(r.Context(), &bmstuv1.GetStatusRequest{UserId: userID})
	if err != nil {
		WriteError(w, r, err)
		return
	}

	out := bmstuStatusResponse{
		Status:    bmstuStatusToString(resp.GetStatus()),
		LastError: resp.GetLastError(),
	}
	if ts := resp.GetLastLoginAt(); ts != nil {
		t := ts.AsTime()
		out.LastLoginAt = &t
	}
	if ts := resp.GetSessionExpiresAt(); ts != nil {
		t := ts.AsTime()
		out.SessionExpiresAt = &t
	}
	WriteJSON(w, http.StatusOK, out)
}

// BmstuDeleteCreds — DELETE /api/bmstu/creds. Требует Auth middleware.
// Идемпотентен.
func (h *Handler) BmstuDeleteCreds(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFrom(r.Context())
	if userID == "" {
		WriteError(w, r, NewUnauthorized("missing user_id in context"))
		return
	}

	if _, err := h.deps.Clients.Bmstu.DeleteCredentials(r.Context(), &bmstuv1.DeleteCredentialsRequest{
		UserId: userID,
	}); err != nil {
		WriteError(w, r, err)
		return
	}
	WriteNoContent(w)
}

// bmstuStatusToString сериализует enum BmstuLinkStatus в строку для api.md.
func bmstuStatusToString(s commonv1.BmstuLinkStatus) string {
	switch s {
	case commonv1.BmstuLinkStatus_BMSTU_LINK_STATUS_NOT_LINKED:
		return "NOT_LINKED"
	case commonv1.BmstuLinkStatus_BMSTU_LINK_STATUS_VALID:
		return "VALID"
	case commonv1.BmstuLinkStatus_BMSTU_LINK_STATUS_INVALID:
		return "INVALID"
	case commonv1.BmstuLinkStatus_BMSTU_LINK_STATUS_EXPIRED:
		return "EXPIRED"
	default:
		return "UNSPECIFIED"
	}
}
