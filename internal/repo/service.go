package repo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/XbLuzk/logicmap/internal/config"
	"github.com/google/uuid"
)

type repoStore interface {
	UpsertRepo(ctx context.Context, path, name string) (id uuid.UUID, created bool, err error)
	GetRepo(ctx context.Context, id uuid.UUID, staleThreshold time.Duration) (*Repo, error)
	CreateTask(ctx context.Context, repoID uuid.UUID, taskType string) (uuid.UUID, error)
	GetActiveTask(ctx context.Context, repoID uuid.UUID, staleThreshold time.Duration) (*Task, error)
	GetTask(ctx context.Context, taskID uuid.UUID) (*Task, error)
	EnqueueIndexJob(ctx context.Context, taskID, repoID uuid.UUID, jobType string) error
}

type RepoService struct {
	store repoStore
	cfg   *config.Config
}

func NewRepoService(store repoStore, cfg *config.Config) *RepoService {
	return &RepoService{store: store, cfg: cfg}
}

// RegisterRepo 验证 path 在本地文件系统存在，幂等 upsert。
func (s *RepoService) RegisterRepo(ctx context.Context, path string) (uuid.UUID, bool, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return uuid.Nil, false, fmt.Errorf("%w: %s", ErrPathNotFound, path)
		}
		return uuid.Nil, false, fmt.Errorf("stat path: %w", err)
	}

	name := filepath.Base(filepath.Clean(path))
	id, created, err := s.store.UpsertRepo(ctx, path, name)
	if err != nil {
		return uuid.Nil, false, err
	}

	return id, created, nil
}

// GetRepo 从 store 获取 repo（status 已派生）。
func (s *RepoService) GetRepo(ctx context.Context, id uuid.UUID) (*Repo, error) {
	return s.store.GetRepo(ctx, id, s.staleThreshold())
}

// TriggerIndex 触发索引 task，并投递到 Redis Streams。
func (s *RepoService) TriggerIndex(ctx context.Context, repoID uuid.UUID, jobType string) (uuid.UUID, error) {
	if _, err := s.store.GetRepo(ctx, repoID, s.staleThreshold()); err != nil {
		return uuid.Nil, err
	}

	activeTask, err := s.store.GetActiveTask(ctx, repoID, s.staleThreshold())
	if err != nil {
		return uuid.Nil, err
	}
	if activeTask != nil {
		return uuid.Nil, &IndexInProgressError{TaskID: activeTask.ID}
	}

	taskID, err := s.store.CreateTask(ctx, repoID, jobType)
	if err != nil {
		return uuid.Nil, err
	}

	if err := s.store.EnqueueIndexJob(ctx, taskID, repoID, jobType); err != nil {
		return uuid.Nil, err
	}

	return taskID, nil
}

// GetTask 查询 task。
func (s *RepoService) GetTask(ctx context.Context, taskID uuid.UUID) (*Task, error) {
	return s.store.GetTask(ctx, taskID)
}

func (s *RepoService) staleThreshold() time.Duration {
	if s.cfg == nil || s.cfg.StaleTaskThresholdMinutes <= 0 {
		return 10 * time.Minute
	}
	return time.Duration(s.cfg.StaleTaskThresholdMinutes) * time.Minute
}
