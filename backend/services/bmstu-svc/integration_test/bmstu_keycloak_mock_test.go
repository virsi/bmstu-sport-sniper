//go:build integration

// Package integration_test drives bmstu-svc against:
//   - A real Postgres database (bmstu_db migrations applied).
//   - A mock Keycloak / portal4 HTTP server — we never touch lks.bmstu.ru.
//
// Scenarios:
//   - StoreCredentials succeeds when the mock accepts the test-login.
//   - GetStatus returns NOT_LINKED before, then VALID after storing.
//   - DeleteCredentials removes everything (creds + session cascade).
//   - Bad credentials → StoreCredentials returns error and DB stays clean.
//   - StoreCredentials is upsert: second write keeps a single row but with
//     a fresh ciphertext (AES-GCM nonce rotation).
//
// Run them with:
//
//	cd backend/services/bmstu-svc
//	go test -tags integration ./integration_test/... -v -timeout 120s
package integration_test

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	bmstuv1 "github.com/fizcultor/backend/gen/bmstu/v1"
	commonv1 "github.com/fizcultor/backend/gen/common/v1"
	"github.com/fizcultor/backend/services/bmstu-svc/internal/oidc"
	bmstuserver "github.com/fizcultor/backend/services/bmstu-svc/internal/server"
	"github.com/fizcultor/backend/services/bmstu-svc/internal/session"
	bmstustore "github.com/fizcultor/backend/services/bmstu-svc/internal/store"
	"github.com/fizcultor/backend/tests/testhelpers"
)

// testMasterKey is a 32-byte AES-256 key in hex used during integration
// tests. The bytes are fixed so ciphertext shape is deterministic across
// test runs.
const testMasterKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// stubGroupsClient is a no-op groups client; FetchGroups is not under test.
type stubGroupsClient struct{}

func (stubGroupsClient) Fetch(_ context.Context, _ *http.Client, _ string) ([]*commonv1.Slot, error) {
	return nil, errors.New("groups not under test")
}

// keycloakStub replicates the legacy Keycloak/portal4 four-step redirect
// flow so the production oidc.Client can authenticate against it without
// reaching lks.bmstu.ru. Returns success only when login/password match.
type keycloakStub struct {
	validUser     string
	validPassword string
}

