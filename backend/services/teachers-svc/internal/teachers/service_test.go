package teachers_test

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/fizcultor/backend/gen/common/v1"
	teachersv1 "github.com/fizcultor/backend/gen/teachers/v1"
	"github.com/fizcultor/backend/services/teachers-svc/internal/store"
	"github.com/fizcultor/backend/services/teachers-svc/internal/teachers"
)

// mockStore — in-memory реализация teachers.Store.
type mockStore struct {
	byUID map[string]store.Teacher
	all   []store.Teacher
}

func newMockStore() *mockStore {
	return &mockStore{
		byUID: map[string]store.Teacher{},
	}
}

func (m *mockStore) GetByUID(_ context.Context, uid string) (store.Teacher, error) {
	t, ok := m.byUID[uid]
	if !ok {
		return store.Teacher{}, store.ErrNotFound
	}
	return t, nil
}

func (m *mockStore) BatchGet(_ context.Context, uids []string) ([]store.Teacher, error) {
	var out []store.Teacher
	for _, u := range uids {
		if t, ok := m.byUID[u]; ok {
			out = append(out, t)
		}
	}
	return out, nil
}

func (m *mockStore) List(_ context.Context, p store.ListParams) ([]store.Teacher, error) {
	limit := int(p.Limit)
	if limit <= 0 {
		limit = 50
	}
	offset := int(p.Offset)
	from := offset
	if from > len(m.all) {
		from = len(m.all)
	}
	to := from + limit
	if to > len(m.all) {
		to = len(m.all)
	}
	return m.all[from:to], nil
}

func (m *mockStore) Count(_ context.Context) (int64, error) {
	return int64(len(m.byUID)), nil
}

func (m *mockStore) Upsert(_ context.Context, p store.UpsertParams) (store.UpsertResult, error) {
	_, existed := m.byUID[p.UID]
	t := store.Teacher{
		UID:            p.UID,
		Name:           p.Name,
		NameNormalized: p.NameNormalized,
		Rating:         p.Rating,
		SourceURL:      p.SourceURL,
		ImportedAt:     time.Now().UTC(),
	}
	if !existed {
		m.all = append(m.all, t)
	}
	m.byUID[p.UID] = t
	return store.UpsertResult{Teacher: t, Inserted: !existed}, nil
}

// --- tests ---

func TestGet_NotFound(t *testing.T) {
	svc := teachers.New(newMockStore())
	_, err := svc.Get(context.Background(), &teachersv1.GetRequest{Uid: "missing"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("want NotFound, got %v", err)
	}
}

func TestGet_OK(t *testing.T) {
	m := newMockStore()
	r := 4.5
	m.byUID["u1"] = store.Teacher{UID: "u1", Name: "Иванов И.И.", NameNormalized: "иванов и.и.", Rating: &r}
	svc := teachers.New(m)
	resp, err := svc.Get(context.Background(), &teachersv1.GetRequest{Uid: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetTeacher().GetRating() != 4.5 {
		t.Errorf("rating mismatch: %v", resp.GetTeacher().GetRating())
	}
}

func TestGet_EmptyUID(t *testing.T) {
	svc := teachers.New(newMockStore())
	_, err := svc.Get(context.Background(), &teachersv1.GetRequest{Uid: ""})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("want InvalidArgument, got %v", err)
	}
}

func TestBatchGet_DedupAndFilter(t *testing.T) {
	m := newMockStore()
	m.byUID["u1"] = store.Teacher{UID: "u1", Name: "A"}
	m.byUID["u2"] = store.Teacher{UID: "u2", Name: "B"}
	svc := teachers.New(m)
	resp, err := svc.BatchGet(context.Background(), &teachersv1.BatchGetRequest{
		Uids: []string{"u1", "u1", "  ", "missing", "u2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.GetTeachers()) != 2 {
		t.Fatalf("expected 2, got %d", len(resp.GetTeachers()))
	}
}

func TestBatchGet_Empty(t *testing.T) {
	svc := teachers.New(newMockStore())
	resp, err := svc.BatchGet(context.Background(), &teachersv1.BatchGetRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.GetTeachers()) != 0 {
		t.Fatalf("expected empty")
	}
}

func TestList_Pagination(t *testing.T) {
	m := newMockStore()
	for i := 0; i < 5; i++ {
		_, _ = m.Upsert(context.Background(), store.UpsertParams{
			UID: string(rune('a' + i)), Name: "t", NameNormalized: "t",
		})
	}
	svc := teachers.New(m)
	resp, err := svc.List(context.Background(), &teachersv1.ListRequest{
		Page: &commonv1.PageRequest{PageSize: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.GetTeachers()) != 3 {
		t.Fatalf("expected 3, got %d", len(resp.GetTeachers()))
	}
	if resp.GetPage().GetNextPageToken() != "3" {
		t.Errorf("expected next page token '3', got %q", resp.GetPage().GetNextPageToken())
	}
}

func TestList_NoPage(t *testing.T) {
	m := newMockStore()
	for i := 0; i < 3; i++ {
		_, _ = m.Upsert(context.Background(), store.UpsertParams{
			UID: string(rune('a' + i)), Name: "t",
		})
	}
	svc := teachers.New(m)
	resp, err := svc.List(context.Background(), &teachersv1.ListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.GetTeachers()) != 3 {
		t.Errorf("expected 3, got %d", len(resp.GetTeachers()))
	}
	// 3 < 50 → нет next_page_token
	if resp.GetPage().GetNextPageToken() != "" {
		t.Errorf("expected empty next, got %q", resp.GetPage().GetNextPageToken())
	}
}

func TestRefresh_InlineJSON(t *testing.T) {
	m := newMockStore()
	svc := teachers.New(m)
	inline := `{"тестовый учитель":{"rating":"4.5"}}`
	resp, err := svc.Refresh(context.Background(), &teachersv1.RefreshRequest{InlineJson: &inline})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetTotal() != 1 || resp.GetInserted() != 1 {
		t.Errorf("stats: total=%d inserted=%d", resp.GetTotal(), resp.GetInserted())
	}
}

func TestRefresh_Embedded(t *testing.T) {
	m := newMockStore()
	svc := teachers.New(m)
	resp, err := svc.Refresh(context.Background(), &teachersv1.RefreshRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetTotal() <= 100 {
		t.Errorf("expected >100 records, got %d", resp.GetTotal())
	}
	// Повторный Refresh: всё update (insert == 0).
	resp2, err := svc.Refresh(context.Background(), &teachersv1.RefreshRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp2.GetInserted() != 0 {
		t.Errorf("second refresh: inserted should be 0, got %d", resp2.GetInserted())
	}
}

func TestBootstrap_SkipsIfNonEmpty(t *testing.T) {
	m := newMockStore()
	_, _ = m.Upsert(context.Background(), store.UpsertParams{UID: "x", Name: "x", NameNormalized: "x"})
	if err := teachers.Bootstrap(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	// После bootstrap должно остаться 1 (не залилось 200+).
	if len(m.byUID) != 1 {
		t.Errorf("expected 1, got %d", len(m.byUID))
	}
}

func TestBootstrap_ImportsIfEmpty(t *testing.T) {
	m := newMockStore()
	if err := teachers.Bootstrap(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if len(m.byUID) < 100 {
		t.Errorf("expected at least 100 imported, got %d", len(m.byUID))
	}
}
