package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	agentpkg "github.com/XbLuzk/logicmap/internal/agent"
	"github.com/XbLuzk/logicmap/internal/config"
	"github.com/XbLuzk/logicmap/internal/db"
	"github.com/XbLuzk/logicmap/internal/embedding"
	"github.com/XbLuzk/logicmap/internal/impact"
	"github.com/XbLuzk/logicmap/internal/indexer"
	"github.com/XbLuzk/logicmap/internal/llm"
	"github.com/XbLuzk/logicmap/internal/query"
	"github.com/XbLuzk/logicmap/internal/repo"
	"github.com/XbLuzk/logicmap/internal/server"
	"github.com/XbLuzk/logicmap/internal/webhook"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	defer pool.Close()

	if err := db.RunMigrations(ctx, pool); err != nil {
		log.Fatalf("migrations: %v", err)
	}

	redisClient, err := db.NewRedisClient(cfg.RedisURL)
	if err != nil {
		log.Fatalf("redis: %v", err)
	}
	defer redisClient.Close()

	var embedder embedding.EmbeddingClient
	switch cfg.EmbeddingBackend {
	case "ollama":
		embedder = embedding.NewOllamaClient(cfg.OllamaBaseURL, cfg.EmbeddingModel, nil)
	default:
		embedder = embedding.NewOpenAIClient(cfg.EmbeddingAPIKey, cfg.EmbeddingModel, nil)
	}

	var llmClient llm.LLMClient
	switch cfg.LLMBackend {
	case "openai":
		llmClient = llm.NewOpenAIClient(cfg.LLMAPIKey, cfg.LLMModel, nil)
	case "ollama":
		llmClient = llm.NewOllamaClient(cfg.OllamaBaseURL, cfg.LLMModel, nil)
	default:
		llmClient = llm.NewAnthropicClient(cfg.LLMAPIKey, cfg.LLMModel, nil)
	}

	repoStore := repo.NewRepoStore(pool, redisClient)
	repoSvc := repo.NewRepoService(repoStore, cfg)
	indexStore := indexer.NewIndexStore(pool)
	impactStore := impact.NewImpactStore(pool)

	tools := agentpkg.NewToolsImpl(pool, embedder)
	ag := agentpkg.NewAgent(llmClient, tools, "")

	cache := query.NewQueryCache(redisClient, cfg.QueryCacheTTL)
	querySvc := query.NewQueryService(repoStore, ag, cache, cfg)
	impactSvc := impact.NewImpactService(impactStore, repoStore, llmClient, cfg)
	ghWebhook := webhook.NewGitHubWebhookHandler(impactSvc, cfg)

	idx := indexer.NewIndexer(indexStore, repoStore, indexer.ParseFile, embedder, cfg)
	worker := indexer.NewWorker(redisClient, idx, cfg)

	go func() {
		if err := worker.Start(ctx); err != nil && ctx.Err() == nil {
			log.Printf("worker error: %v", err)
		}
	}()

	r := server.NewRouter(repoSvc, querySvc, impactSvc, ghWebhook)

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: r,
	}

	go func() {
		log.Printf("LogicMap listening on :%d", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down...")

	shutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutCtx); err != nil {
		log.Printf("shutdown error: %v", err)
	}

	log.Println("shutdown complete")
}
