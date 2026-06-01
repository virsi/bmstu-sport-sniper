package orchestrator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	authv1 "github.com/fizcultor/backend/gen/auth/v1"
	bmstuv1 "github.com/fizcultor/backend/gen/bmstu/v1"
	commonv1 "github.com/fizcultor/backend/gen/common/v1"
	filterv1 "github.com/fizcultor/backend/gen/filter/v1"
	notifierv1 "github.com/fizcultor/backend/gen/notifier/v1"
)

// ---------- mocks ----------

type mockAuth struct{}

func (m *mockAuth) GetMe(_ context.Context, _ *authv1.GetMeRequest, _ ...grpc.CallOption) (*commonv1.User, error) {
	return &commonv1.User{Id: "u"}, nil
}

type mockBmstu struct {
	mu    sync.Mutex
	calls int
	slots []*commonv1.Slot
	err   error
}

func (m *mockBmstu) FetchGroups(_ context.Context, _ *bmstuv1.FetchGroupsRequest, _ ...grpc.CallOption) (*bmstuv1.FetchGroupsResponse, error) {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	return &bmstuv1.FetchGroupsResponse{Slots: m.slots}, nil
}

type mockFilter struct {
	mu            sync.Mutex
	matchCalls    int
	markSeenCalls int
	markSeenIDs   []string
	matched       []*commonv1.MatchedSlot
	matchErr      error
	markSeenErr   error
}

func (m *mockFilter) MatchSlots(_ context.Context, _ *filterv1.MatchSlotsRequest, _ ...grpc.CallOption) (*filterv1.MatchSlotsResponse, error) {
	m.mu.Lock()
	m.matchCalls++
	m.mu.Unlock()
	if m.matchErr != nil {
		return nil, m.matchErr
	}
	return &filterv1.MatchSlotsResponse{Matched: m.matched}, nil
}

func (m *mockFilter) MarkSeen(_ context.Context, in *filterv1.MarkSeenRequest, _ ...grpc.CallOption) (*filterv1.MarkSeenResponse, error) {
	m.mu.Lock()
	m.markSeenCalls++
	m.markSeenIDs = append(m.markSeenIDs, in.GetSlotIds()...)
	m.mu.Unlock()
	if m.markSeenErr != nil {
		return nil, m.markSeenErr
	}
	return &filterv1.MarkSeenResponse{}, nil
}

type mockNotifier struct {
	mu                 sync.Mutex
	notifyCalls        int
	sendDirectCalls    int
	notifyErr          error
	notifyDeliveredBy  []commonv1.AlertChannel
	notifyFailedBy     []commonv1.AlertChannel
	sendDirectErr      error
	sendDirectMessages []string
}

func (m *mockNotifier) NotifyMatched(_ context.Context, _ *notifierv1.NotifyMatchedRequest, _ ...grpc.CallOption) (*notifierv1.NotifyMatchedResponse, error) {
	m.mu.Lock()
	m.notifyCalls++
	m.mu.Unlock()
	if m.notifyErr != nil {
		return nil, m.notifyErr
	}
	delivered := m.notifyDeliveredBy
	if delivered == nil {
		delivered = []commonv1.AlertChannel{commonv1.AlertChannel_ALERT_CHANNEL_TELEGRAM}
	}
	return &notifierv1.NotifyMatchedResponse{
		DeliveredBy: delivered, FailedBy: m.notifyFailedBy,
	}, nil
}

func (m *mockNotifier) SendDirect(_ context.Context, in *notifierv1.SendDirectRequest, _ ...grpc.CallOption) (*notifierv1.SendDirectResponse, error) {
	m.mu.Lock()
	m.sendDirectCalls++
	m.sendDirectMessages = append(m.sendDirectMessages, in.GetText())
	m.mu.Unlock()
	if m.sendDirectErr != nil {
		return nil, m.sendDirectErr
	}
	return &notifierv1.SendDirectResponse{}, nil
}

type staticUsers struct{ ids []string }

func (s staticUsers) List(_ context.Context) ([]string, error) { return s.ids, nil }

// ---------- helpers ----------

func newSlot(id string) *commonv1.Slot {
	return &commonv1.Slot{Id: id, Week: 1, Time: "10:00-11:30", Place: "Зал", TeacherName: "T"}
}

func newMatched(id string, isNew bool) *commonv1.MatchedSlot {
	return &commonv1.MatchedSlot{Slot: newSlot(id), IsNew: isNew}
}

