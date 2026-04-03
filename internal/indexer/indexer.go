package indexer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/XbLuzk/logicmap/internal/config"
	"github.com/XbLuzk/logicmap/internal/embedding"
	"github.com/XbLuzk/logicmap/internal/repo"
	"github.com/google/uuid"
	"golang.org/x/sync/semaphore"
)

type indexStore interface {
	BulkInsertFunctions(ctx context.Context, repoID uuid.UUID, funcs []FunctionRecord) (int64, error)
	BulkInsertCallEdges(ctx context.Context, edges []CallEdgeRecord) (int64, error)
	BulkInsertUnresolvedEdges(ctx context.Context, edges []UnresolvedEdgeRecord) error
	GetFunctionMap(ctx context.Context, repoID uuid.UUID) (map[string][]uuid.UUID, error)
	DeleteFunctionsByRepo(ctx context.Context, repoID uuid.UUID) error
	DeleteFunctionsByFiles(ctx context.Context, repoID uuid.UUID, filePaths []string) error
	UpdateTaskStatus(ctx context.Context, taskID uuid.UUID, status string, errorMsg string, stats map[string]any) error
	UpdateTaskStarted(ctx context.Context, taskID uuid.UUID) error
	DropHNSWIndex(ctx context.Context) error
	RecreateHNSWIndex(ctx context.Context) error
	UpdateRepoIndexedFiles(ctx context.Context, repoID uuid.UUID, files []string) error
}

type repoReader interface {
	GetRepo(ctx context.Context, id uuid.UUID, staleThreshold time.Duration) (*repo.Repo, error)
}

type Indexer struct {
	store     indexStore
	repoStore repoReader
	parser    func(string) ([]Function, []RawCallEdge, error)
	embedder  embedding.EmbeddingClient
	cfg       *config.Config
}

func NewIndexer(store *IndexStore, repoStore *repo.RepoStore, parser func(string) ([]Function, []RawCallEdge, error), embedder embedding.EmbeddingClient, cfg *config.Config) *Indexer {
	if parser == nil {
		parser = ParseFile
	}
	return &Indexer{
		store:     store,
		repoStore: repoStore,
		parser:    parser,
		embedder:  embedder,
		cfg:       cfg,
	}
}

func (idx *Indexer) RunFullIndex(ctx context.Context, taskID, repoID uuid.UUID) error {
	stats := map[string]any{
		"files_total":       0,
		"files_processed":   0,
		"functions_indexed": 0,
		"errors":            []string{},
	}

	if err := idx.store.UpdateTaskStarted(ctx, taskID); err != nil {
		return fmt.Errorf("start task: %w", err)
	}

	staleThreshold := idx.staleThreshold()
	r, err := idx.repoStore.GetRepo(ctx, repoID, staleThreshold)
	if err != nil {
		return idx.failTask(ctx, taskID, stats, fmt.Errorf("get repo: %w", err))
	}

	if err := idx.store.DropHNSWIndex(ctx); err != nil {
		return idx.failTask(ctx, taskID, stats, fmt.Errorf("drop hnsw index: %w", err))
	}

	if err := idx.store.DeleteFunctionsByRepo(ctx, repoID); err != nil {
		return idx.failTask(ctx, taskID, stats, fmt.Errorf("delete repo functions: %w", err))
	}

	allFiles, err := collectSourceFiles(r.Path)
	if err != nil {
		return idx.failTask(ctx, taskID, stats, fmt.Errorf("scan source files: %w", err))
	}

	stats["files_total"] = len(allFiles)

	res, err := idx.processFiles(ctx, repoID, allFiles)
	if err != nil {
		return idx.failTask(ctx, taskID, stats, err)
	}

	stats["files_processed"] = len(allFiles) - len(res.erroredFiles)
	stats["functions_indexed"] = int(res.functionsIndexed)
	stats["errors"] = res.errors

	if err := idx.store.RecreateHNSWIndex(ctx); err != nil {
		return idx.failTask(ctx, taskID, stats, fmt.Errorf("recreate hnsw index: %w", err))
	}

	if err := idx.store.UpdateRepoIndexedFiles(ctx, repoID, encodeFileSnapshots(allFiles)); err != nil {
		return idx.failTask(ctx, taskID, stats, fmt.Errorf("update indexed files snapshot: %w", err))
	}

	status := "completed"
	if len(allFiles) > 0 && float64(len(res.erroredFiles))/float64(len(allFiles)) > 0.2 {
		status = "partial"
	}

	if err := idx.store.UpdateTaskStatus(ctx, taskID, status, "", stats); err != nil {
		return fmt.Errorf("update task status: %w", err)
	}

	return nil
}

