package indexer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/XbLuzk/logicmap/internal/config"
	"github.com/XbLuzk/logicmap/internal/repo"
	"github.com/google/uuid"
)

type mockIndexStore struct {
	mu sync.Mutex

	functionsByRepo map[uuid.UUID][]FunctionRecord
	callEdges       []CallEdgeRecord
	unresolvedEdges []UnresolvedEdgeRecord
	deletedByFiles  []string

	startedTasks []uuid.UUID
	taskUpdates  []taskUpdate

	dropCalled     bool
	recreateCalled bool
	snapshots      map[uuid.UUID][]string
}

type taskUpdate struct {
	taskID   uuid.UUID
	status   string
	errorMsg string
	stats    map[string]any
}

func newMockIndexStore() *mockIndexStore {
	return &mockIndexStore{
		functionsByRepo: make(map[uuid.UUID][]FunctionRecord),
		snapshots:       make(map[uuid.UUID][]string),
	}
}

func (m *mockIndexStore) BulkInsertFunctions(_ context.Context, repoID uuid.UUID, funcs []FunctionRecord) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.functionsByRepo[repoID] = append(m.functionsByRepo[repoID], funcs...)
	return int64(len(funcs)), nil
}

func (m *mockIndexStore) BulkInsertCallEdges(_ context.Context, edges []CallEdgeRecord) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callEdges = append(m.callEdges, edges...)
	return int64(len(edges)), nil
}

func (m *mockIndexStore) BulkInsertUnresolvedEdges(_ context.Context, edges []UnresolvedEdgeRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.unresolvedEdges = append(m.unresolvedEdges, edges...)
	return nil
}

func (m *mockIndexStore) GetFunctionMap(_ context.Context, repoID uuid.UUID) (map[string][]uuid.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make(map[string][]uuid.UUID)
	for _, fn := range m.functionsByRepo[repoID] {
		result[fn.Name] = append(result[fn.Name], fn.ID)
	}
	return result, nil
}

func (m *mockIndexStore) DeleteFunctionsByRepo(_ context.Context, repoID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.functionsByRepo, repoID)
	return nil
}

func (m *mockIndexStore) DeleteFunctionsByFiles(_ context.Context, repoID uuid.UUID, filePaths []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	deleteSet := make(map[string]struct{}, len(filePaths))
	for _, p := range filePaths {
		deleteSet[p] = struct{}{}
		m.deletedByFiles = append(m.deletedByFiles, p)
	}

	remaining := make([]FunctionRecord, 0, len(m.functionsByRepo[repoID]))
	for _, fn := range m.functionsByRepo[repoID] {
		if _, ok := deleteSet[fn.FilePath]; ok {
			continue
		}
		remaining = append(remaining, fn)
	}
	m.functionsByRepo[repoID] = remaining
	return nil
}

func (m *mockIndexStore) UpdateTaskStatus(_ context.Context, taskID uuid.UUID, status string, errorMsg string, stats map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.taskUpdates = append(m.taskUpdates, taskUpdate{taskID: taskID, status: status, errorMsg: errorMsg, stats: cloneMap(stats)})
	return nil
}

func (m *mockIndexStore) UpdateTaskStarted(_ context.Context, taskID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startedTasks = append(m.startedTasks, taskID)
	return nil
}

func (m *mockIndexStore) DropHNSWIndex(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dropCalled = true
	return nil
}

func (m *mockIndexStore) RecreateHNSWIndex(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recreateCalled = true
	return nil
}

func (m *mockIndexStore) UpdateRepoIndexedFiles(_ context.Context, repoID uuid.UUID, files []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snapshots[repoID] = append([]string(nil), files...)
	return nil
}

func (m *mockIndexStore) lastTaskUpdate(t *testing.T) taskUpdate {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.taskUpdates) == 0 {
		t.Fatalf("expected task status updates")
	}
	return m.taskUpdates[len(m.taskUpdates)-1]
}

type mockRepoReader struct {
	repo *repo.Repo
}

func (m *mockRepoReader) GetRepo(_ context.Context, _ uuid.UUID, _ time.Duration) (*repo.Repo, error) {
	if m.repo == nil {
		return nil, errors.New("repo not found")
	}
	cpy := *m.repo
	cpy.IndexedFiles = append([]string(nil), m.repo.IndexedFiles...)
	return &cpy, nil
}

type mockEmbedder struct{}

func (m *mockEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{1, 2, 3}
	}
	return out, nil
}

