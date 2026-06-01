-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS alert_log (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL,
    slot_id TEXT NOT NULL,
    channel TEXT NOT NULL,
    sent_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    payload JSONB
);

-- Индекс для аналитики "что отсылали юзеру в последнее время".
CREATE INDEX IF NOT EXISTS alert_log_user_id_sent_at_idx
    ON alert_log (user_id, sent_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS alert_log;
-- +goose StatementEnd
