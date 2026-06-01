-- +goose Down
DROP INDEX IF EXISTS bmstu_credentials_updated_at_idx;
DROP TABLE IF EXISTS bmstu_credentials;
