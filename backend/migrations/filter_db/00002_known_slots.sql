-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS known_slots (
    user_id BIGINT NOT NULL,
    slot_id TEXT NOT NULL,
    first_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, slot_id)
);

-- Индекс для GC старых записей (CRON-задача DeleteOlderThan).
-- Полный индекс по first_seen (без частичного предиката), чтобы планировщик мог
-- использовать его для любого WHERE first_seen < $cutoff. Частичный предикат
-- с now() здесь невозможен (IMMUTABLE требуется), поэтому полный b-tree индекс.
CREATE INDEX IF NOT EXISTS known_slots_first_seen_idx
    ON known_slots (first_seen);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS known_slots;
-- +goose StatementEnd
