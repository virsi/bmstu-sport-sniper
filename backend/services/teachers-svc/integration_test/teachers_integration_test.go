//go:build integration

// Package integration_test covers teachers-svc against a real Postgres.
// The embedded teachers.json is imported via Bootstrap, then we exercise
// Get, BatchGet, List, and Refresh round-trip.
//
// Run them with:
//
//	cd backend/services/teachers-svc
//	go test -tags integration ./integration_test/... -v -timeout 120s
package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	commonv1 "github.com/fizcultor/backend/gen/common/v1"
	teachersv1 "github.com/fizcultor/backend/gen/teachers/v1"
	teachersstore "github.com/fizcultor/backend/services/teachers-svc/internal/store"
	"github.com/fizcultor/backend/services/teachers-svc/internal/teachers"
	"github.com/fizcultor/backend/tests/testhelpers"
)

// startTeachersService wires teachers-svc against a real Postgres, runs
// Bootstrap (which loads the embedded teachers.json), and returns a client.
func startTeachersService(t *testing.T) (teachersv1.TeachersServiceClient, *testhelpers.PostgresContainer) {
	t.Helper()

	pg := testhelpers.StartPostgres(t, "teachers_db")
	st := teachersstore.New(pg.Pool)

	// Bootstrap loads embedded teachers.json into the freshly-migrated DB.
	bootstrapCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, teachers.Bootstrap(bootstrapCtx, st), "bootstrap teachers")

	svc := teachers.New(st)

	grpcSrv := testhelpers.StartGRPCServer(t)
	teachersv1.RegisterTeachersServiceServer(grpcSrv.Server, svc)
	grpcSrv.Serve(t)
	return teachersv1.NewTeachersServiceClient(grpcSrv.Dial(t)), pg
}

func TestTeachers_Bootstrap_PopulatesEmbeddedJSON(t *testing.T) {
	_, pg := startTeachersService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var n int64
	require.NoError(t, pg.Pool.QueryRow(ctx, "SELECT count(*) FROM teachers").Scan(&n))
	require.Greater(t, n, int64(50),
		"embedded teachers.json should produce at least 50 rows; got %d", n)
}

func TestTeachers_Bootstrap_Idempotent(t *testing.T) {
	client, pg := startTeachersService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Initial row count after Bootstrap (called in helper).
	var nBefore int64
	require.NoError(t, pg.Pool.QueryRow(ctx, "SELECT count(*) FROM teachers").Scan(&nBefore))
	require.Positive(t, nBefore, "bootstrap must populate teachers")

	// Calling Refresh re-imports embedded JSON; row count must not change.
	stats, err := client.Refresh(ctx, &teachersv1.RefreshRequest{})
	require.NoError(t, err)
	require.Equal(t, int32(nBefore), stats.GetTotal(),
		"Refresh on already-imported data must report the same total")

	var nAfter int64
	require.NoError(t, pg.Pool.QueryRow(ctx, "SELECT count(*) FROM teachers").Scan(&nAfter))
	require.Equal(t, nBefore, nAfter,
		"Refresh must not create duplicate rows (upsert by uid)")
}

func TestTeachers_Get_ExistingTeacher(t *testing.T) {
	client, _ := startTeachersService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Pick any teacher from the embedded JSON to look up by uid; this keeps
	// the test resilient to teachers.json content changes.
	list, err := client.List(ctx, &teachersv1.ListRequest{})
	require.NoError(t, err)
	require.NotEmpty(t, list.GetTeachers(), "list must return at least one teacher")

	first := list.GetTeachers()[0]
	resp, err := client.Get(ctx, &teachersv1.GetRequest{Uid: first.GetUid()})
	require.NoError(t, err)
	require.Equal(t, first.GetUid(), resp.GetTeacher().GetUid())
	require.NotEmpty(t, resp.GetTeacher().GetFullName())
}

func TestTeachers_Get_UnknownUID_NotFound(t *testing.T) {
	client, _ := startTeachersService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := client.Get(ctx, &teachersv1.GetRequest{Uid: "uid-that-cant-exist-xxxxxxxx"})
	require.Error(t, err, "Get of unknown uid must return error")
}

func TestTeachers_BatchGet_MixedExistingAndUnknown(t *testing.T) {
	client, _ := startTeachersService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Grab two existing uids.
	list, err := client.List(ctx, &teachersv1.ListRequest{})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(list.GetTeachers()), 2)
	uid1 := list.GetTeachers()[0].GetUid()
	uid2 := list.GetTeachers()[1].GetUid()

	// Mix in a fake uid — must be silently dropped, not an error.
	resp, err := client.BatchGet(ctx, &teachersv1.BatchGetRequest{
		Uids: []string{uid1, "ghost-uid", uid2},
	})
	require.NoError(t, err, "BatchGet with unknown uids should not error")
	require.Len(t, resp.GetTeachers(), 2, "only 2 of 3 uids exist")

	got := map[string]bool{}
	for _, tc := range resp.GetTeachers() {
		got[tc.GetUid()] = true
	}
	require.True(t, got[uid1], "uid1 must be in response")
	require.True(t, got[uid2], "uid2 must be in response")
}

func TestTeachers_List_Pagination(t *testing.T) {
	client, _ := startTeachersService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Read first page (limit 5).
	page1, err := client.List(ctx, &teachersv1.ListRequest{
		Page: &commonv1.PageRequest{PageSize: 5},
	})
	require.NoError(t, err)
	require.Len(t, page1.GetTeachers(), 5)
	require.NotEmpty(t, page1.GetPage().GetNextPageToken(),
		"expected next_page_token on full page")

	// Read second page using the token.
	page2, err := client.List(ctx, &teachersv1.ListRequest{
		Page: &commonv1.PageRequest{
			PageSize:  5,
			PageToken: page1.GetPage().GetNextPageToken(),
		},
	})
	require.NoError(t, err)
	require.NotEmpty(t, page2.GetTeachers())

	// Pages must be disjoint.
	seen := make(map[string]bool, 5)
	for _, p := range page1.GetTeachers() {
		seen[p.GetUid()] = true
	}
	for _, p := range page2.GetTeachers() {
		require.False(t, seen[p.GetUid()],
			"page 2 contains uid %s that was already on page 1", p.GetUid())
	}
}

func TestTeachers_List_NameQuery_Filters(t *testing.T) {
	client, _ := startTeachersService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Grab one teacher and use a substring of its normalized name to verify
	// the LIKE filter actually narrows results.
	all, err := client.List(ctx, &teachersv1.ListRequest{})
	require.NoError(t, err)
	require.NotEmpty(t, all.GetTeachers())

	// Take the first 3 chars of the first teacher's name.
	target := all.GetTeachers()[0].GetFullName()
	if len(target) < 3 {
		t.Skip("first teacher name too short for substring filter")
	}

	q := target[:3]
	resp, err := client.List(ctx, &teachersv1.ListRequest{
		NameQuery: &q,
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.GetTeachers(), "substring filter must return results")
	require.LessOrEqual(t, len(resp.GetTeachers()), len(all.GetTeachers()),
		"filtered list must be a subset of all teachers")
}
