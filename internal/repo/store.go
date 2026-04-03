package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

var (
	ErrRepoNotFound    = errors.New("repo_not_found")
	ErrTaskNotFound    = errors.New("task_not_found")
	ErrPathNotFound    = errors.New("path_not_found")
	ErrIndexInProgress = errors.New("index_in_progress")
)

type IndexInProgressError struct {
	TaskID uuid.UUID
}

func (e *IndexInProgressError) Error() string {
	return ErrIndexInProgress.Error()
}

func (e *IndexInProgressError) Unwrap() error {
	return ErrIndexInProgress
}

type Repo struct {
	ID           uuid.UUID
	Path         string
	Name         string
	IndexedFiles []string
	Status       string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Task struct {
	ID          uuid.UUID
	RepoID      uuid.UUID
	Type        string
	Status      string
	StartedAt   *time.Time
	CompletedAt *time.Time
	Error       string
	Stats       map[string]any
	CreatedAt   time.Time
}

type RepoStore struct {
	pool  *pgxpool.Pool
	redis *redis.Client
}

func NewRepoStore(pool *pgxpool.Pool, redisClient *redis.Client) *RepoStore {
	return &RepoStore{pool: pool, redis: redisClient}
}

// UpsertRepo 幂等注册，冲突时返回已有 ID。
func (s *RepoStore) UpsertRepo(ctx context.Context, path, name string) (id uuid.UUID, created bool, err error) {
	const q = `
WITH inserted AS (
	INSERT INTO repos (id, path, name)
	VALUES ($1, $2, $3)
	ON CONFLICT (path) DO NOTHING
	RETURNING id::text
)
SELECT id, true FROM inserted
UNION ALL
SELECT r.id::text, false FROM repos r WHERE r.path = $2
LIMIT 1
`

	newID := uuid.New()
	var idStr string
	if err := s.pool.QueryRow(ctx, q, newID.String(), path, name).Scan(&idStr, &created); err != nil {
		return uuid.Nil, false, fmt.Errorf("upsert repo: %w", err)
	}

	parsedID, err := uuid.Parse(idStr)
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("parse repo id: %w", err)
	}

	return parsedID, created, nil
}

// GetRepo 查询 repo（含最近 non-stale task 派生状态）。
func (s *RepoStore) GetRepo(ctx context.Context, id uuid.UUID, staleThreshold time.Duration) (*Repo, error) {
	const q = `
SELECT
	r.id::text,
	r.path,
	r.name,
	COALESCE(r.indexed_files, '[]'::jsonb)::text,
	r.created_at,
	r.updated_at,
	t.status,
	t.created_at
FROM repos r
LEFT JOIN LATERAL (
	SELECT status, created_at
	FROM tasks
	WHERE repo_id = r.id
	ORDER BY created_at DESC
	LIMIT 1
) t ON true
WHERE r.id = $1
`

	var (
		repoIDStr      string
		indexedFilesJS string
		latestStatus   sql.NullString
		latestCreated  sql.NullTime
		repo           Repo
	)

	err := s.pool.QueryRow(ctx, q, id.String()).Scan(
		&repoIDStr,
		&repo.Path,
		&repo.Name,
		&indexedFilesJS,
		&repo.CreatedAt,
		&repo.UpdatedAt,
		&latestStatus,
		&latestCreated,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRepoNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get repo: %w", err)
	}

	repoID, err := uuid.Parse(repoIDStr)
	if err != nil {
		return nil, fmt.Errorf("parse repo id: %w", err)
	}
	repo.ID = repoID

	if err := json.Unmarshal([]byte(indexedFilesJS), &repo.IndexedFiles); err != nil {
		return nil, fmt.Errorf("unmarshal indexed_files: %w", err)
	}

	repo.Status = deriveRepoStatus(latestStatus, latestCreated, staleThreshold)
	return &repo, nil
}

// FindByOwnerRepo 根据 GitHub owner/repo 匹配本地注册仓库。
// 优先精确匹配 repos.name，其次按 repos.path 包含 owner/repo 模式匹配。
func (s *RepoStore) FindByOwnerRepo(ctx context.Context, owner, name string, staleThreshold time.Duration) (*Repo, error) {
	const q = `
SELECT
	r.id::text,
	r.path,
	r.name,
	COALESCE(r.indexed_files, '[]'::jsonb)::text,
	r.created_at,
	r.updated_at,
	t.status,
	t.created_at
FROM repos r
LEFT JOIN LATERAL (
	SELECT status, created_at
	FROM tasks
	WHERE repo_id = r.id
	ORDER BY created_at DESC
	LIMIT 1
) t ON true
WHERE LOWER(r.name) = LOWER($2)
   OR LOWER(r.path) LIKE '%' || LOWER($1 || '/' || $2) || '%'
ORDER BY CASE WHEN LOWER(r.name) = LOWER($2) THEN 0 ELSE 1 END, r.updated_at DESC
LIMIT 1
`

	var (
		repoIDStr      string
		indexedFilesJS string
		latestStatus   sql.NullString
		latestCreated  sql.NullTime
		repo           Repo
	)

	err := s.pool.QueryRow(ctx, q, owner, name).Scan(
		&repoIDStr,
		&repo.Path,
		&repo.Name,
		&indexedFilesJS,
		&repo.CreatedAt,
		&repo.UpdatedAt,
		&latestStatus,
		&latestCreated,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRepoNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find repo by owner/repo: %w", err)
	}

	repoID, err := uuid.Parse(repoIDStr)
	if err != nil {
		return nil, fmt.Errorf("parse repo id: %w", err)
	}
	repo.ID = repoID

	if err := json.Unmarshal([]byte(indexedFilesJS), &repo.IndexedFiles); err != nil {
		return nil, fmt.Errorf("unmarshal indexed_files: %w", err)
	}

	repo.Status = deriveRepoStatus(latestStatus, latestCreated, staleThreshold)
	return &repo, nil
}

