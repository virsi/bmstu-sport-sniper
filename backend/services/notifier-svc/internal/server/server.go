// Package server — gRPC-сервер notifier-svc.
//
// Реализует notifier.v1.NotifierService:
//   - NotifyMatched: форматирует слоты, шлёт в TG, публикует в NATS.
//   - SendDirect:    plain/html-сообщение в TG.
//   - RegisterTelegramChat: обёртка над auth-svc.LinkTelegramComplete
//     (используется в интеграционных тестах вместо реального /start).
//
// Архитектура:
//   - tg_chat_id юзера запрашивается через AuthService.GetMe (proto предусматривает
//     User.telegram_chat_id). Это поддерживает чистоту notifier-svc — он не хранит
//     своих маппингов user_id → chat_id.
//   - Рейтинги преподавателей подтягиваются через TeachersService.BatchGet.
//   - NATS publish идёт в subject "alerts.<user_id>" с JSON-payload-ом по
//     контракту docs/api.md §6 (event «new-slot»).
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	authv1 "github.com/fizcultor/backend/gen/auth/v1"
	commonv1 "github.com/fizcultor/backend/gen/common/v1"
	notifierv1 "github.com/fizcultor/backend/gen/notifier/v1"
	teachersv1 "github.com/fizcultor/backend/gen/teachers/v1"

	"github.com/fizcultor/backend/services/notifier-svc/internal/bot"
	"github.com/fizcultor/backend/services/notifier-svc/internal/format"
)

// AuthClient — узкий интерфейс над auth-svc, нужный notifier-svc.
//
// Намеренно `subset` (а не *authv1.AuthServiceClient), чтобы тесты могли
// подменять только используемые методы.
type AuthClient interface {
	// GetMe возвращает профиль пользователя (включая telegram_chat_id).
	GetMe(ctx context.Context, in *authv1.GetMeRequest, opts ...grpc.CallOption) (*commonv1.User, error)
	// LinkTelegramComplete привязывает chat_id к user_id по одноразовому коду.
	LinkTelegramComplete(ctx context.Context, in *authv1.LinkTelegramCompleteRequest, opts ...grpc.CallOption) (*authv1.LinkTelegramCompleteResponse, error)
}

// TeachersClient — узкий интерфейс над teachers-svc.
type TeachersClient interface {
	// BatchGet — пакетная выборка преподавателей по uid'ам.
	BatchGet(ctx context.Context, in *teachersv1.BatchGetRequest, opts ...grpc.CallOption) (*teachersv1.BatchGetResponse, error)
}

// Publisher — интерфейс publisher'а NATS (для тестов).
type Publisher interface {
	// Publish публикует raw-байты в subject.
	Publish(subject string, data []byte) error
}

// Server — реализация notifierv1.NotifierServiceServer.
type Server struct {
	notifierv1.UnimplementedNotifierServiceServer

	auth     AuthClient
	teachers TeachersClient
	sender   bot.Sender
	pub      Publisher
	logger   *slog.Logger
}

// Deps — зависимости Server, заполняемые в main.
type Deps struct {
	// Auth — клиент auth-svc.
	Auth AuthClient
	// Teachers — клиент teachers-svc.
	Teachers TeachersClient
	// Sender — отправитель TG-сообщений (обычно *bot.Bot).
	Sender bot.Sender
	// Publisher — NATS publisher.
	Publisher Publisher
	// Logger — slog logger, если nil — slog.Default.
	Logger *slog.Logger
}

// New создаёт Server. Возвращает ошибку, если обязательные зависимости nil.
func New(d Deps) (*Server, error) {
	if d.Auth == nil {
		return nil, errors.New("server: auth client is nil")
	}
	if d.Teachers == nil {
		return nil, errors.New("server: teachers client is nil")
	}
	if d.Sender == nil {
		return nil, errors.New("server: sender is nil")
	}
	if d.Publisher == nil {
		return nil, errors.New("server: publisher is nil")
	}
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		auth: d.Auth, teachers: d.Teachers,
		sender: d.Sender, pub: d.Publisher, logger: logger,
	}, nil
}

