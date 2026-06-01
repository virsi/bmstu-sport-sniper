package handler_test

import (
	"context"
	"errors"

	"google.golang.org/grpc"

	authv1 "github.com/fizcultor/backend/gen/auth/v1"
	bmstuv1 "github.com/fizcultor/backend/gen/bmstu/v1"
	commonv1 "github.com/fizcultor/backend/gen/common/v1"
	filterv1 "github.com/fizcultor/backend/gen/filter/v1"
	notifierv1 "github.com/fizcultor/backend/gen/notifier/v1"
	teachersv1 "github.com/fizcultor/backend/gen/teachers/v1"
)

// errUnused — маркер «эндпоинт не должен дёргаться в этом тесте».
var errUnused = errors.New("mock: method not configured")

// dummyVerifier — мини-мок middleware.AuthVerifier для тестов хендлеров,
// которым нужен user_id уже в r.Context(). Не покрывает edge-кейсы Auth —
// для этого есть тесты middleware/auth_test.go.
type dummyVerifier struct {
	userID string
}

func (d *dummyVerifier) VerifyAccess(_ context.Context, _ *authv1.VerifyAccessRequest) (*authv1.VerifyAccessResponse, error) {
	return &authv1.VerifyAccessResponse{UserId: d.userID}, nil
}

// mockAuthClient — реализация authv1.AuthServiceClient с задаваемыми ответами.
type mockAuthClient struct {
	RegisterFn             func(ctx context.Context, in *authv1.RegisterRequest) (*authv1.RegisterResponse, error)
	LoginFn                func(ctx context.Context, in *authv1.LoginRequest) (*authv1.TokenPair, error)
	RefreshFn              func(ctx context.Context, in *authv1.RefreshRequest) (*authv1.TokenPair, error)
	RevokeFn               func(ctx context.Context, in *authv1.RevokeRequest) (*authv1.RevokeResponse, error)
	GetMeFn                func(ctx context.Context, in *authv1.GetMeRequest) (*commonv1.User, error)
	VerifyAccessFn         func(ctx context.Context, in *authv1.VerifyAccessRequest) (*authv1.VerifyAccessResponse, error)
	LinkTelegramInitFn     func(ctx context.Context, in *authv1.LinkTelegramInitRequest) (*authv1.LinkTelegramInitResponse, error)
	LinkTelegramCompleteFn func(ctx context.Context, in *authv1.LinkTelegramCompleteRequest) (*authv1.LinkTelegramCompleteResponse, error)
}

func (m *mockAuthClient) Register(ctx context.Context, in *authv1.RegisterRequest, _ ...grpc.CallOption) (*authv1.RegisterResponse, error) {
	if m.RegisterFn == nil {
		return nil, errUnused
	}
	return m.RegisterFn(ctx, in)
}

func (m *mockAuthClient) Login(ctx context.Context, in *authv1.LoginRequest, _ ...grpc.CallOption) (*authv1.TokenPair, error) {
	if m.LoginFn == nil {
		return nil, errUnused
	}
	return m.LoginFn(ctx, in)
}

func (m *mockAuthClient) Refresh(ctx context.Context, in *authv1.RefreshRequest, _ ...grpc.CallOption) (*authv1.TokenPair, error) {
	if m.RefreshFn == nil {
		return nil, errUnused
	}
	return m.RefreshFn(ctx, in)
}

func (m *mockAuthClient) Revoke(ctx context.Context, in *authv1.RevokeRequest, _ ...grpc.CallOption) (*authv1.RevokeResponse, error) {
	if m.RevokeFn == nil {
		return nil, errUnused
	}
	return m.RevokeFn(ctx, in)
}

// GetMe возвращает *common.v1.User.
func (m *mockAuthClient) GetMe(ctx context.Context, in *authv1.GetMeRequest, _ ...grpc.CallOption) (*commonv1.User, error) {
	if m.GetMeFn == nil {
		return nil, errUnused
	}
	return m.GetMeFn(ctx, in)
}

func (m *mockAuthClient) VerifyAccess(ctx context.Context, in *authv1.VerifyAccessRequest, _ ...grpc.CallOption) (*authv1.VerifyAccessResponse, error) {
	if m.VerifyAccessFn == nil {
		return nil, errUnused
	}
	return m.VerifyAccessFn(ctx, in)
}

func (m *mockAuthClient) LinkTelegramInit(ctx context.Context, in *authv1.LinkTelegramInitRequest, _ ...grpc.CallOption) (*authv1.LinkTelegramInitResponse, error) {
	if m.LinkTelegramInitFn == nil {
		return nil, errUnused
	}
	return m.LinkTelegramInitFn(ctx, in)
}

func (m *mockAuthClient) LinkTelegramComplete(ctx context.Context, in *authv1.LinkTelegramCompleteRequest, _ ...grpc.CallOption) (*authv1.LinkTelegramCompleteResponse, error) {
	if m.LinkTelegramCompleteFn == nil {
		return nil, errUnused
	}
	return m.LinkTelegramCompleteFn(ctx, in)
}

