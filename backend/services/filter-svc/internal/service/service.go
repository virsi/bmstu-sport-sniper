// Package service реализует gRPC-сервер filter-svc.
//
// Связывает store-слой (filter_db) с pure-функцией match.Match. Сервис
// stateless: каждый вызов гарантирует консистентность через БД. user_id
// поступает в запросах от gateway-svc (в proto = строка UUIDv7, в БД = BIGINT;
// конвертация — в этом слое).
package service

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/fizcultor/backend/gen/common/v1"
	filterv1 "github.com/fizcultor/backend/gen/filter/v1"
	"github.com/fizcultor/backend/services/filter-svc/internal/match"
	"github.com/fizcultor/backend/services/filter-svc/internal/store"
)

// Store — интерфейс БД-слоя, потребляемый Service. Один интерфейс — для
// удобства мокирования в юнит-тестах. KISS: один пакет, один интерфейс.
type Store interface {
	CreateFilter(ctx context.Context, p store.CreateFilterParams) (store.Filter, error)
	GetFilterByID(ctx context.Context, id int64) (store.Filter, error)
	ListFiltersByUser(ctx context.Context, userID int64, includeDisabled bool) ([]store.Filter, error)
	UpdateFilter(ctx context.Context, p store.UpdateFilterParams) (store.Filter, error)
	DeleteFilter(ctx context.Context, id, userID int64) (int64, error)

	GetKnownSlotsByUser(ctx context.Context, userID int64) (map[string]struct{}, error)
	InsertKnownSlots(ctx context.Context, userID int64, slotIDs []string) error
	ResetKnownSlots(ctx context.Context, userID int64) (int64, error)

	InsertAlertLog(ctx context.Context, p store.InsertAlertLogParams) (store.AlertLog, error)
}

// Service — реализация filterv1.FilterServiceServer.
type Service struct {
	filterv1.UnimplementedFilterServiceServer
	store Store
}

// New создаёт Service.
func New(s Store) *Service {
	return &Service{store: s}
}

// ---------------------------------------------------------------------------
// CRUD
// ---------------------------------------------------------------------------

// CreateFilter создаёт фильтр пользователя.
func (s *Service) CreateFilter(ctx context.Context, req *filterv1.CreateFilterRequest) (*filterv1.CreateFilterResponse, error) {
	userID, err := parseUserID(req.GetUserId())
	if err != nil {
		return nil, err
	}
	if vErr := validateTimeRange(req.TimeFrom, req.TimeTo); vErr != nil {
		return nil, vErr
	}
	if vErr := validateRating(req.MinRating); vErr != nil {
		return nil, vErr
	}

	timeFrom, err := timeOfDayFromProto(req.TimeFrom)
	if err != nil {
		return nil, err
	}
	timeTo, err := timeOfDayFromProto(req.TimeTo)
	if err != nil {
		return nil, err
	}

	dow := dayOfWeekToString(req.GetDayOfWeek())
	created, err := s.store.CreateFilter(ctx, store.CreateFilterParams{
		UserID:     userID,
		Section:    normalizeStringPtr(req.Section),
		TeacherUID: normalizeStringPtr(req.TeacherUid),
		DayOfWeek:  dow,
		TimeFrom:   timeFrom,
		TimeTo:     timeTo,
		MinRating:  req.MinRating,
		MinVacancy: 1,
		Enabled:    req.GetEnabled(),
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create filter: %v", err)
	}
	return &filterv1.CreateFilterResponse{Filter: filterToProto(created)}, nil
}

// GetFilter возвращает один фильтр.
func (s *Service) GetFilter(ctx context.Context, req *filterv1.GetFilterRequest) (*filterv1.GetFilterResponse, error) {
	userID, err := parseUserID(req.GetUserId())
	if err != nil {
		return nil, err
	}
	filterID, err := strconv.ParseInt(req.GetFilterId(), 10, 64)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "bad filter_id: %v", err)
	}
	f, err := s.store.GetFilterByID(ctx, filterID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "filter not found")
		}
		return nil, status.Errorf(codes.Internal, "get filter: %v", err)
	}
	if f.UserID != userID {
		return nil, status.Errorf(codes.PermissionDenied, "filter does not belong to user")
	}
	return &filterv1.GetFilterResponse{Filter: filterToProto(f)}, nil
}

