package embedding

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestOpenAIEmbedHappyPath(t *testing.T) {
	t.Helper()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("expected Authorization header, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"index":1,"embedding":[` + zerosJSON(embeddingDimension) + `]},{"index":0,"embedding":[` + onesJSON(embeddingDimension) + `]}]}`))
	}))
	defer server.Close()

	client := NewOpenAIClient("test-key", "text-embedding-3-small", server.Client())
	client.baseURL = server.URL

	got, err := client.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected one API call, got %d", calls.Load())
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 embeddings, got %d", len(got))
	}
	if len(got[0]) != embeddingDimension || len(got[1]) != embeddingDimension {
		t.Fatalf("expected embedding dimension %d, got %d and %d", embeddingDimension, len(got[0]), len(got[1]))
	}
	if got[0][0] != 1 || got[1][0] != 0 {
		t.Fatalf("expected ordered embeddings by index, got first values %v and %v", got[0][0], got[1][0])
	}
}

func TestOpenAIEmbedEmptyInput(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewOpenAIClient("test-key", "text-embedding-3-small", server.Client())
	client.baseURL = server.URL

	got, err := client.Embed(context.Background(), nil)
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty embeddings, got %d", len(got))
	}
	if calls.Load() != 0 {
		t.Fatalf("expected no API call, got %d", calls.Load())
	}
}

func TestOpenAIEmbedUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("unauthorized"))
	}))
	defer server.Close()

	client := NewOpenAIClient("bad-key", "text-embedding-3-small", server.Client())
	client.baseURL = server.URL

	_, err := client.Embed(context.Background(), []string{"a"})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}

func TestOpenAIEmbedRateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("rate limit"))
	}))
	defer server.Close()

	client := NewOpenAIClient("test-key", "text-embedding-3-small", server.Client())
	client.baseURL = server.URL

	_, err := client.Embed(context.Background(), []string{"a"})
	if !errors.Is(err, ErrRateLimit) {
		t.Fatalf("expected ErrRateLimit, got %v", err)
	}
}

func zerosJSON(n int) string {
	if n <= 0 {
		return ""
	}
	buf := make([]byte, 0, n*2)
	for i := 0; i < n; i++ {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = append(buf, '0')
	}
	return string(buf)
}

func onesJSON(n int) string {
	if n <= 0 {
		return ""
	}
	buf := make([]byte, 0, n*2)
	for i := 0; i < n; i++ {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = append(buf, '1')
	}
	return string(buf)
}
