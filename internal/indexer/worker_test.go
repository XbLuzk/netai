package indexer

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type mockWorkerIndexer struct {
	mu               sync.Mutex
	fullCalls        int
	incrementalCalls int
	lastTaskID       uuid.UUID
	lastRepoID       uuid.UUID
}

func (m *mockWorkerIndexer) RunFullIndex(_ context.Context, taskID, repoID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fullCalls++
	m.lastTaskID = taskID
	m.lastRepoID = repoID
	return nil
}

func (m *mockWorkerIndexer) RunIncrementalIndex(_ context.Context, taskID, repoID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.incrementalCalls++
	m.lastTaskID = taskID
	m.lastRepoID = repoID
	return nil
}

type mockRedisStreamClient struct {
	mu sync.Mutex

	messages       []redis.XMessage
	xreadCalls     int
	xackIDs        []string
	xautoClaimCall int

	onAck func()
}

func (m *mockRedisStreamClient) XGroupCreateMkStream(ctx context.Context, stream, group, start string) *redis.StatusCmd {
	cmd := redis.NewStatusCmd(ctx)
	cmd.SetVal("OK")
	return cmd
}

func (m *mockRedisStreamClient) XAutoClaim(ctx context.Context, _ *redis.XAutoClaimArgs) *redis.XAutoClaimCmd {
	m.mu.Lock()
	m.xautoClaimCall++
	m.mu.Unlock()

	cmd := redis.NewXAutoClaimCmd(ctx)
	cmd.SetVal(nil, "0-0")
	return cmd
}

func (m *mockRedisStreamClient) XReadGroup(ctx context.Context, _ *redis.XReadGroupArgs) *redis.XStreamSliceCmd {
	m.mu.Lock()
	m.xreadCalls++
	callNum := m.xreadCalls
	messages := append([]redis.XMessage(nil), m.messages...)
	if callNum == 1 {
		m.messages = nil
	}
	m.mu.Unlock()

	cmd := redis.NewXStreamSliceCmd(ctx)
	if callNum == 1 && len(messages) > 0 {
		cmd.SetVal([]redis.XStream{{Stream: workerStream, Messages: messages}})
		return cmd
	}

	if ctx.Err() != nil {
		cmd.SetErr(ctx.Err())
		return cmd
	}
	cmd.SetErr(redis.Nil)
	return cmd
}

func (m *mockRedisStreamClient) XAck(ctx context.Context, stream, group string, ids ...string) *redis.IntCmd {
	m.mu.Lock()
	m.xackIDs = append(m.xackIDs, ids...)
	onAck := m.onAck
	m.mu.Unlock()

	if onAck != nil {
		onAck()
	}

	cmd := redis.NewIntCmd(ctx)
	cmd.SetVal(int64(len(ids)))
	return cmd
}

func TestWorkerProcessesMessage(t *testing.T) {
	taskID := uuid.New()
	repoID := uuid.New()

	idx := &mockWorkerIndexer{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	redisMock := &mockRedisStreamClient{
		messages: []redis.XMessage{{
			ID: "1711111111111-0",
			Values: map[string]any{
				"task_id": taskID.String(),
				"repo_id": repoID.String(),
				"type":    "full",
			},
		}},
		onAck: cancel,
	}

	w := &Worker{
		redis:             redisMock,
		indexer:           idx,
		consumerName:      "worker-test",
		autoClaimInterval: time.Hour,
		autoClaimFn: func(context.Context, string) (string, error) {
			return "0-0", nil
		},
	}

	if err := w.Start(ctx); err != nil {
		t.Fatalf("Start error: %v", err)
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()
	if idx.fullCalls != 1 {
		t.Fatalf("expected full index called once, got %d", idx.fullCalls)
	}
	if idx.lastTaskID != taskID || idx.lastRepoID != repoID {
		t.Fatalf("worker called with wrong ids: task=%s repo=%s", idx.lastTaskID, idx.lastRepoID)
	}
}

func TestWorkerXAutoClaim(t *testing.T) {
	idx := &mockWorkerIndexer{}
	redisMock := &mockRedisStreamClient{}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	w := &Worker{
		redis:             redisMock,
		indexer:           idx,
		consumerName:      "worker-test-autoclaim",
		autoClaimInterval: 20 * time.Millisecond,
		autoClaimMinIdle:  30 * time.Second,
	}
	w.autoClaimFn = w.defaultAutoClaim

	if err := w.Start(ctx); err != nil {
		t.Fatalf("Start error: %v", err)
	}

	redisMock.mu.Lock()
	defer redisMock.mu.Unlock()
	if redisMock.xautoClaimCall == 0 {
		t.Fatalf("expected XAutoClaim to be called")
	}
}
