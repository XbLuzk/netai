package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/XbLuzk/logicmap/internal/embedding"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

type pgQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

type ToolsImpl struct {
	pool     pgQuerier
	embedder embedding.EmbeddingClient
}

func NewToolsImpl(pool *pgxpool.Pool, embedder embedding.EmbeddingClient) *ToolsImpl {
	return &ToolsImpl{pool: pool, embedder: embedder}
}

func (t *ToolsImpl) GetFunctionSource(ctx context.Context, repoID, functionName string) (string, error) {
	const q = `
SELECT source
FROM functions
WHERE repo_id = $1 AND name = $2
LIMIT 1
`

	var source string
	err := t.pool.QueryRow(ctx, q, repoID, functionName).Scan(&source)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get function source: %w", err)
	}
	return source, nil
}

func (t *ToolsImpl) GetCallees(ctx context.Context, repoID, functionName string) ([]CalleeInfo, error) {
	const q = `
SELECT f.name, f.file_path, false as external
FROM call_edges ce
JOIN functions f ON f.id = ce.callee_id
JOIN functions caller ON caller.id = ce.caller_id
WHERE caller.repo_id = $1 AND caller.name = $2

UNION ALL

SELECT ue.callee_name_raw, '', true
FROM unresolved_edges ue
JOIN functions f ON f.id = ue.caller_id
WHERE ue.repo_id = $1 AND f.name = $2
`

	rows, err := t.pool.Query(ctx, q, repoID, functionName)
	if err != nil {
		return nil, fmt.Errorf("get callees: %w", err)
	}
	defer rows.Close()

	out := make([]CalleeInfo, 0)
	for rows.Next() {
		var item CalleeInfo
		if err := rows.Scan(&item.Name, &item.FilePath, &item.External); err != nil {
			return nil, fmt.Errorf("scan callee: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate callees: %w", err)
	}

	return out, nil
}

func (t *ToolsImpl) GetCallers(ctx context.Context, repoID, functionName string) ([]CallerInfo, error) {
	const q = `
SELECT f.name, f.file_path
FROM call_edges ce
JOIN functions f ON f.id = ce.caller_id
JOIN functions callee ON callee.id = ce.callee_id
WHERE callee.repo_id = $1 AND callee.name = $2
`

	rows, err := t.pool.Query(ctx, q, repoID, functionName)
	if err != nil {
		return nil, fmt.Errorf("get callers: %w", err)
	}
	defer rows.Close()

	out := make([]CallerInfo, 0)
	for rows.Next() {
		var item CallerInfo
		if err := rows.Scan(&item.Name, &item.FilePath); err != nil {
			return nil, fmt.Errorf("scan caller: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate callers: %w", err)
	}

	return out, nil
}

func (t *ToolsImpl) SearchSimilarCode(ctx context.Context, repoID, query string) ([]SimilarCodeResult, error) {
	if t.embedder == nil {
		return nil, errors.New("embedder is nil")
	}
	vectors, err := t.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(vectors) == 0 {
		return nil, nil
	}

	const q = `
SELECT name, file_path, source, 1 - (embedding <-> $1) AS score
FROM functions
WHERE repo_id = $2
ORDER BY embedding <-> $1
LIMIT 5
`

	rows, err := t.pool.Query(ctx, q, pgvector.NewVector(vectors[0]), repoID)
	if err != nil {
		return nil, fmt.Errorf("search similar code: %w", err)
	}
	defer rows.Close()

	out := make([]SimilarCodeResult, 0, 5)
	for rows.Next() {
		var item SimilarCodeResult
		if err := rows.Scan(&item.Name, &item.FilePath, &item.Source, &item.Score); err != nil {
			return nil, fmt.Errorf("scan similar code row: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate similar code rows: %w", err)
	}

	return out, nil
}
