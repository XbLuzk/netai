package impact

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/XbLuzk/logicmap/internal/config"
	"github.com/XbLuzk/logicmap/internal/llm"
	"github.com/XbLuzk/logicmap/internal/repo"
	"github.com/google/uuid"
)

var ErrRepoNotReady = errors.New("repo_not_ready")

type RepoNotReadyError struct {
	Status string
}

func (e *RepoNotReadyError) Error() string { return ErrRepoNotReady.Error() }
func (e *RepoNotReadyError) Unwrap() error { return ErrRepoNotReady }

type ImpactService struct {
	store     *ImpactStore
	repoStore *repo.RepoStore
	llm       llm.LLMClient
	cfg       *config.Config

	getRepoFn     func(ctx context.Context, id uuid.UUID, staleThreshold time.Duration) (*repo.Repo, error)
	getCallersFn  func(ctx context.Context, repoID uuid.UUID, functionNames []string, depth int) ([]AffectedFunction, error)
	streamLLMFn   func(ctx context.Context, msgs []llm.Message, tools []llm.ToolDef) (<-chan llm.LLMEvent, error)
	findRepoFn    func(ctx context.Context, owner, name string, staleThreshold time.Duration) (*repo.Repo, error)
	getFnsByFiles func(ctx context.Context, repoID uuid.UUID, filePaths []string) ([]string, error)
}

func NewImpactService(store *ImpactStore, repoStore *repo.RepoStore, llmClient llm.LLMClient, cfg *config.Config) *ImpactService {
	return &ImpactService{store: store, repoStore: repoStore, llm: llmClient, cfg: cfg}
}

type ImpactResult struct {
	ChangedFunctions []string         `json:"changed_functions"`
	AffectedCallers  []AffectedCaller `json:"affected_callers"`
	Summary          string           `json:"summary"`
	AffectedFiles    []string         `json:"affected_files"`
}

type AffectedCaller struct {
	Function string `json:"function"`
	File     string `json:"file"`
	Depth    int    `json:"depth"`
}

// Analyze 分析函数变更影响。
func (s *ImpactService) Analyze(ctx context.Context, repoID uuid.UUID, functionNames []string, depth int) (*ImpactResult, error) {
	repoObj, err := s.getRepo(ctx, repoID)
	if err != nil {
		return nil, err
	}
	if repoObj.Status != "indexed" && repoObj.Status != "partial" {
		return nil, &RepoNotReadyError{Status: repoObj.Status}
	}

	normalizedChanged := normalizeStrings(functionNames)
	affected, err := s.getCallersByDepth(ctx, repoID, normalizedChanged, depth)
	if err != nil {
		return nil, err
	}

	result := &ImpactResult{
		ChangedFunctions: normalizedChanged,
		AffectedCallers:  make([]AffectedCaller, 0, len(affected)),
		AffectedFiles:    []string{},
	}

	if len(affected) == 0 {
		result.Summary = "未找到相关函数"
		return result, nil
	}

	fileSeen := make(map[string]struct{})
	for _, item := range affected {
		if item.Depth <= 0 {
			continue
		}
		result.AffectedCallers = append(result.AffectedCallers, AffectedCaller{
			Function: item.Name,
			File:     item.FilePath,
			Depth:    item.Depth,
		})
		if item.FilePath != "" {
			if _, ok := fileSeen[item.FilePath]; !ok {
				fileSeen[item.FilePath] = struct{}{}
				result.AffectedFiles = append(result.AffectedFiles, item.FilePath)
			}
		}
	}

	if len(result.AffectedCallers) == 0 {
		result.Summary = "未找到相关函数"
		return result, nil
	}

	result.Summary = s.generateSummary(ctx, result)
	return result, nil
}

func (s *ImpactService) FindIndexedRepoByOwnerRepo(ctx context.Context, owner, name string) (*repo.Repo, error) {
	repoObj, err := s.findRepoByOwnerRepo(ctx, owner, name)
	if err != nil {
		return nil, err
	}
	if repoObj.Status != "indexed" && repoObj.Status != "partial" {
		return nil, repo.ErrRepoNotFound
	}
	return repoObj, nil
}

func (s *ImpactService) GetFunctionsByFilePaths(ctx context.Context, repoID uuid.UUID, filePaths []string) ([]string, error) {
	if s.getFnsByFiles != nil {
		return s.getFnsByFiles(ctx, repoID, filePaths)
	}
	if s.store == nil {
		return nil, errors.New("impact store is nil")
	}
	return s.store.GetFunctionsByFilePaths(ctx, repoID, filePaths)
}

func (s *ImpactService) getRepo(ctx context.Context, id uuid.UUID) (*repo.Repo, error) {
	if s.getRepoFn != nil {
		return s.getRepoFn(ctx, id, s.staleThreshold())
	}
	if s.repoStore == nil {
		return nil, errors.New("repo store is nil")
	}
	return s.repoStore.GetRepo(ctx, id, s.staleThreshold())
}

func (s *ImpactService) getCallersByDepth(ctx context.Context, repoID uuid.UUID, functionNames []string, depth int) ([]AffectedFunction, error) {
	if s.getCallersFn != nil {
		return s.getCallersFn(ctx, repoID, functionNames, depth)
	}
	if s.store == nil {
		return nil, errors.New("impact store is nil")
	}
	return s.store.GetCallersByDepth(ctx, repoID, functionNames, depth)
}

func (s *ImpactService) findRepoByOwnerRepo(ctx context.Context, owner, name string) (*repo.Repo, error) {
	if s.findRepoFn != nil {
		return s.findRepoFn(ctx, owner, name, s.staleThreshold())
	}
	if s.repoStore == nil {
		return nil, errors.New("repo store is nil")
	}
	return s.repoStore.FindByOwnerRepo(ctx, owner, name, s.staleThreshold())
}

func (s *ImpactService) generateSummary(ctx context.Context, result *ImpactResult) string {
	const fallback = "影响分析完成"

	payload, err := json.Marshal(result)
	if err != nil {
		return fallback
	}
	prompt := "以下是代码变更影响分析结果，请用简洁的中文描述影响范围：\n" + string(payload)

	streamFn := s.streamLLMFn
	if streamFn == nil {
		if s.llm == nil {
			return fallback
		}
		streamFn = s.llm.StreamWithTools
	}

	events, err := streamFn(ctx, []llm.Message{{Role: "user", Content: prompt}}, nil)
	if err != nil {
		return fallback
	}

	var summary strings.Builder
	for ev := range events {
		switch ev.Type {
		case llm.EventText:
			summary.WriteString(ev.TextChunk)
		case llm.EventError:
			return fallback
		}
	}
	if strings.TrimSpace(summary.String()) == "" {
		return fallback
	}
	return strings.TrimSpace(summary.String())
}

func (s *ImpactService) staleThreshold() time.Duration {
	if s.cfg == nil || s.cfg.StaleTaskThresholdMinutes <= 0 {
		return 10 * time.Minute
	}
	return time.Duration(s.cfg.StaleTaskThresholdMinutes) * time.Minute
}

func normalizeStrings(items []string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}
