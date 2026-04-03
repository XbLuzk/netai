package query

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/XbLuzk/logicmap/internal/agent"
	"github.com/redis/go-redis/v9"
)

type QueryCache struct {
	client *redis.Client
	ttl    time.Duration
}

type CachedEvent struct {
	Type    string           `json:"type"`
	Content string           `json:"content,omitempty"`
	Chain   *agent.CallChain `json:"chain,omitempty"`
	Message string           `json:"message,omitempty"`
}

func NewQueryCache(client *redis.Client, ttl time.Duration) *QueryCache {
	return &QueryCache{client: client, ttl: ttl}
}

// CacheKey 计算缓存键：SHA256(repoID + ":" + question) hex string
func (c *QueryCache) CacheKey(repoID, question string) string {
	sum := sha256.Sum256([]byte(repoID + ":" + question))
	return hex.EncodeToString(sum[:])
}

// Get 返回缓存的结构化 JSON，cache miss 返回 nil, nil
func (c *QueryCache) Get(ctx context.Context, key string) ([]CachedEvent, error) {
	if c == nil || c.client == nil {
		return nil, nil
	}

	raw, err := c.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var events []CachedEvent
	if err := json.Unmarshal([]byte(raw), &events); err != nil {
		return nil, err
	}

	return events, nil
}

// Set 异步写入缓存（写失败静默忽略）
func (c *QueryCache) Set(ctx context.Context, key string, events []CachedEvent) {
	if c == nil || c.client == nil || c.ttl <= 0 || len(events) == 0 {
		return
	}

	go func() {
		buf, err := json.Marshal(events)
		if err != nil {
			return
		}
		_ = c.client.Set(ctx, key, buf, c.ttl).Err()
	}()
}
