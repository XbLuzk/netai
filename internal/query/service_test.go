package query

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/XbLuzk/logicmap/internal/agent"
	"github.com/XbLuzk/logicmap/internal/config"
	"github.com/XbLuzk/logicmap/internal/repo"
	"github.com/google/uuid"
)

func TestQueryService_CacheHit(t *testing.T) {
	repoID := uuid.New().String()
	agentCalled := false

	svc := &QueryService{
		cfg: &config.Config{StaleTaskThresholdMinutes: 10},
		getRepoFn: func(ctx context.Context, id uuid.UUID, staleThreshold time.Duration) (*repo.Repo, error) {
			return &repo.Repo{ID: id, Status: "indexed"}, nil
		},
		cacheGetFn: func(ctx context.Context, key string) ([]CachedEvent, error) {
			return []CachedEvent{
				{Type: string(agent.AgentEventText), Content: "from cache"},
				{Type: string(agent.AgentEventDone)},
			}, nil
		},
		runAgentFn: func(ctx context.Context, question, repoID string) <-chan agent.AgentEvent {
			agentCalled = true
			ch := make(chan agent.AgentEvent)
			close(ch)
			return ch
		},
	}

	ch, err := svc.Query(context.Background(), repoID, "q")
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}

	events := collectEvents(t, ch)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Type != agent.AgentEventText || events[0].Content != "from cache" {
		t.Fatalf("unexpected first event: %+v", events[0])
	}
	if events[1].Type != agent.AgentEventDone {
		t.Fatalf("unexpected second event: %+v", events[1])
	}
	if agentCalled {
		t.Fatalf("agent should not be called on cache hit")
	}
}

func TestQueryService_CacheMiss(t *testing.T) {
	repoID := uuid.New().String()
	agentCalled := false
	cacheSetCalled := make(chan []CachedEvent, 1)

	svc := &QueryService{
		cfg: &config.Config{StaleTaskThresholdMinutes: 10},
		getRepoFn: func(ctx context.Context, id uuid.UUID, staleThreshold time.Duration) (*repo.Repo, error) {
			return &repo.Repo{ID: id, Status: "indexed"}, nil
		},
		cacheGetFn: func(ctx context.Context, key string) ([]CachedEvent, error) {
			return nil, nil
		},
		cacheSetFn: func(ctx context.Context, key string, events []CachedEvent) {
			cacheSetCalled <- events
		},
		runAgentFn: func(ctx context.Context, question, repoID string) <-chan agent.AgentEvent {
			agentCalled = true
			ch := make(chan agent.AgentEvent, 2)
			ch <- agent.AgentEvent{Type: agent.AgentEventText, Content: "streaming"}
			ch <- agent.AgentEvent{Type: agent.AgentEventDone}
			close(ch)
			return ch
		},
	}

	ch, err := svc.Query(context.Background(), repoID, "q")
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}

	events := collectEvents(t, ch)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if !agentCalled {
		t.Fatalf("agent should be called on cache miss")
	}

	select {
	case got := <-cacheSetCalled:
		if len(got) != 2 {
			t.Fatalf("expected 2 cached events, got %d", len(got))
		}
		if got[0].Type != string(agent.AgentEventText) || got[1].Type != string(agent.AgentEventDone) {
			t.Fatalf("unexpected cached events: %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for cache set call")
	}
}

func TestQueryService_RepoNotFound(t *testing.T) {
	repoID := uuid.New().String()

	svc := &QueryService{
		getRepoFn: func(ctx context.Context, id uuid.UUID, staleThreshold time.Duration) (*repo.Repo, error) {
			return nil, repo.ErrRepoNotFound
		},
	}

	_, err := svc.Query(context.Background(), repoID, "q")
	if !errors.Is(err, repo.ErrRepoNotFound) {
		t.Fatalf("expected ErrRepoNotFound, got %v", err)
	}
}

func TestQueryService_RepoNotReady(t *testing.T) {
	repoID := uuid.New().String()

	svc := &QueryService{
		getRepoFn: func(ctx context.Context, id uuid.UUID, staleThreshold time.Duration) (*repo.Repo, error) {
			return &repo.Repo{ID: id, Status: "indexing"}, nil
		},
	}

	_, err := svc.Query(context.Background(), repoID, "q")
	if !errors.Is(err, ErrRepoNotReady) {
		t.Fatalf("expected ErrRepoNotReady, got %v", err)
	}

	var notReady *RepoNotReadyError
	if !errors.As(err, &notReady) {
		t.Fatalf("expected RepoNotReadyError, got %T", err)
	}
	if notReady.Status != "indexing" {
		t.Fatalf("expected status indexing, got %s", notReady.Status)
	}
}

func collectEvents(t *testing.T, ch <-chan agent.AgentEvent) []agent.AgentEvent {
	t.Helper()

	out := make([]agent.AgentEvent, 0, 4)
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()

	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-timer.C:
			t.Fatal("timed out waiting for events")
		}
	}
}
