//go:build integration

// Package integration_test drives notifier-svc end-to-end against a real
// NATS testcontainer.
//
// Scenarios:
//   - NotifyMatched with channel=SSE publishes the right "alerts.<user_id>"
//     payload on NATS.
//   - NotifyMatched with channel=TELEGRAM calls the bot Sender.
//   - Empty matched list is a no-op (OK response, no side effects).
//   - User without linked TG → Telegram channel reported as failed.
//
// Run them with:
//
//	cd backend/services/notifier-svc
//	go test -tags integration ./integration_test/... -v -timeout 120s
package integration_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	authv1 "github.com/fizcultor/backend/gen/auth/v1"
	commonv1 "github.com/fizcultor/backend/gen/common/v1"
	notifierv1 "github.com/fizcultor/backend/gen/notifier/v1"
	teachersv1 "github.com/fizcultor/backend/gen/teachers/v1"
	notifierserver "github.com/fizcultor/backend/services/notifier-svc/internal/server"
	"github.com/fizcultor/backend/tests/testhelpers"
)

// strPtr returns &s. Helper for proto optional-string fields.
func strPtr(s string) *string { return &s }

// fakeAuthClient stubs only the AuthService methods notifier-svc calls.
type fakeAuthClient struct {
	chatID int64
}

func (f *fakeAuthClient) GetMe(_ context.Context, in *authv1.GetMeRequest, _ ...grpc.CallOption) (*commonv1.User, error) {
	u := &commonv1.User{Id: in.GetUserId()}
	if f.chatID != 0 {
		// Mirror gateway behaviour: only set the optional field when linked.
		chat := f.chatID
		u.TelegramChatId = &chat
	}
	return u, nil
}

func (f *fakeAuthClient) LinkTelegramComplete(_ context.Context, _ *authv1.LinkTelegramCompleteRequest, _ ...grpc.CallOption) (*authv1.LinkTelegramCompleteResponse, error) {
	return &authv1.LinkTelegramCompleteResponse{UserId: "user-from-link"}, nil
}

// fakeTeachersClient stubs BatchGet with an empty result — rating lookup is
// optional for these tests.
type fakeTeachersClient struct{}

func (fakeTeachersClient) BatchGet(_ context.Context, _ *teachersv1.BatchGetRequest, _ ...grpc.CallOption) (*teachersv1.BatchGetResponse, error) {
	return &teachersv1.BatchGetResponse{}, nil
}

// recordingSender captures SendHTML calls so tests can assert what was
// (or was not) sent to Telegram.
type recordingSender struct {
	mu    sync.Mutex
	calls []sendCall
	err   error
}

type sendCall struct {
	chatID int64
	text   string
}

func (r *recordingSender) SendHTML(chatID int64, text string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, sendCall{chatID: chatID, text: text})
	return r.err
}

func (r *recordingSender) Calls() []sendCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]sendCall, len(r.calls))
	copy(cp, r.calls)
	return cp
}

// natsPublisher adapts *nats.Conn to notifierserver.Publisher.
type natsPublisher struct{ conn *nats.Conn }

func (n natsPublisher) Publish(subject string, data []byte) error {
	return n.conn.Publish(subject, data)
}

// startNotifierService wires a notifier-svc with the given dependencies and
// returns a connected gRPC client.
func startNotifierService(
	t *testing.T,
	auth notifierserver.AuthClient,
	teachers notifierserver.TeachersClient,
	sender *recordingSender,
	pub notifierserver.Publisher,
) notifierv1.NotifierServiceClient {
	t.Helper()

	srv, err := notifierserver.New(notifierserver.Deps{
		Auth: auth, Teachers: teachers, Sender: sender, Publisher: pub,
	})
	require.NoError(t, err, "build notifier server")

	grpcSrv := testhelpers.StartGRPCServer(t)
	notifierv1.RegisterNotifierServiceServer(grpcSrv.Server, srv)
	grpcSrv.Serve(t)
	return notifierv1.NewNotifierServiceClient(grpcSrv.Dial(t))
}