func (idx *Indexer) RunIncrementalIndex(ctx context.Context, taskID, repoID uuid.UUID) error {
	stats := map[string]any{
		"files_total":       0,
		"files_processed":   0,
		"functions_indexed": 0,
		"errors":            []string{},
	}

	if err := idx.store.UpdateTaskStarted(ctx, taskID); err != nil {
		return fmt.Errorf("start task: %w", err)
	}

	staleThreshold := idx.staleThreshold()
	r, err := idx.repoStore.GetRepo(ctx, repoID, staleThreshold)
	if err != nil {
		return idx.failTask(ctx, taskID, stats, fmt.Errorf("get repo: %w", err))
	}

	currentFiles, err := collectSourceFiles(r.Path)
	if err != nil {
		return idx.failTask(ctx, taskID, stats, fmt.Errorf("scan source files: %w", err))
	}

	snapshotMap := decodeSnapshotEntries(r.IndexedFiles)
	currentMap := make(map[string]time.Time, len(currentFiles))
	for _, file := range currentFiles {
		currentMap[file.Path] = file.ModTime
	}

	changedFiles := make([]fileSnapshot, 0)
	for _, file := range currentFiles {
		lastMod, ok := snapshotMap[file.Path]
		if !ok || !file.ModTime.Equal(lastMod) {
			changedFiles = append(changedFiles, file)
		}
	}

	deletedPaths := make([]string, 0)
	for path := range snapshotMap {
		if _, ok := currentMap[path]; !ok {
			deletedPaths = append(deletedPaths, path)
		}
	}
	sort.Strings(deletedPaths)

	totalFiles := len(changedFiles) + len(deletedPaths)
	stats["files_total"] = totalFiles

	toDelete := make([]string, 0, totalFiles)
	for _, file := range changedFiles {
		toDelete = append(toDelete, file.Path)
	}
	toDelete = append(toDelete, deletedPaths...)

	if err := idx.store.DeleteFunctionsByFiles(ctx, repoID, toDelete); err != nil {
		return idx.failTask(ctx, taskID, stats, fmt.Errorf("delete changed/deleted functions: %w", err))
	}

	res, err := idx.processFiles(ctx, repoID, changedFiles)
	if err != nil {
		return idx.failTask(ctx, taskID, stats, err)
	}

	stats["files_processed"] = totalFiles - len(res.erroredFiles)
	stats["functions_indexed"] = int(res.functionsIndexed)
	stats["errors"] = res.errors

	if err := idx.store.UpdateRepoIndexedFiles(ctx, repoID, encodeFileSnapshots(currentFiles)); err != nil {
		return idx.failTask(ctx, taskID, stats, fmt.Errorf("update indexed files snapshot: %w", err))
	}

	status := "completed"
	if totalFiles > 0 && float64(len(res.erroredFiles))/float64(totalFiles) > 0.2 {
		status = "partial"
	}

	if err := idx.store.UpdateTaskStatus(ctx, taskID, status, "", stats); err != nil {
		return fmt.Errorf("update task status: %w", err)
	}

	return nil
}

type fileSnapshot struct {
	Path    string
	ModTime time.Time
}

type parsedFile struct {
	Path      string
	ModTime   time.Time
	Functions []Function
	RawEdges  []RawCallEdge
}

type edgesForFile struct {
	Path  string
	Edges []RawCallEdge
}

type processResult struct {
	functionsIndexed int64
	errors           []string
	erroredFiles     map[string]struct{}
}

