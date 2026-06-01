package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound — запись в БД не найдена. Сервисный слой маппит в errs.NotFound /
// gRPC NotFound.
var ErrNotFound = errors.New("store: not found")

// Store — фасад над pgxpool.Pool с типизированными методами доступа к filter_db.
type Store struct {
	pool *pgxpool.Pool
}

// New создаёт Store с готовым pgxpool.Pool.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// ----------------------------------------------------------------------------
// filters
// ----------------------------------------------------------------------------

const filterColumns = "id, user_id, section, teacher_uid, day_of_week, time_from, time_to, min_rating, min_vacancy, enabled, created_at, updated_at"

// CreateFilterParams — параметры CreateFilter.
type CreateFilterParams struct {
	// UserID — владелец.
	UserID int64
	// Section — фильтр по секции (nil = любая).
	Section *string
	// TeacherUID — фильтр по преподавателю (nil = любой).
	TeacherUID *string
	// DayOfWeek — день недели (nil = любой).
	DayOfWeek *string
	// TimeFrom — нижняя граница времени.
	TimeFrom *TimeOfDay
	// TimeTo — верхняя граница времени.
	TimeTo *TimeOfDay
	// MinRating — минимальный рейтинг.
	MinRating *float64
	// MinVacancy — минимум свободных мест.
	MinVacancy int32
	// Enabled — активность.
	Enabled bool
}

// CreateFilter вставляет новый фильтр.
func (s *Store) CreateFilter(ctx context.Context, p CreateFilterParams) (Filter, error) {
	const q = "INSERT INTO filters (user_id, section, teacher_uid, day_of_week, time_from, time_to, min_rating, min_vacancy, enabled) " +
		"VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING " + filterColumns
	row := s.pool.QueryRow(ctx, q,
		p.UserID,
		p.Section,
		p.TeacherUID,
		p.DayOfWeek,
		valueOrNil(p.TimeFrom),
		valueOrNil(p.TimeTo),
		p.MinRating,
		p.MinVacancy,
		p.Enabled,
	)
	return scanFilter(row)
}

// GetFilterByID возвращает фильтр по id. ErrNotFound если нет.
func (s *Store) GetFilterByID(ctx context.Context, id int64) (Filter, error) {
	const q = "SELECT " + filterColumns + " FROM filters WHERE id = $1"
	row := s.pool.QueryRow(ctx, q, id)
	return scanFilter(row)
}

// ListFiltersByUser возвращает фильтры пользователя, отсортированные по created_at DESC.
// Если includeDisabled=false — только enabled=true.
func (s *Store) ListFiltersByUser(ctx context.Context, userID int64, includeDisabled bool) ([]Filter, error) {
	const q = "SELECT " + filterColumns + " FROM filters " +
		"WHERE user_id = $1 AND ($2 OR enabled) ORDER BY created_at DESC"
	rows, err := s.pool.Query(ctx, q, userID, includeDisabled)
	if err != nil {
		return nil, fmt.Errorf("store: list filters: %w", err)
	}
	defer rows.Close()
	return scanFilters(rows)
}

// UpdateFilterParams — поля для UpdateFilter.
type UpdateFilterParams struct {
	// ID — фильтр.
	ID int64
	// UserID — владелец (для безопасной проверки в WHERE).
	UserID int64
	// Section — новое значение (nil = снять ограничение).
	Section *string
	// TeacherUID — новое значение.
	TeacherUID *string
	// DayOfWeek — новый день.
	DayOfWeek *string
	// TimeFrom — новое время.
	TimeFrom *TimeOfDay
	// TimeTo — новое время.
	TimeTo *TimeOfDay
	// MinRating — новый рейтинг.
	MinRating *float64
	// Enabled — новый флаг.
	Enabled bool
}

