// Package server — gRPC-реализация BmstuServiceServer.
//
// Обязанности:
//   - Маппинг бизнес-ошибок (oidc.Err*, session.Err*) на gRPC-коды.
//   - Один retry при ErrSessionExpired в FetchGroups (см. ADR).
//   - Никогда не логировать секреты: только user_id и result.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	bmstuv1 "github.com/fizcultor/backend/gen/bmstu/v1"
	commonv1 "github.com/fizcultor/backend/gen/common/v1"
	"github.com/fizcultor/backend/pkg/crypto"

	"github.com/fizcultor/backend/services/bmstu-svc/internal/oidc"
	"github.com/fizcultor/backend/services/bmstu-svc/internal/session"
	"github.com/fizcultor/backend/services/bmstu-svc/internal/store"
)

// Store — узкий контракт, нужный серверу.
type Store interface {
	UpsertCredentials(ctx context.Context, arg store.UpsertCredentialsParams) error
	GetCredentialsStatus(ctx context.Context, userID string) (store.BmstuCredentialStatus, error)
	DeleteCredentials(ctx context.Context, userID string) error
	GetSession(ctx context.Context, userID string) (store.BmstuSession, error)
	DeleteSession(ctx context.Context, userID string) error
}

// SessionManager — узкий контракт session.Manager.
type SessionManager interface {
	Acquire(ctx context.Context, userID string) (*http.Client, error)
	Refresh(ctx context.Context, userID string) (*http.Client, error)
	Invalidate(ctx context.Context, userID string) error
}

// OIDCClient — узкий контракт oidc.Client для test-login.
type OIDCClient interface {
	Login(ctx context.Context, login, password string) (*oidc.LoginResult, error)
}

// GroupsClient — узкий контракт groups.Client.
type GroupsClient interface {
	Fetch(ctx context.Context, hc *http.Client, semesterUUID string) ([]*commonv1.Slot, error)
}

// Config — параметры конструктора.
type Config struct {
	MasterKey    []byte
	SemesterUUID string
	Logger       *slog.Logger
}

// Server — реализация BmstuServiceServer.
type Server struct {
	bmstuv1.UnimplementedBmstuServiceServer

	st     Store
	mgr    SessionManager
	oidc   OIDCClient
	groups GroupsClient
	cfg    Config
	log    *slog.Logger
}

// New строит Server.
func New(st Store, mgr SessionManager, o OIDCClient, gc GroupsClient, cfg Config) (*Server, error) {
	if len(cfg.MasterKey) != crypto.KeySize {
		return nil, crypto.ErrKeySize
	}
	if cfg.SemesterUUID == "" {
		return nil, errors.New("server: empty SemesterUUID")
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Server{st: st, mgr: mgr, oidc: o, groups: gc, cfg: cfg, log: log}, nil
}

// StoreCredentials шифрует креды, валидирует через test-login и сохраняет.
// Никогда не логирует password.
func (s *Server) StoreCredentials(ctx context.Context, req *bmstuv1.StoreCredentialsRequest) (*bmstuv1.StoreCredentialsResponse, error) {
	if req.GetUserId() == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if req.GetLogin() == "" || req.GetPassword() == "" {
		return nil, status.Error(codes.InvalidArgument, "login and password are required")
	}

	// Шаг 1: тест-логин ДО шифрования (если креды плохие, нечего и сохранять).
	if _, err := s.oidc.Login(ctx, req.GetLogin(), req.GetPassword()); err != nil {
		s.log.Warn("store_credentials: test login failed",
			slog.String("user_id", req.GetUserId()),
			slog.String("result", "auth_error"),
		)
		return nil, mapOIDCError(err)
	}

	encL, err := crypto.Encrypt(s.cfg.MasterKey, []byte(req.GetLogin()))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "encrypt login: %v", err)
	}
	encP, err := crypto.Encrypt(s.cfg.MasterKey, []byte(req.GetPassword()))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "encrypt password: %v", err)
	}

	now := time.Now().UTC()
	if err := s.st.UpsertCredentials(ctx, store.UpsertCredentialsParams{
		UserID:      req.GetUserId(),
		EncLogin:    encL,
		EncPassword: encP,
		// Nonce — первые NonceSize байт шифр-блоба (см. pkg/crypto). Поля
		// заведены отдельно для аудита/логирования; decrypt их не использует.
		NonceLogin:    encL[:crypto.NonceSize],
		NoncePassword: encP[:crypto.NonceSize],
		LastLoginAt:   &now,
	}); err != nil {
		s.log.Error("store_credentials: upsert failed",
			slog.String("user_id", req.GetUserId()),
			slog.Any("error", err),
		)
		return nil, status.Errorf(codes.Internal, "store creds: %v", err)
	}

	s.log.Info("store_credentials: ok",
		slog.String("user_id", req.GetUserId()),
		slog.String("result", "ok"),
	)
	return &bmstuv1.StoreCredentialsResponse{
		Status:      commonv1.BmstuLinkStatus_BMSTU_LINK_STATUS_VALID,
		LastLoginAt: timestamppb.New(now),
	}, nil
}

// DeleteCredentials удаляет креды (и каскадом сессию). Идемпотентен.
func (s *Server) DeleteCredentials(ctx context.Context, req *bmstuv1.DeleteCredentialsRequest) (*bmstuv1.DeleteCredentialsResponse, error) {
	if req.GetUserId() == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if err := s.st.DeleteCredentials(ctx, req.GetUserId()); err != nil {
		return nil, status.Errorf(codes.Internal, "delete creds: %v", err)
	}
	s.log.Info("delete_credentials: ok", slog.String("user_id", req.GetUserId()))
	return &bmstuv1.DeleteCredentialsResponse{}, nil
}

