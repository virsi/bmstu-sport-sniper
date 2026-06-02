package server

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	bmstuv1 "github.com/fizcultor/backend/gen/bmstu/v1"
	commonv1 "github.com/fizcultor/backend/gen/common/v1"

	"github.com/fizcultor/backend/services/bmstu-svc/internal/oidc"
	"github.com/fizcultor/backend/services/bmstu-svc/internal/session"
	"github.com/fizcultor/backend/services/bmstu-svc/internal/store"
)

const testKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func decodeKey(t *testing.T) []byte {
	t.Helper()
	b, err := hex.DecodeString(testKey)
	require.NoError(t, err)
	return b
}

// fakeStore — in-memory Store.
type fakeStore struct {
	creds             map[string]store.BmstuCredential
	credStatus        map[string]store.BmstuCredentialStatus
	sessions          map[string]store.BmstuSession
	upsertCalled      bool
	deleteCredsCalled bool
	deleteSessCalled  bool
	upsertCredsErr    error
	getCredsStatusErr error
	deleteCredsErr    error
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		creds:      map[string]store.BmstuCredential{},
		credStatus: map[string]store.BmstuCredentialStatus{},
		sessions:   map[string]store.BmstuSession{},
	}
}

func (f *fakeStore) UpsertCredentials(_ context.Context, arg store.UpsertCredentialsParams) error {
	if f.upsertCredsErr != nil {
		return f.upsertCredsErr
	}
	f.upsertCalled = true
	now := time.Now().UTC()
	f.creds[arg.UserID] = store.BmstuCredential{
		UserID:      arg.UserID,
		EncLogin:    arg.EncLogin,
		EncPassword: arg.EncPassword,
		LastLoginAt: arg.LastLoginAt,
		CreatedAt:   now,
		UpdatedAt:   now,
		HealthGroup: arg.HealthGroup,
	}
	f.credStatus[arg.UserID] = store.BmstuCredentialStatus{
		UserID:      arg.UserID,
		LastLoginAt: arg.LastLoginAt,
		CreatedAt:   now,
		UpdatedAt:   now,
		HealthGroup: arg.HealthGroup,
	}
	return nil
}

func (f *fakeStore) GetCredentials(_ context.Context, userID string) (store.BmstuCredential, error) {
	c, ok := f.creds[userID]
	if !ok {
		return store.BmstuCredential{}, pgx.ErrNoRows
	}
	return c, nil
}

func (f *fakeStore) GetCredentialsStatus(_ context.Context, userID string) (store.BmstuCredentialStatus, error) {
	if f.getCredsStatusErr != nil {
		return store.BmstuCredentialStatus{}, f.getCredsStatusErr
	}
	c, ok := f.credStatus[userID]
	if !ok {
		return store.BmstuCredentialStatus{}, pgx.ErrNoRows
	}
	return c, nil
}

func (f *fakeStore) DeleteCredentials(_ context.Context, userID string) error {
	if f.deleteCredsErr != nil {
		return f.deleteCredsErr
	}
	f.deleteCredsCalled = true
	delete(f.creds, userID)
	delete(f.credStatus, userID)
	delete(f.sessions, userID)
	return nil
}

func (f *fakeStore) GetSession(_ context.Context, userID string) (store.BmstuSession, error) {
	s, ok := f.sessions[userID]
	if !ok {
		return store.BmstuSession{}, pgx.ErrNoRows
	}
	return s, nil
}

func (f *fakeStore) DeleteSession(_ context.Context, userID string) error {
	f.deleteSessCalled = true
	delete(f.sessions, userID)
	return nil
}

// fakeManager — управляемый SessionManager.
type fakeManager struct {
	acquireErrs []error
	refreshErr  error
	hcAcquire   *http.Client
	hcRefresh   *http.Client
	calls       struct {
		acquire    int
		refresh    int
		invalidate int
	}
}

func (f *fakeManager) Acquire(_ context.Context, _ string) (*http.Client, error) {
	idx := f.calls.acquire
	f.calls.acquire++
	if idx < len(f.acquireErrs) && f.acquireErrs[idx] != nil {
		return nil, f.acquireErrs[idx]
	}
	if f.hcAcquire == nil {
		return &http.Client{}, nil
	}
	return f.hcAcquire, nil
}

