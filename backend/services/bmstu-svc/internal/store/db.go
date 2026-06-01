package store

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DBTX — минимальный интерфейс, который удовлетворяет pgxpool.Pool, pgx.Conn
// и pgx.Tx. Совпадает по форме с sqlc-сгенерированным DBTX, что упростит
// миграцию на sqlc в будущем.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// New создаёт *Queries поверх DBTX. Не клонирует, не закрывает соединение.
func New(db DBTX) *Queries {
	return &Queries{db: db}
}

// Queries — фасад методов доступа к bmstu_db.
type Queries struct {
	db DBTX
}
