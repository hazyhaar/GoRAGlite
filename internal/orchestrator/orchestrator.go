// Package orchestrator decides what to process and coordinates workers.
package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"goraglite/internal/db"
	"goraglite/internal/workflow"
)

// Orchestrator coordinates workflow execution.
type Orchestrator struct {
	corpusDB    *db.DB
	workflowsDB *db.DB
	engine      *workflow.Engine
	dataDir     string

	mu           sync.RWMutex
	workers      map[string]*Worker
	workflowMap  map[string]string // mime_type -> workflow_id
	maxWorkers   int
	pollInterval time.Duration
}

// Worker represents a workflow execution worker.
type Worker struct {
	ID        string
	Status    WorkerStatus
	CurrentRun string
	StartedAt time.Time
}

// WorkerStatus represents worker state.
type WorkerStatus string

const (
	WorkerIdle    WorkerStatus = "idle"
	WorkerBusy    WorkerStatus = "busy"
	WorkerStopped WorkerStatus = "stopped"
)

// Config holds orchestrator configuration.
type Config struct {
	DataDir      string
	MaxWorkers   int
	PollInterval time.Duration
}

// DefaultConfig returns sensible defaults.
func DefaultConfig(dataDir string) Config {
	return Config{
		DataDir:      dataDir,
		MaxWorkers:   4,
		PollInterval: 5 * time.Second,
	}
}

// New creates a new orchestrator.
func New(corpusDB, workflowsDB *db.DB, engine *workflow.Engine, cfg Config) *Orchestrator {
	return &Orchestrator{
		corpusDB:     corpusDB,
		workflowsDB:  workflowsDB,
		engine:       engine,
		dataDir:      cfg.DataDir,
		workers:      make(map[string]*Worker),
		workflowMap:  defaultWorkflowMap(),
		maxWorkers:   cfg.MaxWorkers,
		pollInterval: cfg.PollInterval,
	}
}

// defaultWorkflowMap returns default mime type to workflow mappings.
func defaultWorkflowMap() map[string]string {
	return map[string]string{
		"application/pdf":                   "pdf_chunking_v1",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document": "docx_chunking_v1",
		"application/msword":                "docx_chunking_v1",
		"text/plain":                        "text_chunking_v1",
		"text/markdown":                     "text_chunking_v1",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": "xlsx_chunking_v1",
		"application/vnd.ms-excel":          "xlsx_chunking_v1",
	}
}

// Ingest imports a file into the corpus.
// HOROS: Files are copied to external storage, not stored as BLOB.
func (o *Orchestrator) Ingest(ctx context.Context, path string) (string, error) {
	// Open file for hashing
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	// Get file info
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("stat file: %w", err)
	}

	// Calculate hash (content ID) - stream to avoid loading entire file
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("hash file: %w", err)
	}
	checksum := hex.EncodeToString(hasher.Sum(nil))
	id := checksum // Use checksum as ID

	// Check if already exists
	var existingID string
	err = o.corpusDB.QueryRowContext(ctx,
		"SELECT id FROM raw_files WHERE id = ?", id,
	).Scan(&existingID)
	if err == nil {
		return id, nil // Already ingested
	}

	// Read a small portion for MIME detection
	file.Seek(0, io.SeekStart)
	header := make([]byte, 512)
	n, _ := file.Read(header)
	mimeType := detectMimeType(path, header[:n])

	// HOROS: Copy file to external storage (cp/rm compatible)
	// Storage layout: {dataDir}/storage/raw/{hash[0:2]}/{hash}
	storageDir := filepath.Join(o.dataDir, "storage", "raw", checksum[:2])
	if err := os.MkdirAll(storageDir, 0755); err != nil {
		return "", fmt.Errorf("create storage dir: %w", err)
	}

	externalPath := filepath.Join(storageDir, checksum)
	if err := copyFile(path, externalPath); err != nil {
		return "", fmt.Errorf("copy to storage: %w", err)
	}

	// Insert into corpus (external_path instead of content BLOB)
	_, err = o.corpusDB.ExecContext(ctx, `
		INSERT INTO raw_files (id, source_path, mime_type, size, external_path, checksum, status)
		VALUES (?, ?, ?, ?, ?, ?, 'pending')
	`, id, path, mimeType, info.Size(), externalPath, checksum)
	if err != nil {
		// Clean up copied file on failure
		os.Remove(externalPath)
		return "", fmt.Errorf("insert file: %w", err)
	}

	// Log audit
	o.corpusDB.ExecContext(ctx, `
		INSERT INTO audit_log (actor, action, target, details)
		VALUES ('orchestrator', 'ingest', ?, ?)
	`, id, fmt.Sprintf(`{"path":"%s","external_path":"%s","mime":"%s","size":%d}`, path, externalPath, mimeType, info.Size()))

	return id, nil
}

