package handler

import (
	"net/http"
	"time"
)

// RefreshCookieName — имя cookie с refresh-token. Короткое, чтобы не раздувать
// каждый запрос на /api/auth/*. Менять опасно — отвалятся уже залогиненные клиенты,
// у которых cookie выпущен под старым именем.
const RefreshCookieName = "rt"

// refreshCookiePath — Path-атрибут cookie. Ограничивает отправку cookie только
// эндпоинтами аутентификации, чтобы остальные ручки (BMSTU/filters/SSE) не получали
// refresh-token и не светили его в логах/CSRF-сценариях.
const refreshCookiePath = "/api/auth"

// refreshCookieMaxAge — Max-Age в секундах (30 дней, ровно TTL refresh-token-а
// на стороне auth-svc, см. JWT_REFRESH_TTL_SECONDS в .env.example).
const refreshCookieMaxAge = 30 * 24 * 60 * 60

// CookieConfig — параметры refresh-token cookie. Передаётся в Handler.Deps
// при инициализации gateway-svc.
type CookieConfig struct {
	// Secure — выставляет флаг Secure (cookie только по HTTPS). В dev на http://localhost
	// браузер игнорирует Secure-cookie, поэтому в dev обязательно false.
	Secure bool
	// Domain — Domain-атрибут. Пусто ⇒ host-only cookie (привязан к origin запроса).
	Domain string
}

// setRefreshCookie кладёт refresh-token в httpOnly cookie.
//
// Атрибуты:
//   - HttpOnly: недоступно JS (защита от XSS-кражи токена).
//   - SameSite=Strict: cookie не уходит на cross-site навигацию (CSRF-защита).
//   - Path=/api/auth: cookie прилетает только на login/refresh/logout, не светит
//     refresh в логах остальных эндпоинтов.
//   - Secure/Domain: см. CookieConfig.
//   - Max-Age=2592000 (30 дней): синхронизировано с TTL refresh на auth-svc.
func setRefreshCookie(w http.ResponseWriter, cfg CookieConfig, refreshToken string) {
	http.SetCookie(w, &http.Cookie{
		Name:     RefreshCookieName,
		Value:    refreshToken,
		Path:     refreshCookiePath,
		Domain:   cfg.Domain,
		MaxAge:   refreshCookieMaxAge,
		Secure:   cfg.Secure,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

// clearRefreshCookie удаляет refresh-token cookie. Браузер уберёт cookie по MaxAge=-1
// (явно сильнее, чем Expires=now, чтобы не зависеть от часов клиента).
func clearRefreshCookie(w http.ResponseWriter, cfg CookieConfig) {
	http.SetCookie(w, &http.Cookie{
		Name:     RefreshCookieName,
		Value:    "",
		Path:     refreshCookiePath,
		Domain:   cfg.Domain,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		Secure:   cfg.Secure,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

// refreshFromRequest достаёт refresh-token: сначала из cookie, потом (fallback) из body.
// Возвращает пустую строку, если ни там, ни там нет.
//
// Fallback на body нужен в transition-период, пока старые клиенты ещё шлют
// refresh в body; после миграции его можно убрать.
func refreshFromRequest(r *http.Request, bodyRefresh string) string {
	if c, err := r.Cookie(RefreshCookieName); err == nil && c.Value != "" {
		return c.Value
	}
	return bodyRefresh
}
