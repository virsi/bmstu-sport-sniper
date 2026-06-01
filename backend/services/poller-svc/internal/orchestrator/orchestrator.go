package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	authv1 "github.com/fizcultor/backend/gen/auth/v1"
	bmstuv1 "github.com/fizcultor/backend/gen/bmstu/v1"
	commonv1 "github.com/fizcultor/backend/gen/common/v1"
	filterv1 "github.com/fizcultor/backend/gen/filter/v1"
	notifierv1 "github.com/fizcultor/backend/gen/notifier/v1"

	"github.com/sony/gobreaker"
)

// AuthClient — тонкий интерфейс над auth-svc, нужный poller'у.
type AuthClient interface {
	GetMe(ctx context.Context, in *authv1.GetMeRequest, opts ...grpc.CallOption) (*commonv1.User, error)
}

// BmstuClient — тонкий интерфейс над bmstu-svc.
type BmstuClient interface {
	FetchGroups(ctx context.Context, in *bmstuv1.FetchGroupsRequest, opts ...grpc.CallOption) (*bmstuv1.FetchGroupsResponse, error)
}

// FilterClient — тонкий интерфейс над filter-svc.
type FilterClient interface {
	MatchSlots(ctx context.Context, in *filterv1.MatchSlotsRequest, opts ...grpc.CallOption) (*filterv1.MatchSlotsResponse, error)
	MarkSeen(ctx context.Context, in *filterv1.MarkSeenRequest, opts ...grpc.CallOption) (*filterv1.MarkSeenResponse, error)
}

// NotifierClient — тонкий интерфейс над notifier-svc.
type NotifierClient interface {
	NotifyMatched(ctx context.Context, in *notifierv1.NotifyMatchedRequest, opts ...grpc.CallOption) (*notifierv1.NotifyMatchedResponse, error)
	SendDirect(ctx context.Context, in *notifierv1.SendDirectRequest, opts ...grpc.CallOption) (*notifierv1.SendDirectResponse, error)
}

// ActiveUsers — источник списка пользователей, которых нужно опрашивать.
//
// Архитектурное ограничение текущей итерации: в proto filter-svc нет
// ListActiveUsers, поэтому реализация подмонтирована через интерфейс. В dev
// — статический список из env, в prod — gRPC-метод filter-svc (когда появится).
type ActiveUsers interface {
	// List возвращает user_id'ы для опроса в текущем тике.
	List(ctx context.Context) ([]string, error)
}

// Config — параметры orchestrator'а.
type Config struct {
	// PollInterval — базовый интервал тикера (например, 60s).
	PollInterval time.Duration
	// Jitter — случайный сдвиг ±Jitter перед каждым тиком (антибан LKS).
	Jitter time.Duration
	// PerUserJitter — задержка перед запросом для одного юзера, рандомно [0..N].
	PerUserJitter time.Duration
	// Concurrency — кол-во параллельных опросов юзеров (semaphore).
	Concurrency int
	// SemesterUUID — UUID семестра для LKS (диагностика; bmstu-svc сам знает).
	SemesterUUID string
}

// DefaultConfig возвращает разумные дефолты.
func DefaultConfig() Config {
	return Config{
		PollInterval:  60 * time.Second,
		Jitter:        15 * time.Second,
		PerUserJitter: 3 * time.Second,
		Concurrency:   10,
	}
}

// Deps — зависимости Orchestrator.
type Deps struct {
	Auth     AuthClient
	Bmstu    BmstuClient
	Filter   FilterClient
	Notifier NotifierClient
	Users    ActiveUsers
	Logger   *slog.Logger
}

// Orchestrator — главный цикл poller-svc.
//
// Каждый тик:
//  1. ActiveUsers.List → []user_id
//  2. Для каждого юзера в горутине (limited semaphore):
//     a. bmstu.FetchGroups
//     b. filter.MatchSlots
//     c. notifier.NotifyMatched
//     d. filter.MarkSeen (ТОЛЬКО при успехе notify)
//
// Резильентность:
//   - per-user backoff (ErrCount, LastFailAt)
//   - global circuit-breaker на bmstu (gobreaker)
//   - при FAILED_PRECONDITION от bmstu → notifier.SendDirect «обнови пароль»
//   - known_slots не очищается при ошибке (фикс legacy main.py:312)
type Orchestrator struct {
	cfg     Config
	deps    Deps
	backoff *Backoff
	breaker *gobreaker.CircuitBreaker
}

// New создаёт orchestrator с проверкой обязательных deps.
func New(cfg Config, deps Deps) (*Orchestrator, error) {
	if cfg.PollInterval <= 0 {
		return nil, errors.New("orchestrator: PollInterval must be > 0")
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 10
	}
	if deps.Auth == nil || deps.Bmstu == nil || deps.Filter == nil ||
		deps.Notifier == nil || deps.Users == nil {
		return nil, errors.New("orchestrator: missing dependency")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	deps.Logger = logger

	cb := gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        "bmstu",
		MaxRequests: 3,
		Interval:    60 * time.Second,
		Timeout:     2 * time.Minute,
		ReadyToTrip: func(c gobreaker.Counts) bool {
			return c.ConsecutiveFailures >= 5
		},
		OnStateChange: func(name string, from, to gobreaker.State) {
			logger.Warn("circuit breaker state change",
				slog.String("name", name),
				slog.String("from", from.String()),
				slog.String("to", to.String()),
			)
		},
	})

	return &Orchestrator{
		cfg:     cfg,
		deps:    deps,
		backoff: NewBackoff(DefaultBackoff()),
		breaker: cb,
	}, nil
}