func newTestOrchestrator(t *testing.T, deps Deps) *Orchestrator {
	t.Helper()
	if deps.Logger == nil {
		deps.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	o, err := New(Config{
		PollInterval: time.Hour, // тикер не сработает в тесте; вызываем runCycle вручную
		Concurrency:  4,
	}, deps)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return o
}

// ---------- tests ----------

func TestOrchestrator_HappyPath(t *testing.T) {
	bmstu := &mockBmstu{slots: []*commonv1.Slot{newSlot("s-1"), newSlot("s-2")}}
	filter := &mockFilter{matched: []*commonv1.MatchedSlot{
		newMatched("s-1", true),
		newMatched("s-2", false),
	}}
	notifier := &mockNotifier{}
	users := staticUsers{ids: []string{"u-1"}}

	o := newTestOrchestrator(t, Deps{
		Auth: &mockAuth{}, Bmstu: bmstu, Filter: filter,
		Notifier: notifier, Users: users,
	})
	o.runCycle(context.Background())

	if bmstu.calls != 1 {
		t.Errorf("bmstu calls: %d", bmstu.calls)
	}
	if filter.matchCalls != 1 {
		t.Errorf("match calls: %d", filter.matchCalls)
	}
	if notifier.notifyCalls != 1 {
		t.Errorf("notify calls: %d", notifier.notifyCalls)
	}
	if filter.markSeenCalls != 1 {
		t.Errorf("markSeen calls: %d", filter.markSeenCalls)
	}
	if len(filter.markSeenIDs) != 1 || filter.markSeenIDs[0] != "s-1" {
		t.Errorf("ожидаем MarkSeen только по s-1 (is_new=true), got=%v", filter.markSeenIDs)
	}
}

func TestOrchestrator_BmstuFails_NoMarkSeen(t *testing.T) {
	bmstu := &mockBmstu{err: errors.New("upstream down")}
	filter := &mockFilter{}
	notifier := &mockNotifier{}

	o := newTestOrchestrator(t, Deps{
		Auth: &mockAuth{}, Bmstu: bmstu, Filter: filter,
		Notifier: notifier, Users: staticUsers{ids: []string{"u-1"}},
	})
	o.runCycle(context.Background())

	if filter.matchCalls != 0 || filter.markSeenCalls != 0 {
		t.Errorf("filter не должен вызываться при bmstu fail; match=%d, mark=%d",
			filter.matchCalls, filter.markSeenCalls)
	}
	if notifier.notifyCalls != 0 {
		t.Error("notifier не должен вызываться при bmstu fail")
	}
}

func TestOrchestrator_NotifierFails_NoMarkSeen(t *testing.T) {
	bmstu := &mockBmstu{slots: []*commonv1.Slot{newSlot("s-1")}}
	filter := &mockFilter{matched: []*commonv1.MatchedSlot{newMatched("s-1", true)}}
	notifier := &mockNotifier{notifyErr: errors.New("notifier fail")}

	o := newTestOrchestrator(t, Deps{
		Auth: &mockAuth{}, Bmstu: bmstu, Filter: filter,
		Notifier: notifier, Users: staticUsers{ids: []string{"u-1"}},
	})
	o.runCycle(context.Background())

	if filter.markSeenCalls != 0 {
		t.Errorf("MarkSeen НЕ должен вызываться при notifier fail; calls=%d", filter.markSeenCalls)
	}
}

func TestOrchestrator_NotifierZeroDelivered_NoMarkSeen(t *testing.T) {
	// notifier вернул OK, но ничего не доставил (все каналы failed).
	bmstu := &mockBmstu{slots: []*commonv1.Slot{newSlot("s-1")}}
	filter := &mockFilter{matched: []*commonv1.MatchedSlot{newMatched("s-1", true)}}
	notifier := &mockNotifier{
		notifyDeliveredBy: []commonv1.AlertChannel{}, // явно пусто
		notifyFailedBy:    []commonv1.AlertChannel{commonv1.AlertChannel_ALERT_CHANNEL_TELEGRAM},
	}

	o := newTestOrchestrator(t, Deps{
		Auth: &mockAuth{}, Bmstu: bmstu, Filter: filter,
		Notifier: notifier, Users: staticUsers{ids: []string{"u-1"}},
	})
	o.runCycle(context.Background())

	if filter.markSeenCalls != 0 {
		t.Errorf("MarkSeen не должен вызываться при пустом delivered_by; calls=%d", filter.markSeenCalls)
	}
}

func TestOrchestrator_EmptyMatched_NoNotify(t *testing.T) {
	bmstu := &mockBmstu{slots: []*commonv1.Slot{newSlot("s-1")}}
	filter := &mockFilter{matched: []*commonv1.MatchedSlot{
		newMatched("s-1", false), // не is_new
	}}
	notifier := &mockNotifier{}

	o := newTestOrchestrator(t, Deps{
		Auth: &mockAuth{}, Bmstu: bmstu, Filter: filter,
		Notifier: notifier, Users: staticUsers{ids: []string{"u-1"}},
	})
	o.runCycle(context.Background())

	if notifier.notifyCalls != 0 {
		t.Error("notifier не должен вызываться при пустом is_new")
	}
	if filter.markSeenCalls != 0 {
		t.Error("MarkSeen не должен вызываться при пустом is_new")
	}
}

func TestOrchestrator_EmptySlotsFromBmstu_NoFilter(t *testing.T) {
	// LKS вернул 0 слотов — это норма, обработать без вызова filter.
	bmstu := &mockBmstu{slots: nil}
	filter := &mockFilter{}
	notifier := &mockNotifier{}

	o := newTestOrchestrator(t, Deps{
		Auth: &mockAuth{}, Bmstu: bmstu, Filter: filter,
		Notifier: notifier, Users: staticUsers{ids: []string{"u-1"}},
	})
	o.runCycle(context.Background())

	if filter.matchCalls != 0 {
		t.Error("filter.MatchSlots не нужен для пустого ответа LKS")
	}
}

func TestOrchestrator_BmstuFailedPrecondition_SendDirect(t *testing.T) {
	bmstu := &mockBmstu{err: status.Error(codes.FailedPrecondition, "creds invalid")}
	filter := &mockFilter{}
	notifier := &mockNotifier{}

	o := newTestOrchestrator(t, Deps{
		Auth: &mockAuth{}, Bmstu: bmstu, Filter: filter,
		Notifier: notifier, Users: staticUsers{ids: []string{"u-1"}},
	})
	o.runCycle(context.Background())

	if notifier.sendDirectCalls != 1 {
		t.Fatalf("ожидаем SendDirect=1 при FAILED_PRECONDITION, got=%d", notifier.sendDirectCalls)
	}
	if notifier.notifyCalls != 0 {
		t.Errorf("NotifyMatched не должен вызываться, got=%d", notifier.notifyCalls)
	}
	if filter.matchCalls != 0 {
		t.Errorf("MatchSlots не должен вызываться, got=%d", filter.matchCalls)
	}
}

func TestOrchestrator_MarkSeenFails_NotFatal(t *testing.T) {
	// MarkSeen упал, но цикл должен завершиться без ошибки. Дубль на следующем тике.
	bmstu := &mockBmstu{slots: []*commonv1.Slot{newSlot("s-1")}}
	filter := &mockFilter{
		matched:     []*commonv1.MatchedSlot{newMatched("s-1", true)},
		markSeenErr: errors.New("db unavailable"),
	}
	notifier := &mockNotifier{}

	o := newTestOrchestrator(t, Deps{
		Auth: &mockAuth{}, Bmstu: bmstu, Filter: filter,
		Notifier: notifier, Users: staticUsers{ids: []string{"u-1"}},
	})
	// Не должно паниковать.
	o.runCycle(context.Background())

	if filter.markSeenCalls != 1 {
		t.Errorf("ожидаем 1 попытку MarkSeen, got=%d", filter.markSeenCalls)
	}
}

func TestOrchestrator_Concurrency_RespectsSemaphore(t *testing.T) {
	// 20 юзеров, concurrency=4: следим, что параллельно никогда не > 4.
	var maxInflight int32
	mu := sync.Mutex{}
	var inflight int32

	bmstu := &concurrencyBmstu{
		onCall: func() {
			mu.Lock()
			inflight++
			if inflight > maxInflight {
				maxInflight = inflight
			}
			mu.Unlock()
			time.Sleep(20 * time.Millisecond)
			mu.Lock()
			inflight--
			mu.Unlock()
		},
		slots: nil,
	}
	filter := &mockFilter{}
	notifier := &mockNotifier{}
	users := make([]string, 20)
	for i := range users {
		users[i] = "u-" + string(rune('a'+i))
	}

	o, err := New(Config{PollInterval: time.Hour, Concurrency: 4}, Deps{
		Auth: &mockAuth{}, Bmstu: bmstu, Filter: filter,
		Notifier: notifier, Users: staticUsers{ids: users},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	o.runCycle(context.Background())
	if maxInflight > 4 {
		t.Errorf("превышен semaphore: maxInflight=%d", maxInflight)
	}
}

type concurrencyBmstu struct {
	onCall func()
	slots  []*commonv1.Slot
}

func (m *concurrencyBmstu) FetchGroups(_ context.Context, _ *bmstuv1.FetchGroupsRequest, _ ...grpc.CallOption) (*bmstuv1.FetchGroupsResponse, error) {
	m.onCall()
	return &bmstuv1.FetchGroupsResponse{Slots: m.slots}, nil
}

func TestOrchestrator_BackoffSkipsRepeatedFailures(t *testing.T) {
	bmstu := &mockBmstu{err: errors.New("transient")}
	filter := &mockFilter{}
	notifier := &mockNotifier{}

	o, err := New(Config{PollInterval: time.Hour, Concurrency: 4}, Deps{
		Auth: &mockAuth{}, Bmstu: bmstu, Filter: filter,
		Notifier: notifier, Users: staticUsers{ids: []string{"u-1"}},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	o.runCycle(context.Background()) // 1-я ошибка
	o.runCycle(context.Background()) // должен скипнуться по backoff
	if bmstu.calls != 1 {
		t.Errorf("ожидаем 1 вызов bmstu (второй скипнут backoff), got=%d", bmstu.calls)
	}
}

func TestEnvUsers_Parse(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"u1", []string{"u1"}},
		{"u1,u2", []string{"u1", "u2"}},
		{" u1 , u2 ,,  u3 ", []string{"u1", "u2", "u3"}},
	}
	for _, c := range cases {
		got, err := NewEnvUsers(c.in).List(context.Background())
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != len(c.want) {
			t.Errorf("len mismatch for %q: got=%v, want=%v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("idx %d for %q: got=%q, want=%q", i, c.in, got[i], c.want[i])
			}
		}
	}
}