func (f *fakeManager) Refresh(_ context.Context, _ string) (*http.Client, error) {
	f.calls.refresh++
	if f.refreshErr != nil {
		return nil, f.refreshErr
	}
	if f.hcRefresh == nil {
		return &http.Client{}, nil
	}
	return f.hcRefresh, nil
}

func (f *fakeManager) Invalidate(_ context.Context, _ string) error {
	f.calls.invalidate++
	return nil
}

// fakeOIDC — стаб для test-login.
type fakeOIDC struct {
	loginErr error
}

func (f *fakeOIDC) Login(_ context.Context, _, _ string) (*oidc.LoginResult, error) {
	if f.loginErr != nil {
		return nil, f.loginErr
	}
	return &oidc.LoginResult{}, nil
}

// fakeGroups — стаб GroupsClient с пошаговым результатом.
type fakeGroups struct {
	results []fetchResult
	calls   int
}

type fetchResult struct {
	slots []*commonv1.Slot
	err   error
}

func (f *fakeGroups) Fetch(_ context.Context, _ *http.Client, _ string) ([]*commonv1.Slot, error) {
	idx := f.calls
	f.calls++
	if idx >= len(f.results) {
		return nil, errors.New("fakeGroups: out of programmed results")
	}
	r := f.results[idx]
	return r.slots, r.err
}

// --- helpers -----------------------------------------------------------------

// testSemesters имитирует мапу SemesterUUIDFor для всех 4 групп здоровья.
// Возвращает определённые значения, по которым тест может проверить, что
// FetchGroups передал правильный UUID в groups.Fetch.
var testSemesters = map[commonv1.HealthGroup]string{
	commonv1.HealthGroup_HEALTH_GROUP_BASIC:           "sem-basic",
	commonv1.HealthGroup_HEALTH_GROUP_PREPARATORY:     "sem-prep",
	commonv1.HealthGroup_HEALTH_GROUP_SPECIAL_MEDICAL: "sem-smg",
	commonv1.HealthGroup_HEALTH_GROUP_AFK:             "sem-afk",
}

// testSemesterFor — резолвер для server.Config.SemesterFor в тестах.
// UNSPECIFIED трактуется как BASIC (как в реальном Config.SemesterUUIDFor).
func testSemesterFor(hg commonv1.HealthGroup) string {
	if v, ok := testSemesters[hg]; ok {
		return v
	}
	return testSemesters[commonv1.HealthGroup_HEALTH_GROUP_BASIC]
}

func newServerWithMocks(t *testing.T) (*Server, *fakeStore, *fakeManager, *fakeOIDC, *fakeGroups) {
	t.Helper()
	st := newFakeStore()
	mg := &fakeManager{}
	fo := &fakeOIDC{}
	fg := &fakeGroups{}
	s, err := New(st, mg, fo, fg, Config{
		MasterKey:   decodeKey(t),
		SemesterFor: testSemesterFor,
	})
	require.NoError(t, err)
	return s, st, mg, fo, fg
}

func codeOf(t *testing.T, err error) codes.Code {
	t.Helper()
	st, ok := status.FromError(err)
	require.True(t, ok, "not a status error: %v", err)
	return st.Code()
}

// --- tests -------------------------------------------------------------------

func TestStoreCredentials_Success(t *testing.T) {
	s, st, _, _, _ := newServerWithMocks(t)

	resp, err := s.StoreCredentials(context.Background(), &bmstuv1.StoreCredentialsRequest{
		UserId:   "u1",
		Login:    "ivan",
		Password: "p@ss",
	})
	require.NoError(t, err)
	require.Equal(t, commonv1.BmstuLinkStatus_BMSTU_LINK_STATUS_VALID, resp.GetStatus())
	require.NotNil(t, resp.GetLastLoginAt())
	require.True(t, st.upsertCalled)
}

