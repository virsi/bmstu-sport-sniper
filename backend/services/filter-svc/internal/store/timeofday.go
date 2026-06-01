package store

import (
	"database/sql/driver"
	"fmt"
	"time"
)

// TimeOfDay — время суток без даты (Postgres TIME). Хранится как количество
// минут с полуночи, чтобы избежать неоднозначностей с часовыми поясами.
//
// Сериализация в БД: Postgres TIME мапится на time.Time с датой epoch.
// pgx по умолчанию сканит TIME в time.Time, поэтому Scan/Value реализованы
// так, чтобы:
//   - принять time.Time из pgx,
//   - принять строку "HH:MM" или "HH:MM:SS",
//   - вернуть в Value() строку "15:04:05" для совместимости с Postgres.
type TimeOfDay struct {
	// Hour — час 0..23.
	Hour int
	// Minute — минута 0..59.
	Minute int
}

// String возвращает форматированную строку "HH:MM".
func (t TimeOfDay) String() string {
	return fmt.Sprintf("%02d:%02d", t.Hour, t.Minute)
}

// Value реализует driver.Valuer — конвертирует в строку "HH:MM:SS"
// для Postgres-типа TIME.
func (t TimeOfDay) Value() (driver.Value, error) {
	return fmt.Sprintf("%02d:%02d:00", t.Hour, t.Minute), nil
}

// Scan реализует sql.Scanner — принимает time.Time (как pgx скана TIME),
// строку или []byte.
func (t *TimeOfDay) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		return fmt.Errorf("store: nil TimeOfDay")
	case time.Time:
		t.Hour = v.Hour()
		t.Minute = v.Minute()
		return nil
	case string:
		parsed, err := ParseTimeOfDay(v)
		if err != nil {
			return err
		}
		*t = parsed
		return nil
	case []byte:
		parsed, err := ParseTimeOfDay(string(v))
		if err != nil {
			return err
		}
		*t = parsed
		return nil
	default:
		return fmt.Errorf("store: unsupported TimeOfDay source %T", src)
	}
}

// ParseTimeOfDay парсит "HH:MM" или "HH:MM:SS" в TimeOfDay.
func ParseTimeOfDay(s string) (TimeOfDay, error) {
	// time.Parse требует разделители и формат. Пробуем оба формата.
	for _, layout := range []string{"15:04", "15:04:05"} {
		t, err := time.Parse(layout, s)
		if err == nil {
			return TimeOfDay{Hour: t.Hour(), Minute: t.Minute()}, nil
		}
	}
	return TimeOfDay{}, fmt.Errorf("store: bad time-of-day %q", s)
}

// Before возвращает true, если t раньше other.
func (t TimeOfDay) Before(other TimeOfDay) bool {
	return t.toMinutes() < other.toMinutes()
}

// After возвращает true, если t позже other.
func (t TimeOfDay) After(other TimeOfDay) bool {
	return t.toMinutes() > other.toMinutes()
}

// Equal возвращает true, если время эквивалентно.
func (t TimeOfDay) Equal(other TimeOfDay) bool {
	return t.toMinutes() == other.toMinutes()
}

// toMinutes конвертирует время в минуты с полуночи (внутренний helper).
func (t TimeOfDay) toMinutes() int {
	return t.Hour*60 + t.Minute
}
