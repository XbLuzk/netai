package impact

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ImpactStore struct {
	pool *pgxpool.Pool
}

func NewImpactStore(pool *pgxpool.Pool) *ImpactStore {
	return &ImpactStore{pool: pool}
}

type AffectedFunction struct {
	Name     string
	FilePath string
	Depth    int
}

// GetCallersByDepth 递归查询 callers，最多 depth 层。
func (s *ImpactStore) GetCallersByDepth(ctx context.Context, repoID uuid.UUID, functionNames []string, depth int) ([]AffectedFunction, error) {
	if len(functionNames) == 0 {
		return nil, nil
	}
	if depth < 0 {
		depth = 0
	}

	const q = `
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
ORDER BY depth, name, file_path
`

	rows, err := s.pool.Query(ctx, q, repoID, functionNames, depth)
	if err != nil {
		return nil, fmt.Errorf("get callers by depth: %w", err)
	}
	defer rows.Close()

	result := make([]AffectedFunction, 0)
	for rows.Next() {
		var item AffectedFunction
		if err := rows.Scan(&item.Name, &item.FilePath, &item.Depth); err != nil {
			return nil, fmt.Errorf("scan affected function: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate affected functions: %w", err)
	}

	return result, nil
}

// GetFunctionsByFilePaths 根据文件路径列表获取函数列表。
func (s *ImpactStore) GetFunctionsByFilePaths(ctx context.Context, repoID uuid.UUID, filePaths []string) ([]string, error) {
	if len(filePaths) == 0 {
		return nil, nil
	}

	const q = `
SELECT DISTINCT name
FROM functions
WHERE repo_id = $1 AND file_path = ANY($2)
ORDER BY name
`

	rows, err := s.pool.Query(ctx, q, repoID, filePaths)
	if err != nil {
		return nil, fmt.Errorf("get functions by file paths: %w", err)
	}
	defer rows.Close()

	result := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan function name: %w", err)
		}
		result = append(result, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate function names: %w", err)
	}
	return result, nil
}
