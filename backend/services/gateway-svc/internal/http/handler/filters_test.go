package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/fizcultor/backend/gen/common/v1"
	filterv1 "github.com/fizcultor/backend/gen/filter/v1"
)

// ptr возвращает указатель на переданное значение (тестовый хелпер).
func ptr[T any](v T) *T { return &v }

func TestHandler_ListFilters(t *testing.T) {
	t.Parallel()

	mock := &mockFilterClient{
		ListFiltersFn: func(_ context.Context, in *filterv1.ListFiltersRequest) (*filterv1.ListFiltersResponse, error) {
			assert.Equal(t, "u-1", in.GetUserId())
			assert.True(t, in.GetIncludeDisabled(), "default include_disabled=true")
			return &filterv1.ListFiltersResponse{
				Filters: []*commonv1.Filter{
					{
						Id:        "f-1",
						UserId:    "u-1",
						Section:   ptr("Аэробика"),
						DayOfWeek: commonv1.DayOfWeek_DAY_OF_WEEK_WEDNESDAY,
						TimeFrom:  ptr("18:00"),
						TimeTo:    ptr("21:00"),
						MinRating: ptr(4.0),
						Enabled:   true,
						CreatedAt: timestamppb.New(fixedTime),
						UpdatedAt: timestamppb.New(fixedTime),
					},
				},
			}, nil
		},
	}
	h := newHandler(t, withFilter(mock))
	mw := withUserCtx(t, "u-1")
	protected := mw(http.HandlerFunc(h.ListFilters))

	req := httptest.NewRequest(http.MethodGet, "/api/filters", http.NoBody)
	req.Header.Set("Authorization", "Bearer dummy")
	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))

	filters, ok := body["filters"].([]any)
	require.True(t, ok)
	require.Len(t, filters, 1)
	first := filters[0].(map[string]any)
	assert.Equal(t, "f-1", first["id"])
	assert.Equal(t, "Аэробика", first["section"])
	assert.Equal(t, "WEDNESDAY", first["day_of_week"])
	assert.Equal(t, "18:00", first["time_from"])
	assert.Equal(t, 4.0, first["min_rating"])
}

