package auth

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	authv1 "github.com/fizcultor/backend/gen/auth/v1"
	"github.com/fizcultor/backend/pkg/jwtx"
	"github.com/fizcultor/backend/services/auth-svc/internal/store"
)

// ============================================================================
// fakeStore — in-memory мок для тестов сервиса. Реализует interface Store.
// Намеренно простой: одна мапа на users, одна на refresh_tokens; никаких
// goroutines, без транзакций — DDL-семантику тестов это не меняет.
// ============================================================================

type fakeStore struct {
	mu sync.Mutex

	users        map[int64]store.User
	usersByEmail map[string]int64
	nextUserID   int64

	refreshByID   map[int64]store.RefreshToken
	refreshByHash map[string]int64
	nextRefreshID int64
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		users:         map[int64]store.User{},
		usersByEmail:  map[string]int64{},
		refreshByID:   map[int64]store.RefreshToken{},
		refreshByHash: map[string]int64{},
	}
}

func (f *fakeStore) GetUserByEmail(_ context.Context, email string) (store.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.usersByEmail[email]
	if !ok {
		return store.User{}, store.ErrNotFound
	}
	return f.users[id], nil
}

func (f *fakeStore) GetUserByID(_ context.Context, id int64) (store.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[id]
	if !ok {
		return store.User{}, store.ErrNotFound
	}
	return u, nil
}

func (f *fakeStore) GetUserByTgLinkToken(_ context.Context, token string) (store.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.users {
		if u.TgLinkToken != nil && *u.TgLinkToken == token {
			return u, nil
		}
	}
	return store.User{}, store.ErrNotFound
}

func (f *fakeStore) CreateUser(_ context.Context, email, passwordHash string) (store.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, dup := f.usersByEmail[email]; dup {
		return store.User{}, store.ErrAlreadyExists
	}
	f.nextUserID++
	id := f.nextUserID
	u := store.User{
		ID:           id,
		Email:        email,
		PasswordHash: passwordHash,
		IsActive:     true,
		CreatedAt:    time.Now().UTC(),
	}
	f.users[id] = u
	f.usersByEmail[email] = id
	return u, nil
}

func (f *fakeStore) UpdateLastSeen(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[id]
	if !ok {
		return store.ErrNotFound
	}
	now := time.Now().UTC()
	u.LastSeenAt = &now
	f.users[id] = u
	return nil
}

func (f *fakeStore) SetTgChatID(_ context.Context, id, chatID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[id]
	if !ok {
		return store.ErrNotFound
	}
	u.TgChatID = &chatID
	u.TgLinkToken = nil
	f.users[id] = u
	return nil
}

func (f *fakeStore) SetTgLinkToken(_ context.Context, id int64, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[id]
	if !ok {
		return store.ErrNotFound
	}
	tok := token
	u.TgLinkToken = &tok
	f.users[id] = u
	return nil
}

func (f *fakeStore) CreateRefreshToken(_ context.Context, userID int64, tokenHash string, expiresAt time.Time) (store.RefreshToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextRefreshID++
	rt := store.RefreshToken{
		ID:        f.nextRefreshID,
		UserID:    userID,
		TokenHash: tokenHash,
		ExpiresAt: expiresAt,
		Revoked:   false,
		CreatedAt: time.Now().UTC(),
	}
	f.refreshByID[rt.ID] = rt
	f.refreshByHash[tokenHash] = rt.ID
	return rt, nil
}

func (f *fakeStore) GetRefreshTokenByHash(_ context.Context, tokenHash string) (store.RefreshToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.refreshByHash[tokenHash]
	if !ok {
		return store.RefreshToken{}, store.ErrNotFound
	}
	return f.refreshByID[id], nil
}

func (f *fakeStore) RevokeRefreshToken(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	rt, ok := f.refreshByID[id]
	if !ok {
		return store.ErrNotFound
	}
	rt.Revoked = true
	f.refreshByID[id] = rt
	return nil
}

func (f *fakeStore) MarkReplacedBy(_ context.Context, id, newID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	rt, ok := f.refreshByID[id]
	if !ok {
		return store.ErrNotFound
	}
	rt.Revoked = true
	rt.ReplacedBy = &newID
	f.refreshByID[id] = rt
	return nil
}

func (f *fakeStore) RevokeAllForUser(_ context.Context, userID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, rt := range f.refreshByID {
		if rt.UserID == userID && !rt.Revoked {
			rt.Revoked = true
			f.refreshByID[id] = rt
		}
	}
	return nil
}

// ============================================================================
// helpers
// ============================================================================

