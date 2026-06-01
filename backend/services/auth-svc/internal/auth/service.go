package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	authv1 "github.com/fizcultor/backend/gen/auth/v1"
	commonv1 "github.com/fizcultor/backend/gen/common/v1"
	"github.com/fizcultor/backend/pkg/jwtx"
	"github.com/fizcultor/backend/services/auth-svc/internal/store"
)

// JWTIssuer — значение claim "iss" в выпускаемых auth-svc токенах.
const JWTIssuer = "fizcultor-bot-auth"

// MetadataUserIDKey — ключ gRPC-metadata, в котором gateway-svc передаёт user_id
// после успешной валидации access-токена (см. VerifyAccess). Значение —
// строковое представление users.id (BIGINT). Совпадает с ключом, который
// устанавливает pkg/grpcx.WithUserID.
const MetadataUserIDKey = "x-user-id"

// linkTokenTTL — рекомендованное время жизни tg_link_token, отдаётся клиенту
// как expires_at в LinkTelegramInitResponse. Сама БД TTL не enforce'ит — это
// контракт фронта/notifier для UX.
const linkTokenTTL = 10 * time.Minute

// linkTokenBytes — длина «сырой» случайности для tg_link_token (≈22 символа base64).
const linkTokenBytes = 16

// minPasswordBytes — минимальная длина пароля (по контракту с api.md).
const minPasswordBytes = 8

// Минимальный интерфейс persistence-слоя, потребляемый Service.
//
// Реализуется *store.Store и моками в тестах. KISS: один интерфейс на оба
// агрегата (users + refresh_tokens), без отдельных репозиториев — все
// операции в пределах одного auth_db.
//
//nolint:revive // авто-имя длиннее, но описательное: Store задает контракт сервиса.
type Store interface {
	// users.
	GetUserByEmail(ctx context.Context, email string) (store.User, error)
	GetUserByID(ctx context.Context, id int64) (store.User, error)
	GetUserByTgLinkToken(ctx context.Context, token string) (store.User, error)
	CreateUser(ctx context.Context, email, passwordHash string) (store.User, error)
	UpdateLastSeen(ctx context.Context, id int64) error
	SetTgChatID(ctx context.Context, id, chatID int64) error
	SetTgLinkToken(ctx context.Context, id int64, token string) error

	// refresh_tokens.
	CreateRefreshToken(ctx context.Context, userID int64, tokenHash string, expiresAt time.Time) (store.RefreshToken, error)
	GetRefreshTokenByHash(ctx context.Context, tokenHash string) (store.RefreshToken, error)
	RevokeRefreshToken(ctx context.Context, id int64) error
	MarkReplacedBy(ctx context.Context, id, newID int64) error
	RevokeAllForUser(ctx context.Context, userID int64) error
}

// Config — параметры Service (передаются из main, без зависимости на env).
type Config struct {
	// Signer — JWT-подпись для access-токенов.
	Signer *jwtx.Signer
	// Verifier — JWT-валидация (используется в VerifyAccess).
	Verifier *jwtx.Verifier
	// AccessTTL — TTL access-токена.
	AccessTTL time.Duration
	// RefreshTTL — TTL refresh-токена.
	RefreshTTL time.Duration
	// Argon2 — параметры argon2id (нулевые поля → дефолты).
	Argon2 Argon2Params
	// Now — функция текущего времени (для тестов). nil → time.Now.
	Now func() time.Time
	// Logger — slog-логгер. nil → slog.Default().
	Logger *slog.Logger
}

// Service — реализация authv1.AuthServiceServer.
//
// Не зависит от транспортного слоя — методы принимают ctx+req и возвращают
// gRPC-status ошибки. Wiring (регистрация сервера, listener) — в main.
type Service struct {
	authv1.UnimplementedAuthServiceServer

	store Store
	cfg   Config
	log   *slog.Logger
	now   func() time.Time
}

