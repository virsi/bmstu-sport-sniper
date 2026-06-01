package match_test

import (
	"sort"
	"testing"

	commonv1 "github.com/fizcultor/backend/gen/common/v1"
	"github.com/fizcultor/backend/services/filter-svc/internal/match"
)

// helpers.
func ptrStr(s string) *string   { return &s }
func ptrInt(i int) *int         { return &i }
func ptrF64(f float64) *float64 { return &f }

func TestMatch(t *testing.T) {
	const slotID = "sha1-deadbeef"
	const teacherUID = "teach-1"

	// Стандартные сэмпл-слоты для разных тестов.
	monMorning := match.Slot{
		ID:         slotID,
		Section:    "Аэробика",
		TeacherUID: teacherUID,
		DayOfWeek:  commonv1.DayOfWeek_DAY_OF_WEEK_MONDAY,
		Time:       "08:00-09:30",
		Vacancy:    3,
	}
	tueEvening := match.Slot{
		ID:         "sha1-cafebabe",
		Section:    "Силовая",
		TeacherUID: "teach-2",
		DayOfWeek:  commonv1.DayOfWeek_DAY_OF_WEEK_TUESDAY,
		Time:       "18:00-19:30",
		Vacancy:    1,
	}
	zeroVacancy := match.Slot{
		ID:         "sha1-zero",
		Section:    "Аэробика",
		TeacherUID: teacherUID,
		DayOfWeek:  commonv1.DayOfWeek_DAY_OF_WEEK_MONDAY,
		Time:       "08:00-09:30",
		Vacancy:    0,
	}

	tests := []struct {
		name           string
		filters        []match.Filter
		slots          []match.Slot
		known          map[string]struct{}
		ratings        map[string]float64
		wantSlotIDs    []string
		wantNewIDs     []string // подмножество wantSlotIDs с IsNew=true
		wantFiltersFor map[string][]string
	}{
		{
			name:        "empty filters → no match",
			filters:     nil,
			slots:       []match.Slot{monMorning},
			wantSlotIDs: nil,
		},
		{
			name:        "empty slots → no match",
			filters:     []match.Filter{{ID: "f1"}},
			slots:       nil,
			wantSlotIDs: nil,
		},
		{
			name: "single filter — single slot match",
			filters: []match.Filter{
				{ID: "f1", Section: ptrStr("аэробика")},
			},
			slots:       []match.Slot{monMorning},
			wantSlotIDs: []string{slotID},
			wantNewIDs:  []string{slotID},
			wantFiltersFor: map[string][]string{
				slotID: {"f1"},
			},
		},
		{
			name: "multiple filters one slot — merge filter ids",
			filters: []match.Filter{
				{ID: "f1", Section: ptrStr("аэробика")},
				{ID: "f2", TeacherUID: ptrStr(teacherUID)},
				{ID: "f3", DayOfWeek: commonv1.DayOfWeek_DAY_OF_WEEK_MONDAY},
			},
			slots:       []match.Slot{monMorning},
			wantSlotIDs: []string{slotID},
			wantFiltersFor: map[string][]string{
				slotID: {"f1", "f2", "f3"},
			},
		},
		{
			name: "day mismatch — filtered out",
			filters: []match.Filter{
				{ID: "f1", DayOfWeek: commonv1.DayOfWeek_DAY_OF_WEEK_FRIDAY},
			},
			slots:       []match.Slot{monMorning, tueEvening},
			wantSlotIDs: nil,
		},
		{
			name: "time range — partial overlap doesn't match",
			filters: []match.Filter{
				{ID: "f1", TimeFrom: ptrInt(8 * 60), TimeTo: ptrInt(9 * 60)}, // 08:00-09:00, слот 08:00-09:30 не помещается
			},
			slots:       []match.Slot{monMorning},
			wantSlotIDs: nil,
		},
		{
			name: "time range — slot fully inside",
			filters: []match.Filter{
				{ID: "f1", TimeFrom: ptrInt(7*60 + 30), TimeTo: ptrInt(10 * 60)},
			},
			slots:       []match.Slot{monMorning},
			wantSlotIDs: []string{slotID},
		},
		{
			name: "rating filter — passes",
			filters: []match.Filter{
				{ID: "f1", MinRating: ptrF64(4.0)},
			},
			slots:       []match.Slot{monMorning},
			ratings:     map[string]float64{teacherUID: 4.5},
			wantSlotIDs: []string{slotID},
		},
		{
			name: "rating filter — fails",
			filters: []match.Filter{
				{ID: "f1", MinRating: ptrF64(4.0)},
			},
			slots:       []match.Slot{monMorning},
			ratings:     map[string]float64{teacherUID: 3.0},
			wantSlotIDs: nil,
		},
		{
			name: "rating filter — no teacher rating, slot filtered out",
			filters: []match.Filter{
				{ID: "f1", MinRating: ptrF64(4.0)},
			},
			slots:       []match.Slot{monMorning},
			ratings:     nil,
			wantSlotIDs: nil,
		},
		{
			name: "vacancy zero — filtered out",
			filters: []match.Filter{
				{ID: "f1", Section: ptrStr("Аэробика")},
			},
			slots:       []match.Slot{zeroVacancy},
			wantSlotIDs: nil,
		},
		{
			name: "min_vacancy=2 — slot with vacancy=1 filtered out",
			filters: []match.Filter{
				{ID: "f1", MinVacancy: 2},
			},
			slots:       []match.Slot{tueEvening}, // vacancy=1
			wantSlotIDs: nil,
		},
		{
			name: "known slot — IsNew=false",
			filters: []match.Filter{
				{ID: "f1", Section: ptrStr("Аэробика")},
			},
			slots:       []match.Slot{monMorning},
			known:       map[string]struct{}{slotID: {}},
			wantSlotIDs: []string{slotID},
			wantNewIDs:  nil, // не new
		},
		{
			name: "case-insensitive section — matches with different case",
			filters: []match.Filter{
				{ID: "f1", Section: ptrStr("АЭРОБИКА")},
			},
			slots:       []match.Slot{monMorning},
			wantSlotIDs: []string{slotID},
		},
		{
			name: "two slots — both pass independent filter, both returned",
			filters: []match.Filter{
				{ID: "f1", Section: ptrStr("Аэробика")},
				{ID: "f2", Section: ptrStr("Силовая")},
			},
			slots: []match.Slot{monMorning, tueEvening},
			wantSlotIDs: []string{
				slotID,
				"sha1-cafebabe",
			},
			wantFiltersFor: map[string][]string{
				slotID:          {"f1"},
				"sha1-cafebabe": {"f2"},
			},
		},
		{
			name: "malformed time in slot, but filter has no time range — still passes",
			filters: []match.Filter{
				{ID: "f1", Section: ptrStr("Аэробика")},
			},
			slots: []match.Slot{
				{
					ID:        "malformed-time",
					Section:   "Аэробика",
					DayOfWeek: commonv1.DayOfWeek_DAY_OF_WEEK_MONDAY,
					Time:      "bad",
					Vacancy:   2,
				},
			},
			wantSlotIDs: []string{"malformed-time"},
		},
		{
			name: "malformed time in slot, filter has time range — filtered out",
			filters: []match.Filter{
				{ID: "f1", TimeFrom: ptrInt(8 * 60)},
			},
			slots: []match.Slot{
				{
					ID:        "malformed-time",
					DayOfWeek: commonv1.DayOfWeek_DAY_OF_WEEK_MONDAY,
					Time:      "bad",
					Vacancy:   2,
				},
			},
			wantSlotIDs: nil,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			matched := match.Match(tc.filters, tc.slots, tc.known, tc.ratings)

			gotIDs := make([]string, 0, len(matched))
			for _, m := range matched {
				gotIDs = append(gotIDs, m.Slot.ID)
			}
			sort.Strings(gotIDs)

			expected := append([]string(nil), tc.wantSlotIDs...)
			sort.Strings(expected)

			if !equalStringSlices(gotIDs, expected) {
				t.Fatalf("slot IDs:\n got=%v\nwant=%v", gotIDs, expected)
			}

			// Проверка matched_filter_ids.
			for _, m := range matched {
				want, ok := tc.wantFiltersFor[m.Slot.ID]
				if !ok {
					continue
				}
				got := append([]string(nil), m.MatchedFilterIDs...)
				sort.Strings(got)
				sort.Strings(want)
				if !equalStringSlices(got, want) {
					t.Errorf("filters for slot %s:\n got=%v\nwant=%v", m.Slot.ID, got, want)
				}
			}

			// Проверка IsNew.
			newSet := map[string]struct{}{}
			for _, m := range matched {
				if m.IsNew {
					newSet[m.Slot.ID] = struct{}{}
				}
			}
			wantNew := map[string]struct{}{}
			for _, id := range tc.wantNewIDs {
				wantNew[id] = struct{}{}
			}
			// если wantNewIDs == nil, ожидаем что либо никто не new, либо тест не задал — пропустим.
			if tc.wantNewIDs != nil {
				if len(newSet) != len(wantNew) {
					t.Errorf("IsNew count mismatch: got %d, want %d", len(newSet), len(wantNew))
				}
				for id := range wantNew {
					if _, ok := newSet[id]; !ok {
						t.Errorf("expected IsNew=true for slot %s", id)
					}
				}
			}
		})
	}
}

