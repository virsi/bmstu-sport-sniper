//go:build integration

// Package integration_test hosts cross-component integration tests for
// auth-svc. They are compiled only under the `integration` build tag because
// they spin up Postgres via testcontainers, which requires Docker.
//
// Run them with:
//
//	cd backend/services/auth-svc
//	go test -tags integration ./integration_test/... -v -timeout 120s
package integration_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	authv1 "github.com/fizcultor/backend/gen/auth/v1"
	"github.com/fizcultor/backend/pkg/jwtx"
	"github.com/fizcultor/backend/services/auth-svc/internal/auth"
	authstore "github.com/fizcultor/backend/services/auth-svc/internal/store"
	"github.com/fizcultor/backend/tests/testhelpers"
)

// jwtTestSecret is a non-secret 32-byte string used to sign tokens during
// integration tests. It must satisfy auth-svc's >=32 byte requirement.
const jwtTestSecret = "integration-test-secret-32-bytes!" // 33 bytes

// startAuthService wires a complete auth-svc backend (real DB + real gRPC
// server on bufconn) and returns a connected gRPC client.
func startAuthService(t *testing.T) authv1.AuthServiceClient {
	t.Helper()

	pg := testhelpers.StartPostgres(t, "auth_db")
	st := authstore.New(pg.Pool)
	signer := jwtx.NewSigner([]byte(jwtTestSecret), auth.JWTIssuer)
	verifier := jwtx.NewVerifier([]byte(jwtTestSecret))

	svc, err := auth.NewService(st, auth.Config{
		Signer:     signer,
		Verifier:   verifier,
		AccessTTL:  15 * time.Minute,
		RefreshTTL: 7 * 24 * time.Hour,
		Argon2: auth.Argon2Params{
			// Lower than prod defaults to keep tests fast. Password hashing
			// strength is not the property under test.
			MemoryKiB:   16 * 1024,
			Iterations:  1,
			Parallelism: 1,
		},
	})
	require.NoError(t, err, "build auth service")

	grpcSrv := testhelpers.StartGRPCServer(t)
	authv1.RegisterAuthServiceServer(grpcSrv.Server, svc)
	grpcSrv.Serve(t)
	return authv1.NewAuthServiceClient(grpcSrv.Dial(t))
}

func TestAuth_RegisterLoginGetMe_RoundTrip(t *testing.T) {
	client := startAuthService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const email = "alice@example.com"
	const password = "password-123"

	reg, err := client.Register(ctx, &authv1.RegisterRequest{
		Email: email, Password: password,
	})
	require.NoError(t, err, "register should succeed")
	require.NotNil(t, reg.GetUser())
	require.Equal(t, email, reg.GetUser().GetEmail())
	userID := reg.GetUser().GetId()
	require.NotEmpty(t, userID)

	tp, err := client.Login(ctx, &authv1.LoginRequest{
		Email: email, Password: password,
	})
	require.NoError(t, err, "login should succeed with correct credentials")
	require.NotEmpty(t, tp.GetAccessToken())
	require.NotEmpty(t, tp.GetRefreshToken())

	// Access token must verify and decode to the correct user.
	va, err := client.VerifyAccess(ctx, &authv1.VerifyAccessRequest{
		AccessToken: tp.GetAccessToken(),
	})
	require.NoError(t, err, "verify access should succeed")
	require.Equal(t, userID, va.GetUserId(), "access token subject must match user")

	// GetMe via internal call (user_id passed in body).
	me, err := client.GetMe(ctx, &authv1.GetMeRequest{UserId: userID})
	require.NoError(t, err, "GetMe should find the user")
	require.Equal(t, email, me.GetEmail())
}

func TestAuth_Register_DuplicateEmail_AlreadyExists(t *testing.T) {
	client := startAuthService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const email = "dup@example.com"
	_, err := client.Register(ctx, &authv1.RegisterRequest{Email: email, Password: "password-123"})
	require.NoError(t, err)

	_, err = client.Register(ctx, &authv1.RegisterRequest{Email: email, Password: "another-secret"})
	require.Error(t, err, "second register with same email must fail")
}

func TestAuth_Login_WrongPassword_Unauthenticated(t *testing.T) {
	client := startAuthService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const email = "wrongpass@example.com"
	_, err := client.Register(ctx, &authv1.RegisterRequest{Email: email, Password: "correct-horse-battery-staple"})
	require.NoError(t, err)

	_, err = client.Login(ctx, &authv1.LoginRequest{Email: email, Password: "wrong-password"})
	require.Error(t, err, "login with wrong password must fail")
}

