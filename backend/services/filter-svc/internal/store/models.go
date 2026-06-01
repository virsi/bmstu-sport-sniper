// Package store — слой доступа к БД filter_db.
//
// Реализация повторяет API, который сгенерирует sqlc по queries из
// services/filter-svc/sql/queries. Структуры совместимы по полям.
package store

import (
	"time"
)

// Filter — строка таблицы filters.
type Filter struct {
	// ID — внутренний BIGSERIAL ключ.
	ID int64 `json:"id"`
	// UserID — владелец фильтра, FK на auth_db.users.id.
	UserID int64 `json:"user_id"`
	// Section — фильтр по секции (точное совпадение, case-insensitive).
	// nil = «любая секция».
	Section *string `json:"section,omitempty"`
	// TeacherUID — фильтр по преподавателю.
	TeacherUID *string `json:"teacher_uid,omitempty"`
	// DayOfWeek — день недели как строка enum-значения (например "MONDAY").
	// nil = «любой день».
	DayOfWeek *string `json:"day_of_week,omitempty"`
	// TimeFrom — нижняя граница времени слота (включительно).
	TimeFrom *TimeOfDay `json:"time_from,omitempty"`
	// TimeTo — верхняя граница времени слота (включительно).
	TimeTo *TimeOfDay `json:"time_to,omitempty"`
	// MinRating — минимальный рейтинг преподавателя (0..5). nil = без ограничения.
	MinRating *float64 `json:"min_rating,omitempty"`
	// MinVacancy — минимум свободных мест (>=1).
	MinVacancy int32 `json:"min_vacancy"`
	// Enabled — флаг активности.
	Enabled bool `json:"enabled"`
	// CreatedAt — момент создания, UTC.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt — момент последнего обновления, UTC.
	UpdatedAt time.Time `json:"updated_at"`
}

// AlertLog — строка таблицы alert_log.
type AlertLog struct {
	// ID — внутренний BIGSERIAL ключ.
	ID int64 `json:"id"`
	// UserID — кому отправили алёрт.
	UserID int64 `json:"user_id"`
	// SlotID — детерминированный slot.id.
	SlotID string `json:"slot_id"`
	// Channel — канал доставки (telegram/sse/...).
	Channel string `json:"channel"`
	// SentAt — момент отправки.
	SentAt time.Time `json:"sent_at"`
	// Payload — произвольный JSON-payload алёрта (для аналитики).
	Payload []byte `json:"payload,omitempty"`
}