func (idx *Indexer) processFiles(ctx context.Context, repoID uuid.UUID, files []fileSnapshot) (*processResult, error) {
	result := &processResult{
		errors:       make([]string, 0),
		erroredFiles: make(map[string]struct{}),
	}

	parsed, parseErrors, parseErroredFiles := idx.parseFiles(ctx, files)
	result.errors = append(result.errors, parseErrors...)
	for path := range parseErroredFiles {
		result.erroredFiles[path] = struct{}{}
	}

	functionRecords := make([]FunctionRecord, 0)
	edgeByFile := make([]edgesForFile, 0)
	callerIDByFileAndName := make(map[string]uuid.UUID)

	for _, pf := range parsed {
		texts := make([]string, 0, len(pf.Functions))
		for _, fn := range pf.Functions {
			texts = append(texts, fn.Source)
		}

		embeddings, err := embedding.BatchEmbed(ctx, idx.embedder, texts, idx.embeddingConcurrency())
		if err != nil {
			result.errors = append(result.errors, fmt.Sprintf("embedding error: %s: %v", pf.Path, err))
			result.erroredFiles[pf.Path] = struct{}{}
			continue
		}

		for i, fn := range pf.Functions {
			fnID := uuid.New()
			callerIDByFileAndName[fileScopedKey(pf.Path, fn.Name)] = fnID
			functionRecords = append(functionRecords, FunctionRecord{
				ID:        fnID,
				RepoID:    repoID,
				Name:      fn.Name,
				FilePath:  fn.FilePath,
				StartLine: fn.StartLine,
				EndLine:   fn.EndLine,
				Source:    fn.Source,
				Embedding: embeddings[i],
			})
		}

		edgeByFile = append(edgeByFile, edgesForFile{Path: pf.Path, Edges: pf.RawEdges})
	}

	insertedCount, err := idx.store.BulkInsertFunctions(ctx, repoID, functionRecords)
	if err != nil {
		return nil, fmt.Errorf("insert functions: %w", err)
	}
	result.functionsIndexed = insertedCount

	functionMap, err := idx.store.GetFunctionMap(ctx, repoID)
	if err != nil {
		return nil, fmt.Errorf("get function map: %w", err)
	}

	callEdges := make([]CallEdgeRecord, 0)
	unresolvedEdges := make([]UnresolvedEdgeRecord, 0)
	for _, fileEdges := range edgeByFile {
		for _, edge := range fileEdges.Edges {
			callerID, ok := callerIDByFileAndName[fileScopedKey(fileEdges.Path, edge.CallerName)]
			if !ok {
				continue
			}

			calleeIDs := functionMap[edge.CalleeName]
			switch len(calleeIDs) {
			case 0:
				unresolvedEdges = append(unresolvedEdges, UnresolvedEdgeRecord{
					ID:            uuid.New(),
					RepoID:        repoID,
					CallerID:      callerID,
					CalleeNameRaw: edge.CalleeName,
				})
			case 1:
				callEdges = append(callEdges, CallEdgeRecord{
					ID:       uuid.New(),
					RepoID:   repoID,
					CallerID: callerID,
					CalleeID: calleeIDs[0],
				})
			default:
				callEdges = append(callEdges, CallEdgeRecord{
					ID:       uuid.New(),
					RepoID:   repoID,
					CallerID: callerID,
					CalleeID: calleeIDs[0],
				})
				unresolvedEdges = append(unresolvedEdges, UnresolvedEdgeRecord{
					ID:            uuid.New(),
					RepoID:        repoID,
					CallerID:      callerID,
					CalleeNameRaw: edge.CalleeName + " (ambiguous)",
				})
			}
		}
	}

	if _, err := idx.store.BulkInsertCallEdges(ctx, callEdges); err != nil {
		return nil, fmt.Errorf("insert call edges: %w", err)
	}
	if err := idx.store.BulkInsertUnresolvedEdges(ctx, unresolvedEdges); err != nil {
		return nil, fmt.Errorf("insert unresolved edges: %w", err)
	}

	return result, nil
}

