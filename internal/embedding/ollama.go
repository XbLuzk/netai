package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type OllamaClient struct {
	baseURL     string
	model       string
	httpClient  *http.Client
	concurrency int
}

func NewOllamaClient(baseURL, model string, httpClient *http.Client) *OllamaClient {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	if model == "" {
		model = "nomic-embed-text"
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	return &OllamaClient{
		baseURL:     strings.TrimRight(baseURL, "/"),
		model:       model,
		httpClient:  httpClient,
		concurrency: 1,
	}
}

func (c *OllamaClient) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	payload := struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}{
		Model: c.model,
		Input: texts,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal ollama embedding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build ollama embedding request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama embedding request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read ollama embedding response: %w", err)
	}

	if err := mapHTTPError(resp.StatusCode, respBody); err != nil {
		return nil, err
	}

	var parsed struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("decode ollama embedding response: %w", err)
	}

	if len(parsed.Embeddings) != len(texts) {
		return nil, fmt.Errorf("ollama embedding response count mismatch: got %d want %d", len(parsed.Embeddings), len(texts))
	}

	vectors := make([][]float32, len(parsed.Embeddings))
	for i, vec := range parsed.Embeddings {
		vectors[i] = normalizeVector(vec)
	}

	return vectors, nil
}

func normalizeVector(vec []float32) []float32 {
	if len(vec) == embeddingDimension {
		return vec
	}

	normalized := make([]float32, embeddingDimension)
	copy(normalized, vec)
	return normalized
}