// ListFilters возвращает все фильтры пользователя.
func (s *Service) ListFilters(ctx context.Context, req *filterv1.ListFiltersRequest) (*filterv1.ListFiltersResponse, error) {
	userID, err := parseUserID(req.GetUserId())
	if err != nil {
		return nil, err
	}
	filters, err := s.store.ListFiltersByUser(ctx, userID, req.GetIncludeDisabled())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list filters: %v", err)
	}
	out := make([]*commonv1.Filter, 0, len(filters))
	for _, f := range filters {
		out = append(out, filterToProto(f))
	}
	return &filterv1.ListFiltersResponse{Filters: out}, nil
}

// UpdateFilter обновляет фильтр.
//
// Семантика update_mask:
//   - пустая маска = заменить ВСЕ поля значениями из запроса (PATCH semantics из proto-комментария);
//   - непустая маска = обновить только поля из неё. Текущее состояние подгружается, отсутствующие в маске поля остаются.
func (s *Service) UpdateFilter(ctx context.Context, req *filterv1.UpdateFilterRequest) (*filterv1.UpdateFilterResponse, error) {
	userID, err := parseUserID(req.GetUserId())
	if err != nil {
		return nil, err
	}
	filterID, err := strconv.ParseInt(req.GetFilterId(), 10, 64)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "bad filter_id: %v", err)
	}

	current, err := s.store.GetFilterByID(ctx, filterID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "filter not found")
		}
		return nil, status.Errorf(codes.Internal, "get filter: %v", err)
	}
	if current.UserID != userID {
		return nil, status.Errorf(codes.PermissionDenied, "filter does not belong to user")
	}

	if vErr := validateTimeRange(req.TimeFrom, req.TimeTo); vErr != nil {
		return nil, vErr
	}
	if vErr := validateRating(req.MinRating); vErr != nil {
		return nil, vErr
	}

	mask := req.GetUpdateMask()
	apply := func(field string) bool {
		if len(mask) == 0 {
			return true
		}
		for _, m := range mask {
			if m == field {
				return true
			}
		}
		return false
	}

	updated := store.UpdateFilterParams{
		ID:         current.ID,
		UserID:     userID,
		Section:    current.Section,
		TeacherUID: current.TeacherUID,
		DayOfWeek:  current.DayOfWeek,
		TimeFrom:   current.TimeFrom,
		TimeTo:     current.TimeTo,
		MinRating:  current.MinRating,
		Enabled:    current.Enabled,
	}

	if apply("section") {
		updated.Section = normalizeStringPtr(req.Section)
	}
	if apply("teacher_uid") {
		updated.TeacherUID = normalizeStringPtr(req.TeacherUid)
	}
	if apply("day_of_week") {
		updated.DayOfWeek = dayOfWeekToString(req.GetDayOfWeek())
	}
	if apply("time_from") {
		t, tErr := timeOfDayFromProto(req.TimeFrom)
		if tErr != nil {
			return nil, tErr
		}
		updated.TimeFrom = t
	}
	if apply("time_to") {
		t, tErr := timeOfDayFromProto(req.TimeTo)
		if tErr != nil {
			return nil, tErr
		}
		updated.TimeTo = t
	}
	if apply("min_rating") {
		updated.MinRating = req.MinRating
	}
	if apply("enabled") {
		updated.Enabled = req.GetEnabled()
	}

	f, err := s.store.UpdateFilter(ctx, updated)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "filter not found")
		}
		return nil, status.Errorf(codes.Internal, "update filter: %v", err)
	}
	return &filterv1.UpdateFilterResponse{Filter: filterToProto(f)}, nil
}

// DeleteFilter удаляет фильтр. Идемпотентен — двойной delete = OK.
func (s *Service) DeleteFilter(ctx context.Context, req *filterv1.DeleteFilterRequest) (*filterv1.DeleteFilterResponse, error) {
	userID, err := parseUserID(req.GetUserId())
	if err != nil {
		return nil, err
	}
	filterID, err := strconv.ParseInt(req.GetFilterId(), 10, 64)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "bad filter_id: %v", err)
	}
	if _, err := s.store.DeleteFilter(ctx, filterID, userID); err != nil {
		return nil, status.Errorf(codes.Internal, "delete filter: %v", err)
	}
	return &filterv1.DeleteFilterResponse{}, nil
}

