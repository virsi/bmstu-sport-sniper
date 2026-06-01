package handler

import (
	"context"
	"net/http"
	"time"

	bmstuv1 "github.com/fizcultor/backend/gen/bmstu/v1"
	commonv1 "github.com/fizcultor/backend/gen/common/v1"
	"github.com/fizcultor/backend/services/gateway-svc/internal/http/middleware"
)

// slotDTO — REST представление одного слота (api.md §5).
//
// Optional поля показываются как nil. Поля matched_filter_ids и is_new
// у /api/slots приходят пустыми (без матчинга), это норма для KISS V1.
type slotDTO struct {
	ID               string   `json:"id"`
	Week             int32    `json:"week"`
	Time             string   `json:"time"`
	Section          *string  `json:"section,omitempty"`
	Place            string   `json:"place"`
	TeacherName      string   `json:"teacher_name"`
	TeacherUID       *string  `json:"teacher_uid,omitempty"`
	TeacherRating    *float64 `json:"teacher_rating,omitempty"`
	Vacancy          int32    `json:"vacancy"`
	SemesterUUID     string   `json:"semester_uuid"`
	DayOfWeek        string   `json:"day_of_week"`
	MatchedFilterIDs []string `json:"matched_filter_ids"`
	IsNew            bool     `json:"is_new"`
}

// slotsResponse — ответ GET /api/slots (api.md §5).
type slotsResponse struct {
	Slots     []slotDTO `json:"slots"`
	FetchedAt time.Time `json:"fetched_at"`
}

// Slots — GET /api/slots. Требует Auth middleware.
//
// Two modes:
//  1. SlotsEndpointEnabled=false (KISS V1, default) → возвращает пустой
//     массив. Источник истины live-данных — SSE-стрим.
//  2. SlotsEndpointEnabled=true → синхронно дёргает bmstu.FetchGroups
//     с таймаутом SlotsFetchTimeoutSeconds. Удобно для дебага, не для прод-нагрузки.
//
// TODO V2: реализовать filter.GetCachedSlots, кэшировать на стороне filter-svc.
func (h *Handler) Slots(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFrom(r.Context())
	if userID == "" {
		WriteError(w, r, NewUnauthorized("missing user_id in context"))
		return
	}

	if !h.deps.SlotsEndpointEnabled {
		WriteJSON(w, http.StatusOK, slotsResponse{
			Slots:     []slotDTO{},
			FetchedAt: time.Now().UTC(),
		})
		return
	}

	timeout := time.Duration(h.deps.SlotsFetchTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	resp, err := h.deps.Clients.Bmstu.FetchGroups(ctx, &bmstuv1.FetchGroupsRequest{UserId: userID})
	if err != nil {
		WriteError(w, r, err)
		return
	}

	out := slotsResponse{
		Slots:     make([]slotDTO, 0, len(resp.GetSlots())),
		FetchedAt: tsToTime(resp.GetFetchedAt()),
	}
	for _, s := range resp.GetSlots() {
		out.Slots = append(out.Slots, slotToDTO(s, nil, false))
	}
	WriteJSON(w, http.StatusOK, out)
}

// slotToDTO — общий маппер Slot+enrichment → REST DTO. Используется и в
// /api/slots, и (через JSON в payload) в SSE-event.
func slotToDTO(s *commonv1.Slot, matchedFilterIDs []string, isNew bool) slotDTO {
	if s == nil {
		return slotDTO{}
	}
	d := slotDTO{
		ID:               s.GetId(),
		Week:             s.GetWeek(),
		Time:             s.GetTime(),
		Place:            s.GetPlace(),
		TeacherName:      s.GetTeacherName(),
		Vacancy:          s.GetVacancy(),
		SemesterUUID:     s.GetSemesterUuid(),
		DayOfWeek:        dayOfWeekToString(s.GetDayOfWeek()),
		MatchedFilterIDs: matchedFilterIDs,
		IsNew:            isNew,
	}
	if d.MatchedFilterIDs == nil {
		d.MatchedFilterIDs = []string{}
	}
	d.Section = s.Section
	d.TeacherUID = s.TeacherUid
	return d
}
