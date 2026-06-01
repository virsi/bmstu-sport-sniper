//go:build integration

// Package integration_test drives filter-svc against a real Postgres
// (filter_db migrations applied via goose) over a real gRPC connection.
//
// Scenarios:
//   - CRUD round-trip: create → list → update → delete + idempotency.
//   - Deduplication contract: MatchSlots → MarkSeen → MatchSlots returns
//     IsNew=false for the same slots; ResetKnown brings IsNew back to true.
//   - Filter ownership: GetFilter rejects cross-user access.
//   - MarkSeen inserts an alert_log row per slot id.
//
// Run them with:
//
//	cd backend/services/filter-svc
//	go test -tags integration ./integration_test/... -v -timeout 120s
package integration_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	commonv1 "github.com/fizcultor/backend/gen/common/v1"
	filterv1 "github.com/fizcultor/backend/gen/filter/v1"
	filterservice "github.com/fizcultor/backend/services/filter-svc/internal/service"
	filterstore "github.com/fizcultor/backend/services/filter-svc/internal/store"
	"github.com/fizcultor/backend/tests/testhelpers"
)

// startFilterService wires the filter-svc with a real Postgres connection
// and returns a connected gRPC client.
func startFilterService(t *testing.T) filterv1.FilterServiceClient {
	t.Helper()

	pg := testhelpers.StartPostgres(t, "filter_db")
	st := filterstore.New(pg.Pool)
	svc := filterservice.New(st)

	grpcSrv := testhelpers.StartGRPCServer(t)
	filterv1.RegisterFilterServiceServer(grpcSrv.Server, svc)
	grpcSrv.Serve(t)
	return filterv1.NewFilterServiceClient(grpcSrv.Dial(t))
}

// strPtr returns &s. Helper for proto optional-string fields.
func strPtr(s string) *string { return &s }

// boolPtr returns &b. Helper for proto optional-bool fields.
func boolPtr(b bool) *bool { return &b }

// makeSlot builds a *commonv1.Slot with sensible defaults overridden by
// setters. Empty section/teacherUID are translated to nil.
func makeSlot(id, section, time, teacherUID string, vacancy int32) *commonv1.Slot {
	s := &commonv1.Slot{
		Id:      id,
		Week:    1,
		Time:    time,
		Place:   "ГЗ-1",
		Vacancy: vacancy,
	}
	if section != "" {
		s.Section = strPtr(section)
	}
	if teacherUID != "" {
		s.TeacherUid = strPtr(teacherUID)
	}
	return s
}

func TestFilter_CRUD_RoundTrip(t *testing.T) {
	client := startFilterService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const userID = "42"

	// Empty list initially.
	listEmpty, err := client.ListFilters(ctx, &filterv1.ListFiltersRequest{UserId: userID})
	require.NoError(t, err)
	require.Empty(t, listEmpty.GetFilters(), "no filters for fresh user")

	// Create.
	created, err := client.CreateFilter(ctx, &filterv1.CreateFilterRequest{
		UserId:  userID,
		Section: strPtr("Аэробика"),
		Enabled: true,
	})
	require.NoError(t, err, "create filter")
	require.NotNil(t, created.GetFilter())
	filterID := created.GetFilter().GetId()
	require.NotEmpty(t, filterID)
	require.True(t, created.GetFilter().GetEnabled())

	// List → one filter.
	listOne, err := client.ListFilters(ctx, &filterv1.ListFiltersRequest{UserId: userID})
	require.NoError(t, err)
	require.Len(t, listOne.GetFilters(), 1)
	require.Equal(t, "Аэробика", listOne.GetFilters()[0].GetSection())

	// Get by id.
	got, err := client.GetFilter(ctx, &filterv1.GetFilterRequest{UserId: userID, FilterId: filterID})
	require.NoError(t, err)
	require.Equal(t, filterID, got.GetFilter().GetId())

	// Update via update_mask — toggle to disabled.
	updated, err := client.UpdateFilter(ctx, &filterv1.UpdateFilterRequest{
		UserId:     userID,
		FilterId:   filterID,
		Enabled:    boolPtr(false),
		UpdateMask: []string{"enabled"},
	})
	require.NoError(t, err, "update filter")
	require.False(t, updated.GetFilter().GetEnabled(), "enabled must be false after update")

	// Disabled filter is hidden from default list, but visible with include_disabled.
	listEnabledOnly, err := client.ListFilters(ctx, &filterv1.ListFiltersRequest{UserId: userID})
	require.NoError(t, err)
	require.Empty(t, listEnabledOnly.GetFilters(),
		"disabled filter must not appear in enabled-only list")

	listAll, err := client.ListFilters(ctx, &filterv1.ListFiltersRequest{
		UserId: userID, IncludeDisabled: true,
	})
	require.NoError(t, err)
	require.Len(t, listAll.GetFilters(), 1)

	// Delete.
	_, err = client.DeleteFilter(ctx, &filterv1.DeleteFilterRequest{UserId: userID, FilterId: filterID})
	require.NoError(t, err, "delete filter")

	// Delete is idempotent — double delete is fine.
	_, err = client.DeleteFilter(ctx, &filterv1.DeleteFilterRequest{UserId: userID, FilterId: filterID})
	require.NoError(t, err, "delete should be idempotent")
}

