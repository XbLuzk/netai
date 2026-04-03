package agent

import (
	"context"

	"github.com/XbLuzk/logicmap/internal/llm"
)

// ToolExecutor 是工具执行接口，由 Unit 7 注入真实实现
type ToolExecutor interface {
	GetFunctionSource(ctx context.Context, repoID, functionName string) (source string, err error)
	GetCallees(ctx context.Context, repoID, functionName string) (callees []CalleeInfo, err error)
	GetCallers(ctx context.Context, repoID, functionName string) (callers []CallerInfo, err error)
	SearchSimilarCode(ctx context.Context, repoID, query string) (results []SimilarCodeResult, err error)
}

type CalleeInfo struct {
	Name     string `json:"name"`
	FilePath string `json:"file_path"`
	External bool   `json:"external"` // true = unresolved/外部库
}

type CallerInfo struct {
	Name     string `json:"name"`
	FilePath string `json:"file_path"`
}

type SimilarCodeResult struct {
	Name     string  `json:"name"`
	FilePath string  `json:"file_path"`
	Source   string  `json:"source"`
	Score    float32 `json:"score"`
}

// ToolDefs 返回给 LLM 的工具列表（5 个：4 探索 + 1 终止）
func ToolDefs() []llm.ToolDef {
	functionQuerySchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"repo_id":       map[string]any{"type": "string"},
			"function_name": map[string]any{"type": "string"},
		},
		"required": []string{"repo_id", "function_name"},
	}

	searchSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"repo_id": map[string]any{"type": "string"},
			"query":   map[string]any{"type": "string"},
		},
		"required": []string{"repo_id", "query"},
	}

	submitSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"chain": map[string]any{
				"type":        "object",
				"description": "CallChain JSON",
			},
		},
		"required": []string{"chain"},
	}

	return []llm.ToolDef{
		{Name: "get_function_source", Description: "Get function source code by function name.", InputSchema: functionQuerySchema},
		{Name: "get_callees", Description: "Get functions called by the target function.", InputSchema: functionQuerySchema},
		{Name: "get_callers", Description: "Get functions that call the target function.", InputSchema: functionQuerySchema},
		{Name: "search_similar_code", Description: "Search semantically similar code snippets by query.", InputSchema: searchSchema},
		{Name: "submit_chain_result", Description: "Submit final structured call chain result when exploration is complete.", InputSchema: submitSchema},
	}
}
