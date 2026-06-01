package groups

import (
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fizcultor/backend/services/bmstu-svc/internal/oidc"
)

// goldenGroups — фикстура из формата LKS:
//   - один день, два слота;
//   - один слот с teacherUid пустым (legacy формат).
const goldenGroups = `[
  {
    "groups": [
      {
        "id": 1001,
        "week": 14,
        "time": "18:00-19:30",
        "section": "Аэробика",
        "place": "СК «Дворец», зал 3",
        "teacherName": "Иванова Анна Петровна",
        "teacherUid": "uid_42",
        "vacancy": 2
      },
      {
        "id": 1002,
        "week": 14,
        "time": "19:30-21:00",
        "section": "Силовая",
        "place": "СК «Дворец», зал 5",
        "teacherName": "Петров П. П.",
        "teacherUid": "",
        "vacancy": 0
      }
    ]
  }
]`

func newAuthedClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	return &http.Client{Jar: jar}
}

func TestFetch_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/lks-back/api/v1/fv/sem-uuid-1/groups", r.URL.Path)
		require.NotEmpty(t, r.Header.Get("User-Agent"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(goldenGroups))
	}))
	t.Cleanup(srv.Close)

	c := New(srv.URL)
	slots, err := c.Fetch(context.Background(), newAuthedClient(t), "sem-uuid-1")
	require.NoError(t, err)
	require.Len(t, slots, 2)

	first := slots[0]
	require.Equal(t, int32(14), first.Week)
	require.Equal(t, "18:00-19:30", first.Time)
	require.NotNil(t, first.Section)
	require.Equal(t, "Аэробика", *first.Section)
	require.Equal(t, "Иванова Анна Петровна", first.TeacherName)
	require.NotNil(t, first.TeacherUid)
	require.Equal(t, "uid_42", *first.TeacherUid)
	require.Equal(t, int32(2), first.Vacancy)
	require.Equal(t, "sem-uuid-1", first.SemesterUuid)
	require.Contains(t, first.Id, "sha1:")

	// Второй слот: teacherUid отсутствует → optional поле nil.
	second := slots[1]
	require.Nil(t, second.TeacherUid)
}

func TestFetch_EmptyDays(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)

	c := New(srv.URL)
	slots, err := c.Fetch(context.Background(), newAuthedClient(t), "sem")
	require.NoError(t, err)
	require.Empty(t, slots)
}

func TestFetch_NullBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`null`))
	}))
	t.Cleanup(srv.Close)

	c := New(srv.URL)
	slots, err := c.Fetch(context.Background(), newAuthedClient(t), "sem")
	require.NoError(t, err)
	require.Empty(t, slots)
}

func TestFetch_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	c := New(srv.URL)
	_, err := c.Fetch(context.Background(), newAuthedClient(t), "sem")
	require.True(t, errors.Is(err, oidc.ErrSessionExpired), "got %v", err)
}

func TestFetch_Forbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	c := New(srv.URL)
	_, err := c.Fetch(context.Background(), newAuthedClient(t), "sem")
	require.True(t, errors.Is(err, oidc.ErrSessionExpired))
}

func TestFetch_RateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)

	c := New(srv.URL)
	_, err := c.Fetch(context.Background(), newAuthedClient(t), "sem")
	require.True(t, errors.Is(err, oidc.ErrRateLimited))
}

func TestFetch_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)

	c := New(srv.URL)
	_, err := c.Fetch(context.Background(), newAuthedClient(t), "sem")
	require.True(t, errors.Is(err, oidc.ErrUnexpectedResponse))
}

func TestFetch_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"not":"a list"}`))
	}))
	t.Cleanup(srv.Close)

	c := New(srv.URL)
	_, err := c.Fetch(context.Background(), newAuthedClient(t), "sem")
	require.True(t, errors.Is(err, oidc.ErrUnexpectedResponse))
}

func TestFetch_NilHTTPClient(t *testing.T) {
	c := New("https://x")
	_, err := c.Fetch(context.Background(), nil, "sem")
	require.Error(t, err)
}

func TestFetch_EmptySemester(t *testing.T) {
	c := New("https://x")
	_, err := c.Fetch(context.Background(), &http.Client{}, "")
	require.Error(t, err)
}

func TestBuildSlotID_Deterministic(t *testing.T) {
	g := groupDTO{Week: 14, Time: "18:00-19:30", Section: "Аэробика", TeacherUID: "u42"}
	a := buildSlotID("sem", g)
	b := buildSlotID("sem", g)
	require.Equal(t, a, b)

	// Изменение любого ключевого поля → другой id.
	g2 := g
	g2.TeacherUID = "u43"
	require.NotEqual(t, a, buildSlotID("sem", g2))
}

func TestBuildSlotID_ApiIDIsNotUsed(t *testing.T) {
	// id из API не должен влиять на синтетический slot id (ADR).
	g1 := groupDTO{ID: "1", Week: 14, Time: "18:00", Section: "x", TeacherUID: "u"}
	g2 := groupDTO{ID: "9999", Week: 14, Time: "18:00", Section: "x", TeacherUID: "u"}
	require.Equal(t, buildSlotID("sem", g1), buildSlotID("sem", g2))
}

func TestFetch_UsesAuthCookie(t *testing.T) {
	// Подтверждаем, что cookies из переданного jar реально летят на сервер.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got bool
		for _, c := range r.Cookies() {
			if c.Name == "p4sess" && c.Value == "abc" {
				got = true
			}
		}
		require.True(t, got, "p4sess cookie not sent")
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)

	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(srv.URL)
	jar.SetCookies(u, []*http.Cookie{{Name: "p4sess", Value: "abc"}})
	hc := &http.Client{Jar: jar}

	c := New(srv.URL)
	_, err := c.Fetch(context.Background(), hc, "sem")
	require.NoError(t, err)
}
