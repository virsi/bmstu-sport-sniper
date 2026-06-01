// Package store — слой доступа к БД teachers_db.
package store

import "time"

// Teacher — строка таблицы teachers.
type Teacher struct {
	// UID — детерминированный ID (sha1 от нормализованного имени, 16 hex символов).
	UID string `json:"uid"`
	// Name — ФИО как в источнике teachers.json.
	Name string `json:"name"`
	// NameNormalized — lower-cased + trimmed name; используется для substring-поиска.
	NameNormalized string `json:"name_normalized"`
	// Rating — рейтинг 0..5; nil = нет данных.
	Rating *float64 `json:"rating,omitempty"`
	// SourceURL — откуда импортирован рейтинг (если есть).
	SourceURL *string `json:"source_url,omitempty"`
	// ImportedAt — момент последнего импорта/upsert.
	ImportedAt time.Time `json:"imported_at"`
}
