package handler

import (
	"net/http"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	authv1 "github.com/fizcultor/backend/gen/auth/v1"
)

// registerRequest — тело POST /api/auth/register.
type registerRequest struct {
	// Email — нормализуется на стороне auth-svc.
	Email string `json:"email"`
	// Password — plain text, ≥8 символов, хешируется argon2id.
	Password string `json:"password"`
}

// registerResponse — публичный профиль (контракт api.md §1).
type registerResponse struct {
	ID         string    `json:"id"`
	Email      string    `json:"email"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

// Register — POST /api/auth/register.
//
// Прокси к AuthService.Register, маппит User → registerResponse.
// 201 Created при успехе, 409 если email занят, 400 при невалидном email/пароле.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var body registerRequest
	if err := DecodeJSON(r, &body, 0); err != nil {
		WriteError(w, r, err)
		return
	}

	resp, err := h.deps.Clients.Auth.Register(r.Context(), &authv1.RegisterRequest{
		Email:    body.Email,
		Password: body.Password,
	})
	if err != nil {
		WriteError(w, r, err)
		return
	}

	u := resp.GetUser()
	WriteJSON(w, http.StatusCreated, registerResponse{
		ID:         u.GetId(),
		Email:      u.GetEmail(),
		CreatedAt:  tsToTime(u.GetCreatedAt()),
		LastSeenAt: tsToTime(u.GetLastSeenAt()),
	})
}

// loginRequest — тело POST /api/auth/login.
type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// accessTokenResponse — публичный ответ login/refresh.
//
// Refresh-token НЕ возвращается в body — он живёт в httpOnly cookie `rt`,
// недоступной из JS. Это закрывает XSS-вектор кражи долгоживущего токена
// (см. docs/review-findings.md #2).
//
// RefreshExpiresAt оставлен, чтобы фронт мог показать UX «сессия истечёт через…»
// без чтения cookie (которое и так невозможно из JS).
type accessTokenResponse struct {
	AccessToken      string    `json:"access_token"`
	AccessExpiresAt  time.Time `json:"access_expires_at"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
}

// Login — POST /api/auth/login.
//
// 200 + accessTokenResponse, refresh уезжает в Set-Cookie `rt`.
// 401 при любой ошибке кредов.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var body loginRequest
	if err := DecodeJSON(r, &body, 0); err != nil {
		WriteError(w, r, err)
		return
	}

	tp, err := h.deps.Clients.Auth.Login(r.Context(), &authv1.LoginRequest{
		Email:    body.Email,
		Password: body.Password,
	})
	if err != nil {
		WriteError(w, r, err)
		return
	}
	setRefreshCookie(w, h.deps.CookieConfig, tp.GetRefreshToken())
	WriteJSON(w, http.StatusOK, tokenPairToAccessResponse(tp))
}

// refreshRequest — тело POST /api/auth/refresh.
//
// DEPRECATED поле RefreshToken: оставлено только как fallback для старых клиентов,
// ещё не мигрированных на cookie-based refresh. После полной миграции — удалить.
type refreshRequest struct {
	// RefreshToken — DEPRECATED. Использовать cookie `rt`. Поле оставлено
	// для backward-compat периода миграции.
	RefreshToken string `json:"refresh_token,omitempty"`
}

// Refresh — POST /api/auth/refresh.
//
// Источник refresh-token: cookie `rt` (приоритет) → body.refresh_token (fallback).
// Если ни там, ни там нет → 401.
//
// Старый refresh инвалидируется auth-svc'ом (rotation). Reuse уже revoked refresh →
// revoke всех токенов юзера и 401.
//
// На успех ставит новый Set-Cookie (rotation также на cookie-уровне).
func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	var body refreshRequest
	// Тело опционально — refresh может быть только в cookie. EOF/empty body OK.
	_ = DecodeJSON(r, &body, 0)

	refresh := refreshFromRequest(r, body.RefreshToken)
	if refresh == "" {
		// Не пишем cookie clear: возможно, истинный auth flow ещё в работе —
		// фронт после 401 решит, чистить ли локальное состояние.
		WriteError(w, r, NewUnauthorized("missing refresh token"))
		return
	}

	tp, err := h.deps.Clients.Auth.Refresh(r.Context(), &authv1.RefreshRequest{
		RefreshToken: refresh,
	})
	if err != nil {
		// Если refresh пришёл из cookie и auth-svc отверг — чистим cookie,
		// чтобы клиент не зацикливался на одном и том же протухшем токене.
		if _, cerr := r.Cookie(RefreshCookieName); cerr == nil {
			clearRefreshCookie(w, h.deps.CookieConfig)
		}
		WriteError(w, r, err)
		return
	}
	setRefreshCookie(w, h.deps.CookieConfig, tp.GetRefreshToken())
	WriteJSON(w, http.StatusOK, tokenPairToAccessResponse(tp))
}

// logoutRequest — тело POST /api/auth/logout.
//
// DEPRECATED поле RefreshToken: используем cookie, body — fallback на transition.
type logoutRequest struct {
	// RefreshToken — DEPRECATED. См. refreshRequest.RefreshToken.
	RefreshToken string `json:"refresh_token,omitempty"`
}

// Logout — POST /api/auth/logout. 204, идемпотентен.
//
// Access обязателен (роут под Auth middleware). Refresh берётся из cookie (приоритет)
// или из body (fallback). Если ни там, ни там — просто 204 + delete cookie
// (на случай, если cookie всё-таки был, но клиент его не отдал).
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	var body logoutRequest
	// Пустое тело — допустимо.
	_ = DecodeJSON(r, &body, 0)

	refresh := refreshFromRequest(r, body.RefreshToken)
	// Всегда чистим cookie на logout, даже если refresh уже не было —
	// удаление идемпотентно.
	clearRefreshCookie(w, h.deps.CookieConfig)

	if refresh != "" {
		if _, err := h.deps.Clients.Auth.Revoke(r.Context(), &authv1.RevokeRequest{
			RefreshToken: refresh,
		}); err != nil {
			WriteError(w, r, err)
			return
		}
	}
	WriteNoContent(w)
}

// tokenPairToAccessResponse — общий маппер TokenPair → REST JSON без refresh-token.
func tokenPairToAccessResponse(tp *authv1.TokenPair) accessTokenResponse {
	return accessTokenResponse{
		AccessToken:      tp.GetAccessToken(),
		AccessExpiresAt:  tsToTime(tp.GetAccessExpiresAt()),
		RefreshExpiresAt: tsToTime(tp.GetRefreshExpiresAt()),
	}
}

// tsToTime безопасно конвертирует *timestamppb.Timestamp в time.Time.
// nil → zero time, что для JSON выходит как "0001-01-01T00:00:00Z" —
// caller должен это учитывать (обычно не nil в успешном ответе).
func tsToTime(ts *timestamppb.Timestamp) time.Time {
	if ts == nil {
		return time.Time{}
	}
	return ts.AsTime()
}
