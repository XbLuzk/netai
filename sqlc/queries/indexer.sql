-- name: DeleteFunctionsByRepo :exec
DELETE FROM functions
WHERE repo_id = $1;

-- name: DeleteFunctionsByFiles :exec
DELETE FROM functions
WHERE repo_id = $1
  AND file_path = ANY($2::text[]);

-- name: GetFunctionMapByRepo :many
SELECT id, name
FROM functions
WHERE repo_id = $1;

-- name: InsertCallEdge :exec
INSERT INTO call_edges (id, repo_id, caller_id, callee_id)
VALUES ($1, $2, $3, $4)
ON CONFLICT (caller_id, callee_id) DO NOTHING;

-- name: InsertUnresolvedEdge :exec
INSERT INTO unresolved_edges (id, repo_id, caller_id, callee_name_raw)
VALUES ($1, $2, $3, $4);

-- name: UpdateTaskStarted :exec
UPDATE tasks
SET status = 'running',
    started_at = NOW(),
    completed_at = NULL,
    error = NULL
WHERE id = $1;

-- name: UpdateTaskStatus :exec
UPDATE tasks
SET status = $2,
    error = NULLIF($3, ''),
    stats = $4,
    completed_at = CASE WHEN $2 IN ('completed', 'failed', 'partial') THEN NOW() ELSE completed_at END
WHERE id = $1;

-- name: UpdateRepoIndexedFiles :exec
UPDATE repos
SET indexed_files = $2,
    updated_at = NOW()
WHERE id = $1;

-- name: DropFunctionsEmbeddingIndex :exec
DROP INDEX IF EXISTS functions_embedding_idx;

-- name: RecreateFunctionsEmbeddingIndex :exec
CREATE INDEX CONCURRENTLY IF NOT EXISTS functions_embedding_idx
ON functions USING hnsw (embedding vector_cosine_ops);
