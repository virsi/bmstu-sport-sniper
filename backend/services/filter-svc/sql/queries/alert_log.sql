-- name: InsertAlertLog :one
INSERT INTO alert_log (user_id, slot_id, channel, payload)
VALUES ($1, $2, $3, $4)
RETURNING id, user_id, slot_id, channel, sent_at, payload;

-- name: ListAlertLogByUser :many
SELECT id, user_id, slot_id, channel, sent_at, payload
FROM alert_log
WHERE user_id = $1
ORDER BY sent_at DESC
LIMIT $2 OFFSET $3;
