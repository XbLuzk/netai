-- name: GetFunctionSourceForQuery :one
SELECT source
FROM functions
WHERE repo_id = $1 AND name = $2
LIMIT 1;

-- name: GetCalleesForQuery :many
SELECT f.name, f.file_path, false as external
FROM call_edges ce
JOIN functions f ON f.id = ce.callee_id
JOIN functions caller ON caller.id = ce.caller_id
WHERE caller.repo_id = $1 AND caller.name = $2
UNION ALL
SELECT ue.callee_name_raw, '' as file_path, true as external
FROM unresolved_edges ue
JOIN functions f ON f.id = ue.caller_id
WHERE ue.repo_id = $1 AND f.name = $2;

-- name: GetCallersForQuery :many
SELECT f.name, f.file_path
FROM call_edges ce
JOIN functions f ON f.id = ce.caller_id
JOIN functions callee ON callee.id = ce.callee_id
WHERE callee.repo_id = $1 AND callee.name = $2;

-- name: SearchSimilarCodeForQuery :many
SELECT name, file_path, source, 1 - (embedding <-> $1) AS score
FROM functions
WHERE repo_id = $2
ORDER BY embedding <-> $1
LIMIT 5;
