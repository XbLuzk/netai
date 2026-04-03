package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const anthropicURL = "https://api.anthropic.com/v1/messages"

type AnthropicClient struct {
	apiKey     string
	model      string
	httpClient *http.Client
	baseURL    string
}

type anthropicToolAccum struct {
	ID         string
	Name       string
	InputBuf   strings.Builder
	IsToolCall bool
}

func NewAnthropicClient(apiKey, model string, httpClient *http.Client) *AnthropicClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if model == "" {
		model = "claude-opus-4-6"
	}
	return &AnthropicClient{
		apiKey:     apiKey,
		model:      model,
		httpClient: httpClient,
		baseURL:    anthropicURL,
	}
}

func (c *AnthropicClient) StreamWithTools(ctx context.Context, msgs []Message, tools []ToolDef) (<-chan LLMEvent, error) {
	reqBody := map[string]any{
		"model":      c.model,
		"max_tokens": 8096,
		"stream":     true,
		"tools":      anthropicTools(tools),
		"messages":   anthropicMessages(msgs),
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal anthropic request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create anthropic request: %w", err)
	}
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send anthropic request: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("anthropic http %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	events := make(chan LLMEvent)
	go c.readStream(ctx, resp.Body, events)
	return events, nil
}

func (c *AnthropicClient) readStream(ctx context.Context, body io.ReadCloser, out chan<- LLMEvent) {
	defer close(out)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	var dataBuf strings.Builder
	toolBlocks := map[int]*anthropicToolAccum{}

	emitErr := func(err error) {
		select {
		case out <- LLMEvent{Type: EventError, Error: err}:
		case <-ctx.Done():
		}
	}

	for scanner.Scan() {
		if ctx.Err() != nil {
			emitErr(ctx.Err())
			return
		}
		line := scanner.Text()
		if line == "" {
			if dataBuf.Len() > 0 {
				if err := c.handleSSEEvent(ctx, out, toolBlocks, dataBuf.String()); err != nil {
					emitErr(err)
					return
				}
				dataBuf.Reset()
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
		emitErr(fmt.Errorf("read anthropic stream: %w", err))
	}
}

func (c *AnthropicClient) handleSSEEvent(ctx context.Context, out chan<- LLMEvent, toolBlocks map[int]*anthropicToolAccum, payload string) error {
	var event struct {
		Type         string `json:"type"`
		Index        int    `json:"index"`
		ContentBlock struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"content_block"`
		Delta struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			PartialJSON string `json:"partial_json"`
		} `json:"delta"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return fmt.Errorf("parse anthropic event: %w", err)
	}

	send := func(e LLMEvent) error {
		select {
		case out <- e:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	switch event.Type {
	case "content_block_start":
		if event.ContentBlock.Type == "tool_use" {
			toolBlocks[event.Index] = &anthropicToolAccum{
				ID:         event.ContentBlock.ID,
				Name:       event.ContentBlock.Name,
				IsToolCall: true,
			}
		}
	case "content_block_delta":
		if event.Delta.Type == "text_delta" {
			if event.Delta.Text != "" {
				if err := send(LLMEvent{Type: EventText, TextChunk: event.Delta.Text}); err != nil {
					return err
				}
			}
			return nil
		}
		if event.Delta.Type == "input_json_delta" {
			if block, ok := toolBlocks[event.Index]; ok && block.IsToolCall {
				block.InputBuf.WriteString(event.Delta.PartialJSON)
			}
		}
	case "content_block_stop":
		if block, ok := toolBlocks[event.Index]; ok && block.IsToolCall {
			input := map[string]any{}
			raw := strings.TrimSpace(block.InputBuf.String())
			if raw != "" {
				if err := json.Unmarshal([]byte(raw), &input); err != nil {
					return fmt.Errorf("parse anthropic tool input: %w", err)
				}
			}
			if err := send(LLMEvent{Type: EventToolCall, ToolCall: &ToolCall{ID: block.ID, Name: block.Name, Input: input}}); err != nil {
				return err
			}
			delete(toolBlocks, event.Index)
		}
	case "message_stop":
		if err := send(LLMEvent{Type: EventDone}); err != nil {
			return err
		}
	case "error":
		msg := strings.TrimSpace(event.Error.Message)
		if msg == "" {
			msg = "anthropic stream error"
		}
		return errors.New(msg)
	}
	return nil
}

func anthropicTools(tools []ToolDef) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": t.InputSchema,
		})
	}
	return out
}

func anthropicMessages(msgs []Message) []map[string]any {
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case "tool":
			out = append(out, map[string]any{
				"role": "user",
				"content": []map[string]any{{
					"type":        "tool_result",
					"tool_use_id": m.ToolCallID,
					"content":     m.Content,
				}},
			})
		case "assistant":
			content := make([]map[string]any, 0, 1+len(m.ToolCalls))
			if strings.TrimSpace(m.Content) != "" {
				content = append(content, map[string]any{"type": "text", "text": m.Content})
			}
			for _, tc := range m.ToolCalls {
				content = append(content, map[string]any{
					"type":  "tool_use",
					"id":    tc.ID,
					"name":  tc.Name,
					"input": tc.Input,
				})
			}
			if len(content) == 0 {
				content = append(content, map[string]any{"type": "text", "text": ""})
			}
			out = append(out, map[string]any{"role": "assistant", "content": content})
		default:
			out = append(out, map[string]any{
				"role": m.Role,
				"content": []map[string]any{{
					"type": "text",
					"text": m.Content,
				}},
			})
		}
	}
	return out
}
