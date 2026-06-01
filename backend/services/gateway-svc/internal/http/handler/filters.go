package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	commonv1 "github.com/fizcultor/backend/gen/common/v1"
	filterv1 "github.com/fizcultor/backend/gen/filter/v1"
	"github.com/fizcultor/backend/services/gateway-svc/internal/http/middleware"
)

// filterDTO — REST представление фильтра (api.md §4).
//
// optional поля показываются как nil (omitempty), это удобнее для фронта,
// чем пустая строка/ноль.
type filterDTO struct {
	ID         string    `json:"id"`
	UserID     string    `json:"user_id"`
	Section    *string   `json:"section"`
	TeacherUID *string   `json:"teacher_uid"`
	DayOfWeek  string    `json:"day_of_week"`
	TimeFrom   *string   `json:"time_from"`
	TimeTo     *string   `json:"time_to"`
	MinRating  *float64  `json:"min_rating"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// listFiltersResponse — ответ GET /api/filters (api.md §4).
type listFiltersResponse struct {
	Filters []filterDTO `json:"filters"`
}

// filterInput — общий body для POST/PATCH /api/filters.
//
// Все поля указатели — чтобы различать «не задано» vs «явный null/zero».
// PATCH с {"enabled": false} обнулит только Enabled, остальные не тронет.
type filterInput struct {
	Section    *string  `json:"section"`
	TeacherUID *string  `json:"teacher_uid"`
	DayOfWeek  *string  `json:"day_of_week"`
	TimeFrom   *string  `json:"time_from"`
	TimeTo     *string  `json:"time_to"`
	MinRating  *float64 `json:"min_rating"`
	Enabled    *bool    `json:"enabled"`
}

// ListFilters — GET /api/filters. Требует Auth middleware.
//
// Query-param include_disabled (bool, default true). По api.md §4.
func (h *Handler) ListFilters(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFrom(r.Context())
	if userID == "" {
		WriteError(w, r, NewUnauthorized("missing user_id in context"))
		return
	}

	includeDisabled := true
	if v := r.URL.Query().Get("include_disabled"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			WriteError(w, r, NewBadRequest("include_disabled must be a bool"))
			return
		}
		includeDisabled = b
	}

	resp, err := h.deps.Clients.Filter.ListFilters(r.Context(), &filterv1.ListFiltersRequest{
		UserId:          userID,
		IncludeDisabled: includeDisabled,
	})
	if err != nil {
		WriteError(w, r, err)
		return
	}

	out := listFiltersResponse{Filters: make([]filterDTO, 0, len(resp.GetFilters()))}
	for _, f := range resp.GetFilters() {
		out.Filters = append(out.Filters, filterToDTO(f))
	}
	WriteJSON(w, http.StatusOK, out)
}

// CreateFilter — POST /api/filters. Требует Auth middleware.
func (h *Handler) CreateFilter(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFrom(r.Context())
	if userID == "" {
		WriteError(w, r, NewUnauthorized("missing user_id in context"))
		return
	}

	var body filterInput
	if err := DecodeJSON(r, &body, 0); err != nil {
		WriteError(w, r, err)
		return
	}

	dow, ok := parseDayOfWeek(body.DayOfWeek)
	if !ok {
		WriteError(w, r, NewBadRequest("day_of_week must be one of MONDAY..SUNDAY or ANY"))
		return
	}

	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}

	resp, err := h.deps.Clients.Filter.CreateFilter(r.Context(), &filterv1.CreateFilterRequest{
		UserId:     userID,
		Section:    body.Section,
		TeacherUid: body.TeacherUID,
		DayOfWeek:  dow,
		TimeFrom:   body.TimeFrom,
		TimeTo:     body.TimeTo,
		MinRating:  body.MinRating,
		Enabled:    enabled,
	})
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusCreated, filterToDTO(resp.GetFilter()))
}

// UpdateFilter — PATCH /api/filters/:id. Требует Auth middleware.
//
// Поведение по api.md §4:
//   - Любое поле опционально.
//   - null сбрасывает поле в «без ограничения».
//   - Поля, отсутствующие в body, не трогаются (PATCH semantics).
//
// Реализация: собираем update_mask из полей, явно присутствующих в JSON
// (отличить null от missing — задача *T указателей).
func (h *Handler) UpdateFilter(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFrom(r.Context())
	if userID == "" {
		WriteError(w, r, NewUnauthorized("missing user_id in context"))
		return
	}
	filterID := chi.URLParam(r, "id")
	if filterID == "" {
		WriteError(w, r, NewBadRequest("missing filter id in path"))
		return
	}

	// Двухпроходный парс body: сначала «было ли поле», потом значения.
	var present map[string]any
	if err := DecodeJSONInto(r, &present, 0); err != nil {
		WriteError(w, r, err)
		return
	}

	req := &filterv1.UpdateFilterRequest{
		UserId:   userID,
		FilterId: filterID,
	}
	var mask []string

	if v, ok := present["section"]; ok {
		mask = append(mask, "section")
		req.Section = strOrNil(v)
	}
	if v, ok := present["teacher_uid"]; ok {
		mask = append(mask, "teacher_uid")
		req.TeacherUid = strOrNil(v)
	}
	if v, ok := present["day_of_week"]; ok {
		mask = append(mask, "day_of_week")
		s, _ := v.(string)
		dow, parseOK := parseDayOfWeek(&s)
		if !parseOK && v != nil {
			WriteError(w, r, NewBadRequest("day_of_week must be one of MONDAY..SUNDAY or ANY"))
			return
		}
		req.DayOfWeek = dow
	}
	if v, ok := present["time_from"]; ok {
		mask = append(mask, "time_from")
		req.TimeFrom = strOrNil(v)
	}
	if v, ok := present["time_to"]; ok {
		mask = append(mask, "time_to")
		req.TimeTo = strOrNil(v)
	}
	if v, ok := present["min_rating"]; ok {
		mask = append(mask, "min_rating")
		req.MinRating = floatOrNil(v)
	}
	if v, ok := present["enabled"]; ok {
		mask = append(mask, "enabled")
		if b, isBool := v.(bool); isBool {
			req.Enabled = &b
		}
	}
	req.UpdateMask = mask

	resp, err := h.deps.Clients.Filter.UpdateFilter(r.Context(), req)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, filterToDTO(resp.GetFilter()))
}

// DeleteFilter — DELETE /api/filters/:id. Требует Auth middleware.
//
// 204 при успехе. filter-svc вернёт NOT_FOUND/PERMISSION_DENIED, что
// маппится в 404/403 через WriteError.
func (h *Handler) DeleteFilter(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFrom(r.Context())
	if userID == "" {
		WriteError(w, r, NewUnauthorized("missing user_id in context"))
		return
	}
	filterID := chi.URLParam(r, "id")
	if filterID == "" {
		WriteError(w, r, NewBadRequest("missing filter id in path"))
		return
	}

	if _, err := h.deps.Clients.Filter.DeleteFilter(r.Context(), &filterv1.DeleteFilterRequest{
		UserId:   userID,
		FilterId: filterID,
	}); err != nil {
		WriteError(w, r, err)
		return
	}
	WriteNoContent(w)
}

// filterToDTO маппит proto Filter → JSON DTO.
func filterToDTO(f *commonv1.Filter) filterDTO {
	if f == nil {
		return filterDTO{}
	}
	d := filterDTO{
		ID:        f.GetId(),
		UserID:    f.GetUserId(),
		DayOfWeek: dayOfWeekToString(f.GetDayOfWeek()),
		Enabled:   f.GetEnabled(),
		CreatedAt: tsToTime(f.GetCreatedAt()),
		UpdatedAt: tsToTime(f.GetUpdatedAt()),
	}
	d.Section = f.Section
	d.TeacherUID = f.TeacherUid
	d.TimeFrom = f.TimeFrom
	d.TimeTo = f.TimeTo
	d.MinRating = f.MinRating
	return d
}

// dayOfWeekToString сериализует enum в строку api.md (MONDAY..SUNDAY или ANY).
func dayOfWeekToString(d commonv1.DayOfWeek) string {
	switch d {
	case commonv1.DayOfWeek_DAY_OF_WEEK_MONDAY:
		return "MONDAY"
	case commonv1.DayOfWeek_DAY_OF_WEEK_TUESDAY:
		return "TUESDAY"
	case commonv1.DayOfWeek_DAY_OF_WEEK_WEDNESDAY:
		return "WEDNESDAY"
	case commonv1.DayOfWeek_DAY_OF_WEEK_THURSDAY:
		return "THURSDAY"
	case commonv1.DayOfWeek_DAY_OF_WEEK_FRIDAY:
		return "FRIDAY"
	case commonv1.DayOfWeek_DAY_OF_WEEK_SATURDAY:
		return "SATURDAY"
	case commonv1.DayOfWeek_DAY_OF_WEEK_SUNDAY:
		return "SUNDAY"
	default:
		return "ANY"
	}
}

// parseDayOfWeek принимает строку (MONDAY..SUNDAY/ANY) и возвращает enum.
// nil или ANY → DAY_OF_WEEK_UNSPECIFIED, ok=true. Невалидное значение → ok=false.
func parseDayOfWeek(s *string) (commonv1.DayOfWeek, bool) {
	if s == nil || *s == "" || *s == "ANY" {
		return commonv1.DayOfWeek_DAY_OF_WEEK_UNSPECIFIED, true
	}
	switch *s {
	case "MONDAY":
		return commonv1.DayOfWeek_DAY_OF_WEEK_MONDAY, true
	case "TUESDAY":
		return commonv1.DayOfWeek_DAY_OF_WEEK_TUESDAY, true
	case "WEDNESDAY":
		return commonv1.DayOfWeek_DAY_OF_WEEK_WEDNESDAY, true
	case "THURSDAY":
		return commonv1.DayOfWeek_DAY_OF_WEEK_THURSDAY, true
	case "FRIDAY":
		return commonv1.DayOfWeek_DAY_OF_WEEK_FRIDAY, true
	case "SATURDAY":
		return commonv1.DayOfWeek_DAY_OF_WEEK_SATURDAY, true
	case "SUNDAY":
		return commonv1.DayOfWeek_DAY_OF_WEEK_SUNDAY, true
	default:
		return commonv1.DayOfWeek_DAY_OF_WEEK_UNSPECIFIED, false
	}
}

// strOrNil безопасно конвертирует any → *string. JSON null → nil.
func strOrNil(v any) *string {
	if v == nil {
		return nil
	}
	if s, ok := v.(string); ok {
		return &s
	}
	return nil
}

// floatOrNil безопасно конвертирует any → *float64. JSON null → nil.
func floatOrNil(v any) *float64 {
	if v == nil {
		return nil
	}
	if f, ok := v.(float64); ok {
		return &f
	}
	return nil
}
