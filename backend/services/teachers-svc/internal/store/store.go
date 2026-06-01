package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound — запись в БД не найдена.
var ErrNotFound = errors.New("store: not found")

// Store — фасад над pgxpool.Pool для teachers_db.
type Store struct {
	pool *pgxpool.Pool
}

// New создаёт Store.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

const teacherColumns = "uid, name, name_normalized, rating, source_url, imported_at"

// GetByUID возвращает преподавателя. ErrNotFound если нет.
func (s *Store) GetByUID(ctx context.Context, uid string) (Teacher, error) {
	const q = "SELECT " + teacherColumns + " FROM teachers WHERE uid = $1"
	row := s.pool.QueryRow(ctx, q, uid)
	return scanTeacher(row)
}

// BatchGet возвращает преподавателей по списку uid. Отсутствующие молча
// пропускаются (не ошибка).
func (s *Store) BatchGet(ctx context.Context, uids []string) ([]Teacher, error) {
	if len(uids) == 0 {
		return nil, nil
	}
	const q = "SELECT " + teacherColumns + " FROM teachers WHERE uid = ANY($1::text[])"
	rows, err := s.pool.Query(ctx, q, uids)
	if err != nil {
		return nil, fmt.Errorf("store: batch get: %w", err)
	}
	defer rows.Close()
	return scanTeachers(rows)
}

// ListParams — параметры List.
type ListParams struct {
	// NameQuery — substring (case-insensitive) для name_normalized. Пустой = без фильтра.
	NameQuery string
	// Limit — макс. число записей; 0 → 50.
	Limit int32
	// Offset — для пагинации.
	Offset int32
}

// List возвращает страницу преподавателей.
func (s *Store) List(ctx context.Context, p ListParams) ([]Teacher, error) {
	if p.Limit <= 0 {
		p.Limit = 50
	}
	if p.Offset < 0 {
		p.Offset = 0
	}
	const q = "SELECT " + teacherColumns + " FROM teachers " +
		"WHERE ($3 = '' OR name_normalized LIKE '%' || lower($3) || '%') " +
		"ORDER BY name_normalized LIMIT $1 OFFSET $2"
	rows, err := s.pool.Query(ctx, q, p.Limit, p.Offset, p.NameQuery)
	if err != nil {
		return nil, fmt.Errorf("store: list teachers: %w", err)
	}
	defer rows.Close()
	return scanTeachers(rows)
}

// Count возвращает общее число записей. Используется в bootstrap (если 0 → импорт).
func (s *Store) Count(ctx context.Context) (int64, error) {
	const q = "SELECT count(*) FROM teachers"
	var n int64
	if err := s.pool.QueryRow(ctx, q).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count teachers: %w", err)
	}
	return n, nil
}

// UpsertParams — параметры Upsert.
type UpsertParams struct {
	// UID — детерминированный ID.
	UID string
	// Name — ФИО.
	Name string
	// NameNormalized — lower-cased + trimmed имя.
	NameNormalized string
	// Rating — рейтинг (может быть nil).
	Rating *float64
	// SourceURL — необяз. URL источника.
	SourceURL *string
}

// UpsertResult — итог Upsert.
type UpsertResult struct {
	// Teacher — после upsert.
	Teacher Teacher
	// Inserted — true если строка была вставлена; false если обновлена.
	Inserted bool
}

// Upsert вставляет нового преподавателя или обновляет существующего по UID.
// inserted=true если строка была новой (используется в Refresh для статистики).
func (s *Store) Upsert(ctx context.Context, p UpsertParams) (UpsertResult, error) {
	const q = "INSERT INTO teachers (uid, name, name_normalized, rating, source_url) " +
		"VALUES ($1, $2, $3, $4, $5) " +
		"ON CONFLICT (uid) DO UPDATE SET " +
		"    name = EXCLUDED.name, " +
		"    name_normalized = EXCLUDED.name_normalized, " +
		"    rating = EXCLUDED.rating, " +
		"    source_url = EXCLUDED.source_url, " +
		"    imported_at = now() " +
		"RETURNING " + teacherColumns + ", (xmax = 0) AS inserted"
	row := s.pool.QueryRow(ctx, q, p.UID, p.Name, p.NameNormalized, p.Rating, p.SourceURL)

	var (
		t        Teacher
		inserted bool
	)
	if err := row.Scan(&t.UID, &t.Name, &t.NameNormalized, &t.Rating, &t.SourceURL, &t.ImportedAt, &inserted); err != nil {
		return UpsertResult{}, fmt.Errorf("store: upsert teacher: %w", err)
	}
	return UpsertResult{Teacher: t, Inserted: inserted}, nil
}

// scanTeacher маппит одну строку. pgx.ErrNoRows → ErrNotFound.
func scanTeacher(row pgx.Row) (Teacher, error) {
	var t Teacher
	if err := row.Scan(&t.UID, &t.Name, &t.NameNormalized, &t.Rating, &t.SourceURL, &t.ImportedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Teacher{}, ErrNotFound
		}
		return Teacher{}, fmt.Errorf("store: scan teacher: %w", err)
	}
	return t, nil
}

// scanTeachers маппит все строки.
func scanTeachers(rows pgx.Rows) ([]Teacher, error) {
	var result []Teacher
	for rows.Next() {
		t, err := scanTeacher(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iter teachers: %w", err)
	}
	return result, nil
}