func TestHandler_ListFilters_IncludeDisabled(t *testing.T) {
	t.Parallel()

	mock := &mockFilterClient{
		ListFiltersFn: func(_ context.Context, in *filterv1.ListFiltersRequest) (*filterv1.ListFiltersResponse, error) {
			assert.False(t, in.GetIncludeDisabled(), "include_disabled=false")
			return &filterv1.ListFiltersResponse{}, nil
		},
	}
	h := newHandler(t, withFilter(mock))
	mw := withUserCtx(t, "u-1")
	protected := mw(http.HandlerFunc(h.ListFilters))

	req := httptest.NewRequest(http.MethodGet, "/api/filters?include_disabled=false", http.NoBody)
	req.Header.Set("Authorization", "Bearer dummy")
	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestHandler_ListFilters_BadIncludeDisabled(t *testing.T) {
	t.Parallel()

	h := newHandler(t)
	mw := withUserCtx(t, "u-1")
	protected := mw(http.HandlerFunc(h.ListFilters))

	req := httptest.NewRequest(http.MethodGet, "/api/filters?include_disabled=maybe", http.NoBody)
	req.Header.Set("Authorization", "Bearer dummy")
	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandler_CreateFilter(t *testing.T) {
	t.Parallel()

	type tc struct {
		name       string
		body       string
		check      func(*testing.T, *filterv1.CreateFilterRequest)
		wantStatus int
	}

	cases := []tc{
		{
			name: "happy: minimum fields",
			body: `{"section":"Йога","day_of_week":"MONDAY"}`,
			check: func(t *testing.T, req *filterv1.CreateFilterRequest) {
				assert.Equal(t, "u-1", req.GetUserId())
				assert.Equal(t, "Йога", req.GetSection())
				assert.Equal(t, commonv1.DayOfWeek_DAY_OF_WEEK_MONDAY, req.GetDayOfWeek())
				// enabled должен default-нуться в true
				assert.True(t, req.GetEnabled())
			},
			wantStatus: http.StatusCreated,
		},
		{
			name: "happy: ANY day",
			body: `{"day_of_week":"ANY"}`,
			check: func(t *testing.T, req *filterv1.CreateFilterRequest) {
				assert.Equal(t, commonv1.DayOfWeek_DAY_OF_WEEK_UNSPECIFIED, req.GetDayOfWeek())
			},
			wantStatus: http.StatusCreated,
		},
		{
			name:       "invalid day_of_week",
			body:       `{"day_of_week":"FUNDAY"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "explicit enabled=false",
			body: `{"enabled":false}`,
			check: func(t *testing.T, req *filterv1.CreateFilterRequest) {
				assert.False(t, req.GetEnabled())
			},
			wantStatus: http.StatusCreated,
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var captured *filterv1.CreateFilterRequest
			mock := &mockFilterClient{
				CreateFilterFn: func(_ context.Context, in *filterv1.CreateFilterRequest) (*filterv1.CreateFilterResponse, error) {
					captured = in
					return &filterv1.CreateFilterResponse{
						Filter: &commonv1.Filter{
							Id:        "f-new",
							UserId:    in.GetUserId(),
							Section:   in.Section,
							DayOfWeek: in.GetDayOfWeek(),
							Enabled:   in.GetEnabled(),
							CreatedAt: timestamppb.New(fixedTime),
							UpdatedAt: timestamppb.New(fixedTime),
						},
					}, nil
				},
			}
			h := newHandler(t, withFilter(mock))
			mw := withUserCtx(t, "u-1")
			protected := mw(http.HandlerFunc(h.CreateFilter))

			req := httptest.NewRequest(http.MethodPost, "/api/filters", strings.NewReader(c.body))
			req.Header.Set("Authorization", "Bearer dummy")
			rec := httptest.NewRecorder()
			protected.ServeHTTP(rec, req)

			assert.Equal(t, c.wantStatus, rec.Code, "status; body=%s", rec.Body.String())
			if c.check != nil && captured != nil {
				c.check(t, captured)
			}
		})
	}
}

func TestHandler_UpdateFilter(t *testing.T) {
	t.Parallel()

	var captured *filterv1.UpdateFilterRequest
	mock := &mockFilterClient{
		UpdateFilterFn: func(_ context.Context, in *filterv1.UpdateFilterRequest) (*filterv1.UpdateFilterResponse, error) {
			captured = in
			return &filterv1.UpdateFilterResponse{
				Filter: &commonv1.Filter{Id: in.GetFilterId(), UserId: in.GetUserId()},
			}, nil
		},
	}
	h := newHandler(t, withFilter(mock))

	t.Run("partial: only enabled", func(t *testing.T) {
		captured = nil
		mw := withUserCtx(t, "u-1")
		// Используем chi-router, чтобы URL params парсились.
		r := chi.NewRouter()
		r.Patch("/api/filters/{id}", mw(http.HandlerFunc(h.UpdateFilter)).ServeHTTP)

		req := httptest.NewRequest(http.MethodPatch, "/api/filters/f-99",
			strings.NewReader(`{"enabled":false}`))
		req.Header.Set("Authorization", "Bearer dummy")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		require.NotNil(t, captured)
		assert.Equal(t, "f-99", captured.GetFilterId())
		assert.Equal(t, []string{"enabled"}, captured.GetUpdateMask())
		require.NotNil(t, captured.Enabled)
		assert.False(t, *captured.Enabled)
	})

	t.Run("null clears section", func(t *testing.T) {
		captured = nil
		mw := withUserCtx(t, "u-1")
		r := chi.NewRouter()
		r.Patch("/api/filters/{id}", mw(http.HandlerFunc(h.UpdateFilter)).ServeHTTP)

		req := httptest.NewRequest(http.MethodPatch, "/api/filters/f-1",
			strings.NewReader(`{"section":null}`))
		req.Header.Set("Authorization", "Bearer dummy")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		require.NotNil(t, captured)
		assert.Contains(t, captured.GetUpdateMask(), "section")
		assert.Nil(t, captured.Section, "null section → *string nil")
	})

	t.Run("multiple fields with mask", func(t *testing.T) {
		captured = nil
		mw := withUserCtx(t, "u-1")
		r := chi.NewRouter()
		r.Patch("/api/filters/{id}", mw(http.HandlerFunc(h.UpdateFilter)).ServeHTTP)

		body := `{"section":"Силовая","time_from":"08:00","min_rating":3.5,"enabled":true}`
		req := httptest.NewRequest(http.MethodPatch, "/api/filters/f-1", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer dummy")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		require.NotNil(t, captured)
		mask := captured.GetUpdateMask()
		assert.ElementsMatch(t, []string{"section", "time_from", "min_rating", "enabled"}, mask)
		require.NotNil(t, captured.Section)
		assert.Equal(t, "Силовая", *captured.Section)
		require.NotNil(t, captured.MinRating)
		assert.InDelta(t, 3.5, *captured.MinRating, 0.001)
	})
}

func TestHandler_DeleteFilter(t *testing.T) {
	t.Parallel()

	var called bool
	mock := &mockFilterClient{
		DeleteFilterFn: func(_ context.Context, in *filterv1.DeleteFilterRequest) (*filterv1.DeleteFilterResponse, error) {
			called = true
			assert.Equal(t, "u-1", in.GetUserId())
			assert.Equal(t, "f-42", in.GetFilterId())
			return &filterv1.DeleteFilterResponse{}, nil
		},
	}
	h := newHandler(t, withFilter(mock))

	mw := withUserCtx(t, "u-1")
	r := chi.NewRouter()
	r.Delete("/api/filters/{id}", mw(http.HandlerFunc(h.DeleteFilter)).ServeHTTP)

	req := httptest.NewRequest(http.MethodDelete, "/api/filters/f-42", http.NoBody)
	req.Header.Set("Authorization", "Bearer dummy")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.True(t, called)
}

func TestHandler_DeleteFilter_NotFound(t *testing.T) {
	t.Parallel()

	mock := &mockFilterClient{
		DeleteFilterFn: func(context.Context, *filterv1.DeleteFilterRequest) (*filterv1.DeleteFilterResponse, error) {
			return nil, status.Error(codes.NotFound, "not found")
		},
	}
	h := newHandler(t, withFilter(mock))

	mw := withUserCtx(t, "u-1")
	r := chi.NewRouter()
	r.Delete("/api/filters/{id}", mw(http.HandlerFunc(h.DeleteFilter)).ServeHTTP)

	req := httptest.NewRequest(http.MethodDelete, "/api/filters/missing", http.NoBody)
	req.Header.Set("Authorization", "Bearer dummy")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandler_DeleteFilter_OthersFilter_Forbidden(t *testing.T) {
	t.Parallel()

	mock := &mockFilterClient{
		DeleteFilterFn: func(context.Context, *filterv1.DeleteFilterRequest) (*filterv1.DeleteFilterResponse, error) {
			return nil, status.Error(codes.PermissionDenied, "not owner")
		},
	}
	h := newHandler(t, withFilter(mock))

	mw := withUserCtx(t, "u-1")
	r := chi.NewRouter()
	r.Delete("/api/filters/{id}", mw(http.HandlerFunc(h.DeleteFilter)).ServeHTTP)

	req := httptest.NewRequest(http.MethodDelete, "/api/filters/someone-elses", http.NoBody)
	req.Header.Set("Authorization", "Bearer dummy")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}
