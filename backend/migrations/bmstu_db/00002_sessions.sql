-- +goose Up
-- +goose StatementBegin

-- bmstu_sessions хранит gob-сериализованный []*http.Cookie пользователя,
-- зашифрованный AES-256-GCM. cookies_blob = nonce(NonceSize) || ciphertext ||
-- tag(16) по контракту pkg/crypto.Encrypt (NonceSize = 12, см. pkg/crypto).
--
-- nonce: дубль первых NonceSize байт cookies_blob. blob уже содержит nonce
-- внутри себя; отдельная колонка хранится ТОЛЬКО для аудита/наблюдаемости
-- и симметрии с bmstu_credentials. При decrypt не используется —
-- pkg/crypto.Decrypt сам нарезает blob по NonceSize.
--
-- ON DELETE CASCADE из bmstu_credentials: если креды удалили — сессия теряет
-- смысл (нельзя сделать reauth без пароля).
CREATE TABLE IF NOT EXISTS bmstu_sessions (
    user_id          TEXT PRIMARY KEY
        REFERENCES bmstu_credentials(user_id) ON DELETE CASCADE,
    cookies_blob     BYTEA NOT NULL,
    nonce            BYTEA NOT NULL,
    expires_at       TIMESTAMPTZ,
    last_refresh_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS bmstu_sessions_last_refresh_at_idx
    ON bmstu_sessions (last_refresh_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS bmstu_sessions_last_refresh_at_idx;
DROP TABLE IF EXISTS bmstu_sessions;
-- +goose StatementEnd
