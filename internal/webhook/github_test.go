package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/XbLuzk/logicmap/internal/config"
	"github.com/XbLuzk/logicmap/internal/impact"
	"github.com/XbLuzk/logicmap/internal/repo"
	"github.com/google/uuid"
)

type mockImpactService struct {
	findRepoFn           func(ctx context.Context, owner, name string) (*repo.Repo, error)
	getFunctionsByFileFn func(ctx context.Context, repoID uuid.UUID, filePaths []string) ([]string, error)
	analyzeFn            func(ctx context.Context, repoID uuid.UUID, functionNames []string, depth int) (*impact.ImpactResult, error)
	analyzeCalled        bool
}

func (m *mockImpactService) FindIndexedRepoByOwnerRepo(ctx context.Context, owner, name string) (*repo.Repo, error) {
	return m.findRepoFn(ctx, owner, name)
}

func (m *mockImpactService) GetFunctionsByFilePaths(ctx context.Context, repoID uuid.UUID, filePaths []string) ([]string, error) {
	return m.getFunctionsByFileFn(ctx, repoID, filePaths)
}

func (m *mockImpactService) Analyze(ctx context.Context, repoID uuid.UUID, functionNames []string, depth int) (*impact.ImpactResult, error) {
	m.analyzeCalled = true
	return m.analyzeFn(ctx, repoID, functionNames, depth)
}

func TestGitHubWebhook_ValidSignature(t *testing.T) {
	var comments []string
	gh := newGitHubStubServer(t, []string{"handler.go"}, &comments)
	defer gh.Close()

	prevBase := githubAPIBaseURL
	githubAPIBaseURL = gh.URL
	t.Cleanup(func() { githubAPIBaseURL = prevBase })

	repoID := uuid.New()
	mockSvc := &mockImpactService{
		findRepoFn: func(ctx context.Context, owner, name string) (*repo.Repo, error) {
			return &repo.Repo{ID: repoID, Status: "indexed"}, nil
		},
		getFunctionsByFileFn: func(ctx context.Context, repoID uuid.UUID, filePaths []string) ([]string, error) {
			return []string{"processOrder"}, nil
		},
		analyzeFn: func(ctx context.Context, repoID uuid.UUID, functionNames []string, depth int) (*impact.ImpactResult, error) {
			return &impact.ImpactResult{
				ChangedFunctions: []string{"processOrder"},
				AffectedCallers: []impact.AffectedCaller{
					{Function: "handleCheckout", File: "handler.go", Depth: 1},
				},
				Summary:       "修改 processOrder 将影响 1 个调用者",
				AffectedFiles: []string{"handler.go"},
			}, nil
		},
	}

	h := &GitHubWebhookHandler{
		impactSvc: mockSvc,
		cfg:       &config.Config{GitHubWebhookSecret: "secret", GitHubToken: "token"},
	}

	body := []byte(validPayloadJSON("opened"))
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-Hub-Signature-256", signBody("secret", body))
	rec := httptest.NewRecorder()

	h.HandlePRWebhook(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if !mockSvc.analyzeCalled {
		t.Fatal("expected impact Analyze to be called")
	}
	if len(comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(comments))
	}
	if !strings.Contains(comments[0], "LogicMap 影响分析") {
		t.Fatalf("unexpected comment body: %q", comments[0])
	}
}

func TestGitHubWebhook_InvalidSignature(t *testing.T) {
	mockSvc := &mockImpactService{
		findRepoFn: func(ctx context.Context, owner, name string) (*repo.Repo, error) {
			return nil, errors.New("should not be called")
		},
		getFunctionsByFileFn: func(ctx context.Context, repoID uuid.UUID, filePaths []string) ([]string, error) {
			return nil, errors.New("should not be called")
		},
		analyzeFn: func(ctx context.Context, repoID uuid.UUID, functionNames []string, depth int) (*impact.ImpactResult, error) {
			return nil, errors.New("should not be called")
		},
	}
	h := &GitHubWebhookHandler{
		impactSvc: mockSvc,
		cfg:       &config.Config{GitHubWebhookSecret: "secret"},
	}

	body := []byte(validPayloadJSON("opened"))
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-Hub-Signature-256", "sha256=wrong")
	rec := httptest.NewRecorder()

	h.HandlePRWebhook(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", rec.Code)
	}
}

func TestGitHubWebhook_NonPREvent(t *testing.T) {
	mockSvc := &mockImpactService{
		findRepoFn: func(ctx context.Context, owner, name string) (*repo.Repo, error) {
			return nil, errors.New("should not be called")
		},
		getFunctionsByFileFn: func(ctx context.Context, repoID uuid.UUID, filePaths []string) ([]string, error) {
			return nil, errors.New("should not be called")
		},
		analyzeFn: func(ctx context.Context, repoID uuid.UUID, functionNames []string, depth int) (*impact.ImpactResult, error) {
			return nil, errors.New("should not be called")
		},
	}
	h := &GitHubWebhookHandler{
		impactSvc: mockSvc,
		cfg:       &config.Config{GitHubWebhookSecret: "secret"},
	}

	body := []byte(validPayloadJSON("opened"))
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", signBody("secret", body))
	rec := httptest.NewRecorder()

	h.HandlePRWebhook(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ignored") {
		t.Fatalf("expected ignored response, got %s", rec.Body.String())
	}
}

func TestGitHubWebhook_NoIndexedFunctions(t *testing.T) {
	var comments []string
	gh := newGitHubStubServer(t, []string{"new_file.go"}, &comments)
	defer gh.Close()

	prevBase := githubAPIBaseURL
	githubAPIBaseURL = gh.URL
	t.Cleanup(func() { githubAPIBaseURL = prevBase })

	repoID := uuid.New()
	mockSvc := &mockImpactService{
		findRepoFn: func(ctx context.Context, owner, name string) (*repo.Repo, error) {
			return &repo.Repo{ID: repoID, Status: "indexed"}, nil
		},
		getFunctionsByFileFn: func(ctx context.Context, repoID uuid.UUID, filePaths []string) ([]string, error) {
			return nil, nil
		},
		analyzeFn: func(ctx context.Context, repoID uuid.UUID, functionNames []string, depth int) (*impact.ImpactResult, error) {
			return nil, errors.New("should not be called")
		},
	}
	h := &GitHubWebhookHandler{
		impactSvc: mockSvc,
		cfg:       &config.Config{GitHubWebhookSecret: "secret", GitHubToken: "token"},
	}

	body := []byte(validPayloadJSON("opened"))
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-Hub-Signature-256", signBody("secret", body))
	rec := httptest.NewRecorder()

	h.HandlePRWebhook(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if len(comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(comments))
	}
	if !strings.Contains(comments[0], "无可分析的已索引函数") {
		t.Fatalf("unexpected comment: %s", comments[0])
	}
}

func newGitHubStubServer(t *testing.T, files []string, comments *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pulls/1/files"):
			resp := make([]map[string]string, 0, len(files))
			for _, file := range files {
				resp = append(resp, map[string]string{"filename": file})
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/issues/1/comments"):
			var req struct {
				Body string `json:"body"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			*comments = append(*comments, req.Body)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":1}`))
			return
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
}

func validPayloadJSON(action string) string {
	return `{
  "action":"` + action + `",
  "pull_request":{
    "number":1,
    "base":{
      "sha":"base-sha",
      "repo":{
        "name":"logicmap",
        "owner":{"login":"XbLuzk"}
      }
    },
    "head":{"sha":"head-sha"}
  }
}`
}

func signBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