// ---------------------------------------------------------------------------
// MatchSlots / MarkSeen / ResetKnown
// ---------------------------------------------------------------------------

// MatchSlots применяет enabled-фильтры юзера к слотам, читает known_slots,
// возвращает только МАТЧНУВШИЕ слоты с флагом is_new.
//
// НЕ пишет в known_slots — это делает MarkSeen после успешного notify.
// Не вызывает teachers-svc сам: рейтинг приходит из вне (обогащение
// сделает poller или сам filter после Wave 3). Сейчас MinRating без рейтинга
// = слот не матчится (см. match.matchSlot).
func (s *Service) MatchSlots(ctx context.Context, req *filterv1.MatchSlotsRequest) (*filterv1.MatchSlotsResponse, error) {
	userID, err := parseUserID(req.GetUserId())
	if err != nil {
		return nil, err
	}

	dbFilters, err := s.store.ListFiltersByUser(ctx, userID, false /* enabled only */)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list filters: %v", err)
	}
	if len(dbFilters) == 0 || len(req.GetSlots()) == 0 {
		return &filterv1.MatchSlotsResponse{}, nil
	}

	known, err := s.store.GetKnownSlotsByUser(ctx, userID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get known: %v", err)
	}

	mFilters := make([]match.Filter, 0, len(dbFilters))
	for _, f := range dbFilters {
		mf, err := toMatchFilter(f)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "bad stored filter %d: %v", f.ID, err)
		}
		mFilters = append(mFilters, mf)
	}

	mSlots := make([]match.Slot, 0, len(req.GetSlots()))
	for _, s := range req.GetSlots() {
		mSlots = append(mSlots, toMatchSlot(s))
	}

	// teacherRatings — V1 заглушка. teachers-svc BatchGet вызовет poller на своей
	// стороне в Wave 3 (или будет передавать ratings отдельно). Пока: пустая мапа,
	// фильтры с MinRating не сматчатся (логично — без рейтинга нельзя гарантировать порог).
	matched := match.Match(mFilters, mSlots, known, nil)

	resp := &filterv1.MatchSlotsResponse{
		Matched: make([]*commonv1.MatchedSlot, 0, len(matched)),
	}
	// Map back to proto: достаём оригинальный *commonv1.Slot по id, чтобы не терять поля.
	slotByID := make(map[string]*commonv1.Slot, len(req.GetSlots()))
	for _, s := range req.GetSlots() {
		slotByID[s.GetId()] = s
	}
	for _, m := range matched {
		ms := &commonv1.MatchedSlot{
			Slot:             slotByID[m.Slot.ID],
			MatchedFilterIds: m.MatchedFilterIDs,
			IsNew:            m.IsNew,
		}
		if m.TeacherRating != nil {
			ms.TeacherRating = m.TeacherRating
		}
		resp.Matched = append(resp.Matched, ms)
	}
	return resp, nil
}

// MarkSeen помечает slot_id как известные юзеру и записывает в alert_log.
//
// ON CONFLICT DO NOTHING — повторная пометка не меняет first_seen.
// Никогда не удаляет существующие записи (фикс legacy_main.py:312 бага).
func (s *Service) MarkSeen(ctx context.Context, req *filterv1.MarkSeenRequest) (*filterv1.MarkSeenResponse, error) {
	userID, err := parseUserID(req.GetUserId())
	if err != nil {
		return nil, err
	}
	slotIDs := req.GetSlotIds()
	if len(slotIDs) == 0 {
		return &filterv1.MarkSeenResponse{}, nil
	}

	if err := s.store.InsertKnownSlots(ctx, userID, slotIDs); err != nil {
		return nil, status.Errorf(codes.Internal, "insert known: %v", err)
	}

	// Лог алёрта в alert_log. Канал по умолчанию telegram (тип Wave 2 — только TG).
	// Когда poller начнёт прокидывать канал доставки — расширим RPC.
	payload, _ := json.Marshal(map[string]any{"slot_ids": slotIDs})
	for _, slotID := range slotIDs {
		if _, err := s.store.InsertAlertLog(ctx, store.InsertAlertLogParams{
			UserID:  userID,
			SlotID:  slotID,
			Channel: "telegram",
			Payload: payload,
		}); err != nil {
			return nil, status.Errorf(codes.Internal, "insert alert_log: %v", err)
		}
	}
	return &filterv1.MarkSeenResponse{}, nil
}

