package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/XbLuzk/logicmap/internal/llm"
)

const systemPrompt = `You are a code analysis assistant. Your goal is to explore the call graph of a codebase and explain the logic chain of functions.

Use the provided tools to explore function definitions, their callees, callers, and similar code. When you have gathered enough information to answer the user's question, call submit_chain_result with the complete call chain and a natural language description.

Always call submit_chain_result to end the exploration. Do not stop without calling it.`

type Agent struct {
	llm   llm.LLMClient
	tools ToolExecutor
}

func NewAgent(llmClient llm.LLMClient, tools ToolExecutor, _ string) *Agent {
	return &Agent{llm: llmClient, tools: tools}
}

// Run 启动 agentic loop，返回 AgentEvent channel
// 终止条件：
//  1. LLM 调用 submit_chain_result → emit EventChain，break loop
//  2. 工具调用次数 >= 20 → emit EventWarning("truncated")，break
//  3. ctx deadline 超过（外部传入含 30s timeout 的 ctx）→ emit EventWarning("timeout")
//  4. LLM 不再发起工具调用（纯文本响应）→ break
//  5. LLM 返回 error → emit EventError，break
func (a *Agent) Run(ctx context.Context, question, repoID string) <-chan AgentEvent {
	out := make(chan AgentEvent, 32)
	go func() {
		defer close(out)

		emit := func(e AgentEvent) bool {
			select {
			case out <- e:
				return true
			default:
				return false
			}
		}

		if ctx.Err() != nil {
			a.emitContextTermination(ctx, emit)
			return
		}

		messages := []llm.Message{{
			Role:    "user",
			Content: systemPrompt + "\n\nQuestion: " + question,
		}}
		toolCallCount := 0

		for {
			if ctx.Err() != nil {
				a.emitContextTermination(ctx, emit)
				return
			}

			events, err := a.llm.StreamWithTools(ctx, messages, ToolDefs())
			if err != nil {
				emit(AgentEvent{Type: AgentEventError, Err: err})
				return
			}

			var textBuf strings.Builder
			var toolCalls []llm.ToolCall

			for event := range events {
				switch event.Type {
				case llm.EventText:
					if event.TextChunk != "" {
						textBuf.WriteString(event.TextChunk)
						emit(AgentEvent{Type: AgentEventText, Content: event.TextChunk})
					}
				case llm.EventToolCall:
					if event.ToolCall != nil {
						toolCalls = append(toolCalls, *event.ToolCall)
					}
				case llm.EventError:
					emit(AgentEvent{Type: AgentEventError, Err: event.Error})
					return
				case llm.EventDone:
					// turn complete
				}
			}

			if len(toolCalls) == 0 {
				emit(AgentEvent{Type: AgentEventDone, Message: "completed"})
				return
			}

			messages = append(messages, llm.Message{Role: "assistant", Content: textBuf.String(), ToolCalls: toolCalls})

			for _, tc := range toolCalls {
				toolCallCount++
				if toolCallCount >= 20 {
					emit(AgentEvent{Type: AgentEventWarning, Message: "response truncated: tool call limit reached"})
					emit(AgentEvent{Type: AgentEventDone, Message: "truncated"})
					return
				}

				if tc.Name == "submit_chain_result" {
					chain, err := parseChainFromInput(tc.Input)
					if err != nil {
						emit(AgentEvent{Type: AgentEventError, Err: err})
						return
					}
					emit(AgentEvent{Type: AgentEventChain, Chain: &chain})
					emit(AgentEvent{Type: AgentEventDone, Message: "submitted"})
					return
				}

				result, err := a.executeToolCall(ctx, tc, repoID)
				if err != nil {
					emit(AgentEvent{Type: AgentEventError, Err: err})
					return
				}
				messages = append(messages, llm.Message{Role: "tool", ToolCallID: tc.ID, Content: result})
			}
		}
	}()
	return out
}

func (a *Agent) emitContextTermination(ctx context.Context, emit func(AgentEvent) bool) {
	err := ctx.Err()
	if errors.Is(err, context.DeadlineExceeded) {
		emit(AgentEvent{Type: AgentEventWarning, Message: "timeout"})
		emit(AgentEvent{Type: AgentEventDone, Message: "timeout"})
		return
	}
	emit(AgentEvent{Type: AgentEventError, Err: err})
}

func parseChainFromInput(input map[string]any) (CallChain, error) {
	chainRaw, ok := input["chain"]
	if !ok {
		return CallChain{}, fmt.Errorf("submit_chain_result missing chain")
	}
	buf, err := json.Marshal(chainRaw)
	if err != nil {
		return CallChain{}, fmt.Errorf("marshal chain input: %w", err)
	}
	var chain CallChain
	if err := json.Unmarshal(buf, &chain); err != nil {
		return CallChain{}, fmt.Errorf("unmarshal chain input: %w", err)
	}
	return chain, nil
}

func (a *Agent) executeToolCall(ctx context.Context, tc llm.ToolCall, repoID string) (string, error) {
	toolRepoID := stringArg(tc.Input, "repo_id")
	if toolRepoID != "" {
		repoID = toolRepoID
	}

	switch tc.Name {
	case "get_function_source":
		fn := stringArg(tc.Input, "function_name")
		if fn == "" {
			return "", fmt.Errorf("get_function_source requires function_name")
		}
		source, err := a.tools.GetFunctionSource(ctx, repoID, fn)
		if err != nil {
			return "", err
		}
		resp := map[string]any{"source": source}
		return toJSON(resp)
	case "get_callees":
		fn := stringArg(tc.Input, "function_name")
		if fn == "" {
			return "", fmt.Errorf("get_callees requires function_name")
		}
		callees, err := a.tools.GetCallees(ctx, repoID, fn)
		if err != nil {
			return "", err
		}
		return toJSON(callees)
	case "get_callers":
		fn := stringArg(tc.Input, "function_name")
		if fn == "" {
			return "", fmt.Errorf("get_callers requires function_name")
		}
		callers, err := a.tools.GetCallers(ctx, repoID, fn)
		if err != nil {
			return "", err
		}
		return toJSON(callers)
	case "search_similar_code":
		query := stringArg(tc.Input, "query")
		if query == "" {
			return "", fmt.Errorf("search_similar_code requires query")
		}
		results, err := a.tools.SearchSimilarCode(ctx, repoID, query)
		if err != nil {
			return "", err
		}
		return toJSON(results)
	default:
		return "", fmt.Errorf("unknown tool: %s", tc.Name)
	}
}

func toJSON(v any) (string, error) {
	buf, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

func stringArg(input map[string]any, key string) string {
	v, ok := input[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}
