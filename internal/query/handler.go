package query

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/XbLuzk/logicmap/internal/agent"
	"github.com/XbLuzk/logicmap/internal/repo"
	"github.com/google/uuid"
)

type QueryServiceInterface interface {
	Query(ctx context.Context, repoID, question string) (<-chan agent.AgentEvent, error)
}

type QueryHandler struct {
	svc QueryServiceInterface
}

func NewQueryHandler(svc QueryServiceInterface) *QueryHandler {
	return &QueryHandler{svc: svc}
}

func (h *QueryHandler) HandleQuery(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepoID   string `json:"repo_id"`
		Question string `json:"question"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeQueryJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	if strings.TrimSpace(req.RepoID) == "" || strings.TrimSpace(req.Question) == "" {
		writeQueryJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	if _, err := uuid.Parse(req.RepoID); err != nil {
		writeQueryJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}

	stream, err := h.svc.Query(r.Context(), req.RepoID, req.Question)
	if err != nil {
		h.handleQueryError(w, err)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeQueryJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	for ev := range stream {
		payload := map[string]any{"type": ev.Type}
		switch ev.Type {
		case agent.AgentEventText:
			payload["content"] = ev.Content
		case agent.AgentEventChain:
			payload["chain"] = ev.Chain
		case agent.AgentEventWarning, agent.AgentEventDone:
			if ev.Message != "" {
				payload["message"] = ev.Message
			}
		case agent.AgentEventError:
			payload = map[string]any{"type": agent.AgentEventError, "message": errorMessage(ev)}
		}

		if err := writeSSEData(w, payload); err != nil {
			return
		}
		flusher.Flush()

		if ev.Type == agent.AgentEventError {
			return
		}
	}
}

func (h *QueryHandler) handleQueryError(w http.ResponseWriter, err error) {
	if errors.Is(err, repo.ErrRepoNotFound) {
		writeQueryJSON(w, http.StatusNotFound, map[string]string{"error": "repo_not_found"})
		return
	}
	var notReady *RepoNotReadyError
	if errors.As(err, &notReady) {
		writeQueryJSON(w, http.StatusBadRequest, map[string]string{"error": "repo_not_ready", "status": notReady.Status})
		return
	}
	writeQueryJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
}

func writeSSEData(w http.ResponseWriter, payload any) error {
	buf, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", string(buf))
	return err
}

func writeQueryJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func errorMessage(ev agent.AgentEvent) string {
	if ev.Err != nil {
		return ev.Err.Error()
	}
	if ev.Message != "" {
		return ev.Message
	}
	return "internal_error"
}