func (idx *Indexer) parseFiles(ctx context.Context, files []fileSnapshot) ([]parsedFile, []string, map[string]struct{}) {
	type parseResult struct {
		parsed parsedFile
		err    error
	}

	sem := semaphore.NewWeighted(int64(idx.workerConcurrency()))
	outCh := make(chan parseResult, len(files))

	var wg sync.WaitGroup
	for _, file := range files {
		if err := sem.Acquire(ctx, 1); err != nil {
			outCh <- parseResult{err: err}
			break
		}

		wg.Add(1)
		go func(f fileSnapshot) {
			defer wg.Done()
			defer sem.Release(1)

			functions, rawEdges, err := idx.parser(f.Path)
			if err != nil {
				if errors.Is(err, ErrUnsupportedLanguage) {
					outCh <- parseResult{}
					return
				}
				outCh <- parseResult{err: fmt.Errorf("parse error: %s: %w", f.Path, err)}
				return
			}

			outCh <- parseResult{parsed: parsedFile{
				Path:      f.Path,
				ModTime:   f.ModTime,
				Functions: functions,
				RawEdges:  rawEdges,
			}}
		}(file)
	}

	wg.Wait()
	close(outCh)

	parsed := make([]parsedFile, 0)
	errorsList := make([]string, 0)
	erroredFiles := make(map[string]struct{})

	for result := range outCh {
		if result.err != nil {
			errorsList = append(errorsList, result.err.Error())
			if strings.HasPrefix(result.err.Error(), "parse error: ") {
				path := strings.TrimPrefix(result.err.Error(), "parse error: ")
				if i := strings.Index(path, ": "); i > 0 {
					erroredFiles[path[:i]] = struct{}{}
				}
			}
			continue
		}
		if result.parsed.Path != "" {
			parsed = append(parsed, result.parsed)
		}
	}

	sort.Slice(parsed, func(i, j int) bool {
		return parsed[i].Path < parsed[j].Path
	})

	return parsed, errorsList, erroredFiles
}

func collectSourceFiles(root string) ([]fileSnapshot, error) {
	files := make([]fileSnapshot, 0)

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			if path == root {
				return nil
			}
			name := d.Name()
			if name == ".git" || name == "vendor" || name == "node_modules" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}

		if _, err := DetectLanguage(path); err != nil {
			if errors.Is(err, ErrUnsupportedLanguage) {
				return nil
			}
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		files = append(files, fileSnapshot{Path: path, ModTime: info.ModTime().UTC()})
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})
	return files, nil
}

func encodeFileSnapshots(files []fileSnapshot) []string {
	encoded := make([]string, 0, len(files))
	for _, f := range files {
		encoded = append(encoded, fmt.Sprintf("%s|%d", f.Path, f.ModTime.UnixNano()))
	}
	sort.Strings(encoded)
	return encoded
}

func decodeSnapshotEntries(entries []string) map[string]time.Time {
	result := make(map[string]time.Time, len(entries))
	for _, entry := range entries {
		parts := strings.Split(entry, "|")
		if len(parts) < 2 {
			result[entry] = time.Time{}
			continue
		}

		ts, err := strconv.ParseInt(parts[len(parts)-1], 10, 64)
		if err != nil {
			result[entry] = time.Time{}
			continue
		}

		path := strings.Join(parts[:len(parts)-1], "|")
		result[path] = time.Unix(0, ts).UTC()
	}
	return result
}

func fileScopedKey(filePath string, funcName string) string {
	return filePath + "\x00" + funcName
}

func (idx *Indexer) staleThreshold() time.Duration {
	if idx.cfg == nil || idx.cfg.StaleTaskThresholdMinutes <= 0 {
		return 10 * time.Minute
	}
	return time.Duration(idx.cfg.StaleTaskThresholdMinutes) * time.Minute
}

func (idx *Indexer) workerConcurrency() int {
	if idx.cfg == nil || idx.cfg.WorkerConcurrency <= 0 {
		return 1
	}
	return idx.cfg.WorkerConcurrency
}

func (idx *Indexer) embeddingConcurrency() int {
	if idx.cfg == nil || idx.cfg.EmbeddingConcurrency <= 0 {
		return 1
	}
	return idx.cfg.EmbeddingConcurrency
}

func (idx *Indexer) failTask(ctx context.Context, taskID uuid.UUID, stats map[string]any, runErr error) error {
	if err := idx.store.UpdateTaskStatus(ctx, taskID, "failed", runErr.Error(), stats); err != nil {
		return fmt.Errorf("%v; additionally failed to update task status: %w", runErr, err)
	}
	return runErr
}
