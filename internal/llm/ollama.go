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

type OllamaClient struct {
	baseURL    string
	model      string
	httpClient *http.Client
}

func NewOllamaClient(baseURL, model string, httpClient *http.Client) *OllamaClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	if model == "" {
		model = "llama3.1"
	}
	return &OllamaClient{baseURL: baseURL, model: model, httpClient: httpClient}
}

func (c *OllamaClient) StreamWithTools(ctx context.Context, msgs []Message, tools []ToolDef) (<-chan LLMEvent, error) {
	url := c.baseURL + "/api/chat"
	reqBody := map[string]any{
		"model":    c.model,
		"stream":   true,
		"tools":    openAITools(tools),
		"messages": openAIMessages(msgs),
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal ollama request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create ollama request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send ollama request: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama http %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	events := make(chan LLMEvent)
	go c.readStream(ctx, resp.Body, events)
	return events, nil
}

func (c *OllamaClient) readStream(ctx context.Context, body io.ReadCloser, out chan<- LLMEvent) {
	defer close(out)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	type toolAccum struct {
		ID       string
		Name     string
		ArgsJSON strings.Builder
	}
	toolCalls := map[int]*toolAccum{}
	emittedDone := false

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
					return fmt.Errorf("parse ollama tool input: %w", err)
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
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
		if line == "[DONE]" {
			if !emittedDone {
				if err := flushToolCalls(); err != nil {
					emitErr(err)
					return
				}
				if err := emit(LLMEvent{Type: EventDone}); err != nil {
					emitErr(err)
				}
			}
			return
		}

		var chunk struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments any    `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			Done  bool `json:"done"`
			Error any  `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			emitErr(fmt.Errorf("parse ollama event: %w", err))
			return
		}
		if chunk.Error != nil {
			emitErr(fmt.Errorf("ollama stream error: %v", chunk.Error))
			return
		}

		if chunk.Message.Content != "" {
			if err := emit(LLMEvent{Type: EventText, TextChunk: chunk.Message.Content}); err != nil {
				emitErr(err)
				return
			}
		}
		for idx, tc := range chunk.Message.ToolCalls {
			entry, ok := toolCalls[idx]
			if !ok {
				entry = &toolAccum{}
				toolCalls[idx] = entry
			}
			if tc.ID != "" {
				entry.ID = tc.ID
			}
			if tc.Function.Name != "" {
				entry.Name = tc.Function.Name
			}
			switch args := tc.Function.Arguments.(type) {
			case string:
				entry.ArgsJSON.WriteString(args)
			case map[string]any:
				raw, _ := json.Marshal(args)
				entry.ArgsJSON.Write(raw)
			}
		}

		if chunk.Done {
			if err := flushToolCalls(); err != nil {
				emitErr(err)
				return
			}
			emittedDone = true
			if err := emit(LLMEvent{Type: EventDone}); err != nil {
				emitErr(err)
			}
			return
		}
	}
	if err := scanner.Err(); err != nil {
		emitErr(fmt.Errorf("read ollama stream: %w", err))
		return
	}
	if !emittedDone {
		if err := flushToolCalls(); err != nil {
			emitErr(err)
			return
		}
		if err := emit(LLMEvent{Type: EventDone}); err != nil {
			emitErr(err)
		}
	}
}
