package repo

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type repoService interface {
	RegisterRepo(ctx context.Context, path string) (uuid.UUID, bool, error)
	GetRepo(ctx context.Context, id uuid.UUID) (*Repo, error)
	TriggerIndex(ctx context.Context, repoID uuid.UUID, jobType string) (uuid.UUID, error)
	GetTask(ctx context.Context, taskID uuid.UUID) (*Task, error)
}

type Handler struct {
	svc repoService
}

func NewHandler(svc repoService) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/repos", h.handleRegisterRepo)
	r.Get("/repos/{id}", h.handleGetRepo)
	r.Post("/repos/{id}/index", h.handleTriggerIndex)
	r.Get("/tasks/{id}", h.handleGetTask)
	return r
}

func (h *Handler) handleRegisterRepo(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	if strings.TrimSpace(req.Path) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}

	repoID, created, err := h.svc.RegisterRepo(r.Context(), req.Path)
	if err != nil {
		if errors.Is(err, ErrPathNotFound) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "path_not_found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	repo, err := h.svc.GetRepo(r.Context(), repoID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	statusCode := http.StatusCreated
	if !created {
		statusCode = http.StatusOK
	}
	writeJSON(w, statusCode, map[string]string{
		"repo_id": repoID.String(),
		"status":  repo.Status,
	})
}

func (h *Handler) handleGetRepo(w http.ResponseWriter, r *http.Request) {
	repoID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "repo_not_found"})
		return
	}

	repo, err := h.svc.GetRepo(r.Context(), repoID)
	if err != nil {
		if errors.Is(err, ErrRepoNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "repo_not_found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"repo_id":    repo.ID.String(),
		"path":       repo.Path,
		"name":       repo.Name,
		"status":     repo.Status,
		"created_at": repo.CreatedAt,
	})
}

func (h *Handler) handleTriggerIndex(w http.ResponseWriter, r *http.Request) {
	repoID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "repo_not_found"})
		return
	}

	jobType := ""
	if r.Body != nil {
		var req struct {
			Type string `json:"type"`
		}
		err := json.NewDecoder(r.Body).Decode(&req)
		if err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
			return
		}
		if req.Type != "" && req.Type != "full" && req.Type != "incremental" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_type"})
			return
		}
		jobType = req.Type
	}

	if jobType == "" {
		repo, err := h.svc.GetRepo(r.Context(), repoID)
		if err != nil {
			if errors.Is(err, ErrRepoNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "repo_not_found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
			return
		}

		if len(repo.IndexedFiles) > 0 {
			jobType = "incremental"
		} else {
			jobType = "full"
		}
	}

	taskID, err := h.svc.TriggerIndex(r.Context(), repoID, jobType)
	if err != nil {
		if errors.Is(err, ErrRepoNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "repo_not_found"})
			return
		}
		if errors.Is(err, ErrIndexInProgress) {
			resp := map[string]any{"error": "index_in_progress"}
			var inProgress *IndexInProgressError
			if errors.As(err, &inProgress) {
				resp["task_id"] = inProgress.TaskID.String()
			}
			writeJSON(w, http.StatusConflict, resp)
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]string{
		"task_id": taskID.String(),
		"type":    jobType,
	})
}

func (h *Handler) handleGetTask(w http.ResponseWriter, r *http.Request) {
	taskID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "task_not_found"})
		return
	}

	task, err := h.svc.GetTask(r.Context(), taskID)
	if err != nil {
		if errors.Is(err, ErrTaskNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "task_not_found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":           task.ID.String(),
		"repo_id":      task.RepoID.String(),
		"type":         task.Type,
		"status":       task.Status,
		"started_at":   task.StartedAt,
		"completed_at": task.CompletedAt,
		"error":        task.Error,
		"stats":        task.Stats,
		"created_at":   task.CreatedAt,
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
