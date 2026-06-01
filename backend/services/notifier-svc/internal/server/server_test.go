package server

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc"

	authv1 "github.com/fizcultor/backend/gen/auth/v1"
	commonv1 "github.com/fizcultor/backend/gen/common/v1"
	notifierv1 "github.com/fizcultor/backend/gen/notifier/v1"
	teachersv1 "github.com/fizcultor/backend/gen/teachers/v1"
)

// -------- mocks --------

type mockAuth struct {
	user     *commonv1.User
	getErr   error
	linkErr  error
	linkResp *authv1.LinkTelegramCompleteResponse
}

func (m *mockAuth) GetMe(_ context.Context, _ *authv1.GetMeRequest, _ ...grpc.CallOption) (*commonv1.User, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	return m.user, nil
}

func (m *mockAuth) LinkTelegramComplete(_ context.Context, _ *authv1.LinkTelegramCompleteRequest, _ ...grpc.CallOption) (*authv1.LinkTelegramCompleteResponse, error) {
	if m.linkErr != nil {
		return nil, m.linkErr
	}
	return m.linkResp, nil
}

type mockTeachers struct {
	teachers []*teachersv1.Teacher
	err      error
}

func (m *mockTeachers) BatchGet(_ context.Context, _ *teachersv1.BatchGetRequest, _ ...grpc.CallOption) (*teachersv1.BatchGetResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &teachersv1.BatchGetResponse{Teachers: m.teachers}, nil
}

type mockSender struct {
	mu       sync.Mutex
	messages []sentMessage
	err      error
}

type sentMessage struct {
	ChatID int64
	Text   string
}

func (m *mockSender) SendHTML(chatID int64, text string) error {
	if m.err != nil {
		return m.err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, sentMessage{ChatID: chatID, Text: text})
	return nil
}

type mockPub struct {
	mu       sync.Mutex
	messages []natsMessage
	err      error
}

type natsMessage struct {
	Subject string
	Data    []byte
}

func (m *mockPub) Publish(subject string, data []byte) error {
	if m.err != nil {
		return m.err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, natsMessage{Subject: subject, Data: append([]byte(nil), data...)})
	return nil
}

// -------- fixtures --------

func newTestServer(t *testing.T, auth AuthClient, teachers TeachersClient, sender *mockSender, pub *mockPub) *Server {
	t.Helper()
	s, err := New(Deps{Auth: auth, Teachers: teachers, Sender: sender, Publisher: pub, Logger: slog.Default()})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	return s
}

func userWithChat(id string, chat int64) *commonv1.User {
	return &commonv1.User{Id: id, TelegramChatId: &chat}
}

func matchedSlot(id, section, teacherUID string) *commonv1.MatchedSlot {
	sec := section
	uid := teacherUID
	return &commonv1.MatchedSlot{
		Slot: &commonv1.Slot{
			Id:          id,
			Week:        1,
			Time:        "10:00-11:30",
			Section:     &sec,
			Place:       "Зал",
			TeacherName: "Иванов",
			TeacherUid:  &uid,
			Vacancy:     2,
		},
		MatchedFilterIds: []string{"f-1"},
		IsNew:            true,
	}
}

// -------- tests --------

func TestServer_NotifyMatched_HappyPath(t *testing.T) {
	auth := &mockAuth{user: userWithChat("u-1", 1234567890)}
	teachers := &mockTeachers{teachers: []*teachersv1.Teacher{
		{Uid: "T-1", FullName: "Иванов", Rating: 4.7},
	}}
	sender := &mockSender{}
	pub := &mockPub{}

	s := newTestServer(t, auth, teachers, sender, pub)
	resp, err := s.NotifyMatched(context.Background(), &notifierv1.NotifyMatchedRequest{
		UserId:  "u-1",
		Matched: []*commonv1.MatchedSlot{matchedSlot("s-1", "Аэробика", "T-1")},
	})
	if err != nil {
		t.Fatalf("NotifyMatched: %v", err)
	}
	if len(resp.GetDeliveredBy()) != 2 {
		t.Fatalf("ожидаем 2 канала доставки (TG+SSE), получили %d", len(resp.GetDeliveredBy()))
	}
	if len(sender.messages) != 1 {
		t.Fatalf("ожидаем 1 TG-сообщение, получили %d", len(sender.messages))
	}
	if !strings.Contains(sender.messages[0].Text, "Аэробика") {
		t.Errorf("в TG нет секции, got=%q", sender.messages[0].Text)
	}
	if !strings.Contains(sender.messages[0].Text, "4.7") {
		t.Errorf("в TG нет рейтинга 4.7")
	}
	if len(pub.messages) != 1 {
		t.Fatalf("ожидаем 1 NATS-сообщение, получили %d", len(pub.messages))
	}
	if pub.messages[0].Subject != "alerts.u-1" {
		t.Errorf("неверный subject: %s", pub.messages[0].Subject)
	}
}

