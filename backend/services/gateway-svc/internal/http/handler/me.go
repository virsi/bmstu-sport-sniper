package handler

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	authv1 "github.com/fizcultor/backend/gen/auth/v1"
	"github.com/fizcultor/backend/services/gateway-svc/internal/http/middleware"
)

// meResponse — публичный профиль пользователя (api.md §2).
//
// telegram_chat_id опционален: omitempty убирает поле, если TG не привязан.
type meResponse struct {
	ID             string    `json:"id"`
	Email          string    `json:"email"`
	TelegramChatID *int64    `json:"telegram_chat_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	LastSeenAt     time.Time `json:"last_seen_at"`
}

// Me — GET /api/me. Требует Auth middleware.
//
// auth-svc.GetMe берёт user_id из x-user-id metadata (его проставляет
// Auth middleware через grpcx.WithUserID), но мы дублируем в Request,
// чтобы быть явными и для совместимости с реализацией, читающей оба источника.
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFrom(r.Context())
	if userID == "" {
		WriteError(w, r, NewUnauthorized("missing user_id in context"))
		return
	}

	u, err := h.deps.Clients.Auth.GetMe(r.Context(), &authv1.GetMeRequest{UserId: userID})
	if err != nil {
		WriteError(w, r, err)
		return
	}

	resp := meResponse{
		ID:         u.GetId(),
		Email:      u.GetEmail(),
		CreatedAt:  tsToTime(u.GetCreatedAt()),
		LastSeenAt: tsToTime(u.GetLastSeenAt()),
	}
	if tc := u.TelegramChatId; tc != nil {
		resp.TelegramChatID = tc
	}
	WriteJSON(w, http.StatusOK, resp)
}

// telegramInitResponse — ответ POST /api/me/telegram/init (api.md §2).
type telegramInitResponse struct {
	Deeplink  string    `json:"deeplink"`
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

// TelegramInit — POST /api/me/telegram/init. Требует Auth middleware.
//
// auth-svc возвращает deeplink вида `tg://start?token=<code>`. Мы переписываем
// его на каноничный `https://t.me/<bot>?start=<code>` для удобства фронта.
// Если auth-svc уже вернул https-deeplink — оставляем как есть.
func (h *Handler) TelegramInit(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFrom(r.Context())
	if userID == "" {
		WriteError(w, r, NewUnauthorized("missing user_id in context"))
		return
	}

	resp, err := h.deps.Clients.Auth.LinkTelegramInit(r.Context(), &authv1.LinkTelegramInitRequest{
		UserId: userID,
	})
	if err != nil {
		WriteError(w, r, err)
		return
	}

	deeplink := rewriteDeeplink(resp.GetDeeplink(), resp.GetCode(), h.deps.BotUsername)
	WriteJSON(w, http.StatusOK, telegramInitResponse{
		Deeplink:  deeplink,
		Code:      resp.GetCode(),
		ExpiresAt: tsToTime(resp.GetExpiresAt()),
	})
}

// rewriteDeeplink приводит deeplink к https-форме.
//
// Поддерживаемые входные форматы:
//   - "tg://start?token=<code>" → "https://t.me/<bot>?start=<code>".
//   - "https://t.me/<anything>?start=<code>" → не трогаем (уже норм).
//   - произвольный URL без known-схемы → собираем canonical из code.
//
// Если botUsername пуст, возвращаем оригинал (defensive — не должно случаться,
// envDefault в config задаёт значение).
func rewriteDeeplink(raw, code, botUsername string) string {
	if botUsername == "" {
		return raw
	}
	if strings.HasPrefix(raw, "https://t.me/") {
		return raw
	}
	// Если есть код — собираем canonical, не парсим невалидный URL.
	if code != "" {
		return "https://t.me/" + botUsername + "?start=" + url.QueryEscape(code)
	}
	// Fallback: попробуем выдернуть start/token из query произвольного URL.
	if u, err := url.Parse(raw); err == nil {
		q := u.Query()
		c := q.Get("token")
		if c == "" {
			c = q.Get("start")
		}
		if c != "" {
			return "https://t.me/" + botUsername + "?start=" + url.QueryEscape(c)
		}
	}
	return raw
}