// Run запускает главный цикл. Блокирующий метод; завершается по ctx.Done.
func (o *Orchestrator) Run(ctx context.Context) error {
	o.deps.Logger.Info("orchestrator: starting",
		slog.Duration("interval", o.cfg.PollInterval),
		slog.Duration("jitter", o.cfg.Jitter),
		slog.Int("concurrency", o.cfg.Concurrency),
	)
	ticker := time.NewTicker(o.cfg.PollInterval)
	defer ticker.Stop()

	// Тикер срабатывает после первого интервала, поэтому делаем кикстарт.
	o.runCycle(ctx)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if o.cfg.Jitter > 0 {
				wait := o.jitterDuration()
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(wait):
				}
			}
			o.runCycle(ctx)
		}
	}
}

// runCycle — один полный проход по всем активным юзерам.
//
// Возвращает безусловно (ошибки логируются). Внутренний параллелизм ограничен
// semaphore размера Concurrency.
func (o *Orchestrator) runCycle(ctx context.Context) {
	users, err := o.deps.Users.List(ctx)
	if err != nil {
		o.deps.Logger.Error("active users: list failed", slog.Any("error", err))
		return
	}
	if len(users) == 0 {
		o.deps.Logger.Debug("active users: empty list, skip cycle")
		return
	}
	o.shuffle(users)

	sem := make(chan struct{}, o.cfg.Concurrency)
	var wg sync.WaitGroup
	cycleStart := time.Now()

	for _, uid := range users {
		if ctx.Err() != nil {
			break
		}
		if o.backoff.ShouldSkip(uid) {
			o.deps.Logger.Debug("user skipped by backoff", slog.String("user_id", uid))
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(userID string) {
			defer wg.Done()
			defer func() { <-sem }()
			o.pollUser(ctx, userID)
		}(uid)
	}
	wg.Wait()
	o.deps.Logger.Info("cycle finished",
		slog.Int("users", len(users)),
		slog.Duration("elapsed", time.Since(cycleStart)),
	)
}

// pollUser выполняет полный шаг для одного юзера.
//
// Декомпозиция:
//  1. Per-user jitter (анти-всплеск LKS)
//  2. bmstu.FetchGroups через circuit-breaker
//  3. filter.MatchSlots
//  4. notifier.NotifyMatched
//  5. filter.MarkSeen — только если notify не упал
//
// При FAILED_PRECONDITION от bmstu: notify SendDirect «обнови пароль» и выход.
func (o *Orchestrator) pollUser(ctx context.Context, userID string) {
	if d := o.perUserJitter(); d > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(d):
		}
	}
	logger := o.deps.Logger.With(slog.String("user_id", userID))

	slots, fetchErr := o.fetchSlots(ctx, userID)
	if fetchErr != nil {
		o.handleFetchError(ctx, userID, fetchErr, logger)
		return
	}

	if len(slots) == 0 {
		// Норма: нет свободных слотов. Дедуп НЕ очищаем (фикс main.py:312).
		o.backoff.Reset(userID)
		logger.Debug("no slots from bmstu")
		return
	}

	matchResp, err := o.deps.Filter.MatchSlots(ctx, &filterv1.MatchSlotsRequest{
		UserId: userID, Slots: slots,
	})
	if err != nil {
		logger.Warn("filter.MatchSlots failed", slog.Any("error", err))
		o.backoff.RegisterFailure(userID)
		return
	}
	newOnes := filterNew(matchResp.GetMatched())
	if len(newOnes) == 0 {
		o.backoff.Reset(userID)
		logger.Debug("no new matched slots", slog.Int("total_matched", len(matchResp.GetMatched())))
		return
	}

	notifyResp, err := o.deps.Notifier.NotifyMatched(ctx, &notifierv1.NotifyMatchedRequest{
		UserId:  userID,
		Matched: newOnes,
	})
	if err != nil {
		logger.Warn("notifier.NotifyMatched failed", slog.Any("error", err))
		o.backoff.RegisterFailure(userID)
		return
	}
	// Любая успешная доставка → MarkSeen. Если все каналы упали — НЕ помечаем.
	if len(notifyResp.GetDeliveredBy()) == 0 {
		logger.Warn("notify: no channels delivered, skip MarkSeen",
			slog.Any("errors", notifyResp.GetErrorsByChannel()))
		o.backoff.RegisterFailure(userID)
		return
	}

	ids := slotIDs(newOnes)
	if _, err := o.deps.Filter.MarkSeen(ctx, &filterv1.MarkSeenRequest{
		UserId: userID, SlotIds: ids,
	}); err != nil {
		// MarkSeen упал — на следующем тике будет дубль алёрта, это приемлемо.
		logger.Warn("filter.MarkSeen failed", slog.Any("error", err))
	}

	o.backoff.Reset(userID)
	logger.Info("user polled",
		slog.Int("slots_total", len(slots)),
		slog.Int("matched_new", len(newOnes)),
		slog.Int("delivered", len(notifyResp.GetDeliveredBy())),
		slog.Int("failed_channels", len(notifyResp.GetFailedBy())),
	)
}

