package indexer

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgvector "github.com/pgvector/pgvector-go"
)

type FunctionRecord struct {
	ID        uuid.UUID
	RepoID    uuid.UUID
	Name      string
	FilePath  string
	StartLine int
	EndLine   int
	Source    string
	Embedding []float32
}

type CallEdgeRecord struct {
	ID       uuid.UUID
	RepoID   uuid.UUID
	CallerID uuid.UUID
	CalleeID uuid.UUID
}

type UnresolvedEdgeRecord struct {
	ID            uuid.UUID
	RepoID        uuid.UUID
	CallerID      uuid.UUID
	CalleeNameRaw string
}

type IndexStore struct {
	pool *pgxpool.Pool
}

func NewIndexStore(pool *pgxpool.Pool) *IndexStore {
	return &IndexStore{pool: pool}
}

func (s *IndexStore) BulkInsertFunctions(ctx context.Context, repoID uuid.UUID, funcs []FunctionRecord) (int64, error) {
	if len(funcs) == 0 {
		return 0, nil
	}

	rows := make([][]any, 0, len(funcs))
	for _, fn := range funcs {
		rows = append(rows, []any{
			fn.ID.String(),
			repoID.String(),
			fn.Name,
			fn.FilePath,
			fn.StartLine,
			fn.EndLine,
			fn.Source,
			pgvector.NewVector(fn.Embedding),
		})
	}

	count, err := s.pool.CopyFrom(
		ctx,
		pgx.Identifier{"functions"},
		[]string{"id", "repo_id", "name", "file_path", "start_line", "end_line", "source", "embedding"},
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		return 0, fmt.Errorf("copy functions: %w", err)
	}
	return count, nil
}

func (s *IndexStore) BulkInsertCallEdges(ctx context.Context, edges []CallEdgeRecord) (int64, error) {
	if len(edges) == 0 {
		return 0, nil
	}

	batch := &pgx.Batch{}
	for _, edge := range edges {
		batch.Queue(`
INSERT INTO call_edges (id, repo_id, caller_id, callee_id)
VALUES ($1, $2, $3, $4)
ON CONFLICT (caller_id, callee_id) DO NOTHING
`, edge.ID.String(), edge.RepoID.String(), edge.CallerID.String(), edge.CalleeID.String())
	}

	br := s.pool.SendBatch(ctx, batch)
	defer br.Close()

	for range edges {
		if _, err := br.Exec(); err != nil {
			return 0, fmt.Errorf("insert call edges: %w", err)
		}
	}

	return int64(len(edges)), nil
}

func (s *IndexStore) BulkInsertUnresolvedEdges(ctx context.Context, edges []UnresolvedEdgeRecord) error {
	if len(edges) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	for _, edge := range edges {
		batch.Queue(`
INSERT INTO unresolved_edges (id, repo_id, caller_id, callee_name_raw)
VALUES ($1, $2, $3, $4)
`, edge.ID.String(), edge.RepoID.String(), edge.CallerID.String(), edge.CalleeNameRaw)
	}

	br := s.pool.SendBatch(ctx, batch)
	defer br.Close()

	for range edges {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("insert unresolved edges: %w", err)
		}
	}

	return nil
}

func (s *IndexStore) GetFunctionMap(ctx context.Context, repoID uuid.UUID) (map[string][]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id::text, name
FROM functions
WHERE repo_id = $1
`, repoID.String())
	if err != nil {
		return nil, fmt.Errorf("query functions map: %w", err)
	}
	defer rows.Close()

	result := make(map[string][]uuid.UUID)
	for rows.Next() {
		var (
			idStr string
			name  string
		)
		if err := rows.Scan(&idStr, &name); err != nil {
			return nil, fmt.Errorf("scan function map row: %w", err)
		}

		id, err := uuid.Parse(idStr)
		if err != nil {
			return nil, fmt.Errorf("parse function id: %w", err)
		}
		result[name] = append(result[name], id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate function map rows: %w", err)
	}

	return result, nil
}

func (s *IndexStore) DeleteFunctionsByRepo(ctx context.Context, repoID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM functions WHERE repo_id = $1`, repoID.String())
	if err != nil {
		return fmt.Errorf("delete functions by repo: %w", err)
	}
	return nil
}

func (s *IndexStore) DeleteFunctionsByFiles(ctx context.Context, repoID uuid.UUID, filePaths []string) error {
	if len(filePaths) == 0 {
		return nil
	}

	_, err := s.pool.Exec(ctx, `
DELETE FROM functions
WHERE repo_id = $1
  AND file_path = ANY($2)
`, repoID.String(), filePaths)
	if err != nil {
		return fmt.Errorf("delete functions by files: %w", err)
	}
	return nil
}

func (s *IndexStore) UpdateTaskStatus(ctx context.Context, taskID uuid.UUID, status string, errorMsg string, stats map[string]any) error {
	if stats == nil {
		stats = map[string]any{}
	}

	statsJSON, err := json.Marshal(stats)
	if err != nil {
		return fmt.Errorf("marshal task stats: %w", err)
	}

	query := `
UPDATE tasks
SET status = $2,
    error = NULLIF($3, ''),
    stats = $4,
    completed_at = CASE WHEN $2 IN ('completed', 'failed', 'partial') THEN NOW() ELSE completed_at END
WHERE id = $1
`
	_, err = s.pool.Exec(ctx, query, taskID.String(), status, errorMsg, statsJSON)
	if err != nil {
		return fmt.Errorf("update task status: %w", err)
	}
	return nil
}

func (s *IndexStore) UpdateTaskStarted(ctx context.Context, taskID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
UPDATE tasks
SET status = 'running',
    started_at = NOW(),
    completed_at = NULL,
    error = NULL
WHERE id = $1
`, taskID.String())
	if err != nil {
		return fmt.Errorf("update task started: %w", err)
	}
	return nil
}

func (s *IndexStore) DropHNSWIndex(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `DROP INDEX IF EXISTS functions_embedding_idx`)
	if err != nil {
		return fmt.Errorf("drop hnsw index: %w", err)
	}
	return nil
}

func (s *IndexStore) RecreateHNSWIndex(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `CREATE INDEX CONCURRENTLY IF NOT EXISTS functions_embedding_idx ON functions USING hnsw (embedding vector_cosine_ops)`)
	if err != nil {
		return fmt.Errorf("recreate hnsw index: %w", err)
	}
	return nil
}

func (s *IndexStore) UpdateRepoIndexedFiles(ctx context.Context, repoID uuid.UUID, files []string) error {
	filesJSON, err := json.Marshal(files)
	if err != nil {
		return fmt.Errorf("marshal indexed files: %w", err)
	}

	_, err = s.pool.Exec(ctx, `
UPDATE repos
SET indexed_files = $2,
    updated_at = $3
WHERE id = $1
`, repoID.String(), filesJSON, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("update repo indexed files: %w", err)
	}
	return nil
}