func TestMatch_EnrichTeacherRating(t *testing.T) {
	filters := []match.Filter{{ID: "f1", Section: ptrStr("Аэробика")}}
	slot := match.Slot{
		ID:         "sid",
		Section:    "Аэробика",
		TeacherUID: "teach-1",
		DayOfWeek:  commonv1.DayOfWeek_DAY_OF_WEEK_MONDAY,
		Time:       "08:00-09:30",
		Vacancy:    2,
	}
	matched := match.Match(filters, []match.Slot{slot}, nil, map[string]float64{"teach-1": 4.75})
	if len(matched) != 1 {
		t.Fatalf("want 1 match, got %d", len(matched))
	}
	if matched[0].TeacherRating == nil || *matched[0].TeacherRating != 4.75 {
		t.Errorf("teacher rating not propagated: %v", matched[0].TeacherRating)
	}
}

func TestToMinutes(t *testing.T) {
	tests := map[string]struct {
		in   string
		want int
		ok   bool
	}{
		"00:00":          {"00:00", 0, true},
		"23:59":          {"23:59", 23*60 + 59, true},
		"08:30":          {"08:30", 8*60 + 30, true},
		"single digit h": {"8:00", 8 * 60, true},
		"bad":            {"bad", 0, false},
		"empty":          {"", 0, false},
		"too long":       {"08:300", 0, false},
		"out of range":   {"25:00", 0, false},
	}
	for name, tc := range tests {
		tc := tc
		t.Run(name, func(t *testing.T) {
			got, ok := match.ToMinutes(tc.in)
			if ok != tc.ok || got != tc.want {
				t.Errorf("ToMinutes(%q) = (%d, %v), want (%d, %v)", tc.in, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// equalStringSlices: nil treated equal to empty.
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
