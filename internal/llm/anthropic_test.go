package llm

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAnthropicStreamText(t *testing.T) {
	client := NewAnthropicClient("k", "m", mockHTTPClient(200, "text/event-stream", strings.Join([]string{
		"data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hello \"}}",
		"",
		"data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"world\"}}",
		"",
		"data: {\"type\":\"message_stop\"}",
		"",
		"",
	}, "\n")))
	client.baseURL = "http://anthropic.mock/v1/messages"

	events, err := client.StreamWithTools(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("StreamWithTools() error = %v", err)
	}

	got := collectLLMEvents(t, events)
	if len(got) < 3 {
		t.Fatalf("expected at least 3 events, got %d", len(got))
	}
	if got[0].Type != EventText || got[0].TextChunk != "hello " {
		t.Fatalf("unexpected first event: %+v", got[0])
	}
	if got[1].Type != EventText || got[1].TextChunk != "world" {
		t.Fatalf("unexpected second event: %+v", got[1])
	}
	if got[len(got)-1].Type != EventDone {
		t.Fatalf("expected final done event, got %+v", got[len(got)-1])
	}
}

func TestAnthropicStreamToolCall(t *testing.T) {
	client := NewAnthropicClient("k", "m", mockHTTPClient(200, "text/event-stream", strings.Join([]string{
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"get_function_source\"}}",
		"",
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"repo_id\\\":\\\"r1\\\",\"}}",
		"",
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"\\\"function_name\\\":\\\"Foo\\\"}\"}}",
		"",
		"data: {\"type\":\"content_block_stop\",\"index\":0}",
		"",
		"data: {\"type\":\"message_stop\"}",
		"",
		"",
	}, "\n")))
	client.baseURL = "http://anthropic.mock/v1/messages"

	events, err := client.StreamWithTools(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("StreamWithTools() error = %v", err)
	}

	got := collectLLMEvents(t, events)
	var tool *ToolCall
	for _, e := range got {
		if e.Type == EventToolCall {
			tool = e.ToolCall
			break
		}
	}
	if tool == nil {
		t.Fatalf("expected tool call event, got %+v", got)
	}
	if tool.ID != "toolu_1" || tool.Name != "get_function_source" {
		t.Fatalf("unexpected tool metadata: %+v", tool)
	}
	if tool.Input["repo_id"] != "r1" || tool.Input["function_name"] != "Foo" {
		t.Fatalf("unexpected tool input: %+v", tool.Input)
	}
}

func TestAnthropicStreamError(t *testing.T) {
	client := NewAnthropicClient("k", "m", mockHTTPClient(http.StatusUnauthorized, "application/json", "unauthorized"))
	client.baseURL = "http://anthropic.mock/v1/messages"

	_, err := client.StreamWithTools(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected 401 in error, got %v", err)
	}
}

func collectLLMEvents(t *testing.T, ch <-chan LLMEvent) []LLMEvent {
	t.Helper()
	out := make([]LLMEvent, 0)
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
			t.Fatalf("timed out waiting for llm events")
		}
	}
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func mockHTTPClient(statusCode int, contentType, body string) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: statusCode,
			Header:     http.Header{"Content-Type": []string{contentType}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})}
}