func TestFullIndexHappyPath(t *testing.T) {
	repoDir, files := createGoFiles(t, 10)

	store := newMockIndexStore()
	repoID := uuid.New()
	taskID := uuid.New()
	idx := &Indexer{
		store:     store,
		repoStore: &mockRepoReader{repo: &repo.Repo{ID: repoID, Path: repoDir}},
		parser: func(path string) ([]Function, []RawCallEdge, error) {
			base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
			if base == "f0" {
				return []Function{{Name: "root", FilePath: path, StartLine: 1, EndLine: 2, Source: "func root() {}"}}, nil, nil
			}
			caller := "fn_" + base
			return []Function{{Name: caller, FilePath: path, StartLine: 1, EndLine: 2, Source: "func " + caller + "() { root() }"}}, []RawCallEdge{{CallerName: caller, CalleeName: "root"}}, nil
		},
		embedder: &mockEmbedder{},
		cfg:      &config.Config{WorkerConcurrency: 4, EmbeddingConcurrency: 2},
	}

	if err := idx.RunFullIndex(context.Background(), taskID, repoID); err != nil {
		t.Fatalf("RunFullIndex error: %v", err)
	}

	update := store.lastTaskUpdate(t)
	if update.status != "completed" {
		t.Fatalf("expected completed, got %s", update.status)
	}
	if !store.dropCalled || !store.recreateCalled {
		t.Fatalf("expected hnsw drop/recreate to be called")
	}
	if got := intFromAny(t, update.stats["files_total"]); got != len(files) {
		t.Fatalf("files_total=%d want=%d", got, len(files))
	}
	if got := intFromAny(t, update.stats["files_processed"]); got != len(files) {
		t.Fatalf("files_processed=%d want=%d", got, len(files))
	}
	if got := intFromAny(t, update.stats["functions_indexed"]); got != len(files) {
		t.Fatalf("functions_indexed=%d want=%d", got, len(files))
	}
	if len(store.callEdges) == 0 {
		t.Fatalf("expected call edges inserted")
	}
}

func TestFullIndexPartialFailure(t *testing.T) {
	repoDir, files := createGoFiles(t, 10)
	failSet := map[string]struct{}{}
	for i := 0; i < 3; i++ {
		failSet[files[i]] = struct{}{}
	}

	store := newMockIndexStore()
	repoID := uuid.New()
	taskID := uuid.New()
	idx := &Indexer{
		store:     store,
		repoStore: &mockRepoReader{repo: &repo.Repo{ID: repoID, Path: repoDir}},
		parser: func(path string) ([]Function, []RawCallEdge, error) {
			if _, ok := failSet[path]; ok {
				return nil, nil, fmt.Errorf("boom")
			}
			name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
			return []Function{{Name: name, FilePath: path, StartLine: 1, EndLine: 2, Source: "func"}}, nil, nil
		},
		embedder: &mockEmbedder{},
		cfg:      &config.Config{WorkerConcurrency: 3, EmbeddingConcurrency: 2},
	}

	if err := idx.RunFullIndex(context.Background(), taskID, repoID); err != nil {
		t.Fatalf("RunFullIndex error: %v", err)
	}

	update := store.lastTaskUpdate(t)
	if update.status != "partial" {
		t.Fatalf("expected partial, got %s", update.status)
	}
	if got := intFromAny(t, update.stats["files_processed"]); got != 7 {
		t.Fatalf("files_processed=%d want=7", got)
	}
	errorsList, ok := update.stats["errors"].([]string)
	if !ok {
		t.Fatalf("errors should be []string, got %T", update.stats["errors"])
	}
	if len(errorsList) != 3 {
		t.Fatalf("errors len=%d want=3", len(errorsList))
	}
}