// copyFile copies a file from src to dst.
// HOROS: Simple cp operation for Data-Physics law compliance.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}

	return out.Sync()
}

// IngestDir imports all files from a directory.
func (o *Orchestrator) IngestDir(ctx context.Context, dirPath string, recursive bool) ([]string, error) {
	var ids []string

	walkFn := func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if !recursive && path != dirPath {
				return filepath.SkipDir
			}
			return nil
		}

		id, err := o.Ingest(ctx, path)
		if err != nil {
			return fmt.Errorf("ingest %s: %w", path, err)
		}
		ids = append(ids, id)
		return nil
	}

	if err := filepath.WalkDir(dirPath, walkFn); err != nil {
		return ids, err
	}

	return ids, nil
}

// ProcessPending processes all pending files.
func (o *Orchestrator) ProcessPending(ctx context.Context) error {
	// Query pending files
	rows, err := o.corpusDB.QueryContext(ctx, `
		SELECT id, mime_type FROM raw_files WHERE status = 'pending' LIMIT 100
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type pendingFile struct {
		ID       string
		MimeType string
	}
	var pending []pendingFile

	for rows.Next() {
		var p pendingFile
		if err := rows.Scan(&p.ID, &p.MimeType); err != nil {
			continue
		}
		pending = append(pending, p)
	}

	if len(pending) == 0 {
		return nil
	}

	// Group by workflow
	workflowFiles := make(map[string][]string)
	for _, p := range pending {
		workflowID, ok := o.workflowMap[p.MimeType]
		if !ok {
			continue
		}
		workflowFiles[workflowID] = append(workflowFiles[workflowID], p.ID)
	}

	// Execute workflows
	for workflowID, fileIDs := range workflowFiles {
		cfg := workflow.RunConfig{
			BatchSize: 10,
			Parameters: map[string]string{
				"file_ids": strings.Join(fileIDs, ","),
			},
		}

		run, err := o.engine.Run(ctx, workflowID, cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "workflow %s failed: %v\n", workflowID, err)
			continue
		}

		// Move run db to queue
		queuePath := filepath.Join(o.dataDir, "queue", "pending", filepath.Base(run.DBPath))
		if err := os.Rename(run.DBPath, queuePath); err != nil {
			fmt.Fprintf(os.Stderr, "move run to queue: %v\n", err)
		}
	}

	return nil
}

// Search executes a search query.
func (o *Orchestrator) Search(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	cfg := workflow.RunConfig{
		Parameters: map[string]string{
			"query":  query,
			"top_k":  fmt.Sprintf("%d", topK),
			"layers": "structure,lexical,blend",
		},
	}

	run, err := o.engine.Run(ctx, "search_v1", cfg)
	if err != nil {
		return nil, fmt.Errorf("search workflow: %w", err)
	}

	// Read results from run db
	runDB, err := db.Open(db.DefaultConfig(run.DBPath, db.DBTypeRun))
	if err != nil {
		return nil, fmt.Errorf("open run db: %w", err)
	}
	defer runDB.Close()

	rows, err := runDB.QueryContext(ctx, `
		SELECT chunk_id, score, layer_scores, snippet, file_id
		FROM _output
		ORDER BY score DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("query results: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		var layerScores string
		if err := rows.Scan(&r.ChunkID, &r.Score, &layerScores, &r.Snippet, &r.FileID); err != nil {
			continue
		}
		r.LayerScores = layerScores
		results = append(results, r)
	}

	// Cleanup run db
	os.Remove(run.DBPath)

	return results, nil
}

// SearchResult holds a single search result.
type SearchResult struct {
	ChunkID     string  `json:"chunk_id"`
	Score       float64 `json:"score"`
	LayerScores string  `json:"layer_scores"`
	Snippet     string  `json:"snippet"`
	FileID      string  `json:"file_id"`
	FilePath    string  `json:"file_path,omitempty"`
}

// Status returns orchestrator status.
type Status struct {
	PendingFiles   int            `json:"pending_files"`
	ProcessedFiles int            `json:"processed_files"`
	TotalChunks    int            `json:"total_chunks"`
	TotalVectors   int            `json:"total_vectors"`
	Workers        []WorkerStatus `json:"workers"`
	Workflows      []string       `json:"workflows"`
}

// Status returns current orchestrator status.
func (o *Orchestrator) Status(ctx context.Context) (*Status, error) {
	status := &Status{}

	// Count files
	o.corpusDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM raw_files WHERE status = 'pending'",
	).Scan(&status.PendingFiles)

	o.corpusDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM raw_files WHERE status = 'vectorized'",
	).Scan(&status.ProcessedFiles)

	o.corpusDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM chunks",
	).Scan(&status.TotalChunks)

	o.corpusDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM chunk_vectors",
	).Scan(&status.TotalVectors)

	// Worker status
	o.mu.RLock()
	for _, w := range o.workers {
		status.Workers = append(status.Workers, w.Status)
	}
	o.mu.RUnlock()

	// Available workflows
	for _, wfID := range o.workflowMap {
		found := false
		for _, existing := range status.Workflows {
			if existing == wfID {
				found = true
				break
			}
		}
		if !found {
			status.Workflows = append(status.Workflows, wfID)
		}
	}

	return status, nil
}