// NewService создаёт Service со ссылкой на Store и конфиг.
// Возвращает ошибку, если конфиг невалиден (nil Signer/Verifier, ≤0 TTL).
func NewService(s Store, cfg Config) (*Service, error) {
	if s == nil {
		return nil, errors.New("auth: nil store")
	}
	if cfg.Signer == nil {
		return nil, errors.New("auth: nil signer")
	}
	if cfg.Verifier == nil {
		return nil, errors.New("auth: nil verifier")
	}
	if cfg.AccessTTL <= 0 {
		return nil, errors.New("auth: AccessTTL must be > 0")
	}
	if cfg.RefreshTTL <= 0 {
		return nil, errors.New("auth: RefreshTTL must be > 0")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Service{store: s, cfg: cfg, log: log, now: now}, nil
}

// ============================================================================
// Register
// ============================================================================

// Register создаёт пользователя. Email нормализуется (lowercase+trim),
// пароль хешируется argon2id.
//
// Errors:
//   - InvalidArgument: невалидный email, пароль < 8 байт.
//   - AlreadyExists: email занят.
//   - Internal: БД-ошибки.
func (s *Service) Register(ctx context.Context, req *authv1.RegisterRequest) (*authv1.RegisterResponse, error) {
	email, err := normalizeEmail(req.GetEmail())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid email")
	}
	if len(req.GetPassword()) < minPasswordBytes {
		return nil, status.Error(codes.InvalidArgument, "password must be at least 8 characters")
	}

	hash, err := HashPassword(req.GetPassword(), s.cfg.Argon2)
	if err != nil {
		s.log.ErrorContext(ctx, "hash password", slog.Any("error", err))
		return nil, status.Error(codes.Internal, "internal error")
	}

	u, err := s.store.CreateUser(ctx, email, hash)
	if err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			return nil, status.Error(codes.AlreadyExists, "email already registered")
		}
		s.log.ErrorContext(ctx, "create user", slog.Any("error", err))
		return nil, status.Error(codes.Internal, "internal error")
	}

	return &authv1.RegisterResponse{User: userToProto(u)}, nil
}

// ============================================================================
// Login
// ============================================================================

// Login проверяет креды и выпускает пару access/refresh.
// Возвращает UNAUTHENTICATED при любой ошибке проверки (не различает причины).
func (s *Service) Login(ctx context.Context, req *authv1.LoginRequest) (*authv1.TokenPair, error) {
	email, err := normalizeEmail(req.GetEmail())
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid credentials")
	}

	u, err := s.store.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// Anti-enumeration: одинаковый ответ для «нет email» и «неверный пароль».
			return nil, status.Error(codes.Unauthenticated, "invalid credentials")
		}
		s.log.ErrorContext(ctx, "get user", slog.Any("error", err))
		return nil, status.Error(codes.Internal, "internal error")
	}
	if !u.IsActive {
		return nil, status.Error(codes.Unauthenticated, "invalid credentials")
	}

	ok, err := VerifyPassword(req.GetPassword(), u.PasswordHash)
	if err != nil {
		s.log.ErrorContext(ctx, "verify password", slog.Any("error", err), slog.Int64("user_id", u.ID))
		return nil, status.Error(codes.Internal, "internal error")
	}
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "invalid credentials")
	}

	pair, err := s.issueTokenPair(ctx, u.ID)
	if err != nil {
		s.log.ErrorContext(ctx, "issue token pair", slog.Any("error", err), slog.Int64("user_id", u.ID))
		return nil, status.Error(codes.Internal, "internal error")
	}

	if err := s.store.UpdateLastSeen(ctx, u.ID); err != nil {
		// Не критично — лог, но возвращаем токены.
		s.log.WarnContext(ctx, "update last_seen", slog.Any("error", err), slog.Int64("user_id", u.ID))
	}
	return pair, nil
}

// ============================================================================
// Refresh
// ============================================================================