func TestStoreCredentials_BadLogin(t *testing.T) {
	s, st, _, fo, _ := newServerWithMocks(t)
	fo.loginErr = oidc.ErrBadCredentials

	_, err := s.StoreCredentials(context.Background(), &bmstuv1.StoreCredentialsRequest{
		UserId:   "u1",
		Login:    "ivan",
		Password: "wrong",
	})
	require.Error(t, err)
	require.Equal(t, codes.Unauthenticated, codeOf(t, err))
	require.False(t, st.upsertCalled, "creds must NOT be saved on bad login")
}

func TestStoreCredentials_RateLimited(t *testing.T) {
	s, _, _, fo, _ := newServerWithMocks(t)
	fo.loginErr = oidc.ErrRateLimited

	_, err := s.StoreCredentials(context.Background(), &bmstuv1.StoreCredentialsRequest{
		UserId: "u1", Login: "x", Password: "y",
	})
	require.Equal(t, codes.ResourceExhausted, codeOf(t, err))
}

func TestStoreCredentials_MissingFields(t *testing.T) {
	s, _, _, _, _ := newServerWithMocks(t)

	cases := []*bmstuv1.StoreCredentialsRequest{
		{UserId: "", Login: "x", Password: "y"},
		{UserId: "u", Login: "", Password: "y"},
		{UserId: "u", Login: "x", Password: ""},
	}
	for _, c := range cases {
		_, err := s.StoreCredentials(context.Background(), c)
		require.Equal(t, codes.InvalidArgument, codeOf(t, err), "input: %+v", c)
	}
}

func TestDeleteCredentials_OK(t *testing.T) {
	s, st, _, _, _ := newServerWithMocks(t)
	_, err := s.DeleteCredentials(context.Background(), &bmstuv1.DeleteCredentialsRequest{UserId: "u1"})
	require.NoError(t, err)
	require.True(t, st.deleteCredsCalled)
}

func TestDeleteCredentials_NoUserID(t *testing.T) {
	s, _, _, _, _ := newServerWithMocks(t)
	_, err := s.DeleteCredentials(context.Background(), &bmstuv1.DeleteCredentialsRequest{})
	require.Equal(t, codes.InvalidArgument, codeOf(t, err))
}

func TestGetStatus_NotLinked(t *testing.T) {
	s, _, _, _, _ := newServerWithMocks(t)
	resp, err := s.GetStatus(context.Background(), &bmstuv1.GetStatusRequest{UserId: "ghost"})
	require.NoError(t, err)
	require.Equal(t, commonv1.BmstuLinkStatus_BMSTU_LINK_STATUS_NOT_LINKED, resp.GetStatus())
}

func TestGetStatus_Valid(t *testing.T) {
	s, st, _, _, _ := newServerWithMocks(t)
	now := time.Now().UTC()
	st.credStatus["u1"] = store.BmstuCredentialStatus{UserID: "u1", LastLoginAt: &now}

	resp, err := s.GetStatus(context.Background(), &bmstuv1.GetStatusRequest{UserId: "u1"})
	require.NoError(t, err)
	require.Equal(t, commonv1.BmstuLinkStatus_BMSTU_LINK_STATUS_VALID, resp.GetStatus())
	require.NotNil(t, resp.GetLastLoginAt())
}

func TestGetStatus_Expired(t *testing.T) {
	s, st, _, _, _ := newServerWithMocks(t)
	now := time.Now().UTC()
	past := now.Add(-time.Hour)
	st.credStatus["u1"] = store.BmstuCredentialStatus{UserID: "u1", LastLoginAt: &now}
	st.sessions["u1"] = store.BmstuSession{UserID: "u1", ExpiresAt: &past}

	resp, err := s.GetStatus(context.Background(), &bmstuv1.GetStatusRequest{UserId: "u1"})
	require.NoError(t, err)
	require.Equal(t, commonv1.BmstuLinkStatus_BMSTU_LINK_STATUS_EXPIRED, resp.GetStatus())
}

