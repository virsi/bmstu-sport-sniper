package store

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// isUniqueViolation возвращает true, если err — это pgconn.PgError с
// SQLSTATE 23505 (unique_violation). Используется для маппинга в
// ErrAlreadyExists.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == pgUniqueViolation
	}
	return false
}
