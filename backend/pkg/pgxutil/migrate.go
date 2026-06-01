package pgxutil

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// MigrateUp прогоняет SQL-файлы из embed.FS в лексикографическом порядке
// внутри одной транзакции. Имена файлов должны заканчиваться на ".up.sql".
// Применяется только для bootstrap-сценариев — для prod-миграций используйте
// goose/golang-migrate CLI.
//
// Логика идемпотентности — на стороне SQL (IF NOT EXISTS). Эта функция не
// ведёт таблицу schema_migrations.
func MigrateUp(ctx context.Context, pool *pgxpool.Pool, dir fs.FS, root string) error {
	if pool == nil {
		return errors.New("pgxutil: nil pool")
	}
	entries, err := fs.ReadDir(dir, root)
	if err != nil {
		return fmt.Errorf("pgxutil: read migrations dir: %w", err)
	}

	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".up.sql") {
			continue
		}
		files = append(files, e.Name())
	}
	sort.Strings(files)

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pgxutil: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, name := range files {
		sqlBytes, rerr := fs.ReadFile(dir, root+"/"+name)
		if rerr != nil {
			return fmt.Errorf("pgxutil: read %s: %w", name, rerr)
		}
		if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
			return fmt.Errorf("pgxutil: exec %s: %w", name, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("pgxutil: commit: %w", err)
	}
	return nil
}
