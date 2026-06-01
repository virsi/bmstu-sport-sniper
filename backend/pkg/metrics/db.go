// DB-инструментация — простой обёртки `Observe(query, fn)` для wrapping
// SQL-операций. KISS-альтернатива pgx tracer/hook'ам, которые требуют
// реализовать целый интерфейс с pgx.TraceQueryStartData и т.п.
//
// Использование:
//
//	row := r.Observe("users.GetByEmail", func() error {
//	    return pool.QueryRow(ctx, sql, email).Scan(&u.ID, &u.Email)
//	})
//
// Caller передаёт имя запроса; cardinality остаётся низкой (мы НЕ кладём
// сам SQL — слишком много вариаций после prepare-сериализации).

package metrics

import "time"

// Observe измеряет длительность fn и инкрементит DBQueriesTotal/DBQueryDuration.
//
// query — короткое имя запроса (например, "users.GetByEmail" или "filters.Insert").
// fn — функция, выполняющая SQL. Если возвращает не-nil ошибку — статус метрик
// будет "error", иначе "ok".
//
// Возвращает оригинальную ошибку fn, не оборачивая её — caller должен
// продолжать использовать errors.Is/As на нижележащем pgx-error'е.
func (r *Registry) Observe(query string, fn func() error) error {
	start := time.Now()
	err := fn()
	dur := time.Since(start).Seconds()

	status := "ok"
	if err != nil {
		status = "error"
	}
	r.DBQueriesTotal.WithLabelValues(query, status).Inc()
	r.DBQueryDuration.WithLabelValues(query).Observe(dur)
	return err
}
