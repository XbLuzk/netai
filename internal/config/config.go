package config

import (
	"fmt"
	"runtime"
	"time"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	DatabaseURL               string        `env:"DATABASE_URL,required"`
	RedisURL                  string        `env:"REDIS_URL" envDefault:"redis://localhost:6379"`
	LLMBackend                string        `env:"LLM_BACKEND" envDefault:"anthropic"`
	LLMAPIKey                 string        `env:"LLM_API_KEY"`
	LLMModel                  string        `env:"LLM_MODEL" envDefault:"claude-opus-4-6"`
	EmbeddingBackend          string        `env:"EMBEDDING_BACKEND" envDefault:"openai"`
	EmbeddingAPIKey           string        `env:"EMBEDDING_API_KEY"`
	EmbeddingModel            string        `env:"EMBEDDING_MODEL" envDefault:"text-embedding-3-small"`
	QueryCacheTTL             time.Duration `env:"QUERY_CACHE_TTL" envDefault:"1h"`
	WorkerConcurrency         int           `env:"WORKER_CONCURRENCY" envDefault:"0"`
	EmbeddingConcurrency      int           `env:"EMBEDDING_CONCURRENCY" envDefault:"3"`
	StaleTaskThresholdMinutes int           `env:"STALE_TASK_THRESHOLD_MINUTES" envDefault:"10"`
	GitHubToken               string        `env:"GITHUB_TOKEN"`
	GitHubWebhookSecret       string        `env:"GITHUB_WEBHOOK_SECRET"`
	Port                      int           `env:"PORT" envDefault:"8080"`
	OllamaBaseURL             string        `env:"OLLAMA_BASE_URL" envDefault:"http://localhost:11434"`
}

func Load() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, err
	}
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required and must not be empty")
	}
	if cfg.WorkerConcurrency == 0 {
		cfg.WorkerConcurrency = max(1, runtime.GOMAXPROCS(0)-1)
	}
	return cfg, nil
}
