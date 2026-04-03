package impact

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/XbLuzk/logicmap/internal/repo"
	"github.com/google/uuid"
)

type ImpactServiceInterface interface {
	Analyze(ctx context.Context, repoID uuid.UUID, functionNames []string, depth int) (*ImpactResult, error)
}

type Handler struct {
	svc ImpactServiceInterface
}

func NewHandler(svc ImpactServiceInterface) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) HandleImpact(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepoID    string   `json:"repo_id"`
		Functions []string `json:"functions"`
		Depth     *int     `json:"depth"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeImpactJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}

	if strings.TrimSpace(req.RepoID) == "" || len(req.Functions) == 0 {
		writeImpactJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}

	depth := 3
	if req.Depth != nil {
		depth = *req.Depth
	}
	if depth <= 0 || depth > 10 {
		writeImpactJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}

	repoID, err := uuid.Parse(req.RepoID)
	if err != nil {
		writeImpactJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}

	result, err := h.svc.Analyze(r.Context(), repoID, req.Functions, depth)
	if err != nil {
		h.handleImpactError(w, err)
		return
	}

	writeImpactJSON(w, http.StatusOK, result)
}

func (h *Handler) HandleConfig(w http.ResponseWriter, _ *http.Request) {
	writeImpactJSON(w, http.StatusOK, map[string]any{
		"webhook_endpoint": "/webhooks/github",
		"webhook_events":   []string{"pull_request"},
		"webhook_actions":  []string{"opened", "synchronize"},
	})
}

func (h *Handler) handleImpactError(w http.ResponseWriter, err error) {
	if errors.Is(err, repo.ErrRepoNotFound) {
		writeImpactJSON(w, http.StatusNotFound, map[string]string{"error": "repo_not_found"})
		return
	}
	var notReady *RepoNotReadyError
	if errors.As(err, &notReady) {
		writeImpactJSON(w, http.StatusBadRequest, map[string]string{"error": "repo_not_ready", "status": notReady.Status})
		return
	}
	writeImpactJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
}

func writeImpactJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
