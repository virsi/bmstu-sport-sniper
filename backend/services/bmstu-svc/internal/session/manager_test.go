package session

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/fizcultor/backend/pkg/crypto"

	"github.com/fizcultor/backend/services/bmstu-svc/internal/oidc"
	"github.com/fizcultor/backend/services/bmstu-svc/internal/store"
)

// testKey — детерминированный 32-байтный ключ для unit-тестов.
const testKeyHex = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func mustKey(t *testing.T) []byte {
	t.Helper()
	k, err := hex.DecodeString(testKeyHex)
	require.NoError(t, err)
	require.Len(t, k, crypto.KeySize)
	return k
}

// fakeStore — in-memory реализация Store.
type fakeStore struct {
	creds         map[string]store.BmstuCredential
	sessions      map[string]store.BmstuSession
	touchedLogin  bool
	deletedCalled bool
	upsertCalled  bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		creds:    map[string]store.BmstuCredential{},
		sessions: map[string]store.BmstuSession{},
	}
}

func (f *fakeStore) GetCredentials(_ context.Context, userID string) (store.BmstuCredential, error) {
	c, ok := f.creds[userID]
	if !ok {
		return store.BmstuCredential{}, pgx.ErrNoRows
	}
	return c, nil
}

func (f *fakeStore) UpsertSession(_ context.Context, arg store.UpsertSessionParams) error {
	f.upsertCalled = true
	f.sessions[arg.UserID] = store.BmstuSession{
		UserID:        arg.UserID,
		CookiesBlob:   arg.CookiesBlob,
		Nonce:         arg.Nonce,
		ExpiresAt:     arg.ExpiresAt,
		LastRefreshAt: time.Now().UTC(),
	}
	return nil
}

func (f *fakeStore) GetSession(_ context.Context, userID string) (store.BmstuSession, error) {
	s, ok := f.sessions[userID]
	if !ok {
		return store.BmstuSession{}, pgx.ErrNoRows
	}
	return s, nil
}

func (f *fakeStore) DeleteSession(_ context.Context, userID string) error {
	f.deletedCalled = true
	delete(f.sessions, userID)
	return nil
}

func (f *fakeStore) TouchCredentialsLastLogin(_ context.Context, _ string) error {
	f.touchedLogin = true
	return nil
}

// fakeOIDC — управляемый стаб OIDCClient.
type fakeOIDC struct {
	loginCalls int
	loginErr   error
	cookies    []*http.Cookie
	alive      bool
}

func (f *fakeOIDC) Login(_ context.Context, _, _ string) (*oidc.LoginResult, error) {
	f.loginCalls++
	if f.loginErr != nil {
		return nil, f.loginErr
	}
	return &oidc.LoginResult{FinalURL: "https://lks.bmstu.ru/profile", SessionCookies: f.cookies}, nil
}

func (f *fakeOIDC) IsAlive(_ context.Context, _ *http.Client) bool { return f.alive }