func TestFilter_GetFilter_OtherUser_PermissionDenied(t *testing.T) {
	client := startFilterService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	created, err := client.CreateFilter(ctx, &filterv1.CreateFilterRequest{
		UserId: "100", Section: strPtr("Силовая"), Enabled: true,
	})
	require.NoError(t, err)
	filterID := created.GetFilter().GetId()

	// Other user tries to read it → must fail.
	_, err = client.GetFilter(ctx, &filterv1.GetFilterRequest{
		UserId: "200", FilterId: filterID,
	})
	require.Error(t, err, "GetFilter for filter owned by another user must fail")
}

func TestFilter_MatchSlots_NewSlot_IsNewTrue(t *testing.T) {
	client := startFilterService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const userID = "1001"

	// Filter wide-open (only section).
	_, err := client.CreateFilter(ctx, &filterv1.CreateFilterRequest{
		UserId: userID, Section: strPtr("Аэробика"), Enabled: true,
	})
	require.NoError(t, err)

	resp, err := client.MatchSlots(ctx, &filterv1.MatchSlotsRequest{
		UserId: userID,
		Slots: []*commonv1.Slot{
			makeSlot("slot-1", "Аэробика", "08:30-10:00", "", 5),
			makeSlot("slot-2", "Силовая", "10:00-11:30", "", 3),
		},
	})
	require.NoError(t, err)
	require.Len(t, resp.GetMatched(), 1, "only aerobic slot must match")
	require.Equal(t, "slot-1", resp.GetMatched()[0].GetSlot().GetId())
	require.True(t, resp.GetMatched()[0].GetIsNew(),
		"never-seen slot must be marked as new")
}

func TestFilter_MarkSeen_ThenMatch_IsNewFalse(t *testing.T) {
	client := startFilterService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const userID = "1002"

	_, err := client.CreateFilter(ctx, &filterv1.CreateFilterRequest{
		UserId: userID, Section: strPtr("Аэробика"), Enabled: true,
	})
	require.NoError(t, err)

	slot := makeSlot("slot-x", "Аэробика", "08:30-10:00", "", 5)

	// 1st match — is_new=true.
	match1, err := client.MatchSlots(ctx, &filterv1.MatchSlotsRequest{
		UserId: userID, Slots: []*commonv1.Slot{slot},
	})
	require.NoError(t, err)
	require.Len(t, match1.GetMatched(), 1)
	require.True(t, match1.GetMatched()[0].GetIsNew())

	// Mark seen.
	_, err = client.MarkSeen(ctx, &filterv1.MarkSeenRequest{
		UserId: userID, SlotIds: []string{"slot-x"},
	})
	require.NoError(t, err, "MarkSeen succeeds")

	// 2nd match — is_new=false.
	match2, err := client.MatchSlots(ctx, &filterv1.MatchSlotsRequest{
		UserId: userID, Slots: []*commonv1.Slot{slot},
	})
	require.NoError(t, err)
	require.Len(t, match2.GetMatched(), 1)
	require.False(t, match2.GetMatched()[0].GetIsNew(),
		"after MarkSeen the slot must no longer be marked as new")
}

