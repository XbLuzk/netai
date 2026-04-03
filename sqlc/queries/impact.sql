-- name: GetCallersByDepth :many
WITH RECURSIVE callers AS (
    SELECT f.id, f.name, f.file_path, 0 AS depth
    FROM functions f
    WHERE f.repo_id = $1 AND f.name = ANY($2)

    UNION

    SELECT f.id, f.name, f.file_path, c.depth + 1
    FROM callers c
    JOIN call_edges ce ON ce.callee_id = c.id
    JOIN functions f ON f.id = ce.caller_id
    WHERE c.depth < $3
)
SELECT name, file_path, MAX(depth) AS depth
FROM callers
GROUP BY name, file_path
ORDER BY depth, name, file_path;

-- name: GetFunctionsByFilePaths :many
SELECT DISTINCT name
FROM functions
WHERE repo_id = $1 AND file_path = ANY($2)
ORDER BY name;
