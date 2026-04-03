package embedding

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOllamaEmbedHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"embeddings":[[` + onesJSON(768) + `]]}`))
	}))
	defer server.Close()

	client := NewOllamaClient(server.URL, "nomic-embed-text", server.Client())
	got, err := client.Embed(context.Background(), []string{"a"})
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 embedding, got %d", len(got))
	}
	if len(got[0]) != embeddingDimension {
		t.Fatalf("expected %d dims, got %d", embeddingDimension, len(got[0]))
	}
	if got[0][0] != 1 {
		t.Fatalf("expected first value 1, got %v", got[0][0])
	}
	if got[0][1000] != 0 {
		t.Fatalf("expected padded area to be zero, got %v", got[0][1000])
	}
}

func TestOllamaEmbedPadding(t *testing.T) {
	vec := make([]float32, 768)
	for i := range vec {
		vec[i] = 2
	}

	normalized := normalizeVector(vec)
	if len(normalized) != embeddingDimension {
		t.Fatalf("expected %d dims, got %d", embeddingDimension, len(normalized))
	}
	for i := 0; i < 768; i++ {
		if normalized[i] != 2 {
			t.Fatalf("expected preserved value at %d", i)
		}
	}
	for i := 768; i < embeddingDimension; i++ {
		if normalized[i] != 0 {
			t.Fatalf("expected zero padding at %d, got %v", i, normalized[i])
		}
	}
}

func TestOllamaEmbedTruncation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"embeddings":[[` + onesJSON(2000) + `]]}`))
	}))
	defer server.Close()

	client := NewOllamaClient(server.URL, "nomic-embed-text", server.Client())
	got, err := client.Embed(context.Background(), []string{"a"})
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 embedding, got %d", len(got))
	}
	if len(got[0]) != embeddingDimension {
		t.Fatalf("expected %d dims, got %d", embeddingDimension, len(got[0]))
	}
}

func TestOllamaEmbedHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer server.Close()

	client := NewOllamaClient(server.URL, "nomic-embed-text", server.Client())
	_, err := client.Embed(context.Background(), []string{"a"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "embedding API error 500") {
		t.Fatalf("expected API error, got %v", err)
	}
}