// UpdateFilter обновляет фильтр, который принадлежит указанному user_id.
// ErrNotFound если такого нет.
func (s *Store) UpdateFilter(ctx context.Context, p UpdateFilterParams) (Filter, error) {
	const q = "UPDATE filters SET " +
		"section = $3, teacher_uid = $4, day_of_week = $5, " +
		"time_from = $6, time_to = $7, min_rating = $8, enabled = $9, updated_at = now() " +
		"WHERE id = $1 AND user_id = $2 RETURNING " + filterColumns
	row := s.pool.QueryRow(ctx, q,
		p.ID,
		p.UserID,
		p.Section,
		p.TeacherUID,
		p.DayOfWeek,
		valueOrNil(p.TimeFrom),
		valueOrNil(p.TimeTo),
		p.MinRating,
		p.Enabled,
	)
	return scanFilter(row)
}

// DeleteFilter удаляет фильтр; возвращает количество удалённых строк.
func (s *Store) DeleteFilter(ctx context.Context, id, userID int64) (int64, error) {
	const q = "DELETE FROM filters WHERE id = $1 AND user_id = $2"
	tag, err := s.pool.Exec(ctx, q, id, userID)
	if err != nil {
		return 0, fmt.Errorf("store: delete filter: %w", err)
	}
	return tag.RowsAffected(), nil
}

// SetFilterEnabled обновляет только флаг enabled.
func (s *Store) SetFilterEnabled(ctx context.Context, id, userID int64, enabled bool) error {
	const q = "UPDATE filters SET enabled = $3, updated_at = now() WHERE id = $1 AND user_id = $2"
	if _, err := s.pool.Exec(ctx, q, id, userID, enabled); err != nil {
		return fmt.Errorf("store: set enabled: %w", err)
	}
	return nil
}

// ListActiveUsers возвращает список user_id, у которых есть хотя бы один enabled-фильтр.
// Используется poller-svc для определения, кого опрашивать.
func (s *Store) ListActiveUsers(ctx context.Context) ([]int64, error) {
	const q = "SELECT DISTINCT user_id FROM filters WHERE enabled"
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("store: list active users: %w", err)
	}
	defer rows.Close()

	var result []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("store: scan user_id: %w", err)
		}
		result = append(result, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list active users iter: %w", err)
	}
	return result, nil
}

// ----------------------------------------------------------------------------
// known_slots
// ----------------------------------------------------------------------------

// GetKnownSlotsByUser возвращает множество slot_id, известных юзеру.
func (s *Store) GetKnownSlotsByUser(ctx context.Context, userID int64) (map[string]struct{}, error) {
	const q = "SELECT slot_id FROM known_slots WHERE user_id = $1"
	rows, err := s.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("store: get known: %w", err)
	}
	defer rows.Close()

	known := make(map[string]struct{})
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("store: scan known: %w", err)
		}
		known[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: get known iter: %w", err)
	}
	return known, nil
}

// InsertKnownSlots батч-вставляет пары (user_id, slot_id) через UNNEST,
// ON CONFLICT DO NOTHING — повторная пометка идемпотентна.
// Никогда не удаляет существующие записи (фикс legacy_main.py:312).
func (s *Store) InsertKnownSlots(ctx context.Context, userID int64, slotIDs []string) error {
	if len(slotIDs) == 0 {
		return nil
	}
	const q = "INSERT INTO known_slots (user_id, slot_id) " +
		"SELECT $1::bigint, slot_id FROM unnest($2::text[]) AS slot_id " +
		"ON CONFLICT (user_id, slot_id) DO NOTHING"
	if _, err := s.pool.Exec(ctx, q, userID, slotIDs); err != nil {
		return fmt.Errorf("store: insert known: %w", err)
	}
	return nil
}

