package repo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

type mockRepoService struct {
	registerRepoFn func(ctx context.Context, path string) (uuid.UUID, bool, error)
	getRepoFn      func(ctx context.Context, id uuid.UUID) (*Repo, error)
	triggerIndexFn func(ctx context.Context, repoID uuid.UUID, jobType string) (uuid.UUID, error)
	getTaskFn      func(ctx context.Context, taskID uuid.UUID) (*Task, error)
}

func (m *mockRepoService) RegisterRepo(ctx context.Context, path string) (uuid.UUID, bool, error) {
	return m.registerRepoFn(ctx, path)
}

func (m *mockRepoService) GetRepo(ctx context.Context, id uuid.UUID) (*Repo, error) {
	return m.getRepoFn(ctx, id)
}

func (m *mockRepoService) TriggerIndex(ctx context.Context, repoID uuid.UUID, jobType string) (uuid.UUID, error) {
	return m.triggerIndexFn(ctx, repoID, jobType)
}

func (m *mockRepoService) GetTask(ctx context.Context, taskID uuid.UUID) (*Task, error) {
	return m.getTaskFn(ctx, taskID)
}

func TestPostRepos_NewReturns201(t *testing.T) {
	repoID := uuid.New()
	h := NewHandler(&mockRepoService{
		registerRepoFn: func(ctx context.Context, path string) (uuid.UUID, bool, error) {
			return repoID, true, nil
		},
		getRepoFn: func(ctx context.Context, id uuid.UUID) (*Repo, error) {
			return &Repo{ID: repoID, Status: "registered"}, nil
		},
		triggerIndexFn: func(ctx context.Context, repoID uuid.UUID, jobType string) (uuid.UUID, error) {
			return uuid.Nil, errors.New("not implemented")
		},
		getTaskFn: func(ctx context.Context, taskID uuid.UUID) (*Task, error) {
			return nil, errors.New("not implemented")
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/repos", bytes.NewBufferString(`{"path":"/tmp/repo"}`))
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", rec.Code)
	}

	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got["repo_id"] != repoID.String() {
		t.Fatalf("expected repo_id %s, got %s", repoID, got["repo_id"])
	}
	if got["status"] != "registered" {
		t.Fatalf("expected status registered, got %s", got["status"])
	}
}

func TestPostRepos_DuplicatePathReturns200SameRepoID(t *testing.T) {
	repoID := uuid.New()
	registerCalls := 0

	h := NewHandler(&mockRepoService{
		registerRepoFn: func(ctx context.Context, path string) (uuid.UUID, bool, error) {
			registerCalls++
			if registerCalls == 1 {
				return repoID, true, nil
			}
			return repoID, false, nil
		},
		getRepoFn: func(ctx context.Context, id uuid.UUID) (*Repo, error) {
			return &Repo{ID: repoID, Status: "registered"}, nil
		},
		triggerIndexFn: func(ctx context.Context, repoID uuid.UUID, jobType string) (uuid.UUID, error) {
			return uuid.Nil, errors.New("not implemented")
		},
		getTaskFn: func(ctx context.Context, taskID uuid.UUID) (*Task, error) {
			return nil, errors.New("not implemented")
		},
	})

	firstReq := httptest.NewRequest(http.MethodPost, "/repos", bytes.NewBufferString(`{"path":"/tmp/repo"}`))
	firstRec := httptest.NewRecorder()
	h.Routes().ServeHTTP(firstRec, firstReq)
	if firstRec.Code != http.StatusCreated {
		t.Fatalf("expected first status 201, got %d", firstRec.Code)
	}

	secondReq := httptest.NewRequest(http.MethodPost, "/repos", bytes.NewBufferString(`{"path":"/tmp/repo"}`))
	secondRec := httptest.NewRecorder()
	h.Routes().ServeHTTP(secondRec, secondReq)
	if secondRec.Code != http.StatusOK {
		t.Fatalf("expected second status 200, got %d", secondRec.Code)
	}

	var firstBody map[string]string
	if err := json.Unmarshal(firstRec.Body.Bytes(), &firstBody); err != nil {
		t.Fatalf("unmarshal first response: %v", err)
	}
	var secondBody map[string]string
	if err := json.Unmarshal(secondRec.Body.Bytes(), &secondBody); err != nil {
		t.Fatalf("unmarshal second response: %v", err)
	}
	if firstBody["repo_id"] != secondBody["repo_id"] {
		t.Fatalf("expected same repo_id, got %s and %s", firstBody["repo_id"], secondBody["repo_id"])
	}
}

func TestPostRepos_PathNotFoundReturns422(t *testing.T) {
	h := NewHandler(&mockRepoService{
		registerRepoFn: func(ctx context.Context, path string) (uuid.UUID, bool, error) {
			return uuid.Nil, false, ErrPathNotFound
		},
		getRepoFn: func(ctx context.Context, id uuid.UUID) (*Repo, error) {
			return nil, errors.New("not implemented")
		},
		triggerIndexFn: func(ctx context.Context, repoID uuid.UUID, jobType string) (uuid.UUID, error) {
			return uuid.Nil, errors.New("not implemented")
		},
		getTaskFn: func(ctx context.Context, taskID uuid.UUID) (*Task, error) {
			return nil, errors.New("not implemented")
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/repos", bytes.NewBufferString(`{"path":"/not/exist"}`))
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected status 422, got %d", rec.Code)
	}
}

func TestGetRepo_NotFoundReturns404(t *testing.T) {
	h := NewHandler(&mockRepoService{
		registerRepoFn: func(ctx context.Context, path string) (uuid.UUID, bool, error) {
			return uuid.Nil, false, errors.New("not implemented")
		},
		getRepoFn: func(ctx context.Context, id uuid.UUID) (*Repo, error) {
			return nil, ErrRepoNotFound
		},
		triggerIndexFn: func(ctx context.Context, repoID uuid.UUID, jobType string) (uuid.UUID, error) {
			return uuid.Nil, errors.New("not implemented")
		},
		getTaskFn: func(ctx context.Context, taskID uuid.UUID) (*Task, error) {
			return nil, errors.New("not implemented")
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/repos/"+uuid.New().String(), nil)
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rec.Code)
	}
}

func TestPostRepoIndex_ActiveTaskReturns409WithTaskID(t *testing.T) {
	repoID := uuid.New()
	existingTaskID := uuid.New()

	h := NewHandler(&mockRepoService{
		registerRepoFn: func(ctx context.Context, path string) (uuid.UUID, bool, error) {
			return uuid.Nil, false, errors.New("not implemented")
		},
		getRepoFn: func(ctx context.Context, id uuid.UUID) (*Repo, error) {
			return &Repo{ID: repoID, IndexedFiles: nil, Status: "registered"}, nil
		},
		triggerIndexFn: func(ctx context.Context, id uuid.UUID, jobType string) (uuid.UUID, error) {
			return uuid.Nil, &IndexInProgressError{TaskID: existingTaskID}
		},
		getTaskFn: func(ctx context.Context, taskID uuid.UUID) (*Task, error) {
			return nil, errors.New("not implemented")
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/repos/"+repoID.String()+"/index", bytes.NewBufferString(`{"type":"full"}`))
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d", rec.Code)
	}

	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got["task_id"] != existingTaskID.String() {
		t.Fatalf("expected task_id %s, got %s", existingTaskID, got["task_id"])
	}
}

func TestGetTask_ReturnsTaskStatus(t *testing.T) {
	taskID := uuid.New()
	repoID := uuid.New()
	now := time.Now().UTC()

	h := NewHandler(&mockRepoService{
		registerRepoFn: func(ctx context.Context, path string) (uuid.UUID, bool, error) {
			return uuid.Nil, false, errors.New("not implemented")
		},
		getRepoFn: func(ctx context.Context, id uuid.UUID) (*Repo, error) {
			return nil, errors.New("not implemented")
		},
		triggerIndexFn: func(ctx context.Context, repoID uuid.UUID, jobType string) (uuid.UUID, error) {
			return uuid.Nil, errors.New("not implemented")
		},
		getTaskFn: func(ctx context.Context, id uuid.UUID) (*Task, error) {
			return &Task{
				ID:        taskID,
				RepoID:    repoID,
				Type:      "full",
				Status:    "running",
				Stats:     map[string]any{"processed": float64(10)},
				CreatedAt: now,
			}, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/tasks/"+taskID.String(), nil)
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got["id"] != taskID.String() {
		t.Fatalf("expected task id %s, got %v", taskID, got["id"])
	}
	if got["status"] != "running" {
		t.Fatalf("expected task status running, got %v", got["status"])
	}
}