// Refresh ротирует refresh-токен: ревокает старый, выпускает новую пару.
// Reuse-detection: повторное предъявление уже-revoked токена → revoke
// ВСЕХ токенов пользователя + Unauthenticated.
func (s *Service) Refresh(ctx context.Context, req *authv1.RefreshRequest) (*authv1.TokenPair, error) {
	raw := req.GetRefreshToken()
	if raw == "" {
		return nil, status.Error(codes.Unauthenticated, "invalid refresh token")
	}
	hash := HashRefreshToken(raw)

	rt, err := s.store.GetRefreshTokenByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.Unauthenticated, "invalid refresh token")
		}
		s.log.ErrorContext(ctx, "get refresh", slog.Any("error", err))
		return nil, status.Error(codes.Internal, "internal error")
	}

	now := s.now()
	if rt.Revoked {
		// Reuse-attack: предъявили уже revoked-токен → отзываем ВСЁ.
		if revErr := s.store.RevokeAllForUser(ctx, rt.UserID); revErr != nil {
			s.log.ErrorContext(ctx, "revoke all on reuse", slog.Any("error", revErr), slog.Int64("user_id", rt.UserID))
		}
		s.log.WarnContext(ctx, "refresh reuse detected",
			slog.Int64("user_id", rt.UserID),
			slog.Int64("token_id", rt.ID),
		)
		return nil, status.Error(codes.Unauthenticated, "invalid refresh token")
	}
	if !now.Before(rt.ExpiresAt) {
		return nil, status.Error(codes.Unauthenticated, "invalid refresh token")
	}

	pair, newRT, err := s.issueTokenPairWithRefresh(ctx, rt.UserID)
	if err != nil {
		s.log.ErrorContext(ctx, "issue token pair on refresh", slog.Any("error", err), slog.Int64("user_id", rt.UserID))
		return nil, status.Error(codes.Internal, "internal error")
	}

	if err := s.store.MarkReplacedBy(ctx, rt.ID, newRT.ID); err != nil {
		s.log.ErrorContext(ctx, "mark replaced_by", slog.Any("error", err))
		return nil, status.Error(codes.Internal, "internal error")
	}
	return pair, nil
}

// ============================================================================
// Revoke
// ============================================================================

// Revoke отзывает refresh-токен (logout). Идемпотентен.
// Дополнительно отзывает ВСЕ активные refresh пользователя — full logout.
func (s *Service) Revoke(ctx context.Context, req *authv1.RevokeRequest) (*authv1.RevokeResponse, error) {
	raw := req.GetRefreshToken()
	if raw == "" {
		// Идемпотентность: пустой токен — ничего не делаем.
		return &authv1.RevokeResponse{}, nil
	}
	hash := HashRefreshToken(raw)
	rt, err := s.store.GetRefreshTokenByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return &authv1.RevokeResponse{}, nil // идемпотентно.
		}
		s.log.ErrorContext(ctx, "get refresh on revoke", slog.Any("error", err))
		return nil, status.Error(codes.Internal, "internal error")
	}

	if err := s.store.RevokeRefreshToken(ctx, rt.ID); err != nil {
		s.log.ErrorContext(ctx, "revoke refresh", slog.Any("error", err))
		return nil, status.Error(codes.Internal, "internal error")
	}
	if err := s.store.RevokeAllForUser(ctx, rt.UserID); err != nil {
		s.log.ErrorContext(ctx, "revoke all on revoke", slog.Any("error", err))
		return nil, status.Error(codes.Internal, "internal error")
	}
	return &authv1.RevokeResponse{}, nil
}

// ============================================================================
// GetMe
// ============================================================================

