package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

const openAIURL = "https://api.openai.com/v1/chat/completions"

type OpenAIClient struct {
	apiKey     string
	model      string
	httpClient *http.Client
	baseURL    string
}

type openAIToolAccum struct {
	ID       string
	Name     string
	ArgsJSON strings.Builder
}

func NewOpenAIClient(apiKey, model string, httpClient *http.Client) *OpenAIClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if model == "" {
		model = "gpt-4o"
	}
	return &OpenAIClient{
		apiKey:     apiKey,
		model:      model,
		httpClient: httpClient,
		baseURL:    openAIURL,
	}
}

func (c *OpenAIClient) StreamWithTools(ctx context.Context, msgs []Message, tools []ToolDef) (<-chan LLMEvent, error) {
	reqBody := map[string]any{
		"model":    c.model,
		"stream":   true,
		"tools":    openAITools(tools),
		"messages": openAIMessages(msgs),
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal openai request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create openai request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send openai request: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai http %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	events := make(chan LLMEvent)
	go c.readStream(ctx, resp.Body, events)
	return events, nil
}

func (c *OpenAIClient) readStream(ctx context.Context, body io.ReadCloser, out chan<- LLMEvent) {
	defer close(out)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	var dataBuf strings.Builder

	toolCalls := map[int]*openAIToolAccum{}
	sentDone := false

	emit := func(e LLMEvent) error {
		select {
		case out <- e:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	emitErr := func(err error) {
		_ = emit(LLMEvent{Type: EventError, Error: err})
	}

	flushToolCalls := func() error {
		if len(toolCalls) == 0 {
			return nil
		}
		idxs := make([]int, 0, len(toolCalls))
		for idx := range toolCalls {
			idxs = append(idxs, idx)
		}
		sort.Ints(idxs)
		for _, idx := range idxs {
			tc := toolCalls[idx]
			input := map[string]any{}
			raw := strings.TrimSpace(tc.ArgsJSON.String())
			if raw != "" {
				if err := json.Unmarshal([]byte(raw), &input); err != nil {
					return fmt.Errorf("parse openai tool input: %w", err)
				}
			}
			if err := emit(LLMEvent{Type: EventToolCall, ToolCall: &ToolCall{ID: tc.ID, Name: tc.Name, Input: input}}); err != nil {
				return err
			}
		}
		return nil
	}

	for scanner.Scan() {
		if ctx.Err() != nil {
			emitErr(ctx.Err())
			return
		}
		line := scanner.Text()
		if line == "" {
			if dataBuf.Len() > 0 {
				data := strings.TrimSpace(dataBuf.String())
				dataBuf.Reset()
				if data == "[DONE]" {
					if !sentDone {
						if err := emit(LLMEvent{Type: EventDone}); err != nil {
							emitErr(err)
						}
					}
					return
				}
				if err := c.handleSSEData(ctx, out, data, toolCalls, &sentDone, flushToolCalls); err != nil {
					emitErr(err)
					return
				}
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "" {
				continue
			}
			if dataBuf.Len() > 0 {
				dataBuf.WriteByte('\n')
			}
			dataBuf.WriteString(payload)
		}
	}
	if err := scanner.Err(); err != nil {
		emitErr(fmt.Errorf("read openai stream: %w", err))
		return
	}
	if !sentDone {
		if err := emit(LLMEvent{Type: EventDone}); err != nil {
			emitErr(err)
		}
	}
}

func (c *OpenAIClient) handleSSEData(
	ctx context.Context,
	out chan<- LLMEvent,
	payload string,
	toolCalls map[int]*openAIToolAccum,
	sentDone *bool,
	flushToolCalls func() error,
) error {
	var chunk struct {
		Choices []struct {
			Delta struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		return fmt.Errorf("parse openai event: %w", err)
	}
	if chunk.Error != nil {
		return fmt.Errorf("openai stream error: %s", strings.TrimSpace(chunk.Error.Message))
	}
	if len(chunk.Choices) == 0 {
		return nil
	}

	emit := func(e LLMEvent) error {
		select {
		case out <- e:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	for _, choice := range chunk.Choices {
		if choice.Delta.Content != "" {
			if err := emit(LLMEvent{Type: EventText, TextChunk: choice.Delta.Content}); err != nil {
				return err
			}
		}
		for _, tc := range choice.Delta.ToolCalls {
			entry, ok := toolCalls[tc.Index]
			if !ok {
				entry = &openAIToolAccum{}
				toolCalls[tc.Index] = entry
			}
			if tc.ID != "" {
				entry.ID = tc.ID
			}
			if tc.Function.Name != "" {
				entry.Name = tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				entry.ArgsJSON.WriteString(tc.Function.Arguments)
			}
		}
		switch choice.FinishReason {
		case "tool_calls":
			if err := flushToolCalls(); err != nil {
				return err
			}
			*sentDone = true
			if err := emit(LLMEvent{Type: EventDone}); err != nil {
				return err
			}
		case "stop":
			*sentDone = true
			if err := emit(LLMEvent{Type: EventDone}); err != nil {
				return err
			}
		}
	}
	return nil
}

func openAITools(tools []ToolDef) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.InputSchema,
			},
		})
	}
	return out
}

func openAIMessages(msgs []Message) []map[string]any {
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		msg := map[string]any{"role": m.Role, "content": m.Content}
		switch m.Role {
		case "assistant":
			if len(m.ToolCalls) > 0 {
				calls := make([]map[string]any, 0, len(m.ToolCalls))
				for _, tc := range m.ToolCalls {
					args, _ := json.Marshal(tc.Input)
					calls = append(calls, map[string]any{
						"id":   tc.ID,
						"type": "function",
						"function": map[string]any{
							"name":      tc.Name,
							"arguments": string(args),
						},
					})
				}
				msg["tool_calls"] = calls
			}
		case "tool":
			msg["tool_call_id"] = m.ToolCallID
		}
		out = append(out, msg)
	}
	return out
}