func TestServer_NotifyMatched_EmptyMatchedIsNoop(t *testing.T) {
	auth := &mockAuth{user: userWithChat("u-1", 42)}
	teachers := &mockTeachers{}
	sender := &mockSender{}
	pub := &mockPub{}

	s := newTestServer(t, auth, teachers, sender, pub)
	resp, err := s.NotifyMatched(context.Background(), &notifierv1.NotifyMatchedRequest{UserId: "u-1"})
	if err != nil {
		t.Fatalf("NotifyMatched: %v", err)
	}
	if len(resp.GetDeliveredBy()) != 0 || len(resp.GetFailedBy()) != 0 {
		t.Errorf("ожидаем пустой ответ для empty matched, got=%+v", resp)
	}
	if len(sender.messages) != 0 || len(pub.messages) != 0 {
		t.Error("не должно быть отправок для empty matched")
	}
}

func TestServer_NotifyMatched_MissingUserID(t *testing.T) {
	s := newTestServer(t, &mockAuth{}, &mockTeachers{}, &mockSender{}, &mockPub{})
	_, err := s.NotifyMatched(context.Background(), &notifierv1.NotifyMatchedRequest{
		Matched: []*commonv1.MatchedSlot{matchedSlot("s-1", "S", "T-1")},
	})
	if err == nil {
		t.Fatal("ожидаем ошибку при пустом user_id")
	}
}

func TestServer_NotifyMatched_TelegramFails(t *testing.T) {
	auth := &mockAuth{user: userWithChat("u-1", 42)}
	sender := &mockSender{err: errors.New("tg fail")}
	pub := &mockPub{}

	s := newTestServer(t, auth, &mockTeachers{}, sender, pub)
	resp, err := s.NotifyMatched(context.Background(), &notifierv1.NotifyMatchedRequest{
		UserId:  "u-1",
		Matched: []*commonv1.MatchedSlot{matchedSlot("s-1", "S", "T-1")},
	})
	if err != nil {
		t.Fatalf("NotifyMatched: %v", err)
	}
	// TG зафейлен, SSE доставлен.
	if len(resp.GetFailedBy()) != 1 || resp.GetFailedBy()[0] != commonv1.AlertChannel_ALERT_CHANNEL_TELEGRAM {
		t.Errorf("ожидаем TG в FailedBy, got=%v", resp.GetFailedBy())
	}
	if len(resp.GetDeliveredBy()) != 1 || resp.GetDeliveredBy()[0] != commonv1.AlertChannel_ALERT_CHANNEL_SSE {
		t.Errorf("ожидаем SSE в DeliveredBy, got=%v", resp.GetDeliveredBy())
	}
}

func TestServer_NotifyMatched_NoTelegramLink(t *testing.T) {
	// telegram_chat_id отсутствует.
	auth := &mockAuth{user: &commonv1.User{Id: "u-1"}}
	sender := &mockSender{}
	pub := &mockPub{}

	s := newTestServer(t, auth, &mockTeachers{}, sender, pub)
	resp, err := s.NotifyMatched(context.Background(), &notifierv1.NotifyMatchedRequest{
		UserId:  "u-1",
		Matched: []*commonv1.MatchedSlot{matchedSlot("s-1", "S", "T-1")},
		Channels: []commonv1.AlertChannel{
			commonv1.AlertChannel_ALERT_CHANNEL_TELEGRAM,
		},
	})
	if err != nil {
		t.Fatalf("NotifyMatched: %v", err)
	}
	if len(resp.GetFailedBy()) != 1 {
		t.Fatalf("ожидаем 1 fail, got=%v", resp.GetFailedBy())
	}
	if len(sender.messages) != 0 {
		t.Error("не должно отправляться без chat_id")
	}
}

func TestServer_NotifyMatched_AuthFails(t *testing.T) {
	auth := &mockAuth{getErr: errors.New("auth down")}
	s := newTestServer(t, auth, &mockTeachers{}, &mockSender{}, &mockPub{})
	_, err := s.NotifyMatched(context.Background(), &notifierv1.NotifyMatchedRequest{
		UserId:  "u-1",
		Matched: []*commonv1.MatchedSlot{matchedSlot("s-1", "S", "T-1")},
	})
	if err == nil {
		t.Fatal("ожидаем ошибку при auth.GetMe fail")
	}
}

