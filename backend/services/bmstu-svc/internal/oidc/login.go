package oidc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// LoginResult — результат успешного логина.
type LoginResult struct {
	// FinalURL — последний URL после всех redirect'ов (для дебага).
	FinalURL string
	// SessionCookies — cookies, которые отдал Jar после флоу.
	// Используются для последующих API-запросов и для persist в БД.
	SessionCookies []*http.Cookie
}

// Login выполняет 4-шаговый Keycloak handshake против lks.bmstu.ru:
//
//  1. GET /portal4/cookie/login?back=/profile&profile_any=1 — стартует флоу,
//     получает редирект на Keycloak с client_id=sso.
//  2. Follow → GET sso.bmstu.ru/.../auth?... — приходит HTML формы.
//  3. Парсим <form id="kc-form-login" action=...> → формируем POST.
//  4. POST username/password → follow → проверка финального URL.
//
// Все промежуточные cookies остаются в Jar клиента. После успешного
// возврата Jar содержит p4sess и др. — этого достаточно для /lks-back API.
//
// Ошибки:
//   - ErrBadCredentials: финальный HTML снова форма + alert-error.
//   - ErrCaptcha: в форме есть g-recaptcha/h-captcha.
//   - ErrRateLimited: HTTP 429 от Keycloak.
//   - ErrLoginFormNotFound: не удалось найти форму в шаге 2.
//   - ErrUnexpectedResponse: неожиданный код/тело.
func (c *Client) Login(ctx context.Context, login, password string) (*LoginResult, error) {
	if login == "" || password == "" {
		return nil, fmt.Errorf("%w: empty login or password", ErrBadCredentials)
	}

	// Шаг 1+2: стартовая точка → redirect-цепочка до HTML формы.
	formActionURL, err := c.fetchLoginForm(ctx)
	if err != nil {
		return nil, err
	}

	// Шаг 3+4: POST credentials → follow → проверка финального состояния.
	finalURL, err := c.submitCredentials(ctx, formActionURL, login, password)
	if err != nil {
		return nil, err
	}

	// Собираем cookies со всех домен/путей, относящихся к флоу.
	cookies := c.collectSessionCookies()
	return &LoginResult{
		FinalURL:       finalURL,
		SessionCookies: cookies,
	}, nil
}

// fetchLoginForm выполняет шаги 1–2 и возвращает URL action из формы.
func (c *Client) fetchLoginForm(ctx context.Context) (string, error) {
	initURL := strings.TrimRight(c.baseURL, "/") + LoginInitPath +
		"?back=" + url.QueryEscape(c.baseURL+"/profile") + "&profile_any=1"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, initURL, http.NoBody)
	if err != nil {
		return "", fmt.Errorf("oidc: build init request: %w", err)
	}
	c.applyDefaultHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("oidc: init request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("oidc: read init body: %w", err)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return "", ErrRateLimited
	}
	// Ожидаем 200 OK c HTML формы Keycloak (после всех редиректов).
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: init status %d", ErrUnexpectedResponse, resp.StatusCode)
	}
	if hasCaptchaMarker(body) {
		return "", ErrCaptcha
	}

	action, err := extractKcFormAction(body)
	if err != nil {
		return "", err
	}

	// Action может быть относительным — резолвим относительно финального URL.
	resolved, err := resolveActionURL(resp.Request.URL, action)
	if err != nil {
		return "", err
	}
	return resolved, nil
}

// submitCredentials выполняет шаги 3–4 и возвращает финальный URL.
func (c *Client) submitCredentials(ctx context.Context, formActionURL, login, password string) (string, error) {
	form := url.Values{}
	form.Set("username", login)
	form.Set("password", password)
	form.Set("credentialId", "")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, formActionURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("oidc: build submit request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.applyDefaultHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("oidc: submit request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("oidc: read submit body: %w", err)
	}

	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return "", ErrRateLimited
	case resp.StatusCode >= 500:
		return "", fmt.Errorf("%w: submit status %d", ErrUnexpectedResponse, resp.StatusCode)
	case resp.StatusCode != http.StatusOK && !isRedirect(resp.StatusCode):
		return "", fmt.Errorf("%w: submit status %d", ErrUnexpectedResponse, resp.StatusCode)
	}

	// Успех Keycloak'а = либо мы уже на /profile (redirect отработал),
	// либо в теле НЕТ формы kc-form-login.
	if containsLoginFormID(body) {
		switch {
		case hasCaptchaMarker(body):
			return "", ErrCaptcha
		case hasLoginFormError(body):
			return "", ErrBadCredentials
		default:
			// Та же форма без явной ошибки — считаем как bad creds,
			// чтобы не зацикливать ретраи.
			return "", ErrBadCredentials
		}
	}

	finalURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	return finalURL, nil
}

// applyDefaultHeaders ставит UA + Accept'ы, как у браузера.
func (c *Client) applyDefaultHeaders(req *http.Request) {
	req.Header.Set("User-Agent", c.userAgent)
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	}
	if req.Header.Get("Accept-Language") == "" {
		req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en;q=0.8")
	}
}

// collectSessionCookies возвращает cookies из Jar для всех URL флоу:
// lks.bmstu.ru (p4sess) + sso.bmstu.ru (KC_*).
func (c *Client) collectSessionCookies() []*http.Cookie {
	urls := []string{
		c.baseURL,
		"https://sso.bmstu.ru",
	}
	seen := make(map[string]struct{})
	out := make([]*http.Cookie, 0, 4)
	for _, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil {
			continue
		}
		for _, ck := range c.http.Jar.Cookies(u) {
			key := ck.Name + "|" + raw
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			cp := *ck
			// Domain поле от Jar.Cookies не приходит — фиксируем хотя бы host.
			if cp.Domain == "" {
				cp.Domain = u.Host
			}
			out = append(out, &cp)
		}
	}
	return out
}

// resolveActionURL принимает action из <form> (может быть относительным)
// и базу — URL HTML страницы.
func resolveActionURL(base *url.URL, action string) (string, error) {
	if base == nil {
		return "", errors.New("oidc: nil base url")
	}
	ref, err := url.Parse(action)
	if err != nil {
		return "", fmt.Errorf("oidc: parse action: %w", err)
	}
	return base.ResolveReference(ref).String(), nil
}

// isRedirect — short-helper.
func isRedirect(code int) bool {
	return code >= 300 && code <= 399
}
