package embedding

import (
	"context"
	"fmt"
	"sync"

	"golang.org/x/sync/semaphore"
)

const batchSize = 100

// BatchEmbed 将大批量 texts 切分成 batch_size=100 一组，
// 通过 EMBEDDING_CONCURRENCY semaphore 控制并发，
// 合并所有 batch 结果返回
func BatchEmbed(ctx context.Context, client EmbeddingClient, texts []string, concurrency int) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}
	if concurrency <= 0 {
		concurrency = 1
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sem := semaphore.NewWeighted(int64(concurrency))
	results := make([][]float32, len(texts))

	var wg sync.WaitGroup
	errCh := make(chan error, 1)

	for start := 0; start < len(texts); start += batchSize {
		end := min(start+batchSize, len(texts))
		batch := texts[start:end]
		batchStart := start

		if err := sem.Acquire(ctx, 1); err != nil {
			select {
			case firstErr := <-errCh:
				return nil, firstErr
			default:
				return nil, err
			}
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			defer sem.Release(1)

			embeddings, err := client.Embed(ctx, batch)
			if err != nil {
				select {
				case errCh <- err:
					cancel()
				default:
				}
				return
			}

			if len(embeddings) != len(batch) {
				err := fmt.Errorf("embedding batch result count mismatch: got %d want %d", len(embeddings), len(batch))
				select {
				case errCh <- err:
					cancel()
				default:
				}
				return
			}

			for i := range embeddings {
				results[batchStart+i] = embeddings[i]
			}
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case err := <-errCh:
		return nil, err
	case <-done:
		return results, nil
	case <-ctx.Done():
		select {
		case err := <-errCh:
			return nil, err
		default:
			return nil, ctx.Err()
		}
	}
}
