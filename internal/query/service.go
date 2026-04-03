package query

import (
	"context"
	"errors"
	"time"

	"github.com/XbLuzk/logicmap/internal/agent"
	"github.com/XbLuzk/logicmap/internal/config"
	"github.com/XbLuzk/logicmap/internal/repo"
	"github.com/google/uuid"
)

type QueryService struct {
	repoStore *repo.RepoStore
	agent     *agent.Agent
	cache     *QueryCache
	cfg       *config.Config

	getRepoFn  func(ctx context.Context, id uuid.UUID, staleThreshold time.Duration) (*repo.Repo, error)
	runAgentFn func(ctx context.Context, question, repoID string) <-chan agent.AgentEvent
	cacheGetFn func(ctx context.Context, key string) ([]CachedEvent, error)
	cacheSetFn func(ctx context.Context, key string, events []CachedEvent)
}

func NewQueryService(repoStore *repo.RepoStore, ag *agent.Agent, cache *QueryCache, cfg *config.Config) *QueryService {
	return &QueryService{
		repoStore: repoStore,
		agent:     ag,
		cache:     cache,
		cfg:       cfg,
	}
}

var ErrRepoNotReady = errors.New("repo_not_ready")

type RepoNotReadyError struct {
	Status string
}

func (e *RepoNotReadyError) Error() string { return "repo_not_ready" }
func (e *RepoNotReadyError) Unwrap() error { return ErrRepoNotReady }

// Query 执行查询，返回 AgentEvent channel
func (s *QueryService) Query(ctx context.Context, repoID, question string) (<-chan agent.AgentEvent, error) {
	repoUUID, err := uuid.Parse(repoID)
	if err != nil {
		return nil, repo.ErrRepoNotFound
	}

	repoObj, err := s.getRepo(ctx, repoUUID)
	if err != nil {
		return nil, err
	}

	if repoObj.Status != "indexed" && repoObj.Status != "partial" {
		return nil, &RepoNotReadyError{Status: repoObj.Status}
	}

	key := s.cacheKey(repoID, question)
	cached, err := s.cacheGet(ctx, key)
	if err != nil {
		return nil, err
	}
	if len(cached) > 0 {
		return cachedEventsToChannel(cached), nil
	}

	in := s.runAgent(ctx, question, repoID)
	out := make(chan agent.AgentEvent, 32)

	go func() {
		defer close(out)

		captured := make([]CachedEvent, 0, 16)
		shouldPersist := false

		for ev := range in {
			out <- ev
			cachedEv := CachedEvent{Type: string(ev.Type), Content: ev.Content, Chain: ev.Chain, Message: ev.Message}
			if ev.Type == agent.AgentEventError {
				if ev.Err != nil {
					cachedEv.Message = ev.Err.Error()
				}
				shouldPersist = true
			}
			if ev.Type == agent.AgentEventDone {
				shouldPersist = true
			}
			captured = append(captured, cachedEv)
			if shouldPersist {
				s.cacheSet(ctx, key, captured)
				return
			}
		}

		if len(captured) > 0 {
			s.cacheSet(ctx, key, captured)
		}
	}()

	return out, nil
}

func cachedEventsToChannel(events []CachedEvent) <-chan agent.AgentEvent {
	out := make(chan agent.AgentEvent, len(events))
	go func() {
		defer close(out)
		for _, ev := range events {
			item := agent.AgentEvent{
				Type:    agent.AgentEventType(ev.Type),
				Content: ev.Content,
				Chain:   ev.Chain,
				Message: ev.Message,
			}
			if item.Type == agent.AgentEventError && item.Message != "" {
				item.Err = errors.New(item.Message)
			}
			out <- item
		}
	}()
	return out
}

func (s *QueryService) getRepo(ctx context.Context, id uuid.UUID) (*repo.Repo, error) {
	if s.getRepoFn != nil {
		return s.getRepoFn(ctx, id, s.staleThreshold())
	}
	if s.repoStore == nil {
		return nil, errors.New("repo store is nil")
	}
	return s.repoStore.GetRepo(ctx, id, s.staleThreshold())
}

func (s *QueryService) runAgent(ctx context.Context, question, repoID string) <-chan agent.AgentEvent {
	if s.runAgentFn != nil {
		return s.runAgentFn(ctx, question, repoID)
	}
	if s.agent == nil {
		ch := make(chan agent.AgentEvent)
		close(ch)
		return ch
	}
	return s.agent.Run(ctx, question, repoID)
}

func (s *QueryService) cacheGet(ctx context.Context, key string) ([]CachedEvent, error) {
	if s.cacheGetFn != nil {
		return s.cacheGetFn(ctx, key)
	}
	if s.cache == nil {
		return nil, nil
	}
	return s.cache.Get(ctx, key)
}

func (s *QueryService) cacheSet(ctx context.Context, key string, events []CachedEvent) {
	if s.cacheSetFn != nil {
		s.cacheSetFn(ctx, key, events)
		return
	}
	if s.cache == nil {
		return
	}
	s.cache.Set(ctx, key, events)
}

func (s *QueryService) cacheKey(repoID, question string) string {
	if s.cache != nil {
		return s.cache.CacheKey(repoID, question)
	}
	return NewQueryCache(nil, 0).CacheKey(repoID, question)
}

func (s *QueryService) staleThreshold() time.Duration {
	if s.cfg == nil || s.cfg.StaleTaskThresholdMinutes <= 0 {
		return 10 * time.Minute
	}
	return time.Duration(s.cfg.StaleTaskThresholdMinutes) * time.Minute
}
