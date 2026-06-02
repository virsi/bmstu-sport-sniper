package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/fizcultor/backend/gen/common/v1"
	filterv1 "github.com/fizcultor/backend/gen/filter/v1"
	"github.com/fizcultor/backend/services/filter-svc/internal/service"
	"github.com/fizcultor/backend/services/filter-svc/internal/store"
)

// mockStore — тестовая in-memory реализация service.Store.
type mockStore struct {
	filters    map[int64]store.Filter
	known      map[int64]map[string]struct{}
	alertLog   []store.AlertLog
	nextID     int64
	failCreate bool
	failKnown  bool
}

func newMockStore() *mockStore {
	return &mockStore{
		filters: map[int64]store.Filter{},
		known:   map[int64]map[string]struct{}{},
		nextID:  1,
	}
}

func (m *mockStore) CreateFilter(_ context.Context, p store.CreateFilterParams) (store.Filter, error) {
	if m.failCreate {
		return store.Filter{}, errors.New("forced")
	}
	id := m.nextID
	m.nextID++
	now := time.Now().UTC()
	f := store.Filter{
		ID:         id,
		UserID:     p.UserID,
		Section:    p.Section,
		TeacherUID: p.TeacherUID,
		DayOfWeek:  p.DayOfWeek,
		TimeFrom:   p.TimeFrom,
		TimeTo:     p.TimeTo,
		MinRating:  p.MinRating,
		MinVacancy: p.MinVacancy,
		Enabled:    p.Enabled,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	m.filters[id] = f
	return f, nil
}

func (m *mockStore) GetFilterByID(_ context.Context, id int64) (store.Filter, error) {
	f, ok := m.filters[id]
	if !ok {
		return store.Filter{}, store.ErrNotFound
	}
	return f, nil
}

func (m *mockStore) ListFiltersByUser(_ context.Context, userID int64, includeDisabled bool) ([]store.Filter, error) {
	out := make([]store.Filter, 0, len(m.filters))
	for _, f := range m.filters {
		if f.UserID != userID {
			continue
		}
		if !includeDisabled && !f.Enabled {
			continue
		}
		out = append(out, f)
	}
	return out, nil
}

func (m *mockStore) UpdateFilter(_ context.Context, p store.UpdateFilterParams) (store.Filter, error) {
	f, ok := m.filters[p.ID]
	if !ok || f.UserID != p.UserID {
		return store.Filter{}, store.ErrNotFound
	}
	f.Section = p.Section
	f.TeacherUID = p.TeacherUID
	f.DayOfWeek = p.DayOfWeek
	f.TimeFrom = p.TimeFrom
	f.TimeTo = p.TimeTo
	f.MinRating = p.MinRating
	f.Enabled = p.Enabled
	f.UpdatedAt = time.Now().UTC()
	m.filters[p.ID] = f
	return f, nil
}

func (m *mockStore) DeleteFilter(_ context.Context, id, userID int64) (int64, error) {
	if f, ok := m.filters[id]; ok && f.UserID == userID {
		delete(m.filters, id)
		return 1, nil
	}
	return 0, nil
}

func (m *mockStore) GetKnownSlotsByUser(_ context.Context, userID int64) (map[string]struct{}, error) {
	if m.failKnown {
		return nil, errors.New("forced")
	}
	if k, ok := m.known[userID]; ok {
		out := make(map[string]struct{}, len(k))
		for id := range k {
			out[id] = struct{}{}
		}
		return out, nil
	}
	return map[string]struct{}{}, nil
}

func (m *mockStore) InsertKnownSlots(_ context.Context, userID int64, slotIDs []string) error {
	if _, ok := m.known[userID]; !ok {
		m.known[userID] = map[string]struct{}{}
	}
	for _, id := range slotIDs {
		m.known[userID][id] = struct{}{}
	}
	return nil
}

func (m *mockStore) ResetKnownSlots(_ context.Context, userID int64) (int64, error) {
	n := int64(len(m.known[userID]))
	delete(m.known, userID)
	return n, nil
}

func (m *mockStore) InsertAlertLog(_ context.Context, p store.InsertAlertLogParams) (store.AlertLog, error) {
	a := store.AlertLog{
		ID:      int64(len(m.alertLog) + 1),
		UserID:  p.UserID,
		SlotID:  p.SlotID,
		Channel: p.Channel,
		SentAt:  time.Now().UTC(),
		Payload: p.Payload,
	}
	m.alertLog = append(m.alertLog, a)
	return a, nil
}

// -------------- tests --------------

func TestCreateFilter_BadUserID(t *testing.T) {
	svc := service.New(newMockStore())
	_, err := svc.CreateFilter(context.Background(), &filterv1.CreateFilterRequest{UserId: ""})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("want InvalidArgument, got %v", err)
	}
}

