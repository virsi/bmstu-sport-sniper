-- name: GetTeacherByUID :one
SELECT uid, name, name_normalized, rating, source_url, imported_at
FROM teachers
WHERE uid = $1;

-- name: BatchGetTeachers :many
SELECT uid, name, name_normalized, rating, source_url, imported_at
FROM teachers
WHERE uid = ANY($1::text[]);

-- name: ListTeachers :many
SELECT uid, name, name_normalized, rating, source_url, imported_at
FROM teachers
WHERE (sqlc.arg('name_query')::text = '' OR name_normalized LIKE '%' || lower(sqlc.arg('name_query')::text) || '%')
ORDER BY name_normalized
LIMIT $1 OFFSET $2;

-- name: CountTeachers :one
SELECT count(*) FROM teachers;

-- name: UpsertTeacher :one
INSERT INTO teachers (uid, name, name_normalized, rating, source_url)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (uid) DO UPDATE SET
    name = EXCLUDED.name,
    name_normalized = EXCLUDED.name_normalized,
    rating = EXCLUDED.rating,
    source_url = EXCLUDED.source_url,
    imported_at = now()
RETURNING uid, name, name_normalized, rating, source_url, imported_at,
    (xmax = 0) AS inserted;
