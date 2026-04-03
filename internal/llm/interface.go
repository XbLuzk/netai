package llm

import "context"

// Message 表示对话消息
type Message struct {
	Role    string // "user" | "assistant" | "tool"
	Content string
	// ToolCallID 仅在 Role="tool" 时使用（tool result 消息）
	ToolCallID string
	// ToolCalls 仅在 Role="assistant" 时使用（LLM 发起的工具调用）
	ToolCalls []ToolCall
}

// ToolDef 描述一个工具的 schema
type ToolDef struct {
	Name        string
	Description string
	// InputSchema 是 JSON Schema 对象（map[string]any）
	InputSchema map[string]any
}

// ToolCall 表示 LLM 发起的工具调用
type ToolCall struct {
	ID    string // LLM 生成的调用 ID
	Name  string
	Input map[string]any
}

// LLMEvent 是 LLM streaming 的事件类型
type LLMEvent struct {
	Type      EventType
	TextChunk string    // 当 Type == EventText
	ToolCall  *ToolCall // 当 Type == EventToolCall（完整的 tool call，非流式 chunk）
	Error     error     // 当 Type == EventError
}

type EventType string

const (
	EventText     EventType = "text"
	EventToolCall EventType = "tool_call"
	EventDone     EventType = "done"
	EventError    EventType = "error"
)

// LLMClient 支持 streaming tool use 的 LLM 客户端接口
type LLMClient interface {
	// StreamWithTools 发起 streaming 请求，返回事件 channel
	// channel 关闭表示流结束（Done 或 Error 后关闭）
	StreamWithTools(ctx context.Context, msgs []Message, tools []ToolDef) (<-chan LLMEvent, error)
}