func TestCreateFilter_BadRating(t *testing.T) {
	svc := service.New(newMockStore())
	r := 6.0
	_, err := svc.CreateFilter(context.Background(), &filterv1.CreateFilterRequest{
		UserId:    "1",
		MinRating: &r,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("want InvalidArgument, got %v", err)
	}
}

func TestCreateFilter_BadTimeRange(t *testing.T) {
	svc := service.New(newMockStore())
	from := "10:00"
	to := "09:00"
	_, err := svc.CreateFilter(context.Background(), &filterv1.CreateFilterRequest{
		UserId:   "1",
		TimeFrom: &from,
		TimeTo:   &to,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("want InvalidArgument, got %v", err)
	}
}

func TestCreateFilter_OK(t *testing.T) {
	m := newMockStore()
	svc := service.New(m)
	sec := "Аэробика"
	from := "08:00"
	to := "10:00"
	resp, err := svc.CreateFilter(context.Background(), &filterv1.CreateFilterRequest{
		UserId:    "42",
		Section:   &sec,
		DayOfWeek: commonv1.DayOfWeek_DAY_OF_WEEK_MONDAY,
		TimeFrom:  &from,
		TimeTo:    &to,
		Enabled:   true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.GetFilter().GetUserId() != "42" {
		t.Fatalf("user_id mismatch: %v", resp.GetFilter().GetUserId())
	}
	if !resp.GetFilter().GetEnabled() {
		t.Fatal("filter not enabled")
	}
}

func TestGetFilter_NotFound(t *testing.T) {
	svc := service.New(newMockStore())
	_, err := svc.GetFilter(context.Background(), &filterv1.GetFilterRequest{UserId: "1", FilterId: "999"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("want NotFound, got %v", err)
	}
}

func TestGetFilter_PermissionDenied(t *testing.T) {
	m := newMockStore()
	m.filters[1] = store.Filter{ID: 1, UserID: 42, Enabled: true}
	svc := service.New(m)
	_, err := svc.GetFilter(context.Background(), &filterv1.GetFilterRequest{UserId: "100", FilterId: "1"})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("want PermissionDenied, got %v", err)
	}
}

func TestDeleteFilter_Idempotent(t *testing.T) {
	svc := service.New(newMockStore())
	for i := 0; i < 2; i++ {
		_, err := svc.DeleteFilter(context.Background(), &filterv1.DeleteFilterRequest{UserId: "1", FilterId: "1"})
		if err != nil {
			t.Fatalf("delete %d: %v", i, err)
		}
	}
}

func TestMatchSlots_NoFilters(t *testing.T) {
	svc := service.New(newMockStore())
	resp, err := svc.MatchSlots(context.Background(), &filterv1.MatchSlotsRequest{
		UserId: "1",
		Slots:  []*commonv1.Slot{{Id: "s1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.GetMatched()) != 0 {
		t.Fatalf("expected empty matched, got %d", len(resp.GetMatched()))
	}
}

func TestMatchSlots_FilterMatches(t *testing.T) {
	m := newMockStore()
	sec := "Аэробика"
	m.filters[1] = store.Filter{
		ID: 1, UserID: 1, Enabled: true, MinVacancy: 1,
		Section: &sec,
	}
	svc := service.New(m)
	resp, err := svc.MatchSlots(context.Background(), &filterv1.MatchSlotsRequest{
		UserId: "1",
		Slots: []*commonv1.Slot{
			{
				Id:        "slot-1",
				Section:   ptrStr("Аэробика"),
				Vacancy:   2,
				Time:      "08:00-09:30",
				DayOfWeek: commonv1.DayOfWeek_DAY_OF_WEEK_MONDAY,
			},
			{Id: "slot-2", Section: ptrStr("Другое"), Vacancy: 2},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.GetMatched()) != 1 || resp.GetMatched()[0].GetSlot().GetId() != "slot-1" {
		t.Fatalf("unexpected matched: %#v", resp.GetMatched())
	}
	if !resp.GetMatched()[0].GetIsNew() {
		t.Errorf("expected IsNew=true")
	}
}

func TestMatchSlots_KnownDedup(t *testing.T) {
	m := newMockStore()
	sec := "Аэробика"
	m.filters[1] = store.Filter{ID: 1, UserID: 1, Enabled: true, Section: &sec, MinVacancy: 1}
	m.known[1] = map[string]struct{}{"slot-1": {}}
	svc := service.New(m)
	resp, _ := svc.MatchSlots(context.Background(), &filterv1.MatchSlotsRequest{
		UserId: "1",
		Slots: []*commonv1.Slot{
			{Id: "slot-1", Section: ptrStr("Аэробика"), Vacancy: 2},
		},
	})
	if len(resp.GetMatched()) != 1 {
		t.Fatalf("expected one match, got %d", len(resp.GetMatched()))
	}
	if resp.GetMatched()[0].GetIsNew() {
		t.Error("known slot must have IsNew=false")
	}
}

func TestMarkSeen_InsertsKnownAndAlertLog(t *testing.T) {
	m := newMockStore()
	svc := service.New(m)
	_, err := svc.MarkSeen(context.Background(), &filterv1.MarkSeenRequest{
		UserId:  "1",
		SlotIds: []string{"s1", "s2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.known[1]; !ok {
		t.Fatal("known not inserted")
	}
	if len(m.known[1]) != 2 {
		t.Errorf("expected 2 known, got %d", len(m.known[1]))
	}
	if len(m.alertLog) != 2 {
		t.Errorf("expected 2 alert log entries, got %d", len(m.alertLog))
	}
}

func TestMarkSeen_EmptyOK(t *testing.T) {
	m := newMockStore()
	svc := service.New(m)
	_, err := svc.MarkSeen(context.Background(), &filterv1.MarkSeenRequest{UserId: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.alertLog) != 0 {
		t.Errorf("no slots → no alert log; got %d", len(m.alertLog))
	}
}

func TestResetKnown_ClearsCounter(t *testing.T) {
	m := newMockStore()
	m.known[1] = map[string]struct{}{"a": {}, "b": {}}
	svc := service.New(m)
	resp, err := svc.ResetKnown(context.Background(), &filterv1.ResetKnownRequest{UserId: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetClearedCount() != 2 {
		t.Errorf("expected cleared=2, got %d", resp.GetClearedCount())
	}
	if len(m.known[1]) != 0 {
		t.Errorf("known still present: %v", m.known[1])
	}
}

func TestUpdateFilter_AppliesMask(t *testing.T) {
	m := newMockStore()
	oldSec := "old"
	m.filters[1] = store.Filter{ID: 1, UserID: 7, Section: &oldSec, Enabled: true}
	svc := service.New(m)
	newSec := "new"
	resp, err := svc.UpdateFilter(context.Background(), &filterv1.UpdateFilterRequest{
		UserId:     "7",
		FilterId:   "1",
		Section:    &newSec,
		UpdateMask: []string{"section"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetFilter().GetSection() != "new" {
		t.Errorf("section not updated: %v", resp.GetFilter().GetSection())
	}
	if !resp.GetFilter().GetEnabled() {
		t.Errorf("enabled wrongly changed by partial mask")
	}
}

func ptrStr(s string) *string { return &s }