// newTestService возвращает Service с fakeStore и быстрыми argon2-параметрами.
func newTestService(t *testing.T) (*Service, *fakeStore) {
	t.Helper()
	secret := []byte("test-secret-32-bytes-min-padding-xxxxxx") // ≥32 байт.
	signer := jwtx.NewSigner(secret, JWTIssuer)
	verifier := jwtx.NewVerifier(secret)
	fs := newFakeStore()
	svc, err := NewService(fs, Config{
		Signer:     signer,
		Verifier:   verifier,
		AccessTTL:  15 * time.Minute,
		RefreshTTL: 24 * time.Hour,
		Argon2:     fastParams,
	})
	require.NoError(t, err)
	return svc, fs
}

func grpcCode(t *testing.T, err error) codes.Code {
	t.Helper()
	s, ok := status.FromError(err)
	require.True(t, ok, "want grpc status error, got %v", err)
	return s.Code()
}

// ============================================================================
// Register
// ============================================================================

func TestRegister_TableDriven(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		email       string
		password    string
		seed        func(*fakeStore)
		wantCode    codes.Code
		wantSuccess bool
	}{
		{
			name:        "ok",
			email:       "user@example.com",
			password:    "passw0rd!",
			wantSuccess: true,
		},
		{
			name:        "normalize email",
			email:       "  USER@Example.COM  ",
			password:    "passw0rd!",
			wantSuccess: true,
		},
		{
			name:     "short password",
			email:    "user@example.com",
			password: "short",
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "bad email",
			email:    "not-an-email",
			password: "passw0rd!",
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "empty email",
			email:    "",
			password: "passw0rd!",
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "duplicate email",
			email:    "user@example.com",
			password: "passw0rd!",
			seed: func(fs *fakeStore) {
				_, _ = fs.CreateUser(context.Background(), "user@example.com", "x")
			},
			wantCode: codes.AlreadyExists,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc, fs := newTestService(t)
			if tc.seed != nil {
				tc.seed(fs)
			}
			resp, err := svc.Register(context.Background(), &authv1.RegisterRequest{
				Email:    tc.email,
				Password: tc.password,
			})
			if tc.wantSuccess {
				require.NoError(t, err)
				require.NotNil(t, resp)
				require.NotNil(t, resp.GetUser())
				assert.Equal(t, strings.TrimSpace(strings.ToLower(tc.email)), resp.GetUser().GetEmail())
				return
			}
			require.Error(t, err)
			assert.Equal(t, tc.wantCode, grpcCode(t, err))
		})
	}
}

// ============================================================================
// Login
// ============================================================================

func TestLogin_Success(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	ctx := context.Background()

	_, err := svc.Register(ctx, &authv1.RegisterRequest{Email: "a@b.c", Password: "passw0rd!"})
	require.NoError(t, err)

	pair, err := svc.Login(ctx, &authv1.LoginRequest{Email: "a@b.c", Password: "passw0rd!"})
	require.NoError(t, err)
	assert.NotEmpty(t, pair.GetAccessToken())
	assert.NotEmpty(t, pair.GetRefreshToken())
	assert.NotNil(t, pair.GetAccessExpiresAt())
	assert.NotNil(t, pair.GetRefreshExpiresAt())
}

func TestLogin_WrongPassword(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	ctx := context.Background()

	_, err := svc.Register(ctx, &authv1.RegisterRequest{Email: "a@b.c", Password: "passw0rd!"})
	require.NoError(t, err)

	_, err = svc.Login(ctx, &authv1.LoginRequest{Email: "a@b.c", Password: "wrong"})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, grpcCode(t, err))
}

func TestLogin_UnknownEmail(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	_, err := svc.Login(context.Background(), &authv1.LoginRequest{Email: "nobody@b.c", Password: "passw0rd!"})
	require.Error(t, err)
	// Anti-enumeration: тот же код, что и при неверном пароле.
	assert.Equal(t, codes.Unauthenticated, grpcCode(t, err))
}

// ============================================================================
// Refresh — rotation + reuse-detection
// ============================================================================

func TestRefresh_RotationProducesNewPair(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	ctx := context.Background()

	_, err := svc.Register(ctx, &authv1.RegisterRequest{Email: "a@b.c", Password: "passw0rd!"})
	require.NoError(t, err)
	first, err := svc.Login(ctx, &authv1.LoginRequest{Email: "a@b.c", Password: "passw0rd!"})
	require.NoError(t, err)

	second, err := svc.Refresh(ctx, &authv1.RefreshRequest{RefreshToken: first.GetRefreshToken()})
	require.NoError(t, err)

	assert.NotEqual(t, first.GetRefreshToken(), second.GetRefreshToken(), "new refresh issued")
	// Access-токены тоже разные (разные jti/iat).
	assert.NotEqual(t, first.GetAccessToken(), second.GetAccessToken())
}

