-- name: GetKnownSlotsByUser :many
SELECT slot_id FROM known_slots WHERE user_id = $1;

-- name: InsertKnownSlots :exec
-- Батч-вставка через UNNEST: пара (user_id, slot_id). ON CONFLICT DO NOTHING
-- гарантирует идемпотентность — повторная пометка не меняет first_seen.
INSERT INTO known_slots (user_id, slot_id)
SELECT $1::bigint, slot_id FROM unnest($2::text[]) AS slot_id
ON CONFLICT (user_id, slot_id) DO NOTHING;

-- name: DeleteKnownSlotsOlderThan :execrows
-- GC-задача: удалить записи старше cutoff. Вызывается из CRON-helper (отдельно).
DELETE FROM known_slots WHERE first_seen < $1;

-- name: ResetKnownSlots :execrows
DELETE FROM known_slots WHERE user_id = $1;
