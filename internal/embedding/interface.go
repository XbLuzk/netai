package embedding

import (
	"context"
	"errors"
	"fmt"
)

const embeddingDimension = 1536

var (
	ErrUnauthorized = errors.New("embedding: unauthorized (check API key)")
	ErrRateLimit    = errors.New("embedding: rate limit exceeded")
)

type ErrAPIError struct {
	StatusCode int
	Body       string
}

func (e ErrAPIError) Error() string {
	return fmt.Sprintf("embedding API error %d: %s", e.StatusCode, e.Body)
}

// EmbeddingClient 生成文本的向量表示
// 实现必须保证输出向量维度 = 1536（pgvector schema 固定约束）
// Ollama 实现：使用 nomic-embed-text 并通过 truncation 或 padding 保证 1536 维
type EmbeddingClient interface {
	// Embed 批量生成 embedding
	// 空输入返回空切片，不调用 API
	// 返回的切片长度与输入相同，顺序一致
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}
