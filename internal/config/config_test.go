package config

import (
	"runtime"
	"testing"
	"time"
)

func TestLoadSuccessWithRequiredFields(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://logicmap:logicmap@localhost:5432/logicmap?sslmode=disable")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg.DatabaseURL == "" {
		t.Fatal("expected database url to be set")
	}
}

func TestLoadFailsWhenDatabaseURLMissing(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when DATABASE_URL is missing")
	}
}

func TestLoadWorkerConcurrencyDefaultsToGOMAXPROCSMinusOne(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://logicmap:logicmap@localhost:5432/logicmap?sslmode=disable")
	t.Setenv("WORKER_CONCURRENCY", "0")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expected := max(1, runtime.GOMAXPROCS(0)-1)
	if cfg.WorkerConcurrency != expected {
		t.Fatalf("expected worker concurrency %d, got %d", expected, cfg.WorkerConcurrency)
	}
}

func TestLoadParsesQueryCacheTTL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://logicmap:logicmap@localhost:5432/logicmap?sslmode=disable")
	t.Setenv("QUERY_CACHE_TTL", "1h")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg.QueryCacheTTL != time.Hour {
		t.Fatalf("expected query cache ttl %s, got %s", time.Hour, cfg.QueryCacheTTL)
	}
}