// SetWorkflowMapping sets the mime type to workflow mapping.
func (o *Orchestrator) SetWorkflowMapping(mimeType, workflowID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.workflowMap[mimeType] = workflowID
}

// detectMimeType detects the MIME type of a file.
func detectMimeType(path string, content []byte) string {
	// First try by extension
	ext := filepath.Ext(path)
	if ext != "" {
		mimeType := mime.TypeByExtension(ext)
		if mimeType != "" {
			// Strip parameters
			if idx := strings.Index(mimeType, ";"); idx != -1 {
				mimeType = mimeType[:idx]
			}
			return mimeType
		}
	}

	// Common extensions not in mime package
	extMap := map[string]string{
		".md":   "text/markdown",
		".go":   "text/x-go",
		".py":   "text/x-python",
		".js":   "text/javascript",
		".ts":   "text/typescript",
		".rs":   "text/x-rust",
		".sql":  "text/x-sql",
		".yaml": "text/yaml",
		".yml":  "text/yaml",
		".json": "application/json",
		".toml": "text/toml",
	}
	if mimeType, ok := extMap[strings.ToLower(ext)]; ok {
		return mimeType
	}

	// Try content detection for common binary formats
	if len(content) >= 4 {
		// PDF
		if string(content[:4]) == "%PDF" {
			return "application/pdf"
		}
		// ZIP-based (docx, xlsx, etc.)
		if content[0] == 0x50 && content[1] == 0x4B {
			if ext == ".docx" {
				return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
			}
			if ext == ".xlsx" {
				return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
			}
			return "application/zip"
		}
	}

	return "application/octet-stream"
}

// GetChunk retrieves a chunk by ID.
func (o *Orchestrator) GetChunk(ctx context.Context, chunkID string) (*Chunk, error) {
	var c Chunk
	err := o.corpusDB.QueryRowContext(ctx, `
		SELECT c.id, c.file_id, c.content, c.token_count, c.chunk_type, c.position, r.source_path
		FROM chunks c
		JOIN raw_files r ON c.file_id = r.id
		WHERE c.id = ?
	`, chunkID).Scan(&c.ID, &c.FileID, &c.Content, &c.TokenCount, &c.ChunkType, &c.Position, &c.FilePath)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// Chunk represents a text chunk.
type Chunk struct {
	ID         string `json:"id"`
	FileID     string `json:"file_id"`
	Content    string `json:"content"`
	TokenCount int    `json:"token_count"`
	ChunkType  string `json:"chunk_type"`
	Position   int    `json:"position"`
	FilePath   string `json:"file_path"`
}

// GetFile retrieves a file by ID.
func (o *Orchestrator) GetFile(ctx context.Context, fileID string) (*File, error) {
	var f File
	err := o.corpusDB.QueryRowContext(ctx, `
		SELECT id, source_path, mime_type, size, status, imported_at
		FROM raw_files
		WHERE id = ?
	`, fileID).Scan(&f.ID, &f.SourcePath, &f.MimeType, &f.Size, &f.Status, &f.ImportedAt)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// File represents a raw file.
type File struct {
	ID         string    `json:"id"`
	SourcePath string    `json:"source_path"`
	MimeType   string    `json:"mime_type"`
	Size       int64     `json:"size"`
	Status     string    `json:"status"`
	ImportedAt time.Time `json:"imported_at"`
}

// ListFiles returns files with optional filtering.
func (o *Orchestrator) ListFiles(ctx context.Context, status string, limit int) ([]File, error) {
	query := "SELECT id, source_path, mime_type, size, status, imported_at FROM raw_files"
	args := []any{}

	if status != "" {
		query += " WHERE status = ?"
		args = append(args, status)
	}
	query += " ORDER BY imported_at DESC"
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := o.corpusDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []File
	for rows.Next() {
		var f File
		if err := rows.Scan(&f.ID, &f.SourcePath, &f.MimeType, &f.Size, &f.Status, &f.ImportedAt); err != nil {
			continue
		}
		files = append(files, f)
	}

	return files, nil
}
