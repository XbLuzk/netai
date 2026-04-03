package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

const openAIEmbeddingsURL = "https://api.openai.com/v1/embeddings"

type OpenAIClient struct {
	apiKey     string
	model      string
	httpClient *http.Client
	baseURL    string
}

func NewOpenAIClient(apiKey, model string, httpClient *http.Client) *OpenAIClient {
	if model == "" {
		model = "text-embedding-3-small"
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	return &OpenAIClient{
		apiKey:     apiKey,
		model:      model,
		httpClient: httpClient,
		baseURL:    openAIEmbeddingsURL,
	}
}

func (c *OpenAIClient) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	payload := struct {
		Model      string   `json:"model"`
		Input      []string `json:"input"`
		Dimensions int      `json:"dimensions"`
	}{
		Model:      c.model,
		Input:      texts,
		Dimensions: embeddingDimension,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal openai embedding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build openai embedding request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai embedding request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read openai embedding response: %w", err)
	}

	if err := mapHTTPError(resp.StatusCode, respBody); err != nil {
		return nil, err
	}

	var parsed struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("decode openai embedding response: %w", err)
	}

	if len(parsed.Data) != len(texts) {
		return nil, fmt.Errorf("openai embedding response count mismatch: got %d want %d", len(parsed.Data), len(texts))
	}

	sort.Slice(parsed.Data, func(i, j int) bool {
		return parsed.Data[i].Index < parsed.Data[j].Index
	})

	vectors := make([][]float32, len(texts))
	for i, item := range parsed.Data {
		if item.Index != i {
			return nil, fmt.Errorf("openai embedding response index mismatch at position %d: got %d", i, item.Index)
		}
		if len(item.Embedding) != embeddingDimension {
			return nil, fmt.Errorf("openai embedding dimension mismatch at index %d: got %d want %d", i, len(item.Embedding), embeddingDimension)
		}
		vectors[i] = item.Embedding
	}

	return vectors, nil
}

func mapHTTPError(statusCode int, body []byte) error {
	if statusCode >= 200 && statusCode < 300 {
		return nil
	}
	if statusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if statusCode == http.StatusTooManyRequests {
		return ErrRateLimit
	}
	return ErrAPIError{StatusCode: statusCode, Body: strings.TrimSpace(string(body))}
}
