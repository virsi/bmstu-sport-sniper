-- name: CreateFilter :one
INSERT INTO filters (user_id, section, teacher_uid, day_of_week, time_from, time_to, min_rating, min_vacancy, enabled)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING id, user_id, section, teacher_uid, day_of_week, time_from, time_to, min_rating, min_vacancy, enabled, created_at, updated_at;

-- name: GetFilterByID :one
SELECT id, user_id, section, teacher_uid, day_of_week, time_from, time_to, min_rating, min_vacancy, enabled, created_at, updated_at
FROM filters
WHERE id = $1;

-- name: ListFiltersByUser :many
SELECT id, user_id, section, teacher_uid, day_of_week, time_from, time_to, min_rating, min_vacancy, enabled, created_at, updated_at
FROM filters
WHERE user_id = $1
  AND (sqlc.arg('include_disabled')::boolean OR enabled)
ORDER BY created_at DESC;

-- name: UpdateFilter :one
UPDATE filters SET
    section = $3,
    teacher_uid = $4,
    day_of_week = $5,
    time_from = $6,
    time_to = $7,
    min_rating = $8,
    enabled = $9,
    updated_at = now()
WHERE id = $1 AND user_id = $2
RETURNING id, user_id, section, teacher_uid, day_of_week, time_from, time_to, min_rating, min_vacancy, enabled, created_at, updated_at;

-- name: DeleteFilter :execrows
DELETE FROM filters WHERE id = $1 AND user_id = $2;

-- name: SetFilterEnabled :exec
UPDATE filters SET enabled = $3, updated_at = now()
WHERE id = $1 AND user_id = $2;

-- name: ListActiveUsers :many
SELECT DISTINCT user_id FROM filters WHERE enabled;