func TestRefresh_ReuseDetectionRevokesAll(t *testing.T) {
	t.Parallel()
	svc, fs := newTestService(t)
	ctx := context.Background()

	_, err := svc.Register(ctx, &authv1.RegisterRequest{Email: "a@b.c", Password: "passw0rd!"})
	require.NoError(t, err)
	first, err := svc.Login(ctx, &authv1.LoginRequest{Email: "a@b.c", Password: "passw0rd!"})
	require.NoError(t, err)

	// Ротация — first revoked, выпущен second.
	second, err := svc.Refresh(ctx, &authv1.RefreshRequest{RefreshToken: first.GetRefreshToken()})
	require.NoError(t, err)

	// Подаём first ещё раз — это reuse-attack.
	_, err = svc.Refresh(ctx, &authv1.RefreshRequest{RefreshToken: first.GetRefreshToken()})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, grpcCode(t, err))

	// second тоже должен быть revoked (revoke all).
	hash := HashRefreshToken(second.GetRefreshToken())
	rt, err := fs.GetRefreshTokenByHash(ctx, hash)
	require.NoError(t, err)
	assert.True(t, rt.Revoked, "second token must be revoked after reuse-attack")
}

func TestRefresh_InvalidToken(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	_, err := svc.Refresh(context.Background(), &authv1.RefreshRequest{RefreshToken: "never-issued"})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, grpcCode(t, err))
}

func TestRefresh_ExpiredToken(t *testing.T) {
	t.Parallel()
	svc, fs := newTestService(t)
	ctx := context.Background()

	_, err := svc.Register(ctx, &authv1.RegisterRequest{Email: "a@b.c", Password: "passw0rd!"})
	require.NoError(t, err)
	pair, err := svc.Login(ctx, &authv1.LoginRequest{Email: "a@b.c", Password: "passw0rd!"})
	require.NoError(t, err)

	// Перематываем expiry в прошлое прямо в фейке.
	hash := HashRefreshToken(pair.GetRefreshToken())
	fs.mu.Lock()
	id := fs.refreshByHash[hash]
	rt := fs.refreshByID[id]
	rt.ExpiresAt = time.Now().Add(-time.Hour)
	fs.refreshByID[id] = rt
	fs.mu.Unlock()

	_, err = svc.Refresh(ctx, &authv1.RefreshRequest{RefreshToken: pair.GetRefreshToken()})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, grpcCode(t, err))
}

// ============================================================================
// Revoke
// ============================================================================

func TestRevoke_RevokesAllForUser(t *testing.T) {
	t.Parallel()
	svc, fs := newTestService(t)
	ctx := context.Background()

	_, err := svc.Register(ctx, &authv1.RegisterRequest{Email: "a@b.c", Password: "passw0rd!"})
	require.NoError(t, err)

	// Дважды логинимся → две активные сессии (refresh-токена).
	a, err := svc.Login(ctx, &authv1.LoginRequest{Email: "a@b.c", Password: "passw0rd!"})
	require.NoError(t, err)
	b, err := svc.Login(ctx, &authv1.LoginRequest{Email: "a@b.c", Password: "passw0rd!"})
	require.NoError(t, err)

	_, err = svc.Revoke(ctx, &authv1.RevokeRequest{RefreshToken: a.GetRefreshToken()})
	require.NoError(t, err)

	for _, raw := range []string{a.GetRefreshToken(), b.GetRefreshToken()} {
		rt, err := fs.GetRefreshTokenByHash(ctx, HashRefreshToken(raw))
		require.NoError(t, err)
		assert.True(t, rt.Revoked)
	}
}

func TestRevoke_Idempotent(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)

	// Unknown — без ошибки.
	_, err := svc.Revoke(context.Background(), &authv1.RevokeRequest{RefreshToken: "unknown"})
	assert.NoError(t, err)
	// Пустой — без ошибки.
	_, err = svc.Revoke(context.Background(), &authv1.RevokeRequest{RefreshToken: ""})
	assert.NoError(t, err)
}

// ============================================================================
// VerifyAccess
// ============================================================================

func TestVerifyAccess_OK(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	ctx := context.Background()

	reg, err := svc.Register(ctx, &authv1.RegisterRequest{Email: "a@b.c", Password: "passw0rd!"})
	require.NoError(t, err)
	pair, err := svc.Login(ctx, &authv1.LoginRequest{Email: "a@b.c", Password: "passw0rd!"})
	require.NoError(t, err)

	resp, err := svc.VerifyAccess(ctx, &authv1.VerifyAccessRequest{AccessToken: pair.GetAccessToken()})
	require.NoError(t, err)
	assert.Equal(t, reg.GetUser().GetId(), resp.GetUserId())
	assert.NotNil(t, resp.GetExpiresAt())
}

func TestVerifyAccess_BadToken(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)

	_, err := svc.VerifyAccess(context.Background(), &authv1.VerifyAccessRequest{AccessToken: "garbage"})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, grpcCode(t, err))
}