func TestFetchGroups_HappyPath(t *testing.T) {
	s, st, _, _, fg := newServerWithMocks(t)
	// Засеваем кредсы юзера, FetchGroups сначала их читает.
	st.credStatus["u1"] = store.BmstuCredentialStatus{
		UserID:      "u1",
		HealthGroup: "BASIC",
	}
	slot := &commonv1.Slot{Id: "x", Week: 14}
	fg.results = []fetchResult{{slots: []*commonv1.Slot{slot}}}

	resp, err := s.FetchGroups(context.Background(), &bmstuv1.FetchGroupsRequest{UserId: "u1"})
	require.NoError(t, err)
	require.Len(t, resp.GetSlots(), 1)
	require.Equal(t, "sem-basic", resp.GetSemesterUuid())
}

// TestFetchGroups_HealthGroupRoutesSemester проверяет, что 4 разных
// health_group в БД мапятся в 4 разных SemesterUUID при вызове LKS.
// Регрессия на основную фичу health_group.
func TestFetchGroups_HealthGroupRoutesSemester(t *testing.T) {
	cases := []struct {
		dbHealthGroup string
		wantSemester  string
	}{
		{"BASIC", "sem-basic"},
		{"PREPARATORY", "sem-prep"},
		{"SPECIAL_MEDICAL", "sem-smg"},
		{"AFK", "sem-afk"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.dbHealthGroup, func(t *testing.T) {
			s, st, _, _, fg := newServerWithMocks(t)
			st.credStatus["u1"] = store.BmstuCredentialStatus{
				UserID:      "u1",
				HealthGroup: c.dbHealthGroup,
			}
			fg.results = []fetchResult{{slots: nil}}
			resp, err := s.FetchGroups(context.Background(), &bmstuv1.FetchGroupsRequest{UserId: "u1"})
			require.NoError(t, err)
			require.Equal(t, c.wantSemester, resp.GetSemesterUuid())
		})
	}
}

// TestFetchGroups_NoCreds — если в БД нет записи, FetchGroups возвращает
// FailedPrecondition, не падая с pgx.ErrNoRows наружу.
func TestFetchGroups_NoCreds(t *testing.T) {
	s, _, _, _, _ := newServerWithMocks(t)
	_, err := s.FetchGroups(context.Background(), &bmstuv1.FetchGroupsRequest{UserId: "ghost"})
	require.Equal(t, codes.FailedPrecondition, codeOf(t, err))
}

// seedCreds — хелпер, делающий минимальные кредсы для FetchGroups-тестов.
func seedCreds(st *fakeStore, userID string) {
	st.credStatus[userID] = store.BmstuCredentialStatus{
		UserID:      userID,
		HealthGroup: "BASIC",
	}
}

func TestFetchGroups_RetryAfterSessionExpired(t *testing.T) {
	s, st, mg, _, fg := newServerWithMocks(t)
	seedCreds(st, "u1")
	// Первый Fetch — expired, второй — успех.
	fg.results = []fetchResult{
		{err: oidc.ErrSessionExpired},
		{slots: []*commonv1.Slot{{Id: "fresh"}}},
	}

	resp, err := s.FetchGroups(context.Background(), &bmstuv1.FetchGroupsRequest{UserId: "u1"})
	require.NoError(t, err)
	require.Len(t, resp.GetSlots(), 1)
	require.Equal(t, 1, mg.calls.acquire)
	require.Equal(t, 1, mg.calls.invalidate)
	require.Equal(t, 1, mg.calls.refresh)
}

func TestFetchGroups_RetryFailsTwice(t *testing.T) {
	s, st, _, _, fg := newServerWithMocks(t)
	seedCreds(st, "u1")
	fg.results = []fetchResult{
		{err: oidc.ErrSessionExpired},
		{err: oidc.ErrSessionExpired},
	}
	_, err := s.FetchGroups(context.Background(), &bmstuv1.FetchGroupsRequest{UserId: "u1"})
	require.Equal(t, codes.Unavailable, codeOf(t, err))
}

func TestFetchGroups_NotLinked(t *testing.T) {
	s, st, mg, _, _ := newServerWithMocks(t)
	seedCreds(st, "u1")
	mg.acquireErrs = []error{session.ErrCredentialsNotLinked}

	_, err := s.FetchGroups(context.Background(), &bmstuv1.FetchGroupsRequest{UserId: "u1"})
	require.Equal(t, codes.FailedPrecondition, codeOf(t, err))
}

