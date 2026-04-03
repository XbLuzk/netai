package llm

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestOpenAIStreamText(t *testing.T) {
	client := NewOpenAIClient("k", "gpt-4o", mockHTTPClient(200, "text/event-stream", strings.Join([]string{
		"data: {\"choices\":[{\"delta\":{\"content\":\"hello \"},\"finish_reason\":null}]}",
		"",
		"data: {\"choices\":[{\"delta\":{\"content\":\"world\"},\"finish_reason\":\"stop\"}]}",
		"",
		"data: [DONE]",
		"",
	}, "\n")))
	client.baseURL = "http://openai.mock/v1/chat/completions"

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
}

func TestOpenAIStreamToolCall(t *testing.T) {
	client := NewOpenAIClient("k", "gpt-4o", mockHTTPClient(200, "text/event-stream", strings.Join([]string{
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"get_function_source\",\"arguments\":\"{\\\"repo_id\\\":\\\"r1\\\",\"}}]},\"finish_reason\":null}]}",
		"",
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"function_name\\\":\\\"Foo\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}",
		"",
		"data: [DONE]",
		"",
	}, "\n")))
	client.baseURL = "http://openai.mock/v1/chat/completions"

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
	if tool.ID != "call_1" || tool.Name != "get_function_source" {
		t.Fatalf("unexpected tool metadata: %+v", tool)
	}
	if tool.Input["repo_id"] != "r1" || tool.Input["function_name"] != "Foo" {
		t.Fatalf("unexpected tool input: %+v", tool.Input)
	}
}

func TestOpenAIStreamError(t *testing.T) {
	client := NewOpenAIClient("k", "gpt-4o", mockHTTPClient(http.StatusInternalServerError, "application/json", "server error"))
	client.baseURL = "http://openai.mock/v1/chat/completions"

	_, err := client.StreamWithTools(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected 500 in error, got %v", err)
	}
}