// CreateTask 插入 task，返回 task ID。
func (s *RepoStore) CreateTask(ctx context.Context, repoID uuid.UUID, taskType string) (uuid.UUID, error) {
	const q = `
INSERT INTO tasks (id, repo_id, type, status)
VALUES ($1, $2, $3, 'pending')
`

	taskID := uuid.New()
	_, err := s.pool.Exec(ctx, q, taskID.String(), repoID.String(), taskType)
	if err != nil {
		return uuid.Nil, fmt.Errorf("create task: %w", err)
	}

	return taskID, nil
}

// GetActiveTask 查询是否有 running/pending 非过期 task。
func (s *RepoStore) GetActiveTask(ctx context.Context, repoID uuid.UUID, staleThreshold time.Duration) (*Task, error) {
	const q = `
SELECT
	id::text,
	repo_id::text,
	type,
	status,
	started_at,
	completed_at,
	COALESCE(error, ''),
	COALESCE(stats, '{}'::jsonb)::text,
	created_at
FROM tasks
WHERE repo_id = $1 AND status IN ('pending', 'running')
ORDER BY created_at DESC
LIMIT 1
`

	task, err := scanTaskRow(s.pool.QueryRow(ctx, q, repoID.String()))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get active task: %w", err)
	}

	if time.Since(task.CreatedAt) > staleThreshold {
		return nil, nil
	}

	return task, nil
}

// GetTask 查询 task by ID。
func (s *RepoStore) GetTask(ctx context.Context, taskID uuid.UUID) (*Task, error) {
	const q = `
SELECT
	id::text,
	repo_id::text,
	type,
	status,
	started_at,
	completed_at,
	COALESCE(error, ''),
	COALESCE(stats, '{}'::jsonb)::text,
	created_at
FROM tasks
WHERE id = $1
`

	task, err := scanTaskRow(s.pool.QueryRow(ctx, q, taskID.String()))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTaskNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get task: %w", err)
	}

	return task, nil
}

// EnqueueIndexJob 发布消息到 Redis Streams "index-jobs"。
func (s *RepoStore) EnqueueIndexJob(ctx context.Context, taskID, repoID uuid.UUID, jobType string) error {
	if s.redis == nil {
		return fmt.Errorf("enqueue index job: redis client is nil")
	}

	if err := s.redis.XAdd(ctx, &redis.XAddArgs{
		Stream: "index-jobs",
		Values: map[string]any{
			"task_id": taskID.String(),
			"repo_id": repoID.String(),
			"type":    jobType,
		},
	}).Err(); err != nil {
		return fmt.Errorf("enqueue index job: %w", err)
	}

	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTaskRow(row rowScanner) (*Task, error) {
	var (
		taskIDStr   string
		repoIDStr   string
		startedAt   sql.NullTime
		completedAt sql.NullTime
		statsJSON   string
		task        Task
	)

	err := row.Scan(
		&taskIDStr,
		&repoIDStr,
		&task.Type,
		&task.Status,
		&startedAt,
		&completedAt,
		&task.Error,
		&statsJSON,
		&task.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	taskID, err := uuid.Parse(taskIDStr)
	if err != nil {
		return nil, fmt.Errorf("parse task id: %w", err)
	}
	repoID, err := uuid.Parse(repoIDStr)
	if err != nil {
		return nil, fmt.Errorf("parse repo id: %w", err)
	}
	task.ID = taskID
	task.RepoID = repoID

	if startedAt.Valid {
		t := startedAt.Time
		task.StartedAt = &t
	}
	if completedAt.Valid {
		t := completedAt.Time
		task.CompletedAt = &t
	}

	if err := json.Unmarshal([]byte(statsJSON), &task.Stats); err != nil {
		return nil, fmt.Errorf("unmarshal task stats: %w", err)
	}
	if task.Stats == nil {
		task.Stats = map[string]any{}
	}

	return &task, nil
}

func deriveRepoStatus(latestStatus sql.NullString, latestCreated sql.NullTime, staleThreshold time.Duration) string {
	if !latestStatus.Valid || !latestCreated.Valid {
		return "registered"
	}

	isFreshActive := (latestStatus.String == "pending" || latestStatus.String == "running") &&
		time.Since(latestCreated.Time) <= staleThreshold
	if isFreshActive {
		return "indexing"
	}

	switch latestStatus.String {
	case "completed":
		return "indexed"
	case "partial":
		return "partial"
	case "failed":
		return "failed"
	default:
		return "registered"
	}
}