func TestFetchGroups_RateLimited(t *testing.T) {
	s, st, _, _, fg := newServerWithMocks(t)
	seedCreds(st, "u1")
	fg.results = []fetchResult{{err: oidc.ErrRateLimited}}
	_, err := s.FetchGroups(context.Background(), &bmstuv1.FetchGroupsRequest{UserId: "u1"})
	require.Equal(t, codes.ResourceExhausted, codeOf(t, err))
}

func TestRefreshSession_OK(t *testing.T) {
	s, st, mg, _, _ := newServerWithMocks(t)
	future := time.Now().Add(time.Hour).UTC()
	st.sessions["u1"] = store.BmstuSession{UserID: "u1", ExpiresAt: &future}

	resp, err := s.RefreshSession(context.Background(), &bmstuv1.RefreshSessionRequest{UserId: "u1"})
	require.NoError(t, err)
	require.Equal(t, 1, mg.calls.refresh)
	require.NotNil(t, resp.GetSessionExpiresAt())
}

func TestRefreshSession_BadCreds(t *testing.T) {
	s, _, mg, _, _ := newServerWithMocks(t)
	mg.refreshErr = oidc.ErrBadCredentials
	_, err := s.RefreshSession(context.Background(), &bmstuv1.RefreshSessionRequest{UserId: "u1"})
	require.Equal(t, codes.Unauthenticated, codeOf(t, err))
}

func TestNew_RejectsBadKey(t *testing.T) {
	_, err := New(newFakeStore(), &fakeManager{}, &fakeOIDC{}, &fakeGroups{}, Config{
		MasterKey:   []byte("short"),
		SemesterFor: testSemesterFor,
	})
	require.Error(t, err)
}

func TestNew_RejectsNilSemesterResolver(t *testing.T) {
	_, err := New(newFakeStore(), &fakeManager{}, &fakeOIDC{}, &fakeGroups{}, Config{
		MasterKey: decodeKey(t),
	})
	require.Error(t, err)
}

func TestStoreCredentials_PersistsHealthGroup(t *testing.T) {
	s, st, _, _, _ := newServerWithMocks(t)

	_, err := s.StoreCredentials(context.Background(), &bmstuv1.StoreCredentialsRequest{
		UserId:      "u1",
		Login:       "ivan",
		Password:    "p@ss",
		HealthGroup: commonv1.HealthGroup_HEALTH_GROUP_PREPARATORY,
	})
	require.NoError(t, err)
	require.Equal(t, "PREPARATORY", st.creds["u1"].HealthGroup)
	require.Equal(t, "PREPARATORY", st.credStatus["u1"].HealthGroup)
}

// TestStoreCredentials_UnspecifiedDefaultsBasic — UNSPECIFIED enum
// нормализуется в "BASIC" (соответствует DEFAULT в схеме).
func TestStoreCredentials_UnspecifiedDefaultsBasic(t *testing.T) {
	s, st, _, _, _ := newServerWithMocks(t)

	_, err := s.StoreCredentials(context.Background(), &bmstuv1.StoreCredentialsRequest{
		UserId:   "u1",
		Login:    "ivan",
		Password: "p@ss",
		// HealthGroup намеренно опущен → UNSPECIFIED.
	})
	require.NoError(t, err)
	require.Equal(t, "BASIC", st.creds["u1"].HealthGroup)
}

func TestGetStatus_ReturnsHealthGroup(t *testing.T) {
	s, st, _, _, _ := newServerWithMocks(t)
	st.credStatus["u1"] = store.BmstuCredentialStatus{
		UserID:      "u1",
		HealthGroup: "AFK",
	}

	resp, err := s.GetStatus(context.Background(), &bmstuv1.GetStatusRequest{UserId: "u1"})
	require.NoError(t, err)
	require.Equal(t, commonv1.HealthGroup_HEALTH_GROUP_AFK, resp.GetHealthGroup())
}