// ResetKnown очищает known_slots пользователя — явная админ-операция.
func (s *Service) ResetKnown(ctx context.Context, req *filterv1.ResetKnownRequest) (*filterv1.ResetKnownResponse, error) {
	userID, err := parseUserID(req.GetUserId())
	if err != nil {
		return nil, err
	}
	n, err := s.store.ResetKnownSlots(ctx, userID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "reset known: %v", err)
	}
	if n > int64(int32Max) {
		n = int64(int32Max)
	}
	return &filterv1.ResetKnownResponse{ClearedCount: int32(n)}, nil
}

// ---------------------------------------------------------------------------
// converters / validators
// ---------------------------------------------------------------------------

const int32Max = (1 << 31) - 1

// parseUserID конвертирует proto user_id (string) в int64. В V1 user_id — это
// числовой BIGSERIAL из auth_db.users. Если в будущем перейдём на UUIDv7,
// меняем парсер.
func parseUserID(s string) (int64, error) {
	if s == "" {
		return 0, status.Error(codes.InvalidArgument, "user_id is required")
	}
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, status.Errorf(codes.InvalidArgument, "bad user_id: %v", err)
	}
	return id, nil
}

// normalizeStringPtr возвращает trimmed *string или nil, если входная строка
// пустая/состоит из пробелов. Если *s != nil, но строка пустая — возвращаем nil
// (= снять ограничение).
func normalizeStringPtr(s *string) *string {
	if s == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*s)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// timeOfDayFromProto парсит "HH:MM" → *store.TimeOfDay.
// nil или пустая строка → nil.
func timeOfDayFromProto(s *string) (*store.TimeOfDay, error) {
	if s == nil {
		return nil, nil
	}
	v := strings.TrimSpace(*s)
	if v == "" {
		return nil, nil
	}
	t, err := store.ParseTimeOfDay(v)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "bad time-of-day %q: %v", *s, err)
	}
	return &t, nil
}

// validateTimeRange проверяет, что time_from <= time_to (если оба заданы).
func validateTimeRange(from, to *string) error {
	if from == nil || to == nil {
		return nil
	}
	fromT, err := store.ParseTimeOfDay(strings.TrimSpace(*from))
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "bad time_from: %v", err)
	}
	toT, err := store.ParseTimeOfDay(strings.TrimSpace(*to))
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "bad time_to: %v", err)
	}
	if fromT.After(toT) {
		return status.Error(codes.InvalidArgument, "time_from > time_to")
	}
	return nil
}

// validateRating проверяет, что rating в [0, 5].
func validateRating(r *float64) error {
	if r == nil {
		return nil
	}
	if *r < 0 || *r > 5 {
		return status.Errorf(codes.InvalidArgument, "min_rating must be in [0, 5], got %v", *r)
	}
	return nil
}

// dayOfWeekToString конвертирует enum → строку для БД ("MONDAY", ...).
// UNSPECIFIED → nil.
func dayOfWeekToString(dow commonv1.DayOfWeek) *string {
	switch dow {
	case commonv1.DayOfWeek_DAY_OF_WEEK_UNSPECIFIED:
		return nil
	case commonv1.DayOfWeek_DAY_OF_WEEK_MONDAY:
		return ptrStr("MONDAY")
	case commonv1.DayOfWeek_DAY_OF_WEEK_TUESDAY:
		return ptrStr("TUESDAY")
	case commonv1.DayOfWeek_DAY_OF_WEEK_WEDNESDAY:
		return ptrStr("WEDNESDAY")
	case commonv1.DayOfWeek_DAY_OF_WEEK_THURSDAY:
		return ptrStr("THURSDAY")
	case commonv1.DayOfWeek_DAY_OF_WEEK_FRIDAY:
		return ptrStr("FRIDAY")
	case commonv1.DayOfWeek_DAY_OF_WEEK_SATURDAY:
		return ptrStr("SATURDAY")
	case commonv1.DayOfWeek_DAY_OF_WEEK_SUNDAY:
		return ptrStr("SUNDAY")
	default:
		return nil
	}
}

