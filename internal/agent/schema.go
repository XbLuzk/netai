package agent

// CallChain 是 LLM 探索后提交的结构化调用链
type CallChain struct {
	Nodes       []FunctionNode `json:"nodes"`
	Edges       []ChainEdge    `json:"edges"`
	Description string         `json:"description"`
}

type FunctionNode struct {
	Name     string `json:"name"`
	FilePath string `json:"file_path"`
	Source   string `json:"source,omitempty"` // 关键函数附代码片段
}

type ChainEdge struct {
	From string `json:"from"` // function name
	To   string `json:"to"`
}

// AgentEvent 是 Agent 向外 emit 的事件
type AgentEvent struct {
	Type    AgentEventType `json:"type"`
	Content string         `json:"content,omitempty"` // EventText 时的文本块
	Chain   *CallChain     `json:"chain,omitempty"`   // EventChain 时的调用链
	Message string         `json:"message,omitempty"` // EventWarning/EventDone 时
	Err     error          `json:"-"`                 // EventError 时
}

type AgentEventType string

const (
	AgentEventText    AgentEventType = "text"
	AgentEventChain   AgentEventType = "chain"
	AgentEventWarning AgentEventType = "warning"
	AgentEventDone    AgentEventType = "done"
	AgentEventError   AgentEventType = "error"
)
