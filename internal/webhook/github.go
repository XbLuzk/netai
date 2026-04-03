package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/XbLuzk/logicmap/internal/config"
	"github.com/XbLuzk/logicmap/internal/impact"
	"github.com/XbLuzk/logicmap/internal/repo"
	"github.com/google/uuid"
)

var githubAPIBaseURL = "https://api.github.com"

type GitHubWebhookHandler struct {
	impactSvc impactService
	cfg       *config.Config
}

type impactService interface {
	FindIndexedRepoByOwnerRepo(ctx context.Context, owner, name string) (*repo.Repo, error)
	GetFunctionsByFilePaths(ctx context.Context, repoID uuid.UUID, filePaths []string) ([]string, error)
	Analyze(ctx context.Context, repoID uuid.UUID, functionNames []string, depth int) (*impact.ImpactResult, error)
}

func NewGitHubWebhookHandler(impactSvc *impact.ImpactService, cfg *config.Config) *GitHubWebhookHandler {
	return &GitHubWebhookHandler{impactSvc: impactSvc, cfg: cfg}
}

// HandlePRWebhook 处理 GitHub PR webhook。
// POST /webhooks/github
func (h *GitHubWebhookHandler) HandlePRWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeWebhookJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}

	signature := r.Header.Get("X-Hub-Signature-256")
	if !verifySignature([]byte(h.cfg.GitHubWebhookSecret), body, signature) {
		writeWebhookJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_signature"})
		return
	}

	if r.Header.Get("X-GitHub-Event") != "pull_request" {
		writeWebhookJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}

	var payload struct {
		Action      string `json:"action"`
		PullRequest struct {
			Number int `json:"number"`
			Base   struct {
				SHA  string `json:"sha"`
				Repo struct {
					Name  string `json:"name"`
					Owner struct {
						Login string `json:"login"`
					} `json:"owner"`
				} `json:"repo"`
			} `json:"base"`
			Head struct {
				SHA string `json:"sha"`
			} `json:"head"`
		} `json:"pull_request"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		writeWebhookJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}

	if payload.Action != "opened" && payload.Action != "synchronize" {
		writeWebhookJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}

	owner := payload.PullRequest.Base.Repo.Owner.Login
	repoName := payload.PullRequest.Base.Repo.Name
	pullNumber := payload.PullRequest.Number
	baseSHA := payload.PullRequest.Base.SHA
	headSHA := payload.PullRequest.Head.SHA
	_ = baseSHA
	_ = headSHA

	changedFiles, err := h.fetchPRFiles(r.Context(), owner, repoName, pullNumber)
	if err != nil {
		writeWebhookJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	matchedRepo, err := h.impactSvc.FindIndexedRepoByOwnerRepo(r.Context(), owner, repoName)
	if err != nil {
		comment := "未找到对应的 LogicMap 索引，请先注册并索引该仓库"
		if postErr := h.postPRComment(r.Context(), owner, repoName, pullNumber, comment); postErr != nil {
			writeWebhookJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
			return
		}
		writeWebhookJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	functions, err := h.impactSvc.GetFunctionsByFilePaths(r.Context(), matchedRepo.ID, changedFiles)
	if err != nil {
		writeWebhookJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	if len(functions) == 0 {
		markdown := "## LogicMap 影响分析\n\n无可分析的已索引函数（变更文件可能为新文件或未索引语言）。"
		if err := h.postPRComment(r.Context(), owner, repoName, pullNumber, markdown); err != nil {
			writeWebhookJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
			return
		}
		writeWebhookJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	result, err := h.impactSvc.Analyze(r.Context(), matchedRepo.ID, functions, 3)
	if err != nil {
		writeWebhookJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	markdown := buildImpactCommentMarkdown(changedFiles, result)
	if err := h.postPRComment(r.Context(), owner, repoName, pullNumber, markdown); err != nil {
		writeWebhookJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	writeWebhookJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *GitHubWebhookHandler) fetchPRFiles(ctx context.Context, owner, repo string, pullNumber int) ([]string, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/files", githubAPIBaseURL, owner, repo, pullNumber)
	resp, err := githubRequest(ctx, h.cfg.GitHubToken, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("github files api: http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var files []struct {
		Filename string `json:"filename"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&files); err != nil {
		return nil, fmt.Errorf("decode github files response: %w", err)
	}

	seen := make(map[string]struct{})
	paths := make([]string, 0, len(files))
	for _, file := range files {
		name := strings.TrimSpace(file.Filename)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		paths = append(paths, name)
	}
	sort.Strings(paths)
	return paths, nil
}

func (h *GitHubWebhookHandler) postPRComment(ctx context.Context, owner, repo string, pullNumber int, markdown string) error {
	url := fmt.Sprintf("%s/repos/%s/%s/issues/%d/comments", githubAPIBaseURL, owner, repo, pullNumber)
	payload := map[string]string{"body": markdown}
	resp, err := githubRequest(ctx, h.cfg.GitHubToken, http.MethodPost, url, payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("github comment api: http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func buildImpactCommentMarkdown(changedFiles []string, result *impact.ImpactResult) string {
	if result == nil || len(result.AffectedCallers) == 0 {
		return "## LogicMap 影响分析\n\n无可分析的已索引函数（变更文件可能为新文件或未索引语言）。"
	}

	var b strings.Builder
	b.WriteString("## LogicMap 影响分析\n\n")
	b.WriteString(fmt.Sprintf("**变更文件**：%d 个文件 | **受影响调用者**：%d 个函数\n\n", len(changedFiles), len(result.AffectedCallers)))
	b.WriteString("| 函数 | 文件 | 调用深度 |\n")
	b.WriteString("|------|------|---------|\n")
	for _, caller := range result.AffectedCallers {
		b.WriteString(fmt.Sprintf("| %s | %s | %d |\n", caller.Function, caller.File, caller.Depth))
	}
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("**分析摘要**：%s\n\n", strings.TrimSpace(result.Summary)))
	b.WriteString("---\n")
	b.WriteString("*由 [LogicMap](https://github.com/XbLuzk/logicmap) 自动生成*")
	return b.String()
}

func verifySignature(secret, body []byte, signature string) bool {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

// githubRequest 发送带 token 认证的 GitHub API 请求。
func githubRequest(ctx context.Context, token, method, url string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal github request body: %w", err)
		}
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, fmt.Errorf("create github request: %w", err)
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send github request: %w", err)
	}
	return resp, nil
}

func writeWebhookJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
