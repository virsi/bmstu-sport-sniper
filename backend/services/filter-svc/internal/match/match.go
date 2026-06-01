// Package match реализует чистую функцию матчинга слотов против пользовательских
// фильтров. Без I/O: вход — слайсы Filter, Slot, set known. Выход —
// слайс MatchedSlot.
//
// Использование:
//
//	matched := match.Match(filters, slots, known)
//
// Логика:
//   - Слот фильтруется, если vacancy < min_vacancy (или vacancy < 1 если фильтр
//     не указал min_vacancy явно).
//   - Section сравнивается case-insensitive (Unicode lower).
//   - TeacherUID — точное совпадение.
//   - DayOfWeek — точное совпадение по enum.
//   - TimeRange — слот.time = "HH:MM-HH:MM"; нужно, чтобы start слота
//     ≥ time_from И end слота ≤ time_to. Если поле фильтра пустое — не учитывается.
//   - MinRating — если задан, у слота должен быть teacher_uid И в TeacherRatings
//     должна быть оценка ≥ min_rating.
//
// Возвращает MatchedSlot[]: один MatchedSlot на слот, с объединённым списком
// matched_filter_ids; is_new = slot.id NOT IN known.
package match

import (
	"strings"

	commonv1 "github.com/fizcultor/backend/gen/common/v1"
)

// Filter — упрощённая (доменная) копия store.Filter для match-функции.
// Передаётся вызывающим сервисным слоем после конвертации.
type Filter struct {
	// ID — текстовый id фильтра (используется в matched_filter_ids).
	ID string
	// Section — фильтр секции (lower-cased предварительно).
	Section *string
	// TeacherUID — фильтр преподавателя.
	TeacherUID *string
	// DayOfWeek — день недели как enum common.v1.DayOfWeek; UNSPECIFIED = не учитывать.
	DayOfWeek commonv1.DayOfWeek
	// TimeFrom — нижняя граница (минуты с полуночи); nil = не учитывать.
	TimeFrom *int
	// TimeTo — верхняя граница (минуты); nil = не учитывать.
	TimeTo *int
	// MinRating — минимум рейтинга 0..5; nil = не учитывать.
	MinRating *float64
	// MinVacancy — минимум свободных мест; 0 = дефолт 1.
	MinVacancy int32
}

// Slot — упрощённая копия common.v1.Slot для матчинга.
type Slot struct {
	// ID — детерминированный slot.id.
	ID string
	// Section — секция (может быть пустой).
	Section string
	// TeacherUID — preview слота.
	TeacherUID string
	// DayOfWeek — день недели.
	DayOfWeek commonv1.DayOfWeek
	// Time — строка "HH:MM-HH:MM" из BMSTU LKS.
	Time string
	// Vacancy — свободных мест.
	Vacancy int32
}

// MatchedSlot — результат матчинга.
type MatchedSlot struct {
	// Slot — исходный слот.
	Slot Slot
	// MatchedFilterIDs — какие фильтры (по id) подошли.
	MatchedFilterIDs []string
	// TeacherRating — рейтинг преподавателя (если есть).
	TeacherRating *float64
	// IsNew — true, если slot.id отсутствовал в known.
	IsNew bool
}

// Match — чистая функция матчинга.
//
//   - filters: фильтры пользователя (только enabled, caller отвечает за фильтрацию);
//   - slots: кандидаты от bmstu-svc;
//   - known: множество ранее увиденных slot.id для дедупа;
//   - teacherRatings: рейтинги по teacher_uid (для проверки MinRating и обогащения).
//
// Возвращает MatchedSlot[] в стабильном порядке (по индексу slots). MatchedSlot
// создаётся только для слотов с хотя бы одним совпавшим фильтром.
func Match(
	filters []Filter,
	slots []Slot,
	known map[string]struct{},
	teacherRatings map[string]float64,
) []MatchedSlot {
	if len(filters) == 0 || len(slots) == 0 {
		return nil
	}

	result := make([]MatchedSlot, 0, len(slots))
	for _, slot := range slots {
		ms, ok := matchSlot(slot, filters, known, teacherRatings)
		if !ok {
			continue
		}
		result = append(result, ms)
	}
	return result
}