// AlertPayload — структура NATS-сообщения, идущая на subject `alerts.<user_id>`.
// gateway-svc парсит её и шлёт в SSE-канал.
type AlertPayload struct {
	// Slot — детали слота (см. docs/api.md §6, event «new-slot»).
	Slot AlertSlot `json:"slot"`
	// MatchedFilterIDs — какие фильтры юзера сматчили слот (для UI).
	MatchedFilterIDs []string `json:"matched_filter_ids,omitempty"`
	// SentAt — момент publish, ISO-8601 UTC.
	SentAt string `json:"sent_at"`
	// Channel — канал доставки (telegram/sse/…).
	Channel string `json:"channel"`
}

// AlertSlot — плоское представление слота для JSON в SSE.
type AlertSlot struct {
	ID            string  `json:"id"`
	Week          int32   `json:"week"`
	Time          string  `json:"time"`
	Section       string  `json:"section,omitempty"`
	Place         string  `json:"place"`
	TeacherName   string  `json:"teacher_name"`
	TeacherUID    string  `json:"teacher_uid,omitempty"`
	Vacancy       int32   `json:"vacancy"`
	TeacherRating float64 `json:"teacher_rating,omitempty"`
}

// NotifyMatched — реализация notifierv1.NotifierServiceServer.
func (s *Server) NotifyMatched(
	ctx context.Context, req *notifierv1.NotifyMatchedRequest,
) (*notifierv1.NotifyMatchedResponse, error) {
	if req.GetUserId() == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if len(req.GetMatched()) == 0 {
		// Согласно proto-комментарию — no-op возвращает OK.
		return &notifierv1.NotifyMatchedResponse{}, nil
	}

	channels := req.GetChannels()
	if len(channels) == 0 {
		channels = []commonv1.AlertChannel{
			commonv1.AlertChannel_ALERT_CHANNEL_TELEGRAM,
			commonv1.AlertChannel_ALERT_CHANNEL_SSE,
		}
	}

	// Профиль юзера для chat_id.
	user, err := s.auth.GetMe(ctx, &authv1.GetMeRequest{UserId: req.GetUserId()})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "auth.GetMe: %v", err)
	}

	// Рейтинги преподавателей.
	ratingMap := s.fetchRatings(ctx, req.GetMatched())

	resp := &notifierv1.NotifyMatchedResponse{}
	for _, ch := range channels {
		switch ch {
		case commonv1.AlertChannel_ALERT_CHANNEL_TELEGRAM:
			if err := s.deliverTelegram(req.GetMatched(), ratingMap, user); err != nil {
				s.recordFail(resp, ch, err)
				continue
			}
			resp.DeliveredBy = append(resp.DeliveredBy, ch)

		case commonv1.AlertChannel_ALERT_CHANNEL_SSE:
			if err := s.publishAlerts(req.GetUserId(), req.GetMatched(), ratingMap); err != nil {
				s.recordFail(resp, ch, err)
				continue
			}
			resp.DeliveredBy = append(resp.DeliveredBy, ch)

		default:
			// EMAIL/WEB_PUSH — V2.
			s.recordFail(resp, ch, errors.New("channel not implemented"))
		}
	}

	s.logger.Info(
		"notify_matched",
		slog.String("user_id", req.GetUserId()),
		slog.Int("matched", len(req.GetMatched())),
		slog.Int("delivered", len(resp.DeliveredBy)),
		slog.Int("failed", len(resp.FailedBy)),
	)
	return resp, nil
}