func (s *keycloakStub) handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/portal4/cookie/login", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "p4sess_intermediate", Value: "wip"})
		http.Redirect(w, r, "/kc/auth?client_id=sso&tab_id=t1", http.StatusFound)
	})

	mux.HandleFunc("/kc/auth", func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "AUTH_SESSION_ID", Value: "auth-sid"})
		http.SetCookie(w, &http.Cookie{Name: "KC_AUTH_SESSION_HASH", Value: "hash"})
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(
			`<!doctype html><html><body>` +
				`<form id="kc-form-login" action="/kc/submit?execution=e1" method="post">` +
				`<input name="username"><input name="password"></form></body></html>`,
		))
	})

	mux.HandleFunc("/kc/submit", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		user := r.FormValue("username")
		pass := r.FormValue("password")
		if user != s.validUser || pass != s.validPassword {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(
				`<form id="kc-form-login" action="/kc/submit">` +
					`<div class="alert-error">Bad creds</div></form>`,
			))
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
		for _, c := range r.Cookies() {
			if c.Name == "p4sess" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"status":"OK","interval":30}`)
				return
			}
		}
		w.WriteHeader(http.StatusUnauthorized)
	})

	return mux
}

// bmstuTestRig holds everything a bmstu integration test needs.
type bmstuTestRig struct {
	Client    bmstuv1.BmstuServiceClient
	PG        *testhelpers.PostgresContainer
	Keycloak  *httptest.Server
	MasterKey []byte
}

// startBmstuRig wires bmstu-svc against a real Postgres DB and a mock
// Keycloak. validUser / validPassword control which credentials the mock
// accepts.
func startBmstuRig(t *testing.T, validUser, validPassword string) *bmstuTestRig {
	t.Helper()

	pg := testhelpers.StartPostgres(t, "bmstu_db")

	stub := &keycloakStub{validUser: validUser, validPassword: validPassword}
	kc := httptest.NewServer(stub.handler())
	t.Cleanup(kc.Close)

	// oidc.Client pointed at the mock instead of lks.bmstu.ru.
	oidcClient, err := oidc.New(oidc.WithBaseURL(kc.URL))
	require.NoError(t, err)

	// Master key for crypto.Encrypt.
	masterKey, err := hex.DecodeString(testMasterKey)
	require.NoError(t, err)

	queries := bmstustore.New(pg.Pool)

	// Session manager — newClient returns *http.Client with a fresh jar.
	newClient := func() (*http.Client, error) {
		jar, err := cookiejar.New(nil)
		if err != nil {
			return nil, err
		}
		return &http.Client{Jar: jar, Timeout: 10 * time.Second}, nil
	}
	mgr, err := session.New(queries, oidcClient, session.Config{
		MasterKey:  masterKey,
		LKSBaseURL: kc.URL,
	}, newClient)
	require.NoError(t, err)

	srv, err := bmstuserver.New(queries, mgr, oidcClient, stubGroupsClient{}, bmstuserver.Config{
		MasterKey:    masterKey,
		SemesterUUID: "test-semester-uuid",
	})
	require.NoError(t, err, "build bmstu server")

	grpcSrv := testhelpers.StartGRPCServer(t)
	bmstuv1.RegisterBmstuServiceServer(grpcSrv.Server, srv)
	grpcSrv.Serve(t)

	return &bmstuTestRig{
		Client:    bmstuv1.NewBmstuServiceClient(grpcSrv.Dial(t)),
		PG:        pg,
		Keycloak:  kc,
		MasterKey: masterKey,
	}
}

func TestBmstu_StoreCredentials_HappyPath(t *testing.T) {
	rig := startBmstuRig(t, "ivan", "p@ss")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const userID = "user-100"

	// Before: not linked.
	status, err := rig.Client.GetStatus(ctx, &bmstuv1.GetStatusRequest{UserId: userID})
	require.NoError(t, err)
	require.Equal(t, commonv1.BmstuLinkStatus_BMSTU_LINK_STATUS_NOT_LINKED, status.GetStatus(),
		"status must be NOT_LINKED for a fresh user")

	// Store credentials — mock accepts ivan/p@ss.
	storeResp, err := rig.Client.StoreCredentials(ctx, &bmstuv1.StoreCredentialsRequest{
		UserId: userID, Login: "ivan", Password: "p@ss",
	})
	require.NoError(t, err, "StoreCredentials must succeed with valid creds")
	require.Equal(t, commonv1.BmstuLinkStatus_BMSTU_LINK_STATUS_VALID, storeResp.GetStatus())

	// After: status VALID.
	statusAfter, err := rig.Client.GetStatus(ctx, &bmstuv1.GetStatusRequest{UserId: userID})
	require.NoError(t, err)
	require.Equal(t, commonv1.BmstuLinkStatus_BMSTU_LINK_STATUS_VALID, statusAfter.GetStatus())

	// DB-level assertion: row exists with ciphertext that is NOT plaintext.
	var encLogin, encPassword []byte
	require.NoError(t, rig.PG.Pool.QueryRow(ctx,
		"SELECT enc_login, enc_password FROM bmstu_credentials WHERE user_id = $1",
		userID,
	).Scan(&encLogin, &encPassword))
	require.NotEmpty(t, encLogin, "enc_login must be persisted")
	require.NotEmpty(t, encPassword, "enc_password must be persisted")
	require.NotContains(t, string(encLogin), "ivan",
		"persisted login must NOT be plaintext")
	require.NotContains(t, string(encPassword), "p@ss",
		"persisted password must NOT be plaintext")
}

func TestBmstu_StoreCredentials_BadPassword_RejectedNoPersist(t *testing.T) {
	rig := startBmstuRig(t, "ivan", "right-pass")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const userID = "user-200"

	_, err := rig.Client.StoreCredentials(ctx, &bmstuv1.StoreCredentialsRequest{
		UserId: userID, Login: "ivan", Password: "wrong-pass",
	})
	require.Error(t, err, "bad credentials must fail test-login")

	// Nothing in DB.
	var count int
	require.NoError(t, rig.PG.Pool.QueryRow(ctx,
		"SELECT count(*) FROM bmstu_credentials WHERE user_id = $1", userID,
	).Scan(&count))
	require.Zero(t, count, "no creds row may persist after rejected test-login")
}

func TestBmstu_DeleteCredentials_RemovesRowAndIsIdempotent(t *testing.T) {
	rig := startBmstuRig(t, "ivan", "p@ss")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const userID = "user-300"
	_, err := rig.Client.StoreCredentials(ctx, &bmstuv1.StoreCredentialsRequest{
		UserId: userID, Login: "ivan", Password: "p@ss",
	})
	require.NoError(t, err)

	_, err = rig.Client.DeleteCredentials(ctx, &bmstuv1.DeleteCredentialsRequest{UserId: userID})
	require.NoError(t, err, "delete should succeed")

	// Double delete is fine.
	_, err = rig.Client.DeleteCredentials(ctx, &bmstuv1.DeleteCredentialsRequest{UserId: userID})
	require.NoError(t, err, "delete must be idempotent")

	// Row is gone.
	var count int
	require.NoError(t, rig.PG.Pool.QueryRow(ctx,
		"SELECT count(*) FROM bmstu_credentials WHERE user_id = $1", userID,
	).Scan(&count))
	require.Zero(t, count, "credentials row must be gone after delete")

	// Status reverts to NOT_LINKED.
	st, err := rig.Client.GetStatus(ctx, &bmstuv1.GetStatusRequest{UserId: userID})
	require.NoError(t, err)
	require.Equal(t, commonv1.BmstuLinkStatus_BMSTU_LINK_STATUS_NOT_LINKED, st.GetStatus())
}

func TestBmstu_StoreCredentials_EmptyUserID_InvalidArgument(t *testing.T) {
	rig := startBmstuRig(t, "ivan", "p@ss")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := rig.Client.StoreCredentials(ctx, &bmstuv1.StoreCredentialsRequest{
		UserId: "", Login: "ivan", Password: "p@ss",
	})
	require.Error(t, err, "missing user_id must be rejected")
}

func TestBmstu_GetStatus_DBHasNoRow_ReturnsNotLinked(t *testing.T) {
	rig := startBmstuRig(t, "any", "any")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	st, err := rig.Client.GetStatus(ctx, &bmstuv1.GetStatusRequest{UserId: "stranger"})
	require.NoError(t, err, "GetStatus on unknown user must NOT error")
	require.Equal(t, commonv1.BmstuLinkStatus_BMSTU_LINK_STATUS_NOT_LINKED, st.GetStatus())
}

// TestBmstu_StoreCredentials_Update_PreservesUserID asserts that storing
// credentials for the same user_id overwrites the previous row
// (ON CONFLICT UPDATE) rather than creating a duplicate.
func TestBmstu_StoreCredentials_Update_PreservesUserID(t *testing.T) {
	rig := startBmstuRig(t, "ivan", "p@ss")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const userID = "user-400"

	_, err := rig.Client.StoreCredentials(ctx, &bmstuv1.StoreCredentialsRequest{
		UserId: userID, Login: "ivan", Password: "p@ss",
	})
	require.NoError(t, err)

	// Capture the ciphertext from the first write.
	var encLoginFirst []byte
	require.NoError(t, rig.PG.Pool.QueryRow(ctx,
		"SELECT enc_login FROM bmstu_credentials WHERE user_id = $1", userID,
	).Scan(&encLoginFirst))

	// Update credentials.
	_, err = rig.Client.StoreCredentials(ctx, &bmstuv1.StoreCredentialsRequest{
		UserId: userID, Login: "ivan", Password: "p@ss",
	})
	require.NoError(t, err)

	// Still exactly one row for this user.
	var count int
	require.NoError(t, rig.PG.Pool.QueryRow(ctx,
		"SELECT count(*) FROM bmstu_credentials WHERE user_id = $1", userID,
	).Scan(&count))
	require.Equal(t, 1, count, "user must have a single credentials row")

	// Encrypted bytes differ because nonce is fresh each call (AES-GCM property).
	var encLoginSecond []byte
	require.NoError(t, rig.PG.Pool.QueryRow(ctx,
		"SELECT enc_login FROM bmstu_credentials WHERE user_id = $1", userID,
	).Scan(&encLoginSecond))
	require.NotEqual(t, encLoginFirst, encLoginSecond,
		"re-encrypted ciphertext must change (fresh nonce per Encrypt)")
}