// matchSlot проверяет все фильтры против одного слота и собирает MatchedSlot,
// если матчнул хотя бы один. Объединяет matched_filter_ids от всех совпавших.
func matchSlot(
	slot Slot,
	filters []Filter,
	known map[string]struct{},
	teacherRatings map[string]float64,
) (MatchedSlot, bool) {
	startMin, endMin, timeOK := parseTimeRange(slot.Time)
	rating, hasRating := teacherRatings[slot.TeacherUID]

	matchedIDs := make([]string, 0, len(filters))
	for _, f := range filters {
		minVac := f.MinVacancy
		if minVac < 1 {
			minVac = 1
		}
		if slot.Vacancy < minVac {
			continue
		}
		if f.Section != nil && !strings.EqualFold(strings.TrimSpace(*f.Section), strings.TrimSpace(slot.Section)) {
			continue
		}
		if f.TeacherUID != nil && *f.TeacherUID != slot.TeacherUID {
			continue
		}
		if f.DayOfWeek != commonv1.DayOfWeek_DAY_OF_WEEK_UNSPECIFIED && f.DayOfWeek != slot.DayOfWeek {
			continue
		}
		if (f.TimeFrom != nil || f.TimeTo != nil) && !timeOK {
			continue
		}
		if f.TimeFrom != nil && startMin < *f.TimeFrom {
			continue
		}
		if f.TimeTo != nil && endMin > *f.TimeTo {
			continue
		}
		if f.MinRating != nil {
			if !hasRating || rating < *f.MinRating {
				continue
			}
		}
		matchedIDs = append(matchedIDs, f.ID)
	}

	if len(matchedIDs) == 0 {
		return MatchedSlot{}, false
	}

	ms := MatchedSlot{
		Slot:             slot,
		MatchedFilterIDs: matchedIDs,
	}
	if hasRating {
		r := rating
		ms.TeacherRating = &r
	}
	if _, seen := known[slot.ID]; !seen {
		ms.IsNew = true
	}
	return ms, true
}

// parseTimeRange разбирает строку "HH:MM-HH:MM" → (startMin, endMin, ok).
// ok=false если формат не распознан.
func parseTimeRange(s string) (startMin, endMin int, ok bool) {
	idx := strings.IndexByte(s, '-')
	if idx <= 0 || idx == len(s)-1 {
		return 0, 0, false
	}
	startMin, ok = parseHHMM(strings.TrimSpace(s[:idx]))
	if !ok {
		return 0, 0, false
	}
	endMin, ok = parseHHMM(strings.TrimSpace(s[idx+1:]))
	if !ok {
		return 0, 0, false
	}
	return startMin, endMin, true
}

// parseHHMM парсит "HH:MM" → минуты с полуночи; bool=false если формат неверный.
func parseHHMM(s string) (int, bool) {
	if len(s) < 4 || len(s) > 5 {
		return 0, false
	}
	colon := strings.IndexByte(s, ':')
	if colon != 1 && colon != 2 {
		return 0, false
	}
	hh, ok := atoi(s[:colon])
	if !ok {
		return 0, false
	}
	mm, ok := atoi(s[colon+1:])
	if !ok {
		return 0, false
	}
	if hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, false
	}
	return hh*60 + mm, true
}

// atoi — мини-конвертер строки в int без аллокаций; bool=false если не число.
func atoi(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}

// ToMinutes — публичный helper: "HH:MM" → минуты с полуночи. Возвращает (0,false)
// если формат неверный. Используется в сервисном слое для нормализации time_from/to.
func ToMinutes(s string) (int, bool) {
	return parseHHMM(s)
}