func TestVerifyAccess_WrongKind(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	ctx := context.Background()

	// Выпускаем refresh-вид JWT (служебный — никогда наружу так не отдаем,
	// но проверяем, что VerifyAccess отвергает кросс-вид).
	claims := jwtx.NewClaims("1", jwtx.TokenRefresh, "tid", 15*time.Minute)
	tok, err := svc.cfg.Signer.Sign(claims)
	require.NoError(t, err)

	_, err = svc.VerifyAccess(ctx, &authv1.VerifyAccessRequest{AccessToken: tok})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, grpcCode(t, err))
}

// ============================================================================
// GetMe / LinkTelegram
// ============================================================================

func TestGetMe_FromRequestField(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	ctx := context.Background()

	reg, err := svc.Register(ctx, &authv1.RegisterRequest{Email: "a@b.c", Password: "passw0rd!"})
	require.NoError(t, err)

	got, err := svc.GetMe(ctx, &authv1.GetMeRequest{UserId: reg.GetUser().GetId()})
	require.NoError(t, err)
	assert.Equal(t, reg.GetUser().GetId(), got.GetId())
	assert.Equal(t, "a@b.c", got.GetEmail())
}

func TestGetMe_MissingUserID(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	_, err := svc.GetMe(context.Background(), &authv1.GetMeRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, grpcCode(t, err))
}

func TestLinkTelegram_InitAndComplete(t *testing.T) {
	t.Parallel()
	svc, fs := newTestService(t)
	ctx := context.Background()

	reg, err := svc.Register(ctx, &authv1.RegisterRequest{Email: "a@b.c", Password: "passw0rd!"})
	require.NoError(t, err)

	init, err := svc.LinkTelegramInit(ctx, &authv1.LinkTelegramInitRequest{UserId: reg.GetUser().GetId()})
	require.NoError(t, err)
	assert.NotEmpty(t, init.GetCode())
	assert.NotEmpty(t, init.GetDeeplink())

	const chatID = int64(424242)
	cmp, err := svc.LinkTelegramComplete(ctx, &authv1.LinkTelegramCompleteRequest{
		Code:           init.GetCode(),
		TelegramChatId: chatID,
	})
	require.NoError(t, err)
	assert.Equal(t, reg.GetUser().GetId(), cmp.GetUserId())

	// Проверяем, что chat_id записан и tg_link_token зачищен.
	uid, _ := strconv.ParseInt(reg.GetUser().GetId(), 10, 64)
	u, err := fs.GetUserByID(ctx, uid)
	require.NoError(t, err)
	require.NotNil(t, u.TgChatID)
	assert.Equal(t, chatID, *u.TgChatID)
	assert.Nil(t, u.TgLinkToken, "link token must be cleared after Complete")

	// Повторный Complete с тем же кодом → NotFound.
	_, err = svc.LinkTelegramComplete(ctx, &authv1.LinkTelegramCompleteRequest{
		Code:           init.GetCode(),
		TelegramChatId: chatID,
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, grpcCode(t, err))
}

func TestLinkTelegramComplete_BadInput(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	ctx := context.Background()

	_, err := svc.LinkTelegramComplete(ctx, &authv1.LinkTelegramCompleteRequest{Code: "", TelegramChatId: 1})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, grpcCode(t, err))

	_, err = svc.LinkTelegramComplete(ctx, &authv1.LinkTelegramCompleteRequest{Code: "x", TelegramChatId: 0})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, grpcCode(t, err))
}

// ============================================================================
// NewService validation
// ============================================================================

func TestNewService_Validates(t *testing.T) {
	t.Parallel()
	secret := []byte("test-secret-32-bytes-min-padding-xxxxxx")
	signer := jwtx.NewSigner(secret, JWTIssuer)
	verifier := jwtx.NewVerifier(secret)

	tests := []struct {
		name string
		cfg  Config
		fs   Store
	}{
		{"nil store", Config{Signer: signer, Verifier: verifier, AccessTTL: time.Minute, RefreshTTL: time.Hour}, nil},
		{"nil signer", Config{Verifier: verifier, AccessTTL: time.Minute, RefreshTTL: time.Hour}, newFakeStore()},
		{"nil verifier", Config{Signer: signer, AccessTTL: time.Minute, RefreshTTL: time.Hour}, newFakeStore()},
		{"zero access ttl", Config{Signer: signer, Verifier: verifier, RefreshTTL: time.Hour}, newFakeStore()},
		{"zero refresh ttl", Config{Signer: signer, Verifier: verifier, AccessTTL: time.Minute}, newFakeStore()},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewService(tc.fs, tc.cfg)
			assert.Error(t, err)
		})
	}
}