func TestIncrementalIndex(t *testing.T) {
	repoDir := t.TempDir()
	oldTime := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Second)

	old1 := writeGo(t, repoDir, "old1.go")
	old2 := writeGo(t, repoDir, "old2.go")
	old3 := writeGo(t, repoDir, "old3.go")
	old4 := writeGo(t, repoDir, "old4.go")
	new1 := writeGo(t, repoDir, "new1.go")
	new2 := writeGo(t, repoDir, "new2.go")
	deleted := filepath.Join(repoDir, "old5.go")

	for _, p := range []string{old1, old3, old4} {
		if err := os.Chtimes(p, oldTime, oldTime); err != nil {
			t.Fatalf("chtimes %s: %v", p, err)
		}
	}

	snapshot := []string{
		fmt.Sprintf("%s|%d", old1, oldTime.UnixNano()),
		fmt.Sprintf("%s|%d", old2, oldTime.UnixNano()),
		fmt.Sprintf("%s|%d", old3, oldTime.UnixNano()),
		fmt.Sprintf("%s|%d", old4, oldTime.UnixNano()),
		fmt.Sprintf("%s|%d", deleted, oldTime.UnixNano()),
	}

	parserCalls := make(map[string]int)
	var callsMu sync.Mutex

	store := newMockIndexStore()
	repoID := uuid.New()
	taskID := uuid.New()
	idx := &Indexer{
		store: store,
		repoStore: &mockRepoReader{repo: &repo.Repo{
			ID:           repoID,
			Path:         repoDir,
			IndexedFiles: snapshot,
		}},
		parser: func(path string) ([]Function, []RawCallEdge, error) {
			callsMu.Lock()
			parserCalls[path]++
			callsMu.Unlock()

			name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
			return []Function{{Name: name, FilePath: path, StartLine: 1, EndLine: 2, Source: "func"}}, nil, nil
		},
		embedder: &mockEmbedder{},
		cfg:      &config.Config{WorkerConcurrency: 2, EmbeddingConcurrency: 2},
	}

	if err := idx.RunIncrementalIndex(context.Background(), taskID, repoID); err != nil {
		t.Fatalf("RunIncrementalIndex error: %v", err)
	}

	callsMu.Lock()
	defer callsMu.Unlock()
	if parserCalls[new1] != 1 || parserCalls[new2] != 1 {
		t.Fatalf("expected parser called for new files, calls=%v", parserCalls)
	}
	if parserCalls[old1] != 0 || parserCalls[old3] != 0 || parserCalls[old4] != 0 {
		t.Fatalf("expected unchanged files not parsed, calls=%v", parserCalls)
	}

	sortedDeleted := append([]string(nil), store.deletedByFiles...)
	sort.Strings(sortedDeleted)
	if !contains(sortedDeleted, deleted) {
		t.Fatalf("expected deleted file in DeleteFunctionsByFiles, got %v", sortedDeleted)
	}

	update := store.lastTaskUpdate(t)
	if update.status != "completed" {
		t.Fatalf("expected completed, got %s", update.status)
	}
	if got := intFromAny(t, update.stats["files_total"]); got != 4 {
		t.Fatalf("files_total=%d want=4", got)
	}
	if got := intFromAny(t, update.stats["files_processed"]); got != 4 {
		t.Fatalf("files_processed=%d want=4", got)
	}
}

func TestFileErrorsRecordedInStats(t *testing.T) {
	repoDir, files := createGoFiles(t, 5)
	failedFile := files[0]

	store := newMockIndexStore()
	repoID := uuid.New()
	taskID := uuid.New()
	idx := &Indexer{
		store:     store,
		repoStore: &mockRepoReader{repo: &repo.Repo{ID: repoID, Path: repoDir}},
		parser: func(path string) ([]Function, []RawCallEdge, error) {
			if path == failedFile {
				return nil, nil, fmt.Errorf("parse failed")
			}
			name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
			return []Function{{Name: name, FilePath: path, StartLine: 1, EndLine: 2, Source: "func"}}, nil, nil
		},
		embedder: &mockEmbedder{},
		cfg:      &config.Config{WorkerConcurrency: 2, EmbeddingConcurrency: 2},
	}

	if err := idx.RunFullIndex(context.Background(), taskID, repoID); err != nil {
		t.Fatalf("RunFullIndex error: %v", err)
	}

	update := store.lastTaskUpdate(t)
	errorsList, ok := update.stats["errors"].([]string)
	if !ok {
		t.Fatalf("errors should be []string, got %T", update.stats["errors"])
	}

	found := false
	for _, errMsg := range errorsList {
		if strings.Contains(errMsg, failedFile) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected failed file in stats.errors, got %v", errorsList)
	}
}

func createGoFiles(t *testing.T, n int) (string, []string) {
	t.Helper()
	repoDir := t.TempDir()
	paths := make([]string, 0, n)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("f%d.go", i)
		paths = append(paths, writeGo(t, repoDir, name))
	}
	return repoDir, paths
}

func writeGo(t *testing.T, dir string, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("package main\n\nfunc x() {}\n"), 0o644); err != nil {
		t.Fatalf("write file %s: %v", p, err)
	}
	return p
}

func intFromAny(t *testing.T, v any) int {
	t.Helper()
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		t.Fatalf("unexpected numeric type %T", v)
		return 0
	}
}

func contains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func cloneMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}