func TestFilter_ResetKnown_ResetsDedupeState(t *testing.T) {
	client := startFilterService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const userID = "1003"

	_, err := client.CreateFilter(ctx, &filterv1.CreateFilterRequest{
		UserId: userID, Section: strPtr("Аэробика"), Enabled: true,
	})
	require.NoError(t, err)

	slot := makeSlot("slot-z", "Аэробика", "08:30-10:00", "", 5)

	_, err = client.MatchSlots(ctx, &filterv1.MatchSlotsRequest{
		UserId: userID, Slots: []*commonv1.Slot{slot},
	})
	require.NoError(t, err)
	_, err = client.MarkSeen(ctx, &filterv1.MarkSeenRequest{
		UserId: userID, SlotIds: []string{"slot-z"},
	})
	require.NoError(t, err)

	// After MarkSeen it's NOT new.
	match2, err := client.MatchSlots(ctx, &filterv1.MatchSlotsRequest{
		UserId: userID, Slots: []*commonv1.Slot{slot},
	})
	require.NoError(t, err)
	require.False(t, match2.GetMatched()[0].GetIsNew())

	// Reset known.
	reset, err := client.ResetKnown(ctx, &filterv1.ResetKnownRequest{UserId: userID})
	require.NoError(t, err)
	require.GreaterOrEqual(t, reset.GetClearedCount(), int32(1),
		"ResetKnown must report at least 1 cleared row")

	// After reset — is_new=true again.
	match3, err := client.MatchSlots(ctx, &filterv1.MatchSlotsRequest{
		UserId: userID, Slots: []*commonv1.Slot{slot},
	})
	require.NoError(t, err)
	require.True(t, match3.GetMatched()[0].GetIsNew(),
		"after ResetKnown the slot must be new again")
}

func TestFilter_MarkSeen_Idempotent(t *testing.T) {
	client := startFilterService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const userID = "1004"

	_, err := client.CreateFilter(ctx, &filterv1.CreateFilterRequest{
		UserId: userID, Section: strPtr("Аэробика"), Enabled: true,
	})
	require.NoError(t, err)

	// Calling MarkSeen twice with the same id is fine — ON CONFLICT DO NOTHING.
	_, err = client.MarkSeen(ctx, &filterv1.MarkSeenRequest{
		UserId: userID, SlotIds: []string{"dup-slot"},
	})
	require.NoError(t, err)
	_, err = client.MarkSeen(ctx, &filterv1.MarkSeenRequest{
		UserId: userID, SlotIds: []string{"dup-slot"},
	})
	require.NoError(t, err, "MarkSeen must be idempotent")
}

func TestFilter_MatchSlots_NoFilters_EmptyResult(t *testing.T) {
	client := startFilterService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := client.MatchSlots(ctx, &filterv1.MatchSlotsRequest{
		UserId: "9999",
		Slots: []*commonv1.Slot{
			makeSlot("any", "Аэробика", "08:30-10:00", "", 5),
		},
	})
	require.NoError(t, err)
	require.Empty(t, resp.GetMatched(), "without filters, nothing matches")
}

func TestFilter_MatchSlots_DisabledFiltersIgnored(t *testing.T) {
	client := startFilterService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const userID = "1005"

	// Disabled filter only.
	_, err := client.CreateFilter(ctx, &filterv1.CreateFilterRequest{
		UserId: userID, Section: strPtr("Аэробика"), Enabled: false,
	})
	require.NoError(t, err)

	resp, err := client.MatchSlots(ctx, &filterv1.MatchSlotsRequest{
		UserId: userID, Slots: []*commonv1.Slot{
			makeSlot("any", "Аэробика", "08:30-10:00", "", 5),
		},
	})
	require.NoError(t, err)
	require.Empty(t, resp.GetMatched(),
		"disabled filters must not contribute to matches")
}

// TestFilter_AlertLog_PersistedOnMarkSeen confirms MarkSeen inserts one
// row per slot id into alert_log (used for analytics + audit).
func TestFilter_AlertLog_PersistedOnMarkSeen(t *testing.T) {
	pg := testhelpers.StartPostgres(t, "filter_db")
	st := filterstore.New(pg.Pool)
	svc := filterservice.New(st)
	grpcSrv := testhelpers.StartGRPCServer(t)
	filterv1.RegisterFilterServiceServer(grpcSrv.Server, svc)
	grpcSrv.Serve(t)
	client := filterv1.NewFilterServiceClient(grpcSrv.Dial(t))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const userID = "1006"
	uid64, _ := strconv.ParseInt(userID, 10, 64)

	_, err := client.MarkSeen(ctx, &filterv1.MarkSeenRequest{
		UserId: userID, SlotIds: []string{"al-1", "al-2"},
	})
	require.NoError(t, err)

	var n int
	require.NoError(t, pg.Pool.QueryRow(ctx,
		"SELECT count(*) FROM alert_log WHERE user_id = $1", uid64,
	).Scan(&n))
	require.Equal(t, 2, n, "MarkSeen must write one alert_log row per slot_id")
}
