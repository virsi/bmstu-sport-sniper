// Package groups — клиент LKS API /lks-back/api/v1/fv/{semester}/groups.
//
// Принимает уже аутентифицированный *http.Client (с cookies) от session.Manager
// и парсит JSON-схему `[]Day{Groups: []Group}` из legacy_main.py:299-309.
//
// Дедупликация slot.id — детерминированный sha1 по архитектурному ADR:
// sha1(semester|week|time|section|teacher_uid). НЕ используем id из API,
// так как он недостаточно стабилен.
package groups

import (
	"context"
	"crypto/sha1" //nolint:gosec // используется как стабильный hash для slot.id, не для security
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	commonv1 "github.com/fizcultor/backend/gen/common/v1"

	"github.com/fizcultor/backend/services/bmstu-svc/internal/oidc"
)

// dayDTO — внешняя JSON-схема (легаси).
type dayDTO struct {
	Groups []groupDTO `json:"groups"`
	// Прочие поля дня (date и пр.) не используем.
}

// groupDTO — внешняя JSON-схема одного слота-группы.
type groupDTO struct {
	ID          json.Number `json:"id"`
	Week        int         `json:"week"`
	Time        string      `json:"time"`
	Section     string      `json:"section"`
	Place       string      `json:"place"`
	TeacherName string      `json:"teacherName"`
	TeacherUID  string      `json:"teacherUid"`
	Vacancy     int         `json:"vacancy"`
}

// Client — обёртка над http.Client, отвечающая за один эндпоинт /groups.
type Client struct {
	baseURL string
}

// New строит Client. baseURL — без trailing slash (например https://lks.bmstu.ru).
func New(baseURL string) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/")}
}

// Fetch запрашивает /lks-back/api/v1/fv/<semester>/groups через переданный
// httpClient (с cookies), парсит JSON и возвращает плоский []*commonv1.Slot.
//
// Ошибки:
//   - oidc.ErrSessionExpired — на 401/403.
//   - oidc.ErrRateLimited    — на 429.
//   - oidc.ErrUnexpectedResponse — на прочие 5xx/невалидный JSON.
func (c *Client) Fetch(ctx context.Context, httpClient *http.Client, semesterUUID string) ([]*commonv1.Slot, error) {
	if httpClient == nil {
		return nil, errors.New("groups: nil http client")
	}
	if semesterUUID == "" {
		return nil, errors.New("groups: empty semester uuid")
	}

	endpoint := fmt.Sprintf("%s/lks-back/api/v1/fv/%s/groups", c.baseURL, semesterUUID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("groups: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", oidc.DefaultUserAgent)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("groups: request: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// ok
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, oidc.ErrSessionExpired
	case http.StatusTooManyRequests:
		return nil, oidc.ErrRateLimited
	default:
		return nil, fmt.Errorf("%w: groups status %d", oidc.ErrUnexpectedResponse, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("groups: read body: %w", err)
	}

	// Пустой ответ — норма (нет занятий в этот период).
	if len(body) == 0 || string(body) == "null" {
		return []*commonv1.Slot{}, nil
	}

	var days []dayDTO
	if err := json.Unmarshal(body, &days); err != nil {
		return nil, fmt.Errorf("%w: decode: %v", oidc.ErrUnexpectedResponse, err)
	}

	out := make([]*commonv1.Slot, 0, 32)
	for _, d := range days {
		for _, g := range d.Groups {
			out = append(out, toSlot(g, semesterUUID))
		}
	}
	return out, nil
}

// clampInt32 безопасно сужает int (из внешнего JSON) до int32: в proto-схеме
// поля week/vacancy объявлены int32, на практике значения в диапазоне 0..~100,
// но мы защищаемся от потенциального overflow при поломке схемы LKS.
func clampInt32(v int) int32 {
	const maxI32 = 1<<31 - 1
	const minI32 = -1 << 31
	switch {
	case v > maxI32:
		return maxI32
	case v < minI32:
		return minI32
	default:
		return int32(v)
	}
}

// toSlot конвертирует groupDTO в proto Slot с детерминированным id.
func toSlot(g groupDTO, semesterUUID string) *commonv1.Slot {
	slot := &commonv1.Slot{
		Id:           buildSlotID(semesterUUID, g),
		Week:         clampInt32(g.Week),
		Time:         g.Time,
		Place:        g.Place,
		TeacherName:  g.TeacherName,
		Vacancy:      clampInt32(g.Vacancy),
		SemesterUuid: semesterUUID,
		// DayOfWeek вычисляется отдельно (week+time+semester), здесь UNSPECIFIED.
		DayOfWeek: commonv1.DayOfWeek_DAY_OF_WEEK_UNSPECIFIED,
	}
	if g.Section != "" {
		s := g.Section
		slot.Section = &s
	}
	if g.TeacherUID != "" {
		uid := g.TeacherUID
		slot.TeacherUid = &uid
	}
	return slot
}

// buildSlotID — детерминированный sha1 по архитектурному ADR.
// Совпадение хеша между опросами = тот же слот для целей дедупликации.
func buildSlotID(semesterUUID string, g groupDTO) string {
	parts := []string{
		semesterUUID,
		fmt.Sprintf("%d", g.Week),
		g.Time,
		g.Section,
		g.TeacherUID,
	}
	h := sha1.New() //nolint:gosec // stable hash, не security primitive
	_, _ = io.WriteString(h, strings.Join(parts, "|"))
	return "sha1:" + hex.EncodeToString(h.Sum(nil))
}
