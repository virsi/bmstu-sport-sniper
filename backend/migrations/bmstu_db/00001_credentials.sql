-- +goose Up
-- +goose StatementBegin

-- bmstu_credentials хранит BMSTU-логин и пароль пользователя, зашифрованные
-- AES-256-GCM мастер-ключом сервиса. Логин шифруется отдельно от пароля,
-- каждый со своим nonce (NonceSize = 12 байт, см. pkg/crypto) — это сохраняет
-- одно-к-одному соответствие с pkg/crypto API, при этом снижает побочные
-- эффекты при ротации мастер-ключа.
--
-- nonce_login / nonce_password: дубль первых NonceSize байт enc_login /
-- enc_password соответственно. blob, который кладёт pkg/crypto.Encrypt, и
-- так начинается с этих же байт; отдельные колонки хранятся ИСКЛЮЧИТЕЛЬНО
-- для аудита и наблюдаемости (отчётность по unique-ratio, ad-hoc проверки).
-- При decrypt они не используются — pkg/crypto.Decrypt сам нарезает blob
-- по NonceSize. Не вычищаем дубль из соображений простоты схемы.
--
-- Логически user_id ссылается на auth_db.users.id (BIGSERIAL stringified),
-- но физический FK не ставим: auth_db и bmstu_db — разные базы данных
-- (database-per-service по архитектуре).
CREATE TABLE IF NOT EXISTS bmstu_credentials (
    user_id        TEXT PRIMARY KEY,
    enc_login      BYTEA NOT NULL,
    enc_password   BYTEA NOT NULL,
    nonce_login    BYTEA NOT NULL,
    nonce_password BYTEA NOT NULL,
    last_login_at  TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Индекс на updated_at — для мониторинга «зависших» кредов.
CREATE INDEX IF NOT EXISTS bmstu_credentials_updated_at_idx
    ON bmstu_credentials (updated_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS bmstu_credentials_updated_at_idx;
DROP TABLE IF EXISTS bmstu_credentials;
-- +goose StatementEnd
