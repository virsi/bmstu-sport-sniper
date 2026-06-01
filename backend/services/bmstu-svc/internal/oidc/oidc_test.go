package oidc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// keycloakStub — упрощённая имитация Keycloak + portal4 для тестов:
//
//	GET /portal4/cookie/login → 302 на /kc/auth (своё HTML с формой).
//	GET /kc/auth              → 200, форма с action=/kc/submit.
//	POST /kc/submit           → 302 на /portal4/upstream/callback/kc?code=...
//	                            ИЛИ 200 OK с той же формой + alert-error.
//	GET /portal4/upstream/callback/kc?code=... → 302 на /profile + cookie p4sess.
//	GET /profile             → 200 OK.
//	GET /portal4/cookie/watchdog → 200 OK с {status, interval}.
//
// validUser/validPassword управляют успехом сценария; для других значений
// возвращаем форму с alert-error (имитация неверного логина).
type keycloakStub struct {
	validUser     string
	validPassword string
	// rateLimitInit — если true, начальный GET отдаёт 429.
	rateLimitInit bool
	// captchaInit — если true, форма содержит g-recaptcha маркер.
	captchaInit bool
	// expireOnSubmit — если true, POST отдаёт ту же форму без alert-error
	// (имитация истёкшего KC_AUTH_SESSION_HASH).
	expireOnSubmit bool
	// watchdogStatus — что отдавать на /watchdog.
	watchdogStatus string
}