// mockBmstuClient — реализация bmstuv1.BmstuServiceClient.
type mockBmstuClient struct {
	StoreCredentialsFn  func(ctx context.Context, in *bmstuv1.StoreCredentialsRequest) (*bmstuv1.StoreCredentialsResponse, error)
	DeleteCredentialsFn func(ctx context.Context, in *bmstuv1.DeleteCredentialsRequest) (*bmstuv1.DeleteCredentialsResponse, error)
	GetStatusFn         func(ctx context.Context, in *bmstuv1.GetStatusRequest) (*bmstuv1.GetStatusResponse, error)
	FetchGroupsFn       func(ctx context.Context, in *bmstuv1.FetchGroupsRequest) (*bmstuv1.FetchGroupsResponse, error)
	RefreshSessionFn    func(ctx context.Context, in *bmstuv1.RefreshSessionRequest) (*bmstuv1.RefreshSessionResponse, error)
}

func (m *mockBmstuClient) StoreCredentials(ctx context.Context, in *bmstuv1.StoreCredentialsRequest, _ ...grpc.CallOption) (*bmstuv1.StoreCredentialsResponse, error) {
	if m.StoreCredentialsFn == nil {
		return nil, errUnused
	}
	return m.StoreCredentialsFn(ctx, in)
}

func (m *mockBmstuClient) DeleteCredentials(ctx context.Context, in *bmstuv1.DeleteCredentialsRequest, _ ...grpc.CallOption) (*bmstuv1.DeleteCredentialsResponse, error) {
	if m.DeleteCredentialsFn == nil {
		return nil, errUnused
	}
	return m.DeleteCredentialsFn(ctx, in)
}

func (m *mockBmstuClient) GetStatus(ctx context.Context, in *bmstuv1.GetStatusRequest, _ ...grpc.CallOption) (*bmstuv1.GetStatusResponse, error) {
	if m.GetStatusFn == nil {
		return nil, errUnused
	}
	return m.GetStatusFn(ctx, in)
}

func (m *mockBmstuClient) FetchGroups(ctx context.Context, in *bmstuv1.FetchGroupsRequest, _ ...grpc.CallOption) (*bmstuv1.FetchGroupsResponse, error) {
	if m.FetchGroupsFn == nil {
		return nil, errUnused
	}
	return m.FetchGroupsFn(ctx, in)
}

func (m *mockBmstuClient) RefreshSession(ctx context.Context, in *bmstuv1.RefreshSessionRequest, _ ...grpc.CallOption) (*bmstuv1.RefreshSessionResponse, error) {
	if m.RefreshSessionFn == nil {
		return nil, errUnused
	}
	return m.RefreshSessionFn(ctx, in)
}

// mockFilterClient — реализация filterv1.FilterServiceClient.
type mockFilterClient struct {
	CreateFilterFn func(ctx context.Context, in *filterv1.CreateFilterRequest) (*filterv1.CreateFilterResponse, error)
	GetFilterFn    func(ctx context.Context, in *filterv1.GetFilterRequest) (*filterv1.GetFilterResponse, error)
	ListFiltersFn  func(ctx context.Context, in *filterv1.ListFiltersRequest) (*filterv1.ListFiltersResponse, error)
	UpdateFilterFn func(ctx context.Context, in *filterv1.UpdateFilterRequest) (*filterv1.UpdateFilterResponse, error)
	DeleteFilterFn func(ctx context.Context, in *filterv1.DeleteFilterRequest) (*filterv1.DeleteFilterResponse, error)
	MatchSlotsFn   func(ctx context.Context, in *filterv1.MatchSlotsRequest) (*filterv1.MatchSlotsResponse, error)
	MarkSeenFn     func(ctx context.Context, in *filterv1.MarkSeenRequest) (*filterv1.MarkSeenResponse, error)
	ResetKnownFn   func(ctx context.Context, in *filterv1.ResetKnownRequest) (*filterv1.ResetKnownResponse, error)
}

func (m *mockFilterClient) CreateFilter(ctx context.Context, in *filterv1.CreateFilterRequest, _ ...grpc.CallOption) (*filterv1.CreateFilterResponse, error) {
	if m.CreateFilterFn == nil {
		return nil, errUnused
	}
	return m.CreateFilterFn(ctx, in)
}

func (m *mockFilterClient) GetFilter(ctx context.Context, in *filterv1.GetFilterRequest, _ ...grpc.CallOption) (*filterv1.GetFilterResponse, error) {
	if m.GetFilterFn == nil {
		return nil, errUnused
	}
	return m.GetFilterFn(ctx, in)
}

func (m *mockFilterClient) ListFilters(ctx context.Context, in *filterv1.ListFiltersRequest, _ ...grpc.CallOption) (*filterv1.ListFiltersResponse, error) {
	if m.ListFiltersFn == nil {
		return nil, errUnused
	}
	return m.ListFiltersFn(ctx, in)
}