func TestServer_NotifyMatched_TeachersFallbackToMatchedRating(t *testing.T) {
	auth := &mockAuth{user: userWithChat("u-1", 42)}
	// teachers вернул ошибку — должен сработать fallback на m.TeacherRating.
	teachers := &mockTeachers{err: errors.New("teachers down")}
	sender := &mockSender{}
	pub := &mockPub{}

	ms := matchedSlot("s-1", "S", "T-1")
	r := 3.5
	ms.TeacherRating = &r

	s := newTestServer(t, auth, teachers, sender, pub)
	resp, err := s.NotifyMatched(context.Background(), &notifierv1.NotifyMatchedRequest{
		UserId: "u-1", Matched: []*commonv1.MatchedSlot{ms},
	})
	if err != nil {
		t.Fatalf("NotifyMatched: %v", err)
	}
	if len(resp.GetDeliveredBy()) != 2 {
		t.Errorf("ожидаем 2 канала, got=%v", resp.GetDeliveredBy())
	}
	if len(sender.messages) != 1 || !strings.Contains(sender.messages[0].Text, "3.5") {
		t.Errorf("fallback rating не отрендерился; msg=%q", sender.messages[0].Text)
	}
}

func TestServer_SendDirect(t *testing.T) {
	auth := &mockAuth{user: userWithChat("u-1", 999)}
	sender := &mockSender{}
	pub := &mockPub{}
	s := newTestServer(t, auth, &mockTeachers{}, sender, pub)

	resp, err := s.SendDirect(context.Background(), &notifierv1.SendDirectRequest{
		UserId: "u-1", Text: "Привет",
	})
	if err != nil {
		t.Fatalf("SendDirect: %v", err)
	}
	if len(resp.GetDeliveredBy()) != 1 {
		t.Fatalf("ожидаем 1 канал, got=%v", resp.GetDeliveredBy())
	}
	if len(sender.messages) != 1 || sender.messages[0].Text != "Привет" {
		t.Errorf("ожидаем 1 TG-сообщение Привет, got=%+v", sender.messages)
	}
}

func TestServer_SendDirect_EmptyText(t *testing.T) {
	s := newTestServer(t, &mockAuth{}, &mockTeachers{}, &mockSender{}, &mockPub{})
	_, err := s.SendDirect(context.Background(), &notifierv1.SendDirectRequest{UserId: "u-1"})
	if err == nil {
		t.Fatal("ожидаем ошибку для пустого text")
	}
}

func TestServer_RegisterTelegramChat(t *testing.T) {
	auth := &mockAuth{linkResp: &authv1.LinkTelegramCompleteResponse{UserId: "u-1"}}
	sender := &mockSender{}
	s := newTestServer(t, auth, &mockTeachers{}, sender, &mockPub{})

	resp, err := s.RegisterTelegramChat(context.Background(), &notifierv1.RegisterTelegramChatRequest{
		Code: "code-1", TelegramChatId: 42,
	})
	if err != nil {
		t.Fatalf("RegisterTelegramChat: %v", err)
	}
	if resp.GetUserId() != "u-1" {
		t.Errorf("user_id mismatch: %s", resp.GetUserId())
	}
	if !resp.GetWelcomeSent() {
		t.Error("welcome не отправился")
	}
}

func TestServer_RegisterTelegramChat_AuthFails(t *testing.T) {
	auth := &mockAuth{linkErr: errors.New("expired")}
	s := newTestServer(t, auth, &mockTeachers{}, &mockSender{}, &mockPub{})
	_, err := s.RegisterTelegramChat(context.Background(), &notifierv1.RegisterTelegramChatRequest{
		Code: "code-1", TelegramChatId: 42,
	})
	if err == nil {
		t.Fatal("ожидаем NOT_FOUND")
	}
}

func TestServer_Complete_BotLinker(t *testing.T) {
	auth := &mockAuth{linkResp: &authv1.LinkTelegramCompleteResponse{UserId: "u-1"}}
	s := newTestServer(t, auth, &mockTeachers{}, &mockSender{}, &mockPub{})

	uid, err := s.Complete(context.Background(), "tok", 12345)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if uid != "u-1" {
		t.Errorf("uid mismatch: %s", uid)
	}
}

func TestSubjectAlerts_SanitizesUserID(t *testing.T) {
	got := subjectAlerts("user.with.dots")
	if !strings.HasPrefix(got, "alerts.") {
		t.Fatal("missing prefix")
	}
	if strings.Count(got, ".") != 1 {
		t.Errorf("ожидаем 1 точку в %q", got)
	}
}

func TestUniqueTeacherUIDs(t *testing.T) {
	matched := []*commonv1.MatchedSlot{
		matchedSlot("s-1", "S", "T-1"),
		matchedSlot("s-2", "S", "T-2"),
		matchedSlot("s-3", "S", "T-1"), // dup
	}
	got := uniqueTeacherUIDs(matched)
	if len(got) != 2 {
		t.Fatalf("ожидаем 2 уникальных uid, got=%v", got)
	}
}
