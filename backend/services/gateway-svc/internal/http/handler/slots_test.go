package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	bmstuv1 "github.com/fizcultor/backend/gen/bmstu/v1"
	commonv1 "github.com/fizcultor/backend/gen/common/v1"

	"github.com/fizcultor/backend/services/gateway-svc/internal/clients"
	"github.com/fizcultor/backend/services/gateway-svc/internal/http/handler"
)

func TestHandler_Slots_DisabledReturnsEmpty(t *testing.T) {
	t.Parallel()

	// Дефолтный handler.New: SlotsEndpointEnabled=false → не должен дёргать bmstu.
	mock := &mockBmstuClient{
		FetchGroupsFn: func(context.Context, *bmstuv1.FetchGroupsRequest) (*bmstuv1.FetchGroupsResponse, error) {
			t.Fatal("FetchGroups must NOT be called when SlotsEndpointEnabled=false")
			return nil, nil
		},
	}
	h := newHandler(t, withBmstu(mock))
	mw := withUserCtx(t, "u-1")
	protected := mw(http.HandlerFunc(h.Slots))

	req := httptest.NewRequest(http.MethodGet, "/api/slots", http.NoBody)
	req.Header.Set("Authorization", "Bearer dummy")
	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	slots, ok := body["slots"].([]any)
	require.True(t, ok)
	assert.Len(t, slots, 0)
}

func TestHandler_Slots_EnabledFetchesGroups(t *testing.T) {
	t.Parallel()

	mock := &mockBmstuClient{
		FetchGroupsFn: func(_ context.Context, in *bmstuv1.FetchGroupsRequest) (*bmstuv1.FetchGroupsResponse, error) {
			assert.Equal(t, "u-1", in.GetUserId())
			return &bmstuv1.FetchGroupsResponse{
				Slots: []*commonv1.Slot{
					{
						Id:           "slot-1",
						Week:         14,
						Time:         "18:00-19:30",
						Section:      ptr("Аэробика"),
						Place:        "СК \"Дворец\", зал 3",
						TeacherName:  "Иванова А.П.",
						Vacancy:      2,
						SemesterUuid: "sem-uuid",
						DayOfWeek:    commonv1.DayOfWeek_DAY_OF_WEEK_WEDNESDAY,
					},
				},
				FetchedAt: timestamppb.New(fixedTime),
			}, nil
		},
	}
	cls := &clients.Clients{
		Auth:     &mockAuthClient{},
		Bmstu:    mock,
		Filter:   &mockFilterClient{},
		Notifier: &mockNotifierClient{},
		Teachers: &mockTeachersClient{},
	}
	h := handler.New(handler.Deps{
		Clients:                  cls,
		BotUsername:              "FizcultorBot",
		SlotsEndpointEnabled:     true,
		SlotsFetchTimeoutSeconds: 5,
	})
	mw := withUserCtx(t, "u-1")
	protected := mw(http.HandlerFunc(h.Slots))

	req := httptest.NewRequest(http.MethodGet, "/api/slots", http.NoBody)
	req.Header.Set("Authorization", "Bearer dummy")
	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	slots := body["slots"].([]any)
	require.Len(t, slots, 1)
	s := slots[0].(map[string]any)
	assert.Equal(t, "slot-1", s["id"])
	assert.Equal(t, "Аэробика", s["section"])
	assert.Equal(t, "WEDNESDAY", s["day_of_week"])
	assert.EqualValues(t, 14, s["week"])
	assert.EqualValues(t, 2, s["vacancy"])
}

func TestHandler_Slots_EnabledPropagatesError(t *testing.T) {
	t.Parallel()

	mock := &mockBmstuClient{
		FetchGroupsFn: func(context.Context, *bmstuv1.FetchGroupsRequest) (*bmstuv1.FetchGroupsResponse, error) {
			return nil, status.Error(codes.Unavailable, "LKS down")
		},
	}
	cls := &clients.Clients{
		Auth:     &mockAuthClient{},
		Bmstu:    mock,
		Filter:   &mockFilterClient{},
		Notifier: &mockNotifierClient{},
		Teachers: &mockTeachersClient{},
	}
	h := handler.New(handler.Deps{
		Clients:                  cls,
		BotUsername:              "FizcultorBot",
		SlotsEndpointEnabled:     true,
		SlotsFetchTimeoutSeconds: 5,
	})
	mw := withUserCtx(t, "u-1")
	protected := mw(http.HandlerFunc(h.Slots))

	req := httptest.NewRequest(http.MethodGet, "/api/slots", http.NoBody)
	req.Header.Set("Authorization", "Bearer dummy")
	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