// GetMe возвращает профиль пользователя.
//
// user_id извлекается из gRPC-metadata по ключу [MetadataUserIDKey]. Gateway
// должен подставить его после валидации access-токена через VerifyAccess.
// Если в req.user_id есть значение — оно имеет приоритет (для internal вызовов).
func (s *Service) GetMe(ctx context.Context, req *authv1.GetMeRequest) (*commonv1.User, error) {
	uidStr := req.GetUserId()
	if uidStr == "" {
		uidStr = userIDFromMetadata(ctx)
	}
	if uidStr == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user_id")
	}
	uid, err := strconv.ParseInt(uidStr, 10, 64)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid user_id")
	}

	u, err := s.store.GetUserByID(ctx, uid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "user not found")
		}
		s.log.ErrorContext(ctx, "get user by id", slog.Any("error", err))
		return nil, status.Error(codes.Internal, "internal error")
	}
	return userToProto(u), nil
}

// ============================================================================
// VerifyAccess
// ============================================================================

// VerifyAccess валидирует JWT и возвращает claims без обращения к БД.
// Stateless — вызывается gateway на каждом защищённом запросе.
func (s *Service) VerifyAccess(_ context.Context, req *authv1.VerifyAccessRequest) (*authv1.VerifyAccessResponse, error) {
	token := req.GetAccessToken()
	if token == "" {
		return nil, status.Error(codes.Unauthenticated, "missing access token")
	}
	claims, err := s.cfg.Verifier.Verify(token)
	if err != nil {
		if errors.Is(err, jwtx.ErrExpired) {
			return nil, status.Error(codes.Unauthenticated, "token expired")
		}
		return nil, status.Error(codes.Unauthenticated, "invalid token")
	}
	if claims.Kind != jwtx.TokenAccess {
		return nil, status.Error(codes.Unauthenticated, "wrong token kind")
	}
	if claims.UserID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing subject")
	}
	exp := claims.ExpiresAt
	if exp == nil {
		return nil, status.Error(codes.Unauthenticated, "missing exp")
	}
	return &authv1.VerifyAccessResponse{
		UserId:    claims.UserID,
		ExpiresAt: timestamppb.New(exp.Time),
	}, nil
}

// ============================================================================
// LinkTelegramInit / LinkTelegramComplete
// ============================================================================

// LinkTelegramInit генерирует одноразовый код, сохраняет в users.tg_link_token,
// возвращает code + deeplink + expires_at. deeplink собирается на стороне
// gateway (по env BOT_USERNAME), здесь возвращаем только base-форму без bot-name —
// gateway допишет имя бота.
//
// Внимание: TTL=10m не enforce'ится в БД, это контракт фронта/notifier для UX.
func (s *Service) LinkTelegramInit(ctx context.Context, req *authv1.LinkTelegramInitRequest) (*authv1.LinkTelegramInitResponse, error) {
	uidStr := req.GetUserId()
	if uidStr == "" {
		uidStr = userIDFromMetadata(ctx)
	}
	if uidStr == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user_id")
	}
	uid, err := strconv.ParseInt(uidStr, 10, 64)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid user_id")
	}

	code, err := GenerateOpaqueToken(linkTokenBytes)
	if err != nil {
		s.log.ErrorContext(ctx, "generate tg link token", slog.Any("error", err))
		return nil, status.Error(codes.Internal, "internal error")
	}
	if err := s.store.SetTgLinkToken(ctx, uid, code); err != nil {
		s.log.ErrorContext(ctx, "set tg link token", slog.Any("error", err))
		return nil, status.Error(codes.Internal, "internal error")
	}

	return &authv1.LinkTelegramInitResponse{
		Deeplink:  "tg://start?token=" + code, // gateway переписывает на https://t.me/<bot>?start=<code>.
		Code:      code,
		ExpiresAt: timestamppb.New(s.now().Add(linkTokenTTL)),
	}, nil
}

