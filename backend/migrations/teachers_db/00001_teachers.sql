-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS teachers (
    uid TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    name_normalized TEXT NOT NULL,
    rating NUMERIC(3,2),
    source_url TEXT,
    imported_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Индекс для подстрочного поиска по нормализованному имени (LIKE/ILIKE).
CREATE INDEX IF NOT EXISTS teachers_name_normalized_idx
    ON teachers (name_normalized);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS teachers;
-- +goose StatementEnd
