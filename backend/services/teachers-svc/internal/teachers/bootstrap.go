package teachers

import (
	"context"
	"fmt"
	"log/slog"
)

// Bootstrap — one-shot impорт embedded teachers.json при первом старте.
// Если таблица teachers непустая — no-op.
//
// Вызывается из main.go ПОСЛЕ применения миграций и ПЕРЕД стартом gRPC,
// чтобы первый запрос Get/List имел данные. Логирует количество вставленных/обновлённых.
func Bootstrap(ctx context.Context, st Store) error {
	count, err := st.Count(ctx)
	if err != nil {
		return fmt.Errorf("teachers bootstrap: count: %w", err)
	}
	if count > 0 {
		slog.Info("teachers bootstrap: skipped (table non-empty)", slog.Int64("count", count))
		return nil
	}

	stats, err := Import(ctx, st, embeddedJSON)
	if err != nil {
		return fmt.Errorf("teachers bootstrap: import: %w", err)
	}
	slog.Info("teachers bootstrap: imported",
		slog.Int("total", int(stats.Total)),
		slog.Int("inserted", int(stats.Inserted)),
		slog.Int("updated", int(stats.Updated)),
	)
	return nil
}
