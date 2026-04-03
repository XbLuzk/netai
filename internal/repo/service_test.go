package repo

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/XbLuzk/logicmap/internal/config"
	"github.com/google/uuid"
)

type mockStore struct {
	upsertRepoFn     func(ctx context.Context, path, name string) (uuid.UUID, bool, error)
	getRepoFn        func(ctx context.Context, id uuid.UUID, staleThreshold time.Duration) (*Repo, error)
	createTaskFn     func(ctx context.Context, repoID uuid.UUID, taskType string) (uuid.UUID, error)
	getActiveTaskFn  func(ctx context.Context, repoID uuid.UUID, staleThreshold time.Duration) (*Task, error)
	getTaskFn        func(ctx context.Context, taskID uuid.UUID) (*Task, error)
	enqueueIndexFn   func(ctx context.Context, taskID, repoID uuid.UUID, jobType string) error
	createTaskCalls  int
	enqueueTaskCalls int
}

func (m *mockStore) UpsertRepo(ctx context.Context, path, name string) (uuid.UUID, bool, error) {
	if m.upsertRepoFn == nil {
		return uuid.Nil, false, nil
	}
	return m.upsertRepoFn(ctx, path, name)
}

func (m *mockStore) GetRepo(ctx context.Context, id uuid.UUID, staleThreshold time.Duration) (*Repo, error) {
	if m.getRepoFn == nil {
		return nil, nil
	}
	return m.getRepoFn(ctx, id, staleThreshold)
}

func (m *mockStore) CreateTask(ctx context.Context, repoID uuid.UUID, taskType string) (uuid.UUID, error) {
	m.createTaskCalls++
	if m.createTaskFn == nil {
		return uuid.Nil, nil
	}
	return m.createTaskFn(ctx, repoID, taskType)
}

func (m *mockStore) GetActiveTask(ctx context.Context, repoID uuid.UUID, staleThreshold time.Duration) (*Task, error) {
	if m.getActiveTaskFn == nil {
		return nil, nil
	}
	return m.getActiveTaskFn(ctx, repoID, staleThreshold)
}

func (m *mockStore) GetTask(ctx context.Context, taskID uuid.UUID) (*Task, error) {
	if m.getTaskFn == nil {
		return nil, nil
	}
	return m.getTaskFn(ctx, taskID)
}

func (m *mockStore) EnqueueIndexJob(ctx context.Context, taskID, repoID uuid.UUID, jobType string) error {
	m.enqueueTaskCalls++
	if m.enqueueIndexFn == nil {
		return nil
	}
	return m.enqueueIndexFn(ctx, taskID, repoID, jobType)
}

func TestRegisterRepo_PathNotFound(t *testing.T) {
	svc := NewRepoService(&mockStore{}, &config.Config{StaleTaskThresholdMinutes: 10})
	missingPath := filepath.Join(t.TempDir(), "missing-dir")

	_, _, err := svc.RegisterRepo(context.Background(), missingPath)
	if !errors.Is(err, ErrPathNotFound) {
		t.Fatalf("expected ErrPathNotFound, got %v", err)
	}
}

func TestTriggerIndex_ActiveTaskConflict(t *testing.T) {
	repoID := uuid.New()
	activeTaskID := uuid.New()

	store := &mockStore{
		getRepoFn: func(ctx context.Context, id uuid.UUID, staleThreshold time.Duration) (*Repo, error) {
			return &Repo{ID: repoID}, nil
		},
		getActiveTaskFn: func(ctx context.Context, id uuid.UUID, staleThreshold time.Duration) (*Task, error) {
			return &Task{ID: activeTaskID, RepoID: repoID, Status: "running"}, nil
		},
	}

	svc := NewRepoService(store, &config.Config{StaleTaskThresholdMinutes: 10})
	_, err := svc.TriggerIndex(context.Background(), repoID, "full")
	if !errors.Is(err, ErrIndexInProgress) {
		t.Fatalf("expected ErrIndexInProgress, got %v", err)
	}

	var inProgressErr *IndexInProgressError
	if !errors.As(err, &inProgressErr) {
		t.Fatalf("expected IndexInProgressError, got %T", err)
	}
	if inProgressErr.TaskID != activeTaskID {
		t.Fatalf("expected task id %s, got %s", activeTaskID, inProgressErr.TaskID)
	}
}

func TestTriggerIndex_SuccessCallsCreateAndEnqueueOnce(t *testing.T) {
	repoID := uuid.New()
	taskID := uuid.New()

	store := &mockStore{
		getRepoFn: func(ctx context.Context, id uuid.UUID, staleThreshold time.Duration) (*Repo, error) {
			return &Repo{ID: repoID}, nil
		},
		getActiveTaskFn: func(ctx context.Context, id uuid.UUID, staleThreshold time.Duration) (*Task, error) {
			return nil, nil
		},
		createTaskFn: func(ctx context.Context, id uuid.UUID, taskType string) (uuid.UUID, error) {
			if taskType != "incremental" {
				t.Fatalf("expected task type incremental, got %s", taskType)
			}
			return taskID, nil
		},
		enqueueIndexFn: func(ctx context.Context, gotTaskID, gotRepoID uuid.UUID, jobType string) error {
			if gotTaskID != taskID {
				t.Fatalf("expected task id %s, got %s", taskID, gotTaskID)
			}
			if gotRepoID != repoID {
				t.Fatalf("expected repo id %s, got %s", repoID, gotRepoID)
			}
			if jobType != "incremental" {
				t.Fatalf("expected job type incremental, got %s", jobType)
			}
			return nil
		},
	}

	svc := NewRepoService(store, &config.Config{StaleTaskThresholdMinutes: 10})
	gotTaskID, err := svc.TriggerIndex(context.Background(), repoID, "incremental")
	if err != nil {
		t.Fatalf("TriggerIndex error: %v", err)
	}
	if gotTaskID != taskID {
		t.Fatalf("expected task id %s, got %s", taskID, gotTaskID)
	}
	if store.createTaskCalls != 1 {
		t.Fatalf("expected CreateTask called once, got %d", store.createTaskCalls)
	}
	if store.enqueueTaskCalls != 1 {
		t.Fatalf("expected EnqueueIndexJob called once, got %d", store.enqueueTaskCalls)
	}
}
