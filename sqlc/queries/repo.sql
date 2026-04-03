-- name: UpsertRepo :one
WITH inserted AS (
    INSERT INTO repos (id, path, name)
    VALUES ($1, $2, $3)
    ON CONFLICT (path) DO NOTHING
    RETURNING id
)
SELECT id, true AS created FROM inserted
UNION ALL
SELECT r.id, false AS created FROM repos r WHERE r.path = $2
LIMIT 1;

-- name: GetRepoWithLatestTask :one
SELECT
    r.id,
    r.path,
    r.name,
    COALESCE(r.indexed_files, '[]'::jsonb) AS indexed_files,
    r.created_at,
    r.updated_at,
    t.status AS latest_task_status,
    t.created_at AS latest_task_created_at
FROM repos r
LEFT JOIN LATERAL (
    SELECT status, created_at
    FROM tasks
    WHERE repo_id = r.id
    ORDER BY created_at DESC
    LIMIT 1
) t ON true
WHERE r.id = $1;

-- name: CreateTask :exec
INSERT INTO tasks (id, repo_id, type, status)
VALUES ($1, $2, $3, 'pending');

-- name: GetLatestActiveTask :one
SELECT
    id,
    repo_id,
    type,
    status,
    started_at,
    completed_at,
    error,
    stats,
    created_at
FROM tasks
WHERE repo_id = $1
  AND status IN ('pending', 'running')
ORDER BY created_at DESC
LIMIT 1;

-- name: GetTaskByID :one
SELECT
    id,
    repo_id,
    type,
    status,
    started_at,
    completed_at,
    error,
    stats,
    created_at
FROM tasks
WHERE id = $1;