func putCreds(t *testing.T, fs *fakeStore, key []byte, userID, login, password string) {
	t.Helper()
	encL, err := crypto.Encrypt(key, []byte(login))
	require.NoError(t, err)
	encP, err := crypto.Encrypt(key, []byte(password))
	require.NoError(t, err)
	fs.creds[userID] = store.BmstuCredential{
		UserID:        userID,
		EncLogin:      encL,
		EncPassword:   encP,
		NonceLogin:    encL[:crypto.NonceSize],
		NoncePassword: encP[:crypto.NonceSize],
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
}

func TestManager_Acquire_NoCreds(t *testing.T) {
	key := mustKey(t)
	fs := newFakeStore()
	fo := &fakeOIDC{}
	m, err := New(fs, fo, Config{MasterKey: key, LKSBaseURL: "https://lks.bmstu.ru"}, nil)
	require.NoError(t, err)

	_, err = m.Acquire(context.Background(), "u1")
	require.True(t, errors.Is(err, ErrCredentialsNotLinked))
}

func TestManager_Acquire_FreshLogin(t *testing.T) {
	key := mustKey(t)
	fs := newFakeStore()
	putCreds(t, fs, key, "u1", "ivan", "p@ss")

	fo := &fakeOIDC{
		cookies: []*http.Cookie{
			{Name: "p4sess", Value: "abc", Domain: "lks.bmstu.ru"},
			{Name: "AUTH_SESSION_ID", Value: "kk", Domain: "sso.bmstu.ru"},
		},
		alive: true,
	}
	m, err := New(fs, fo, Config{MasterKey: key, LKSBaseURL: "https://lks.bmstu.ru"}, nil)
	require.NoError(t, err)

	hc, err := m.Acquire(context.Background(), "u1")
	require.NoError(t, err)
	require.NotNil(t, hc)
	require.NotNil(t, hc.Jar)
	require.Equal(t, 1, fo.loginCalls)
	require.True(t, fs.upsertCalled)
	require.True(t, fs.touchedLogin)
	require.NotEmpty(t, fs.sessions["u1"].CookiesBlob)
}

func TestManager_Acquire_ReusesAliveSession(t *testing.T) {
	key := mustKey(t)
	fs := newFakeStore()
	putCreds(t, fs, key, "u1", "ivan", "p@ss")

	cookies := []*http.Cookie{{Name: "p4sess", Value: "abc", Domain: "lks.bmstu.ru"}}
	blob, nonce, err := EncodeCookies(key, cookies)
	require.NoError(t, err)
	fs.sessions["u1"] = store.BmstuSession{
		UserID: "u1", CookiesBlob: blob, Nonce: nonce, LastRefreshAt: time.Now(),
	}

	fo := &fakeOIDC{alive: true}
	m, err := New(fs, fo, Config{MasterKey: key, LKSBaseURL: "https://lks.bmstu.ru"}, nil)
	require.NoError(t, err)

	hc, err := m.Acquire(context.Background(), "u1")
	require.NoError(t, err)
	require.NotNil(t, hc.Jar)
	require.Equal(t, 0, fo.loginCalls, "should reuse session without re-login")
}

func TestManager_Acquire_DeadSessionTriggersReLogin(t *testing.T) {
	key := mustKey(t)
	fs := newFakeStore()
	putCreds(t, fs, key, "u1", "ivan", "p@ss")

	cookies := []*http.Cookie{{Name: "p4sess", Value: "stale", Domain: "lks.bmstu.ru"}}
	blob, nonce, _ := EncodeCookies(key, cookies)
	fs.sessions["u1"] = store.BmstuSession{
		UserID: "u1", CookiesBlob: blob, Nonce: nonce, LastRefreshAt: time.Now().Add(-time.Hour),
	}

	fo := &fakeOIDC{
		alive: false, // watchdog говорит «мёртвая»
		cookies: []*http.Cookie{
			{Name: "p4sess", Value: "fresh", Domain: "lks.bmstu.ru"},
		},
	}
	m, err := New(fs, fo, Config{MasterKey: key, LKSBaseURL: "https://lks.bmstu.ru"}, nil)
	require.NoError(t, err)

	_, err = m.Acquire(context.Background(), "u1")
	require.NoError(t, err)
	require.Equal(t, 1, fo.loginCalls)
	require.True(t, fs.deletedCalled)
	require.Equal(t, "fresh", cookieValueFromBlob(t, fs.sessions["u1"].CookiesBlob, key, "p4sess"))
}

func TestManager_Refresh_AlwaysReLogins(t *testing.T) {
	key := mustKey(t)
	fs := newFakeStore()
	putCreds(t, fs, key, "u1", "ivan", "p@ss")

	cookies := []*http.Cookie{{Name: "p4sess", Value: "fresh", Domain: "lks.bmstu.ru"}}
	blob, nonce, _ := EncodeCookies(key, cookies)
	fs.sessions["u1"] = store.BmstuSession{UserID: "u1", CookiesBlob: blob, Nonce: nonce}

	fo := &fakeOIDC{alive: true, cookies: cookies}
	m, err := New(fs, fo, Config{MasterKey: key, LKSBaseURL: "https://lks.bmstu.ru"}, nil)
	require.NoError(t, err)

	_, err = m.Refresh(context.Background(), "u1")
	require.NoError(t, err)
	require.Equal(t, 1, fo.loginCalls, "Refresh must re-login")
}

func TestManager_Refresh_BadCreds(t *testing.T) {
	key := mustKey(t)
	fs := newFakeStore()
	putCreds(t, fs, key, "u1", "ivan", "wrong")

	fo := &fakeOIDC{loginErr: oidc.ErrBadCredentials}
	m, err := New(fs, fo, Config{MasterKey: key, LKSBaseURL: "https://lks.bmstu.ru"}, nil)
	require.NoError(t, err)

	_, err = m.Refresh(context.Background(), "u1")
	require.ErrorIs(t, err, oidc.ErrBadCredentials)
}

func TestCookies_RoundTrip(t *testing.T) {
	key := mustKey(t)
	in := []*http.Cookie{
		{Name: "p4sess", Value: "abc", Domain: "lks.bmstu.ru", Path: "/"},
		{Name: "AUTH_SESSION_ID", Value: "kk", Domain: "sso.bmstu.ru"},
	}
	blob, _, err := EncodeCookies(key, in)
	require.NoError(t, err)

	out, err := DecodeCookies(key, blob)
	require.NoError(t, err)
	require.Len(t, out, 2)
	require.Equal(t, "p4sess", out[0].Name)
	require.Equal(t, "abc", out[0].Value)
	require.Equal(t, "lks.bmstu.ru", out[0].Domain)
}

func TestCookies_DecryptWithWrongKey(t *testing.T) {
	key := mustKey(t)
	otherKey, _ := crypto.NewKey()

	blob, _, err := EncodeCookies(key, []*http.Cookie{{Name: "x", Value: "y"}})
	require.NoError(t, err)

	_, err = DecodeCookies(otherKey, blob)
	require.Error(t, err)
}

func TestLoadJar_GroupsByDomain(t *testing.T) {
	cookies := []*http.Cookie{
		{Name: "p4sess", Value: "v1", Domain: "lks.bmstu.ru"},
		{Name: "KC_AUTH", Value: "v2", Domain: "sso.bmstu.ru"},
	}
	jar, err := LoadJar(cookies, "https://lks.bmstu.ru")
	require.NoError(t, err)

	urlLKS, _ := url.Parse("https://lks.bmstu.ru/lks-back/api/v1/fv/x/groups")
	urlSSO, _ := url.Parse("https://sso.bmstu.ru/kc/realms/ph/protocol/openid-connect/auth")
	require.True(t, hasCookie(jar.Cookies(urlLKS), "p4sess"))
	require.True(t, hasCookie(jar.Cookies(urlSSO), "KC_AUTH"))
}

func TestExpiresUTC(t *testing.T) {
	now := time.Now().UTC()
	future := now.Add(time.Hour)
	in := []*http.Cookie{
		{Name: "a"}, // session-only
		{Name: "b", Expires: now},
		{Name: "c", Expires: future},
	}
	got := ExpiresUTC(in)
	require.NotNil(t, got)
	require.WithinDuration(t, future, *got, time.Second)
}

func TestExpiresUTC_AllSessionOnly(t *testing.T) {
	got := ExpiresUTC([]*http.Cookie{{Name: "x"}})
	require.Nil(t, got)
}

// helpers ------------------------------------------------------------

func hasCookie(cs []*http.Cookie, name string) bool {
	for _, c := range cs {
		if c.Name == name {
			return true
		}
	}
	return false
}

func cookieValueFromBlob(t *testing.T, blob, key []byte, name string) string {
	t.Helper()
	cs, err := DecodeCookies(key, blob)
	require.NoError(t, err)
	for _, c := range cs {
		if c.Name == name {
			return c.Value
		}
	}
	t.Fatalf("cookie %q not found", name)
	return ""
}
