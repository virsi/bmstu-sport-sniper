-- +goose Down
DROP INDEX IF EXISTS bmstu_sessions_last_refresh_at_idx;
DROP TABLE IF EXISTS bmstu_sessions;