func (m *mockFilterClient) UpdateFilter(ctx context.Context, in *filterv1.UpdateFilterRequest, _ ...grpc.CallOption) (*filterv1.UpdateFilterResponse, error) {
	if m.UpdateFilterFn == nil {
		return nil, errUnused
	}
	return m.UpdateFilterFn(ctx, in)
}

func (m *mockFilterClient) DeleteFilter(ctx context.Context, in *filterv1.DeleteFilterRequest, _ ...grpc.CallOption) (*filterv1.DeleteFilterResponse, error) {
	if m.DeleteFilterFn == nil {
		return nil, errUnused
	}
	return m.DeleteFilterFn(ctx, in)
}

func (m *mockFilterClient) MatchSlots(ctx context.Context, in *filterv1.MatchSlotsRequest, _ ...grpc.CallOption) (*filterv1.MatchSlotsResponse, error) {
	if m.MatchSlotsFn == nil {
		return nil, errUnused
	}
	return m.MatchSlotsFn(ctx, in)
}

func (m *mockFilterClient) MarkSeen(ctx context.Context, in *filterv1.MarkSeenRequest, _ ...grpc.CallOption) (*filterv1.MarkSeenResponse, error) {
	if m.MarkSeenFn == nil {
		return nil, errUnused
	}
	return m.MarkSeenFn(ctx, in)
}

func (m *mockFilterClient) ResetKnown(ctx context.Context, in *filterv1.ResetKnownRequest, _ ...grpc.CallOption) (*filterv1.ResetKnownResponse, error) {
	if m.ResetKnownFn == nil {
		return nil, errUnused
	}
	return m.ResetKnownFn(ctx, in)
}

// mockNotifierClient — реализация notifierv1.NotifierServiceClient.
type mockNotifierClient struct {
	NotifyMatchedFn        func(ctx context.Context, in *notifierv1.NotifyMatchedRequest) (*notifierv1.NotifyMatchedResponse, error)
	SendDirectFn           func(ctx context.Context, in *notifierv1.SendDirectRequest) (*notifierv1.SendDirectResponse, error)
	RegisterTelegramChatFn func(ctx context.Context, in *notifierv1.RegisterTelegramChatRequest) (*notifierv1.RegisterTelegramChatResponse, error)
}

func (m *mockNotifierClient) NotifyMatched(ctx context.Context, in *notifierv1.NotifyMatchedRequest, _ ...grpc.CallOption) (*notifierv1.NotifyMatchedResponse, error) {
	if m.NotifyMatchedFn == nil {
		return nil, errUnused
	}
	return m.NotifyMatchedFn(ctx, in)
}

func (m *mockNotifierClient) SendDirect(ctx context.Context, in *notifierv1.SendDirectRequest, _ ...grpc.CallOption) (*notifierv1.SendDirectResponse, error) {
	if m.SendDirectFn == nil {
		return nil, errUnused
	}
	return m.SendDirectFn(ctx, in)
}

func (m *mockNotifierClient) RegisterTelegramChat(ctx context.Context, in *notifierv1.RegisterTelegramChatRequest, _ ...grpc.CallOption) (*notifierv1.RegisterTelegramChatResponse, error) {
	if m.RegisterTelegramChatFn == nil {
		return nil, errUnused
	}
	return m.RegisterTelegramChatFn(ctx, in)
}

// mockTeachersClient — реализация teachersv1.TeachersServiceClient.
type mockTeachersClient struct {
	GetFn      func(ctx context.Context, in *teachersv1.GetRequest) (*teachersv1.GetResponse, error)
	BatchGetFn func(ctx context.Context, in *teachersv1.BatchGetRequest) (*teachersv1.BatchGetResponse, error)
	ListFn     func(ctx context.Context, in *teachersv1.ListRequest) (*teachersv1.ListResponse, error)
	RefreshFn  func(ctx context.Context, in *teachersv1.RefreshRequest) (*teachersv1.RefreshResponse, error)
}

func (m *mockTeachersClient) Get(ctx context.Context, in *teachersv1.GetRequest, _ ...grpc.CallOption) (*teachersv1.GetResponse, error) {
	if m.GetFn == nil {
		return nil, errUnused
	}
	return m.GetFn(ctx, in)
}

func (m *mockTeachersClient) BatchGet(ctx context.Context, in *teachersv1.BatchGetRequest, _ ...grpc.CallOption) (*teachersv1.BatchGetResponse, error) {
	if m.BatchGetFn == nil {
		return nil, errUnused
	}
	return m.BatchGetFn(ctx, in)
}

func (m *mockTeachersClient) List(ctx context.Context, in *teachersv1.ListRequest, _ ...grpc.CallOption) (*teachersv1.ListResponse, error) {
	if m.ListFn == nil {
		return nil, errUnused
	}
	return m.ListFn(ctx, in)
}

func (m *mockTeachersClient) Refresh(ctx context.Context, in *teachersv1.RefreshRequest, _ ...grpc.CallOption) (*teachersv1.RefreshResponse, error) {
	if m.RefreshFn == nil {
		return nil, errUnused
	}
	return m.RefreshFn(ctx, in)
}