// LinkTelegramComplete привязывает chat_id к пользователю по коду.
// Вызывается из notifier-svc после получения /start <code> в Telegram.
// NotFound, если код не найден / уже использован.
func (s *Service) LinkTelegramComplete(ctx context.Context, req *authv1.LinkTelegramCompleteRequest) (*authv1.LinkTelegramCompleteResponse, error) {
	code := req.GetCode()
	if code == "" {
		return nil, status.Error(codes.InvalidArgument, "missing code")
	}
	if req.GetTelegramChatId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "missing telegram_chat_id")
	}

	u, err := s.store.GetUserByTgLinkToken(ctx, code)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "code not found")
		}
		s.log.ErrorContext(ctx, "get user by link token", slog.Any("error", err))
		return nil, status.Error(codes.Internal, "internal error")
	}
	if err := s.store.SetTgChatID(ctx, u.ID, req.GetTelegramChatId()); err != nil {
		s.log.ErrorContext(ctx, "set tg chat id", slog.Any("error", err))
		return nil, status.Error(codes.Internal, "internal error")
	}
	return &authv1.LinkTelegramCompleteResponse{
		UserId: strconv.FormatInt(u.ID, 10),
	}, nil
}

// ============================================================================
// internals
// ============================================================================

// issueTokenPair выпускает access+refresh и сохраняет refresh-hash в БД.
// Без обновления last_seen — оставлено caller'у.
func (s *Service) issueTokenPair(ctx context.Context, userID int64) (*authv1.TokenPair, error) {
	pair, _, err := s.issueTokenPairWithRefresh(ctx, userID)
	return pair, err
}

// issueTokenPairWithRefresh аналогичен issueTokenPair, но возвращает
// также сохранённую запись refresh_tokens — нужно для MarkReplacedBy.
func (s *Service) issueTokenPairWithRefresh(ctx context.Context, userID int64) (*authv1.TokenPair, store.RefreshToken, error) {
	now := s.now()
	accessExp := now.Add(s.cfg.AccessTTL)
	refreshExp := now.Add(s.cfg.RefreshTTL)

	uidStr := strconv.FormatInt(userID, 10)
	claims := jwtx.NewClaims(uidStr, jwtx.TokenAccess, uuid.NewString(), s.cfg.AccessTTL)
	access, err := s.cfg.Signer.Sign(claims)
	if err != nil {
		return nil, store.RefreshToken{}, fmt.Errorf("sign access: %w", err)
	}

	rawRefresh, refreshHash, err := GenerateRefreshToken()
	if err != nil {
		return nil, store.RefreshToken{}, fmt.Errorf("gen refresh: %w", err)
	}
	rt, err := s.store.CreateRefreshToken(ctx, userID, refreshHash, refreshExp)
	if err != nil {
		return nil, store.RefreshToken{}, fmt.Errorf("save refresh: %w", err)
	}

	return &authv1.TokenPair{
		AccessToken:      access,
		RefreshToken:     rawRefresh,
		AccessExpiresAt:  timestamppb.New(accessExp),
		RefreshExpiresAt: timestamppb.New(refreshExp),
	}, rt, nil
}

// normalizeEmail приводит email к lowercase+trim, валидирует через net/mail.
func normalizeEmail(s string) (string, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return "", errors.New("empty email")
	}
	if _, err := mail.ParseAddress(s); err != nil {
		return "", err
	}
	return s, nil
}

// userIDFromMetadata извлекает user-id из incoming gRPC metadata.
// Возвращает "" если ключ отсутствует.
func userIDFromMetadata(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	vals := md.Get(MetadataUserIDKey)
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

// userToProto конвертирует store.User в commonv1.User.
func userToProto(u store.User) *commonv1.User {
	pb := &commonv1.User{
		Id:        strconv.FormatInt(u.ID, 10),
		Email:     u.Email,
		CreatedAt: timestamppb.New(u.CreatedAt),
	}
	if u.LastSeenAt != nil {
		pb.LastSeenAt = timestamppb.New(*u.LastSeenAt)
	} else {
		// Чтобы поле всегда было заполнено — фронт ожидает ненулевое значение.
		pb.LastSeenAt = timestamppb.New(u.CreatedAt)
	}
	if u.TgChatID != nil {
		v := *u.TgChatID
		pb.TelegramChatId = &v
	}
	return pb
}