// SendDirect — прямое сообщение пользователю (системные алёрты).
func (s *Server) SendDirect(
	ctx context.Context, req *notifierv1.SendDirectRequest,
) (*notifierv1.SendDirectResponse, error) {
	if req.GetUserId() == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if strings.TrimSpace(req.GetText()) == "" {
		return nil, status.Error(codes.InvalidArgument, "text is required")
	}

	channels := req.GetChannels()
	if len(channels) == 0 {
		channels = []commonv1.AlertChannel{commonv1.AlertChannel_ALERT_CHANNEL_TELEGRAM}
	}

	resp := &notifierv1.SendDirectResponse{}
	for _, ch := range channels {
		switch ch {
		case commonv1.AlertChannel_ALERT_CHANNEL_TELEGRAM:
			user, err := s.auth.GetMe(ctx, &authv1.GetMeRequest{UserId: req.GetUserId()})
			if err != nil {
				s.recordSendDirectFail(resp, ch, err)
				continue
			}
			chatID := user.GetTelegramChatId()
			if chatID == 0 {
				s.recordSendDirectFail(resp, ch, errors.New("telegram not linked"))
				continue
			}
			if err := s.sender.SendHTML(chatID, req.GetText()); err != nil {
				s.recordSendDirectFail(resp, ch, err)
				continue
			}
			resp.DeliveredBy = append(resp.DeliveredBy, ch)
		case commonv1.AlertChannel_ALERT_CHANNEL_SSE:
			payload, _ := json.Marshal(directPayload{
				Kind: "system", Message: req.GetText(),
				SentAt: time.Now().UTC().Format(time.RFC3339),
			})
			if err := s.pub.Publish(subjectAlerts(req.GetUserId()), payload); err != nil {
				s.recordSendDirectFail(resp, ch, err)
				continue
			}
			resp.DeliveredBy = append(resp.DeliveredBy, ch)
		default:
			s.recordSendDirectFail(resp, ch, errors.New("channel not implemented"))
		}
	}
	return resp, nil
}

// RegisterTelegramChat — gRPC-обёртка над auth-svc.LinkTelegramComplete.
//
// Используется в основном для интеграционных тестов и для completion'а
// привязки из TG-handler /start <code>.
func (s *Server) RegisterTelegramChat(
	ctx context.Context, req *notifierv1.RegisterTelegramChatRequest,
) (*notifierv1.RegisterTelegramChatResponse, error) {
	if req.GetCode() == "" {
		return nil, status.Error(codes.InvalidArgument, "code is required")
	}
	if req.GetTelegramChatId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "telegram_chat_id is required")
	}
	out, err := s.auth.LinkTelegramComplete(ctx, &authv1.LinkTelegramCompleteRequest{
		Code:           req.GetCode(),
		TelegramChatId: req.GetTelegramChatId(),
	})
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "auth.LinkTelegramComplete: %v", err)
	}
	welcomeSent := true
	if err := s.sender.SendHTML(req.GetTelegramChatId(),
		"<b>✅ Аккаунт привязан!</b>\nТеперь алёрты будут приходить сюда."); err != nil {
		welcomeSent = false
		s.logger.Warn("welcome message failed", slog.Any("error", err))
	}
	return &notifierv1.RegisterTelegramChatResponse{
		UserId: out.GetUserId(), WelcomeSent: welcomeSent,
	}, nil
}

// Complete — реализация bot.LinkCompleter. Делегирует AuthClient и
// логирует исход. Используется TG-handler'ом /start <token>.
func (s *Server) Complete(ctx context.Context, token string, chatID int64) (string, error) {
	out, err := s.auth.LinkTelegramComplete(ctx, &authv1.LinkTelegramCompleteRequest{
		Code: token, TelegramChatId: chatID,
	})
	if err != nil {
		return "", fmt.Errorf("server: link: %w", err)
	}
	return out.GetUserId(), nil
}

// ----- helpers -----

func (s *Server) deliverTelegram(
	matched []*commonv1.MatchedSlot,
	ratings map[string]format.TeacherRating,
	user *commonv1.User,
) error {
	chatID := user.GetTelegramChatId()
	if chatID == 0 {
		return errors.New("user has no telegram chat linked")
	}
	slots := make([]format.Slot, 0, len(matched))
	for _, m := range matched {
		slots = append(slots, format.SlotFromProto(m))
	}
	messages := format.FormatBatch(slots, ratings)
	for _, msg := range messages {
		if err := s.sender.SendHTML(chatID, msg); err != nil {
			return fmt.Errorf("telegram send: %w", err)
		}
	}
	return nil
}

