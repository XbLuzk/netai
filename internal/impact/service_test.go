package impact

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/XbLuzk/logicmap/internal/config"
	"github.com/XbLuzk/logicmap/internal/llm"
	"github.com/XbLuzk/logicmap/internal/repo"
	"github.com/google/uuid"
)

type mockImpactLLM struct {
	events []llm.LLMEvent
	err    error
}

func (m *mockImpactLLM) StreamWithTools(ctx context.Context, msgs []llm.Message, tools []llm.ToolDef) (<-chan llm.LLMEvent, error) {
	if m.err != nil {
		return nil, m.err
	}
	ch := make(chan llm.LLMEvent, len(m.events))
	for _, ev := range m.events {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

func TestAnalyze_HappyPath(t *testing.T) {
	repoID := uuid.New()
	svc := &ImpactService{
		cfg: &config.Config{StaleTaskThresholdMinutes: 10},
		getRepoFn: func(ctx context.Context, id uuid.UUID, staleThreshold time.Duration) (*repo.Repo, error) {
			return &repo.Repo{ID: repoID, Status: "indexed"}, nil
		},
		getCallersFn: func(ctx context.Context, id uuid.UUID, functionNames []string, depth int) ([]AffectedFunction, error) {
			return []AffectedFunction{
				{Name: "processOrder", FilePath: "service/order.go", Depth: 0},
				{Name: "handleCheckout", FilePath: "handler.go", Depth: 1},
				{Name: "TestHandleCheckout", FilePath: "handler_test.go", Depth: 2},
			}, nil
		},
		llm: &mockImpactLLM{events: []llm.LLMEvent{
			{Type: llm.EventText, TextChunk: "修改 processOrder 将影响 2 个调用者"},
			{Type: llm.EventDone},
		}},
	}

	result, err := svc.Analyze(context.Background(), repoID, []string{"processOrder", "validateInput"}, 3)
	if err != nil {
		t.Fatalf("Analyze error: %v", err)
	}

	if len(result.ChangedFunctions) != 2 {
		t.Fatalf("expected 2 changed functions, got %d", len(result.ChangedFunctions))
	}
	if len(result.AffectedCallers) != 2 {
		t.Fatalf("expected 2 affected callers, got %d", len(result.AffectedCallers))
	}
	if result.Summary == "" {
		t.Fatalf("expected non-empty summary")
	}
}

func TestAnalyze_FunctionNotFound(t *testing.T) {
	repoID := uuid.New()
	svc := &ImpactService{
		cfg: &config.Config{StaleTaskThresholdMinutes: 10},
		getRepoFn: func(ctx context.Context, id uuid.UUID, staleThreshold time.Duration) (*repo.Repo, error) {
			return &repo.Repo{ID: repoID, Status: "indexed"}, nil
		},
		getCallersFn: func(ctx context.Context, id uuid.UUID, functionNames []string, depth int) ([]AffectedFunction, error) {
			return nil, nil
		},
		llm: &mockImpactLLM{},
	}

	result, err := svc.Analyze(context.Background(), repoID, []string{"missingFn"}, 3)
	if err != nil {
		t.Fatalf("Analyze error: %v", err)
	}
	if result.Summary != "未找到相关函数" {
		t.Fatalf("expected summary 未找到相关函数, got %q", result.Summary)
	}
}

func TestAnalyze_RepoNotFound(t *testing.T) {
	repoID := uuid.New()
	svc := &ImpactService{
		cfg: &config.Config{StaleTaskThresholdMinutes: 10},
		getRepoFn: func(ctx context.Context, id uuid.UUID, staleThreshold time.Duration) (*repo.Repo, error) {
			return nil, repo.ErrRepoNotFound
		},
	}

	_, err := svc.Analyze(context.Background(), repoID, []string{"x"}, 3)
	if !errors.Is(err, repo.ErrRepoNotFound) {
		t.Fatalf("expected ErrRepoNotFound, got %v", err)
	}
}

func TestAnalyze_CycleDetection(t *testing.T) {
	repoID := uuid.New()
	svc := &ImpactService{
		cfg: &config.Config{StaleTaskThresholdMinutes: 10},
		getRepoFn: func(ctx context.Context, id uuid.UUID, staleThreshold time.Duration) (*repo.Repo, error) {
			return &repo.Repo{ID: repoID, Status: "indexed"}, nil
		},
		getCallersFn: func(ctx context.Context, id uuid.UUID, functionNames []string, depth int) ([]AffectedFunction, error) {
			// 模拟 A -> B -> A 环，查询应快速返回且不死循环。
			return []AffectedFunction{
				{Name: "A", FilePath: "a.go", Depth: 0},
				{Name: "B", FilePath: "b.go", Depth: 1},
				{Name: "A", FilePath: "a.go", Depth: 2},
			}, nil
		},
		llm: &mockImpactLLM{events: []llm.LLMEvent{
			{Type: llm.EventText, TextChunk: "存在循环调用但已受深度限制"},
			{Type: llm.EventDone},
		}},
	}

	done := make(chan struct{})
	go func() {
		_, _ = svc.Analyze(context.Background(), repoID, []string{"A"}, 3)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Analyze should not hang on cyclic graph")
	}
}
