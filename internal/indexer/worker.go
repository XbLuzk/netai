package indexer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/XbLuzk/logicmap/internal/config"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	workerGroupName = "logicmap-workers"
	workerStream    = "index-jobs"
)

type indexRunner interface {
	RunFullIndex(ctx context.Context, taskID, repoID uuid.UUID) error
	RunIncrementalIndex(ctx context.Context, taskID, repoID uuid.UUID) error
}

type redisStreamClient interface {
	XGroupCreateMkStream(ctx context.Context, stream, group, start string) *redis.StatusCmd
	XAutoClaim(ctx context.Context, a *redis.XAutoClaimArgs) *redis.XAutoClaimCmd
	XReadGroup(ctx context.Context, a *redis.XReadGroupArgs) *redis.XStreamSliceCmd
	XAck(ctx context.Context, stream, group string, ids ...string) *redis.IntCmd
}

type Worker struct {
	redis        redisStreamClient
	indexer      indexRunner
	cfg          *config.Config
	consumerName string

	autoClaimInterval time.Duration
	autoClaimMinIdle  time.Duration
	autoClaimFn       func(ctx context.Context, start string) (string, error)
}

func NewWorker(redisClient *redis.Client, indexer *Indexer, cfg *config.Config) *Worker {
	consumerName := "worker-" + uuid.NewString()
	w := &Worker{
		redis:             redisClient,
		indexer:           indexer,
		cfg:               cfg,
		consumerName:      consumerName,
		autoClaimInterval: 10 * time.Second,
		autoClaimMinIdle:  30 * time.Second,
	}
	w.autoClaimFn = w.defaultAutoClaim
	return w
}

func (w *Worker) Start(ctx context.Context) error {
	if w.redis == nil {
		return fmt.Errorf("worker redis client is nil")
	}
	if w.indexer == nil {
		return fmt.Errorf("worker indexer is nil")
	}

	if err := w.redis.XGroupCreateMkStream(ctx, workerStream, workerGroupName, "$").Err(); err != nil && !isBusyGroupErr(err) {
		return fmt.Errorf("create worker group: %w", err)
	}

	go w.runAutoClaimLoop(ctx)

	for {
		if ctx.Err() != nil {
			return nil
		}

		streams, err := w.redis.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    workerGroupName,
			Consumer: w.consumerName,
			Streams:  []string{workerStream, ">"},
			Count:    10,
			Block:    5 * time.Second,
		}).Result()
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				if ctx.Err() != nil {
					return nil
				}
				continue
			}
			if errors.Is(err, redis.Nil) {
				continue
			}
			return fmt.Errorf("read worker stream: %w", err)
		}

		for _, stream := range streams {
			for _, msg := range stream.Messages {
				w.processMessage(ctx, msg)
				_ = w.redis.XAck(ctx, workerStream, workerGroupName, msg.ID).Err()
			}
		}
	}
}

func (w *Worker) processMessage(ctx context.Context, msg redis.XMessage) {
	taskIDStr := valueToString(msg.Values["task_id"])
	repoIDStr := valueToString(msg.Values["repo_id"])
	jobType := strings.ToLower(strings.TrimSpace(valueToString(msg.Values["type"])))

	taskID, err := uuid.Parse(taskIDStr)
	if err != nil {
		return
	}
	repoID, err := uuid.Parse(repoIDStr)
	if err != nil {
		return
	}

	switch jobType {
	case "full":
		_ = w.indexer.RunFullIndex(ctx, taskID, repoID)
	case "incremental":
		_ = w.indexer.RunIncrementalIndex(ctx, taskID, repoID)
	default:
		return
	}
}

func (w *Worker) runAutoClaimLoop(ctx context.Context) {
	ticker := time.NewTicker(w.autoClaimInterval)
	defer ticker.Stop()

	start := "0-0"
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			nextStart, err := w.autoClaimFn(ctx, start)
			if err != nil {
				continue
			}
			if nextStart == "" || nextStart == "0-0" {
				start = "0-0"
				continue
			}
			start = nextStart
		}
	}
}

func (w *Worker) defaultAutoClaim(ctx context.Context, start string) (string, error) {
	_, nextStart, err := w.redis.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   workerStream,
		Group:    workerGroupName,
		Consumer: w.consumerName,
		MinIdle:  w.autoClaimMinIdle,
		Start:    start,
		Count:    100,
	}).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "0-0", nil
		}
		return start, err
	}
	return nextStart, nil
}

func valueToString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	default:
		return fmt.Sprint(v)
	}
}

func isBusyGroupErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "BUSYGROUP")
}