func TestAuth_Refresh_RotatesTokens(t *testing.T) {
	client := startAuthService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const email = "rot@example.com"
	_, err := client.Register(ctx, &authv1.RegisterRequest{Email: email, Password: "password-123"})
	require.NoError(t, err)

	tp1, err := client.Login(ctx, &authv1.LoginRequest{Email: email, Password: "password-123"})
	require.NoError(t, err)

	tp2, err := client.Refresh(ctx, &authv1.RefreshRequest{RefreshToken: tp1.GetRefreshToken()})
	require.NoError(t, err, "refresh with valid token should succeed")
	require.NotEqual(t, tp1.GetRefreshToken(), tp2.GetRefreshToken(),
		"refresh must rotate to a new token")

	// Old refresh must now be rejected (single-use).
	_, err = client.Refresh(ctx, &authv1.RefreshRequest{RefreshToken: tp1.GetRefreshToken()})
	require.Error(t, err, "old refresh token must not be reusable after rotation")

	// New refresh still works.
	tp3, err := client.Refresh(ctx, &authv1.RefreshRequest{RefreshToken: tp2.GetRefreshToken()})
	require.NoError(t, err, "newly issued refresh should work")
	require.NotEqual(t, tp2.GetRefreshToken(), tp3.GetRefreshToken())
}

func TestAuth_Refresh_ReuseAfterRotation_RevokesEntireSession(t *testing.T) {
	client := startAuthService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const email = "reuse@example.com"
	_, err := client.Register(ctx, &authv1.RegisterRequest{Email: email, Password: "password-123"})
	require.NoError(t, err)

	// Two parallel sessions: login twice → two distinct refresh tokens issued.
	sess1, err := client.Login(ctx, &authv1.LoginRequest{Email: email, Password: "password-123"})
	require.NoError(t, err)
	sess2, err := client.Login(ctx, &authv1.LoginRequest{Email: email, Password: "password-123"})
	require.NoError(t, err)
	require.NotEqual(t, sess1.GetRefreshToken(), sess2.GetRefreshToken())

	// Rotate session 1 normally.
	rotated1, err := client.Refresh(ctx, &authv1.RefreshRequest{RefreshToken: sess1.GetRefreshToken()})
	require.NoError(t, err)

	// Now reuse the old token from session 1 (already revoked by rotation).
	// This must trigger reuse-detection → revoke all refresh tokens for the user.
	_, err = client.Refresh(ctx, &authv1.RefreshRequest{RefreshToken: sess1.GetRefreshToken()})
	require.Error(t, err, "reusing revoked refresh must fail")

	// Both other refresh tokens (sess2 and rotated1) must also now be revoked.
	_, err = client.Refresh(ctx, &authv1.RefreshRequest{RefreshToken: sess2.GetRefreshToken()})
	require.Error(t, err, "session 2 must be revoked by reuse-detection on session 1")

	_, err = client.Refresh(ctx, &authv1.RefreshRequest{RefreshToken: rotated1.GetRefreshToken()})
	require.Error(t, err, "rotated session 1 token must also be revoked")
}

func TestAuth_Revoke_FullLogout(t *testing.T) {
	client := startAuthService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const email = "revoke@example.com"
	_, err := client.Register(ctx, &authv1.RegisterRequest{Email: email, Password: "password-123"})
	require.NoError(t, err)

	tp1, err := client.Login(ctx, &authv1.LoginRequest{Email: email, Password: "password-123"})
	require.NoError(t, err)
	tp2, err := client.Login(ctx, &authv1.LoginRequest{Email: email, Password: "password-123"})
	require.NoError(t, err)

	// Revoke one — auth-svc applies full-session semantics → both go away.
	_, err = client.Revoke(ctx, &authv1.RevokeRequest{RefreshToken: tp1.GetRefreshToken()})
	require.NoError(t, err, "revoke should succeed")

	_, err = client.Refresh(ctx, &authv1.RefreshRequest{RefreshToken: tp1.GetRefreshToken()})
	require.Error(t, err, "revoked refresh must not be usable")

	_, err = client.Refresh(ctx, &authv1.RefreshRequest{RefreshToken: tp2.GetRefreshToken()})
	require.Error(t, err, "second refresh must also be revoked (full logout)")
}