// DeleteKnownSlotsOlderThan удаляет записи старше cutoff. Возвращает число
// удалённых строк. Используется внешним CRON-helper.
func (s *Store) DeleteKnownSlotsOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	const q = "DELETE FROM known_slots WHERE first_seen < $1"
	tag, err := s.pool.Exec(ctx, q, cutoff)
	if err != nil {
		return 0, fmt.Errorf("store: delete known older: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ResetKnownSlots полностью очищает known_slots пользователя. Возвращает количество
// удалённых строк. Вызывается из ResetKnown RPC.
func (s *Store) ResetKnownSlots(ctx context.Context, userID int64) (int64, error) {
	const q = "DELETE FROM known_slots WHERE user_id = $1"
	tag, err := s.pool.Exec(ctx, q, userID)
	if err != nil {
		return 0, fmt.Errorf("store: reset known: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ----------------------------------------------------------------------------
// alert_log
// ----------------------------------------------------------------------------

// InsertAlertLogParams — параметры InsertAlertLog.
type InsertAlertLogParams struct {
	// UserID — кому отправлен алёрт.
	UserID int64
	// SlotID — id слота.
	SlotID string
	// Channel — канал доставки.
	Channel string
	// Payload — JSON payload, может быть nil.
	Payload []byte
}

// InsertAlertLog записывает запись в журнал алёртов.
func (s *Store) InsertAlertLog(ctx context.Context, p InsertAlertLogParams) (AlertLog, error) {
	const q = "INSERT INTO alert_log (user_id, slot_id, channel, payload) " +
		"VALUES ($1, $2, $3, $4) " +
		"RETURNING id, user_id, slot_id, channel, sent_at, payload"
	row := s.pool.QueryRow(ctx, q, p.UserID, p.SlotID, p.Channel, p.Payload)

	var a AlertLog
	if err := row.Scan(&a.ID, &a.UserID, &a.SlotID, &a.Channel, &a.SentAt, &a.Payload); err != nil {
		return AlertLog{}, fmt.Errorf("store: insert alert_log: %w", err)
	}
	return a, nil
}

// ListAlertLogByUser возвращает страницу записей alert_log юзера.
// limit, offset — параметры пагинации. limit<=0 заменяется на 50.
func (s *Store) ListAlertLogByUser(ctx context.Context, userID int64, limit, offset int32) ([]AlertLog, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	const q = "SELECT id, user_id, slot_id, channel, sent_at, payload FROM alert_log " +
		"WHERE user_id = $1 ORDER BY sent_at DESC LIMIT $2 OFFSET $3"
	rows, err := s.pool.Query(ctx, q, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("store: list alert_log: %w", err)
	}
	defer rows.Close()

	var result []AlertLog
	for rows.Next() {
		var a AlertLog
		if err := rows.Scan(&a.ID, &a.UserID, &a.SlotID, &a.Channel, &a.SentAt, &a.Payload); err != nil {
			return nil, fmt.Errorf("store: scan alert_log: %w", err)
		}
		result = append(result, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list alert_log iter: %w", err)
	}
	return result, nil
}

// ----------------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------------

// scanFilter считывает строку pgx.Row в Filter. pgx.ErrNoRows → ErrNotFound.
func scanFilter(row pgx.Row) (Filter, error) {
	var (
		f                Filter
		timeFrom, timeTo *time.Time
	)
	if err := row.Scan(
		&f.ID,
		&f.UserID,
		&f.Section,
		&f.TeacherUID,
		&f.DayOfWeek,
		&timeFrom,
		&timeTo,
		&f.MinRating,
		&f.MinVacancy,
		&f.Enabled,
		&f.CreatedAt,
		&f.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Filter{}, ErrNotFound
		}
		return Filter{}, fmt.Errorf("store: scan filter: %w", err)
	}
	if timeFrom != nil {
		v := TimeOfDay{Hour: timeFrom.Hour(), Minute: timeFrom.Minute()}
		f.TimeFrom = &v
	}
	if timeTo != nil {
		v := TimeOfDay{Hour: timeTo.Hour(), Minute: timeTo.Minute()}
		f.TimeTo = &v
	}
	return f, nil
}

// scanFilters читает все строки в []Filter.
func scanFilters(rows pgx.Rows) ([]Filter, error) {
	var result []Filter
	for rows.Next() {
		f, err := scanFilter(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: filters iter: %w", err)
	}
	return result, nil
}

// valueOrNil конвертирует *TimeOfDay → driver.Valuer для pgx (или nil).
func valueOrNil(t *TimeOfDay) any {
	if t == nil {
		return nil
	}
	return *t
}