// GetStatus возвращает статус привязки. Не лезет в Keycloak — только в БД.
func (s *Server) GetStatus(ctx context.Context, req *bmstuv1.GetStatusRequest) (*bmstuv1.GetStatusResponse, error) {
	if req.GetUserId() == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	creds, err := s.st.GetCredentialsStatus(ctx, req.GetUserId())
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return &bmstuv1.GetStatusResponse{
			Status: commonv1.BmstuLinkStatus_BMSTU_LINK_STATUS_NOT_LINKED,
		}, nil
	case err != nil:
		return nil, status.Errorf(codes.Internal, "get creds status: %v", err)
	}

	resp := &bmstuv1.GetStatusResponse{
		Status: commonv1.BmstuLinkStatus_BMSTU_LINK_STATUS_VALID,
	}
	if creds.LastLoginAt != nil {
		resp.LastLoginAt = timestamppb.New(*creds.LastLoginAt)
	}

	sess, err := s.st.GetSession(ctx, req.GetUserId())
	if err == nil {
		if sess.ExpiresAt != nil {
			resp.SessionExpiresAt = timestamppb.New(*sess.ExpiresAt)
			if sess.ExpiresAt.Before(time.Now().UTC()) {
				resp.Status = commonv1.BmstuLinkStatus_BMSTU_LINK_STATUS_EXPIRED
			}
		}
	}
	return resp, nil
}

// FetchGroups: один retry при истёкшей сессии.
//
//  1. Acquire → запрос /groups.
//  2. Если ErrSessionExpired → Invalidate + Refresh → повторный запрос.
//  3. Если и тут ErrSessionExpired/ErrBadCredentials — Unavailable/Unauthenticated.
func (s *Server) FetchGroups(ctx context.Context, req *bmstuv1.FetchGroupsRequest) (*bmstuv1.FetchGroupsResponse, error) {
	if req.GetUserId() == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	hc, err := s.mgr.Acquire(ctx, req.GetUserId())
	if err != nil {
		return nil, mapSessionError(err)
	}

	slots, err := s.groups.Fetch(ctx, hc, s.cfg.SemesterUUID)
	if err == nil {
		return s.successResponse(slots), nil
	}

	if !errors.Is(err, oidc.ErrSessionExpired) {
		return nil, mapOIDCError(err)
	}

	// Один retry: invalidate + refresh + retry fetch.
	if invErr := s.mgr.Invalidate(ctx, req.GetUserId()); invErr != nil {
		s.log.Warn("fetch_groups: invalidate failed",
			slog.String("user_id", req.GetUserId()),
			slog.Any("error", invErr),
		)
	}
	hc, err = s.mgr.Refresh(ctx, req.GetUserId())
	if err != nil {
		return nil, mapSessionError(err)
	}
	slots, err = s.groups.Fetch(ctx, hc, s.cfg.SemesterUUID)
	if err != nil {
		return nil, mapOIDCError(err)
	}
	return s.successResponse(slots), nil
}

// successResponse собирает FetchGroupsResponse с UTC now() как fetched_at.
func (s *Server) successResponse(slots []*commonv1.Slot) *bmstuv1.FetchGroupsResponse {
	return &bmstuv1.FetchGroupsResponse{
		Slots:        slots,
		FetchedAt:    timestamppb.New(time.Now().UTC()),
		SemesterUuid: s.cfg.SemesterUUID,
	}
}

// RefreshSession форсирует re-login.
func (s *Server) RefreshSession(ctx context.Context, req *bmstuv1.RefreshSessionRequest) (*bmstuv1.RefreshSessionResponse, error) {
	if req.GetUserId() == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if _, err := s.mgr.Refresh(ctx, req.GetUserId()); err != nil {
		return nil, mapSessionError(err)
	}
	// expires_at из новой сессии БД.
	resp := &bmstuv1.RefreshSessionResponse{
		Status: commonv1.BmstuLinkStatus_BMSTU_LINK_STATUS_VALID,
	}
	if sess, err := s.st.GetSession(ctx, req.GetUserId()); err == nil && sess.ExpiresAt != nil {
		resp.SessionExpiresAt = timestamppb.New(*sess.ExpiresAt)
	}
	return resp, nil
}

// mapOIDCError конвертит oidc.* ошибки в gRPC-код.
func mapOIDCError(err error) error {
	switch {
	case errors.Is(err, oidc.ErrBadCredentials):
		return status.Error(codes.Unauthenticated, "bmstu: bad credentials")
	case errors.Is(err, oidc.ErrSessionExpired):
		return status.Error(codes.Unavailable, "bmstu: session expired")
	case errors.Is(err, oidc.ErrRateLimited):
		return status.Error(codes.ResourceExhausted, "bmstu: rate limited")
	case errors.Is(err, oidc.ErrCaptcha):
		return status.Error(codes.FailedPrecondition, "bmstu: captcha required")
	case errors.Is(err, oidc.ErrLoginFormNotFound):
		return status.Error(codes.Internal, "bmstu: login form not found")
	case errors.Is(err, oidc.ErrUnexpectedResponse):
		return status.Error(codes.Unavailable, "bmstu: upstream unavailable")
	default:
		return status.Error(codes.Internal, fmt.Sprintf("bmstu: %v", err))
	}
}

// mapSessionError — обёртка над mapOIDCError + ErrCredentialsNotLinked.
func mapSessionError(err error) error {
	if errors.Is(err, session.ErrCredentialsNotLinked) {
		return status.Error(codes.FailedPrecondition, "bmstu: credentials not linked")
	}
	return mapOIDCError(err)
}
