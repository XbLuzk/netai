package query

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/XbLuzk/logicmap/internal/agent"
	"github.com/XbLuzk/logicmap/internal/repo"
)

type mockQueryService struct {
	queryFn func(ctx context.Context, repoID, question string) (<-chan agent.AgentEvent, error)
}

func (m *mockQueryService) Query(ctx context.Context, repoID, question string) (<-chan agent.AgentEvent, error) {
	return m.queryFn(ctx, repoID, question)
}

func TestHandleQuery_CacheMissSSE(t *testing.T) {
	ch := make(chan agent.AgentEvent, 3)
	ch <- agent.AgentEvent{Type: agent.AgentEventText, Content: "analyzing"}
	ch <- agent.AgentEvent{Type: agent.AgentEventChain, Chain: &agent.CallChain{Description: "chain ok"}}
	ch <- agent.AgentEvent{Type: agent.AgentEventDone}
	close(ch)

	h := NewQueryHandler(&mockQueryService{queryFn: func(ctx context.Context, repoID, question string) (<-chan agent.AgentEvent, error) {
		return ch, nil
	}})

	reqBody := []byte(`{"repo_id":"11111111-1111-1111-1111-111111111111","question":"哪个函数处理 HTTP 请求？"}`)
	req := httptest.NewRequest(http.MethodPost, "/query", bytes.NewReader(reqBody))
	rec := httptest.NewRecorder()

	h.HandleQuery(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "data: {") {
		t.Fatalf("expected SSE data prefix, got %q", body)
	}
	if !strings.Contains(body, "\n\n") {
		t.Fatalf("expected SSE delimiter \\n\\n, got %q", body)
	}
	if !strings.Contains(body, `"type":"text"`) {
		t.Fatalf("expected text event in body, got %q", body)
	}
	if !strings.Contains(body, `"type":"chain"`) {
		t.Fatalf("expected chain event in body, got %q", body)
	}
	if !strings.Contains(body, `"type":"done"`) {
		t.Fatalf("expected done event in body, got %q", body)
	}
}

func TestHandleQuery_RepoNotFound(t *testing.T) {
	h := NewQueryHandler(&mockQueryService{queryFn: func(ctx context.Context, repoID, question string) (<-chan agent.AgentEvent, error) {
		return nil, repo.ErrRepoNotFound
	}})

	req := httptest.NewRequest(http.MethodPost, "/query", bytes.NewBufferString(`{"repo_id":"11111111-1111-1111-1111-111111111111","question":"q"}`))
	rec := httptest.NewRecorder()

	h.HandleQuery(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rec.Code)
	}

	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got["error"] != "repo_not_found" {
		t.Fatalf("expected repo_not_found, got %s", got["error"])
	}
}

func TestHandleQuery_RepoNotReady(t *testing.T) {
	h := NewQueryHandler(&mockQueryService{queryFn: func(ctx context.Context, repoID, question string) (<-chan agent.AgentEvent, error) {
		return nil, &RepoNotReadyError{Status: "indexing"}
	}})

	req := httptest.NewRequest(http.MethodPost, "/query", bytes.NewBufferString(`{"repo_id":"11111111-1111-1111-1111-111111111111","question":"q"}`))
	rec := httptest.NewRecorder()

	h.HandleQuery(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rec.Code)
	}

	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got["error"] != "repo_not_ready" {
		t.Fatalf("expected repo_not_ready, got %s", got["error"])
	}
	if got["status"] != "indexing" {
		t.Fatalf("expected status indexing, got %s", got["status"])
	}
}

func TestHandleQuery_MissingFields(t *testing.T) {
	h := NewQueryHandler(&mockQueryService{queryFn: func(ctx context.Context, repoID, question string) (<-chan agent.AgentEvent, error) {
		return nil, errors.New("should not be called")
	}})

	req := httptest.NewRequest(http.MethodPost, "/query", bytes.NewBufferString(`{"repo_id":"11111111-1111-1111-1111-111111111111"}`))
	rec := httptest.NewRecorder()

	h.HandleQuery(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rec.Code)
	}
}