func TestNotifier_NotifyMatched_PublishesAlertOnNATS(t *testing.T) {
	natsc := testhelpers.StartNATS(t)

	const userID = "user-42"
	const subject = "alerts.user-42"

	// Subscribe BEFORE calling NotifyMatched to avoid a race on delivery.
	msgCh := make(chan *nats.Msg, 4)
	sub, err := natsc.Conn.Subscribe(subject, func(m *nats.Msg) {
		msgCh <- m
	})
	require.NoError(t, err)
	require.NoError(t, natsc.Conn.Flush())
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	sender := &recordingSender{}
	client := startNotifierService(t,
		&fakeAuthClient{chatID: 12345},
		fakeTeachersClient{},
		sender,
		natsPublisher{conn: natsc.Conn},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.NotifyMatched(ctx, &notifierv1.NotifyMatchedRequest{
		UserId: userID,
		Matched: []*commonv1.MatchedSlot{
			{
				Slot: &commonv1.Slot{
					Id: "slot-aerobic-1", Week: 5, Time: "08:30-10:00",
					Place: "ГЗ-1", TeacherName: "Иванов И.И.",
					Section: strPtr("Аэробика"), Vacancy: 3,
				},
				MatchedFilterIds: []string{"filter-7"},
				IsNew:            true,
			},
		},
		Channels: []commonv1.AlertChannel{commonv1.AlertChannel_ALERT_CHANNEL_SSE},
	})
	require.NoError(t, err)
	require.Contains(t, resp.GetDeliveredBy(), commonv1.AlertChannel_ALERT_CHANNEL_SSE,
		"SSE channel must be reported as delivered")
	require.Empty(t, resp.GetFailedBy(), "no failed channels expected")

	// Wait for the message on NATS.
	select {
	case msg := <-msgCh:
		require.Equal(t, subject, msg.Subject)
		var payload notifierserver.AlertPayload
		require.NoError(t, json.Unmarshal(msg.Data, &payload),
			"alert payload must be valid JSON")
		require.Equal(t, "slot-aerobic-1", payload.Slot.ID)
		require.Equal(t, "Аэробика", payload.Slot.Section)
		require.Equal(t, int32(3), payload.Slot.Vacancy)
		require.Equal(t, []string{"filter-7"}, payload.MatchedFilterIDs)
		require.Equal(t, "sse", payload.Channel)
		require.NotEmpty(t, payload.SentAt, "sent_at must be set")
	case <-time.After(3 * time.Second):
		t.Fatal("expected NATS message on alerts.user-42 within 3s but got none")
	}

	require.Empty(t, sender.Calls(),
		"SSE-only request must not invoke the Telegram sender")
}

func TestNotifier_NotifyMatched_TelegramOnly_DeliversToBot(t *testing.T) {
	natsc := testhelpers.StartNATS(t)

	sender := &recordingSender{}
	client := startNotifierService(t,
		&fakeAuthClient{chatID: 99},
		fakeTeachersClient{},
		sender,
		natsPublisher{conn: natsc.Conn},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.NotifyMatched(ctx, &notifierv1.NotifyMatchedRequest{
		UserId: "u-tg",
		Matched: []*commonv1.MatchedSlot{
			{
				Slot: &commonv1.Slot{
					Id: "s1", Time: "10:00-11:30", Place: "СК",
					TeacherName: "Петров П.П.", Vacancy: 5,
				},
				IsNew: true,
			},
		},
		Channels: []commonv1.AlertChannel{commonv1.AlertChannel_ALERT_CHANNEL_TELEGRAM},
	})
	require.NoError(t, err)
	require.Contains(t, resp.GetDeliveredBy(), commonv1.AlertChannel_ALERT_CHANNEL_TELEGRAM)

	calls := sender.Calls()
	require.NotEmpty(t, calls, "Telegram sender must be called at least once")
	require.Equal(t, int64(99), calls[0].chatID,
		"chat id must match user's telegram_chat_id")
}

func TestNotifier_NotifyMatched_EmptyMatched_NoOp(t *testing.T) {
	natsc := testhelpers.StartNATS(t)

	sender := &recordingSender{}
	client := startNotifierService(t,
		&fakeAuthClient{chatID: 1},
		fakeTeachersClient{},
		sender,
		natsPublisher{conn: natsc.Conn},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.NotifyMatched(ctx, &notifierv1.NotifyMatchedRequest{
		UserId: "u-empty",
	})
	require.NoError(t, err, "empty matched should not error")
	require.Empty(t, resp.GetDeliveredBy(), "no deliveries when matched is empty")
	require.Empty(t, resp.GetFailedBy())
	require.Empty(t, sender.Calls(), "Telegram sender must not be invoked")
}

func TestNotifier_NotifyMatched_TelegramNotLinked_RecordsFailure(t *testing.T) {
	natsc := testhelpers.StartNATS(t)

	sender := &recordingSender{}
	// chatID=0 means TG not linked.
	client := startNotifierService(t,
		&fakeAuthClient{chatID: 0},
		fakeTeachersClient{},
		sender,
		natsPublisher{conn: natsc.Conn},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.NotifyMatched(ctx, &notifierv1.NotifyMatchedRequest{
		UserId: "u-unlinked",
		Matched: []*commonv1.MatchedSlot{
			{Slot: &commonv1.Slot{Id: "s", Place: "G", TeacherName: "T", Vacancy: 1}, IsNew: true},
		},
		Channels: []commonv1.AlertChannel{commonv1.AlertChannel_ALERT_CHANNEL_TELEGRAM},
	})
	require.NoError(t, err,
		"RPC should still return OK; per-channel result tracks the failure")
	require.Empty(t, resp.GetDeliveredBy(),
		"no successful deliveries when TG not linked")
	require.Contains(t, resp.GetFailedBy(), commonv1.AlertChannel_ALERT_CHANNEL_TELEGRAM,
		"telegram channel must be reported as failed")
}
