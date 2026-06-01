-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS filters (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL,
    section TEXT,
    teacher_uid TEXT,
    day_of_week TEXT,
    time_from TIME,
    time_to TIME,
    min_rating NUMERIC(3,2),
    min_vacancy INT NOT NULL DEFAULT 1,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Частичный индекс: poller-svc грузит только активные фильтры по user_id.
CREATE INDEX IF NOT EXISTS filters_user_id_enabled_idx
    ON filters (user_id) WHERE enabled;

-- Композитный индекс для CRUD по конкретному фильтру юзера.
CREATE INDEX IF NOT EXISTS filters_user_id_id_idx
    ON filters (user_id, id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS filters;
-- +goose StatementEnd
