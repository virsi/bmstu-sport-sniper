package handler

import (
	"net/http"
	"time"

	bmstuv1 "github.com/fizcultor/backend/gen/bmstu/v1"
	commonv1 "github.com/fizcultor/backend/gen/common/v1"
	"github.com/fizcultor/backend/services/gateway-svc/internal/http/middleware"
)

// Допустимые строковые значения health_group в REST-теле POST /api/bmstu/creds.
// Совпадают с CHECK в migrations/bmstu_db/00003_health_group.sql и с
// именами без префикса HEALTH_GROUP_* из common.v1.HealthGroup.
const (
	healthGroupBasic          = "BASIC"
	healthGroupPreparatory    = "PREPARATORY"
	healthGroupSpecialMedical = "SPECIAL_MEDICAL"
	healthGroupAFK            = "AFK"
)

// bmstuCredsRequest — тело POST /api/bmstu/creds.
//
// ВАЖНО: login/password логировать НЕЛЬЗЯ — bmstu-svc сам редактирует логи.
// Gateway не должен класть password в slog. health_group — НЕ секрет, ОК
// логировать.
type bmstuCredsRequest struct {
	Login       string `json:"login"`
	Password    string `json:"password"`
	HealthGroup string `json:"health_group"`
}

// bmstuStatusResponse — ответ GET /api/bmstu/status (api.md §3).
//
// Поля LastLoginAt, SessionExpiresAt, LastError, HealthGroup опциональны.
// HealthGroup пуст при status == NOT_LINKED.
type bmstuStatusResponse struct {
	Status           string     `json:"status"`
	HealthGroup      string     `json:"health_group,omitempty"`
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
	hg, ok := healthGroupFromString(body.HealthGroup)
	if !ok {
		WriteError(w, r, NewBadRequest(
			"health_group must be one of: BASIC, PREPARATORY, SPECIAL_MEDICAL, AFK",
		))
		return
	}

	if _, err := h.deps.Clients.Bmstu.StoreCredentials(r.Context(), &bmstuv1.StoreCredentialsRequest{
		UserId:      userID,
		Login:       body.Login,
		Password:    body.Password,
		HealthGroup: hg,
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
		Status:      bmstuStatusToString(resp.GetStatus()),
		HealthGroup: healthGroupToString(resp.GetHealthGroup()),
		LastError:   resp.GetLastError(),
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

// healthGroupFromString парсит строку из REST-тела в proto enum.
//
// Допустимые значения (case-sensitive, как в DB CHECK):
//   - "" → UNSPECIFIED (bmstu-svc подставит дефолт BASIC), ok = true.
//   - "BASIC" / "PREPARATORY" / "SPECIAL_MEDICAL" / "AFK" — соответствующий enum.
//   - всё остальное → UNSPECIFIED, ok = false (клиент должен получить 400).
//
// Пустая строка трактуется как «не указано» намеренно: чтобы не ломать
// старых клиентов, у которых health_group отсутствует в JSON-теле.
func healthGroupFromString(s string) (commonv1.HealthGroup, bool) {
	switch s {
	case "":
		return commonv1.HealthGroup_HEALTH_GROUP_UNSPECIFIED, true
	case healthGroupBasic:
		return commonv1.HealthGroup_HEALTH_GROUP_BASIC, true
	case healthGroupPreparatory:
		return commonv1.HealthGroup_HEALTH_GROUP_PREPARATORY, true
	case healthGroupSpecialMedical:
		return commonv1.HealthGroup_HEALTH_GROUP_SPECIAL_MEDICAL, true
	case healthGroupAFK:
		return commonv1.HealthGroup_HEALTH_GROUP_AFK, true
	default:
		return commonv1.HealthGroup_HEALTH_GROUP_UNSPECIFIED, false
	}
}

// healthGroupToString сериализует proto enum в строку для REST-ответа.
// UNSPECIFIED → "" (поле опускается через omitempty).
func healthGroupToString(hg commonv1.HealthGroup) string {
	switch hg {
	case commonv1.HealthGroup_HEALTH_GROUP_BASIC:
		return healthGroupBasic
	case commonv1.HealthGroup_HEALTH_GROUP_PREPARATORY:
		return healthGroupPreparatory
	case commonv1.HealthGroup_HEALTH_GROUP_SPECIAL_MEDICAL:
		return healthGroupSpecialMedical
	case commonv1.HealthGroup_HEALTH_GROUP_AFK:
		return healthGroupAFK
	case commonv1.HealthGroup_HEALTH_GROUP_UNSPECIFIED:
		return ""
	default:
		return ""
	}
}
