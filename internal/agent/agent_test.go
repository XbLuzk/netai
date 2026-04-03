package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/XbLuzk/logicmap/internal/llm"
)

type mockLLM struct {
	responses []mockLLMResponse
	idx       int
	infinite  bool
}

type mockLLMResponse struct {
	events []llm.LLMEvent
	err    error
}

func (m *mockLLM) StreamWithTools(ctx context.Context, msgs []llm.Message, tools []llm.ToolDef) (<-chan llm.LLMEvent, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if m.infinite {
		ch := make(chan llm.LLMEvent, 2)
		ch <- llm.LLMEvent{Type: llm.EventToolCall, ToolCall: &llm.ToolCall{ID: "tc", Name: "get_function_source", Input: map[string]any{"repo_id": "r", "function_name": "Foo"}}}
		ch <- llm.LLMEvent{Type: llm.EventDone}
		close(ch)
		return ch, nil
	}
	if m.idx >= len(m.responses) {
		ch := make(chan llm.LLMEvent)
		close(ch)
		return ch, nil
	}
	resp := m.responses[m.idx]
	m.idx++
	if resp.err != nil {
		return nil, resp.err
	}
	ch := make(chan llm.LLMEvent, len(resp.events))
	for _, e := range resp.events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

type mockTools struct{}

func (m *mockTools) GetFunctionSource(ctx context.Context, repoID, functionName string) (string, error) {
	return "func Foo(){}", nil
}
func (m *mockTools) GetCallees(ctx context.Context, repoID, functionName string) ([]CalleeInfo, error) {
	return nil, nil
}
func (m *mockTools) GetCallers(ctx context.Context, repoID, functionName string) ([]CallerInfo, error) {
	return nil, nil
}
func (m *mockTools) SearchSimilarCode(ctx context.Context, repoID, query string) ([]SimilarCodeResult, error) {
	return nil, nil
}

func TestAgentHappyPath(t *testing.T) {
	llmClient := &mockLLM{responses: []mockLLMResponse{
		{events: []llm.LLMEvent{
			{Type: llm.EventToolCall, ToolCall: &llm.ToolCall{ID: "t1", Name: "get_function_source", Input: map[string]any{"repo_id": "r1", "function_name": "Foo"}}},
			{Type: llm.EventDone},
		}},
		{events: []llm.LLMEvent{
			{Type: llm.EventToolCall, ToolCall: &llm.ToolCall{ID: "t2", Name: "submit_chain_result", Input: map[string]any{"chain": map[string]any{"nodes": []map[string]any{{"name": "Foo", "file_path": "foo.go"}}, "edges": []map[string]any{}, "description": "ok"}}}},
			{Type: llm.EventDone},
		}},
	}}
	a := NewAgent(llmClient, &mockTools{}, "r1")

	events := collectAgentEvents(t, a.Run(context.Background(), "what is foo", "r1"))
	foundChain := false
	foundDone := false
	for _, e := range events {
		if e.Type == AgentEventChain {
			foundChain = true
			if e.Chain == nil || e.Chain.Description != "ok" {
				t.Fatalf("unexpected chain event: %+v", e)
			}
		}
		if e.Type == AgentEventDone {
			foundDone = true
		}
	}
	if !foundChain {
		t.Fatalf("expected AgentEventChain, got %+v", events)
	}
	if !foundDone {
		t.Fatalf("expected AgentEventDone, got %+v", events)
	}
}

func TestAgentToolCallLimit(t *testing.T) {
	llmClient := &mockLLM{infinite: true}
	a := NewAgent(llmClient, &mockTools{}, "r1")

	events := collectAgentEvents(t, a.Run(context.Background(), "loop", "r1"))
	foundWarn := false
	foundDone := false
	for _, e := range events {
		if e.Type == AgentEventWarning && e.Message == "response truncated: tool call limit reached" {
			foundWarn = true
		}
		if e.Type == AgentEventDone {
			foundDone = true
		}
	}
	if !foundWarn || !foundDone {
		t.Fatalf("expected warning and done, got %+v", events)
	}
}

func TestAgentContextTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	llmClient := &mockLLM{}
	a := NewAgent(llmClient, &mockTools{}, "r1")

	events := collectAgentEvents(t, a.Run(ctx, "timeout", "r1"))
	if len(events) == 0 {
		t.Fatalf("expected events, got none")
	}
	last := events[len(events)-1]
	if last.Type != AgentEventDone && last.Type != AgentEventError {
		t.Fatalf("expected done or error, got %+v", last)
	}
}

func TestAgentLLMError(t *testing.T) {
	llmClient := &mockLLM{responses: []mockLLMResponse{{err: errors.New("boom")}}}
	a := NewAgent(llmClient, &mockTools{}, "r1")

	events := collectAgentEvents(t, a.Run(context.Background(), "q", "r1"))
	if len(events) == 0 {
		t.Fatalf("expected events, got none")
	}
	if events[0].Type != AgentEventError {
		t.Fatalf("expected first event error, got %+v", events[0])
	}
}

func collectAgentEvents(t *testing.T, ch <-chan AgentEvent) []AgentEvent {
	t.Helper()
	out := make([]AgentEvent, 0)
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()

	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, e)
		case <-timer.C:
			t.Fatalf("timed out waiting for agent events")
		}
	}
}