func (s *Server) publishAlerts(
	userID string,
	matched []*commonv1.MatchedSlot,
	ratings map[string]format.TeacherRating,
) error {
	subject := subjectAlerts(userID)
	now := time.Now().UTC().Format(time.RFC3339)
	for _, m := range matched {
		slot := m.GetSlot()
		if slot == nil {
			continue
		}
		payload := AlertPayload{
			Slot: AlertSlot{
				ID:          slot.GetId(),
				Week:        slot.GetWeek(),
				Time:        slot.GetTime(),
				Section:     slot.GetSection(),
				Place:       slot.GetPlace(),
				TeacherName: slot.GetTeacherName(),
				TeacherUID:  slot.GetTeacherUid(),
				Vacancy:     slot.GetVacancy(),
			},
			MatchedFilterIDs: m.GetMatchedFilterIds(),
			SentAt:           now,
			Channel:          "sse",
		}
		if r, ok := ratings[slot.GetTeacherUid()]; ok && slot.GetTeacherUid() != "" {
			payload.Slot.TeacherRating = r.Rating
		}
		data, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("alert marshal: %w", err)
		}
		if err := s.pub.Publish(subject, data); err != nil {
			return fmt.Errorf("nats publish: %w", err)
		}
	}
	return nil
}

func (s *Server) fetchRatings(
	ctx context.Context, matched []*commonv1.MatchedSlot,
) map[string]format.TeacherRating {
	out := make(map[string]format.TeacherRating)
	uids := uniqueTeacherUIDs(matched)

	// Если у MatchedSlot уже есть TeacherRating от filter-svc — используем как
	// fallback. Иначе запрашиваем у teachers-svc.
	for _, m := range matched {
		if m.GetSlot() == nil {
			continue
		}
		uid := m.GetSlot().GetTeacherUid()
		if uid == "" {
			continue
		}
		if m.TeacherRating != nil {
			out[uid] = format.TeacherRating{Rating: m.GetTeacherRating()}
		}
	}

	if len(uids) == 0 {
		return out
	}

	resp, err := s.teachers.BatchGet(ctx, &teachersv1.BatchGetRequest{Uids: uids})
	if err != nil {
		s.logger.Warn("teachers.BatchGet failed", slog.Any("error", err))
		return out
	}
	for _, t := range resp.GetTeachers() {
		if t.GetRating() <= 0 {
			continue
		}
		out[t.GetUid()] = format.TeacherRating{Rating: t.GetRating()}
	}
	return out
}

func uniqueTeacherUIDs(matched []*commonv1.MatchedSlot) []string {
	seen := make(map[string]struct{}, len(matched))
	out := make([]string, 0, len(matched))
	for _, m := range matched {
		if m.GetSlot() == nil {
			continue
		}
		uid := m.GetSlot().GetTeacherUid()
		if uid == "" {
			continue
		}
		if _, ok := seen[uid]; ok {
			continue
		}
		seen[uid] = struct{}{}
		out = append(out, uid)
	}
	return out
}

func (s *Server) recordFail(
	resp *notifierv1.NotifyMatchedResponse, ch commonv1.AlertChannel, err error,
) {
	resp.FailedBy = append(resp.FailedBy, ch)
	resp.ErrorsByChannel = append(resp.ErrorsByChannel, err.Error())
	s.logger.Warn(
		"notify channel failed",
		slog.String("channel", ch.String()),
		slog.Any("error", err),
	)
}

func (s *Server) recordSendDirectFail(
	resp *notifierv1.SendDirectResponse, ch commonv1.AlertChannel, err error,
) {
	resp.FailedBy = append(resp.FailedBy, ch)
	resp.ErrorsByChannel = append(resp.ErrorsByChannel, err.Error())
}

// subjectAlerts возвращает NATS subject вида alerts.<user_id>.
//
// user_id предполагается UUIDv7-строкой; небезопасные символы заменяются,
// чтобы соблюсти ограничения NATS subject grammar.
func subjectAlerts(userID string) string {
	const prefix = "alerts."
	clean := strings.NewReplacer(".", "_", " ", "_", "*", "_", ">", "_").Replace(userID)
	return prefix + clean
}

// directPayload — формат SSE-сообщения для SendDirect (event «status»).
type directPayload struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
	SentAt  string `json:"sent_at"`
}