func TestAuth_Revoke_Idempotent(t *testing.T) {
	client := startAuthService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Empty refresh = no-op.
	_, err := client.Revoke(ctx, &authv1.RevokeRequest{})
	require.NoError(t, err, "revoke with empty token is a no-op")

	// Unknown token = also no-op (anti-enumeration).
	_, err = client.Revoke(ctx, &authv1.RevokeRequest{RefreshToken: "this-token-does-not-exist"})
	require.NoError(t, err, "revoke with unknown token is a no-op")
}

func TestAuth_LinkTelegram_InitCompleteFlow(t *testing.T) {
	client := startAuthService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const email = "tg@example.com"
	reg, err := client.Register(ctx, &authv1.RegisterRequest{Email: email, Password: "password-123"})
	require.NoError(t, err)
	userID := reg.GetUser().GetId()

	init, err := client.LinkTelegramInit(ctx, &authv1.LinkTelegramInitRequest{UserId: userID})
	require.NoError(t, err, "telegram init")
	require.NotEmpty(t, init.GetCode())
	require.NotEmpty(t, init.GetDeeplink())

	const chatID int64 = 424242
	done, err := client.LinkTelegramComplete(ctx, &authv1.LinkTelegramCompleteRequest{
		Code: init.GetCode(), TelegramChatId: chatID,
	})
	require.NoError(t, err, "telegram complete")
	require.Equal(t, userID, done.GetUserId())

	// Second attempt with the same code must fail (single-use).
	_, err = client.LinkTelegramComplete(ctx, &authv1.LinkTelegramCompleteRequest{
		Code: init.GetCode(), TelegramChatId: chatID + 1,
	})
	require.Error(t, err, "telegram link code is single-use")

	// GetMe must now show the chat_id.
	me, err := client.GetMe(ctx, &authv1.GetMeRequest{UserId: userID})
	require.NoError(t, err)
	require.Equal(t, chatID, me.GetTelegramChatId(), "chat_id must be linked to user")
}

// TestAuth_RefreshTokenRow_HasReplacedBy_AfterRotation asserts the
// auth-svc actually writes replaced_by in refresh_tokens on rotation —
// the contract that powers reuse-detection.
func TestAuth_RefreshTokenRow_HasReplacedBy_AfterRotation(t *testing.T) {
	pg := testhelpers.StartPostgres(t, "auth_db")

	st := authstore.New(pg.Pool)
	signer := jwtx.NewSigner([]byte(jwtTestSecret), auth.JWTIssuer)
	verifier := jwtx.NewVerifier([]byte(jwtTestSecret))
	svc, err := auth.NewService(st, auth.Config{
		Signer: signer, Verifier: verifier,
		AccessTTL: 15 * time.Minute, RefreshTTL: 24 * time.Hour,
		Argon2: auth.Argon2Params{MemoryKiB: 16 * 1024, Iterations: 1, Parallelism: 1},
	})
	require.NoError(t, err)

	grpcSrv := testhelpers.StartGRPCServer(t)
	authv1.RegisterAuthServiceServer(grpcSrv.Server, svc)
	grpcSrv.Serve(t)
	client := authv1.NewAuthServiceClient(grpcSrv.Dial(t))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const email = "row@example.com"
	reg, err := client.Register(ctx, &authv1.RegisterRequest{Email: email, Password: "password-123"})
	require.NoError(t, err)
	userIDStr := reg.GetUser().GetId()
	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	require.NoError(t, err)

	tp1, err := client.Login(ctx, &authv1.LoginRequest{Email: email, Password: "password-123"})
	require.NoError(t, err)

	_, err = client.Refresh(ctx, &authv1.RefreshRequest{RefreshToken: tp1.GetRefreshToken()})
	require.NoError(t, err)

	// The old refresh row must have revoked=true and replaced_by != null.
	var revoked bool
	var replacedBy *int64
	row := pg.Pool.QueryRow(ctx,
		"SELECT revoked, replaced_by FROM refresh_tokens "+
			"WHERE user_id = $1 ORDER BY created_at ASC LIMIT 1",
		userID,
	)
	require.NoError(t, row.Scan(&revoked, &replacedBy))
	require.True(t, revoked, "first token must be revoked after rotation")
	require.NotNil(t, replacedBy, "first token must have replaced_by set")
}