func (s *keycloakStub) handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/portal4/cookie/login", func(w http.ResponseWriter, r *http.Request) {
		if s.rateLimitInit {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "p4sess_intermediate", Value: "wip"})
		http.Redirect(w, r, "/kc/auth?client_id=sso&tab_id=t1", http.StatusFound)
	})

	mux.HandleFunc("/kc/auth", func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "AUTH_SESSION_ID", Value: "auth-sid"})
		http.SetCookie(w, &http.Cookie{Name: "KC_AUTH_SESSION_HASH", Value: "hash"})
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		body := `<!doctype html><html><body>` +
			`<form id="kc-form-login" action="/kc/submit?execution=e1" method="post">` +
			`<input name="username"><input name="password"></form>`
		if s.captchaInit {
			body += `<div class="g-recaptcha" data-sitekey="abc"></div>`
		}
		body += `</body></html>`
		_, _ = w.Write([]byte(body))
	})

	mux.HandleFunc("/kc/submit", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		user := r.FormValue("username")
		pass := r.FormValue("password")

		if s.expireOnSubmit {
			// Возвращаем форму без alert-error — имитация expired session,
			// клиент должен принять это за bad credentials (защитное поведение).
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<form id="kc-form-login" action="/kc/submit"></form>`))
			return
		}

		if user != s.validUser || pass != s.validPassword {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<form id="kc-form-login" action="/kc/submit">` +
				`<div class="alert-error">Bad creds</div></form>`))
			return
		}
		http.Redirect(w, r, "/portal4/upstream/callback/kc?code=ok&state=s1", http.StatusFound)
	})

	mux.HandleFunc("/portal4/upstream/callback/kc", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "p4sess", Value: "final", Path: "/"})
		http.Redirect(w, r, "/profile", http.StatusFound)
	})

	mux.HandleFunc("/profile", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><body>profile ok</body></html>`))
	})

	mux.HandleFunc("/portal4/cookie/watchdog", func(w http.ResponseWriter, r *http.Request) {
		var hasP4 bool
		for _, c := range r.Cookies() {
			if c.Name == "p4sess" {
				hasP4 = true
				break
			}
		}
		if !hasP4 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		st := s.watchdogStatus
		if st == "" {
			st = "OK"
		}
		_, _ = fmt.Fprintf(w, `{"status":%q,"interval":30}`, st)
	})

	return mux
}

// newTestClient собирает Client поверх httptest.Server, переопределяя baseURL.
func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c, err := New(WithBaseURL(srv.URL))
	require.NoError(t, err)
	return c
}

func TestLogin_Success(t *testing.T) {
	stub := &keycloakStub{validUser: "ivan", validPassword: "p@ss"}
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)

	c := newTestClient(t, srv)
	res, err := c.Login(context.Background(), "ivan", "p@ss")
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Contains(t, res.FinalURL, "/profile")

	// Проверяем, что p4sess попал в jar.
	u, _ := url.Parse(srv.URL)
	cookies := c.Jar().Cookies(u)
	var hasP4 bool
	for _, ck := range cookies {
		if ck.Name == "p4sess" {
			hasP4 = true
		}
	}
	require.True(t, hasP4, "p4sess cookie not set after login: %+v", cookies)
}

func TestLogin_BadCredentials(t *testing.T) {
	stub := &keycloakStub{validUser: "ivan", validPassword: "p@ss"}
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)

	c := newTestClient(t, srv)
	_, err := c.Login(context.Background(), "ivan", "wrong")
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrBadCredentials), "want ErrBadCredentials, got %v", err)
}

func TestLogin_EmptyCredentials(t *testing.T) {
	c, err := New(WithBaseURL("http://unused"))
	require.NoError(t, err)
	_, err = c.Login(context.Background(), "", "")
	require.True(t, errors.Is(err, ErrBadCredentials))
}

func TestLogin_RateLimited(t *testing.T) {
	stub := &keycloakStub{validUser: "x", validPassword: "y", rateLimitInit: true}
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)

	c := newTestClient(t, srv)
	_, err := c.Login(context.Background(), "x", "y")
	require.True(t, errors.Is(err, ErrRateLimited), "got %v", err)
}

func TestLogin_Captcha(t *testing.T) {
	stub := &keycloakStub{validUser: "x", validPassword: "y", captchaInit: true}
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)

	c := newTestClient(t, srv)
	_, err := c.Login(context.Background(), "x", "y")
	require.True(t, errors.Is(err, ErrCaptcha), "got %v", err)
}

func TestLogin_ExpiredKCSession(t *testing.T) {
	stub := &keycloakStub{validUser: "x", validPassword: "y", expireOnSubmit: true}
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)

	c := newTestClient(t, srv)
	_, err := c.Login(context.Background(), "x", "y")
	// Защитное поведение: форма без alert-error воспринимается как
	// «нет успешного редиректа» = bad credentials, чтобы не зацикливать ретраи.
	require.True(t, errors.Is(err, ErrBadCredentials), "got %v", err)
}

func TestLogin_NoFormFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>no form here</body></html>"))
	}))
	t.Cleanup(srv.Close)

	c, err := New(WithBaseURL(srv.URL))
	require.NoError(t, err)
	_, err = c.Login(context.Background(), "u", "p")
	require.True(t, errors.Is(err, ErrLoginFormNotFound) || errors.Is(err, ErrUnexpectedResponse))
}

func TestWatchdog_OKWhenAuthed(t *testing.T) {
	stub := &keycloakStub{validUser: "u", validPassword: "p", watchdogStatus: "OK"}
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)

	c := newTestClient(t, srv)
	_, err := c.Login(context.Background(), "u", "p")
	require.NoError(t, err)

	st, err := c.Watchdog(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, "OK", st.Status)
	require.True(t, c.IsAlive(context.Background(), nil))
}

func TestWatchdog_ExpiredWhenUnauth(t *testing.T) {
	stub := &keycloakStub{watchdogStatus: "OK"}
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)

	c := newTestClient(t, srv)
	// Без логина — Jar пустой; watchdog должен вернуть 401 → ErrSessionExpired.
	_, err := c.Watchdog(context.Background(), nil)
	require.True(t, errors.Is(err, ErrSessionExpired), "got %v", err)
	require.False(t, c.IsAlive(context.Background(), nil))
}

func TestExtractKcFormAction(t *testing.T) {
	tests := []struct {
		name    string
		html    string
		want    string
		wantErr bool
	}{
		{
			name: "happy path with id and action",
			html: `<form id="kc-form-login" action="/submit"></form>`,
			want: "/submit",
		},
		{
			name: "id before action different order",
			html: `<form action="/x" id="kc-form-login" method="post"></form>`,
			want: "/x",
		},
		{
			name: "ignores other forms first",
			html: `<form id="other" action="/o"></form><form id="kc-form-login" action="/k"></form>`,
			want: "/k",
		},
		{
			name:    "empty body",
			html:    "",
			wantErr: true,
		},
		{
			name:    "missing form id",
			html:    `<form action="/x"></form>`,
			wantErr: true,
		},
		{
			name:    "form with empty action",
			html:    `<form id="kc-form-login" action=""></form>`,
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := extractKcFormAction([]byte(tc.html))
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestResolveActionURL(t *testing.T) {
	base, _ := url.Parse("https://sso.bmstu.ru/kc/realms/ph/protocol/openid-connect/auth")
	got, err := resolveActionURL(base, "/kc/realms/ph/login-actions/authenticate?session_code=X")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(got, "https://sso.bmstu.ru/kc/realms/ph/login-actions/"))
}

func TestHasLoginFormError(t *testing.T) {
	require.True(t, hasLoginFormError([]byte(`<div class="alert-error">x</div>`)))
	require.True(t, hasLoginFormError([]byte(`<div class="pf-c-alert pf-m-danger pf-c-alert--danger"></div>`)))
	require.False(t, hasLoginFormError([]byte(`<div>fine</div>`)))
}