// dayOfWeekFromString — обратная конвертация.
func dayOfWeekFromString(s *string) commonv1.DayOfWeek {
	if s == nil {
		return commonv1.DayOfWeek_DAY_OF_WEEK_UNSPECIFIED
	}
	switch strings.ToUpper(*s) {
	case "MONDAY":
		return commonv1.DayOfWeek_DAY_OF_WEEK_MONDAY
	case "TUESDAY":
		return commonv1.DayOfWeek_DAY_OF_WEEK_TUESDAY
	case "WEDNESDAY":
		return commonv1.DayOfWeek_DAY_OF_WEEK_WEDNESDAY
	case "THURSDAY":
		return commonv1.DayOfWeek_DAY_OF_WEEK_THURSDAY
	case "FRIDAY":
		return commonv1.DayOfWeek_DAY_OF_WEEK_FRIDAY
	case "SATURDAY":
		return commonv1.DayOfWeek_DAY_OF_WEEK_SATURDAY
	case "SUNDAY":
		return commonv1.DayOfWeek_DAY_OF_WEEK_SUNDAY
	default:
		return commonv1.DayOfWeek_DAY_OF_WEEK_UNSPECIFIED
	}
}

func ptrStr(s string) *string { return &s }

// filterToProto конвертирует store.Filter → commonv1.Filter.
func filterToProto(f store.Filter) *commonv1.Filter {
	p := &commonv1.Filter{
		Id:         strconv.FormatInt(f.ID, 10),
		UserId:     strconv.FormatInt(f.UserID, 10),
		Section:    f.Section,
		TeacherUid: f.TeacherUID,
		DayOfWeek:  dayOfWeekFromString(f.DayOfWeek),
		MinRating:  f.MinRating,
		Enabled:    f.Enabled,
		CreatedAt:  timestamppb.New(f.CreatedAt),
		UpdatedAt:  timestamppb.New(f.UpdatedAt),
	}
	if f.TimeFrom != nil {
		s := f.TimeFrom.String()
		p.TimeFrom = &s
	}
	if f.TimeTo != nil {
		s := f.TimeTo.String()
		p.TimeTo = &s
	}
	return p
}

// toMatchFilter конвертирует store.Filter → match.Filter (доменная модель).
func toMatchFilter(f store.Filter) (match.Filter, error) {
	mf := match.Filter{
		ID:         strconv.FormatInt(f.ID, 10),
		TeacherUID: f.TeacherUID,
		DayOfWeek:  dayOfWeekFromString(f.DayOfWeek),
		MinRating:  f.MinRating,
		MinVacancy: f.MinVacancy,
	}
	if f.Section != nil {
		// EqualFold в match сам нормализует регистр, но trim полезен — БД может
		// сохранить trailing space.
		v := strings.TrimSpace(*f.Section)
		mf.Section = &v
	}
	if f.TimeFrom != nil {
		v := f.TimeFrom.Hour*60 + f.TimeFrom.Minute
		mf.TimeFrom = &v
	}
	if f.TimeTo != nil {
		v := f.TimeTo.Hour*60 + f.TimeTo.Minute
		mf.TimeTo = &v
	}
	return mf, nil
}

// toMatchSlot конвертирует commonv1.Slot → match.Slot.
// Не нормализует section: match.Match сам делает case-insensitive сравнение
// через strings.EqualFold, так что слой остаётся идемпотентным относительно входа.
func toMatchSlot(s *commonv1.Slot) match.Slot {
	ms := match.Slot{
		ID:        s.GetId(),
		Section:   s.GetSection(),
		Time:      s.GetTime(),
		DayOfWeek: s.GetDayOfWeek(),
		Vacancy:   s.GetVacancy(),
	}
	if s.TeacherUid != nil {
		ms.TeacherUID = *s.TeacherUid
	}
	return ms
}