// fetchSlots оборачивает bmstu.FetchGroups в circuit-breaker.
func (o *Orchestrator) fetchSlots(ctx context.Context, userID string) ([]*commonv1.Slot, error) {
	out, err := o.breaker.Execute(func() (any, error) {
		resp, err := o.deps.Bmstu.FetchGroups(ctx, &bmstuv1.FetchGroupsRequest{UserId: userID})
		if err != nil {
			return nil, err
		}
		return resp.GetSlots(), nil
	})
	if err != nil {
		return nil, err
	}
	slots, _ := out.([]*commonv1.Slot)
	return slots, nil
}

// handleFetchError разруливает ошибку bmstu: FAILED_PRECONDITION (невалидные
// креды) → SendDirect, иначе backoff.
func (o *Orchestrator) handleFetchError(
	ctx context.Context, userID string, err error, logger *slog.Logger,
) {
	o.backoff.RegisterFailure(userID)

	if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
		logger.Warn("bmstu fetch skipped: circuit breaker", slog.Any("error", err))
		return
	}

	st, ok := status.FromError(err)
	if !ok {
		logger.Warn("bmstu fetch failed", slog.Any("error", err))
		return
	}

	if st.Code() == codes.FailedPrecondition {
		logger.Info("bmstu: session expired, notifying user", slog.String("code", st.Code().String()))
		_, sendErr := o.deps.Notifier.SendDirect(ctx, &notifierv1.SendDirectRequest{
			UserId: userID,
			Text:   "⚠️ Сессия BMSTU истекла. Открой сайт и обнови пароль/логин.",
			Channels: []commonv1.AlertChannel{
				commonv1.AlertChannel_ALERT_CHANNEL_TELEGRAM,
				commonv1.AlertChannel_ALERT_CHANNEL_SSE,
			},
		})
		if sendErr != nil {
			logger.Warn("notifier.SendDirect failed", slog.Any("error", sendErr))
		}
		return
	}
	logger.Warn("bmstu fetch failed",
		slog.String("grpc_code", st.Code().String()),
		slog.String("message", st.Message()),
	)
}

func (o *Orchestrator) jitterDuration() time.Duration {
	if o.cfg.Jitter <= 0 {
		return 0
	}
	// jitter — not security-sensitive: разрешён math/rand/v2.
	return time.Duration(rand.Int64N(int64(o.cfg.Jitter))) //nolint:gosec // jitter only
}

func (o *Orchestrator) perUserJitter() time.Duration {
	if o.cfg.PerUserJitter <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(o.cfg.PerUserJitter))) //nolint:gosec // jitter only
}

func (o *Orchestrator) shuffle(users []string) {
	rand.Shuffle(len(users), func(i, j int) {
		users[i], users[j] = users[j], users[i]
	})
}

func filterNew(matched []*commonv1.MatchedSlot) []*commonv1.MatchedSlot {
	out := make([]*commonv1.MatchedSlot, 0, len(matched))
	for _, m := range matched {
		if m.GetIsNew() {
			out = append(out, m)
		}
	}
	return out
}

func slotIDs(matched []*commonv1.MatchedSlot) []string {
	out := make([]string, 0, len(matched))
	for _, m := range matched {
		if s := m.GetSlot(); s != nil && s.GetId() != "" {
			out = append(out, s.GetId())
		}
	}
	return out
}

// EnvUsers — простая реализация ActiveUsers, читающая user_id'ы из конфига
// (например, "POLL_USER_IDS=uuid1,uuid2"). Используется до появления
// filter-svc.ListActiveUsers RPC.
type EnvUsers struct {
	ids []string
}

// NewEnvUsers создаёт static-источник из comma-separated списка.
func NewEnvUsers(raw string) *EnvUsers {
	if strings.TrimSpace(raw) == "" {
		return &EnvUsers{}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return &EnvUsers{ids: out}
}

// List возвращает копию списка user_id (стабильная сигнатура).
func (e *EnvUsers) List(_ context.Context) ([]string, error) {
	if len(e.ids) == 0 {
		return nil, nil
	}
	out := make([]string, len(e.ids))
	copy(out, e.ids)
	return out, nil
}

// Static — диагностический хелпер: возвращает форматированную строку
// для логов состояния (используется в /readyz и тестах).
func (o *Orchestrator) Static() string {
	return fmt.Sprintf("interval=%s jitter=%s concurrency=%d",
		o.cfg.PollInterval, o.cfg.Jitter, o.cfg.Concurrency)
}
