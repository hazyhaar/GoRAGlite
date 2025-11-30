// Package merger implements the merger component.
// The merger is the sole writer to corpus.db - serialization guaranteed.
package merger

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"goraglite/internal/db"
)

// Merger integrates completed run outputs into the corpus.
// It is the only component that writes to corpus.db.
type Merger struct {
	corpusDB *db.DB
	queueDir string
	doneDir  string
	failDir  string

	mu        sync.Mutex
	running   bool
	stopCh    chan struct{}
	batchSize int
	interval  time.Duration
}

// Config holds merger configuration.
type Config struct {
	QueueDir  string
	DoneDir   string
	FailDir   string
	BatchSize int
	Interval  time.Duration
}

// DefaultConfig returns sensible defaults.
func DefaultConfig(dataDir string) Config {
	return Config{
		QueueDir:  filepath.Join(dataDir, "queue", "pending"),
		DoneDir:   filepath.Join(dataDir, "queue", "done"),
		FailDir:   filepath.Join(dataDir, "queue", "failed"),
		BatchSize: 100,
		Interval:  time.Second,
	}
}

// New creates a new merger.
func New(corpusDB *db.DB, cfg Config) (*Merger, error) {
	// Ensure directories exist
	for _, dir := range []string{cfg.QueueDir, cfg.DoneDir, cfg.FailDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("create dir %s: %w", dir, err)
		}
	}

	return &Merger{
		corpusDB:  corpusDB,
		queueDir:  cfg.QueueDir,
		doneDir:   cfg.DoneDir,
		failDir:   cfg.FailDir,
		batchSize: cfg.BatchSize,
		interval:  cfg.Interval,
		stopCh:    make(chan struct{}),
	}, nil
}

// Start starts the merger loop.
func (m *Merger) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return fmt.Errorf("merger already running")
	}
	m.running = true
	m.mu.Unlock()

	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-m.stopCh:
			return nil
		case <-ticker.C:
			if err := m.processBatch(ctx); err != nil {
				// Log error but continue
				fmt.Fprintf(os.Stderr, "merger batch error: %v\n", err)
			}
		}
	}
}

// Stop stops the merger loop.
func (m *Merger) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running {
		close(m.stopCh)
		m.running = false
	}
}

// ProcessOne processes a single run file immediately.
func (m *Merger) ProcessOne(ctx context.Context, runDBPath string) error {
	return m.mergeRun(ctx, runDBPath)
}

// processBatch processes a batch of pending runs.
func (m *Merger) processBatch(ctx context.Context) error {
	entries, err := os.ReadDir(m.queueDir)
	if err != nil {
		return fmt.Errorf("read queue dir: %w", err)
	}

	// Filter and sort by modification time (FIFO)
	var dbFiles []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".db") {
			continue
		}
		dbFiles = append(dbFiles, filepath.Join(m.queueDir, entry.Name()))
	}

	if len(dbFiles) == 0 {
		return nil
	}

	// Sort by modification time
	sort.Slice(dbFiles, func(i, j int) bool {
		fi, _ := os.Stat(dbFiles[i])
		fj, _ := os.Stat(dbFiles[j])
		if fi == nil || fj == nil {
			return false
		}
		return fi.ModTime().Before(fj.ModTime())
	})

	// Process up to batchSize
	count := m.batchSize
	if len(dbFiles) < count {
		count = len(dbFiles)
	}

	for i := 0; i < count; i++ {
		if err := m.mergeRun(ctx, dbFiles[i]); err != nil {
			// Move to failed
			failPath := filepath.Join(m.failDir, filepath.Base(dbFiles[i]))
			os.Rename(dbFiles[i], failPath)
			fmt.Fprintf(os.Stderr, "merge failed for %s: %v\n", dbFiles[i], err)
			continue
		}

		// Move to done
		donePath := filepath.Join(m.doneDir, filepath.Base(dbFiles[i]))
		os.Rename(dbFiles[i], donePath)
	}

	return nil
}

// mergeRun merges a single run's output into the corpus.
func (m *Merger) mergeRun(ctx context.Context, runDBPath string) error {
	// Verify file exists
	if _, err := os.Stat(runDBPath); err != nil {
		return fmt.Errorf("run db not found: %w", err)
	}

	// Attach run database
	alias := "run_src"
	if err := m.corpusDB.Attach(ctx, runDBPath, alias); err != nil {
		return fmt.Errorf("attach run db: %w", err)
	}
	defer m.corpusDB.Detach(ctx, alias)

	// Verify run completed successfully
	var status string
	err := m.corpusDB.QueryRowContext(ctx, fmt.Sprintf("SELECT status FROM %s._run_meta LIMIT 1", alias)).Scan(&status)
	if err != nil {
		return fmt.Errorf("check run status: %w", err)
	}
	if status != "completed" {
		return fmt.Errorf("run not completed, status: %s", status)
	}

	// Get run metadata
	var runID, workflowID string
	var workflowVersion int
	err = m.corpusDB.QueryRowContext(ctx,
		fmt.Sprintf("SELECT run_id, workflow_id, workflow_version FROM %s._run_meta LIMIT 1", alias),
	).Scan(&runID, &workflowID, &workflowVersion)
	if err != nil {
		return fmt.Errorf("get run meta: %w", err)
	}

	// Check if already merged (idempotency)
	var existingCount int
	m.corpusDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM run_history WHERE run_id = ? AND merge_status = 'merged'",
		runID,
	).Scan(&existingCount)
	if existingCount > 0 {
		return nil // Already merged
	}

	// Merge in transaction
	return m.corpusDB.Transaction(ctx, func(tx *sql.Tx) error {
		// Merge chunks
		chunksInserted, err := m.mergeChunks(ctx, tx, alias, runID)
		if err != nil {
			return fmt.Errorf("merge chunks: %w", err)
		}

		// Merge features
		if err := m.mergeFeatures(ctx, tx, alias); err != nil {
			return fmt.Errorf("merge features: %w", err)
		}

		// Merge vectors
		if err := m.mergeVectors(ctx, tx, alias); err != nil {
			return fmt.Errorf("merge vectors: %w", err)
		}

		// Merge relations
		if err := m.mergeRelations(ctx, tx, alias, runID); err != nil {
			return fmt.Errorf("merge relations: %w", err)
		}

		// Update run history
		_, err = tx.ExecContext(ctx, `
			INSERT OR REPLACE INTO run_history
			(run_id, workflow_id, workflow_version, started_at, finished_at, status, rows_produced, merge_status)
			SELECT run_id, workflow_id, workflow_version, started_at, finished_at, status, ?, 'merged'
			FROM run_src._run_meta
		`, chunksInserted)
		if err != nil {
			return fmt.Errorf("update run history: %w", err)
		}

		// Update raw_files status for processed files
		_, err = tx.ExecContext(ctx, `
			UPDATE raw_files SET status = 'vectorized'
			WHERE id IN (SELECT DISTINCT file_id FROM run_src._output)
		`)
		if err != nil {
			return fmt.Errorf("update file status: %w", err)
		}

		return nil
	})
}

// mergeChunks merges chunks from run output to corpus.
func (m *Merger) mergeChunks(ctx context.Context, tx *sql.Tx, alias, runID string) (int64, error) {
	// Check if _output table exists
	var tableExists int
	err := tx.QueryRowContext(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM %s.sqlite_master WHERE type='table' AND name='_output'", alias),
	).Scan(&tableExists)
	if err != nil || tableExists == 0 {
		return 0, nil // No chunks to merge
	}

	result, err := tx.ExecContext(ctx, fmt.Sprintf(`
		INSERT OR IGNORE INTO chunks
		(id, file_id, unit_ids, content, token_count, chunk_type, overlap_prev, overlap_next, hash, position, parent_id, created_by_run)
		SELECT
			id, file_id, unit_ids, content, token_count, chunk_type,
			overlap_prev, overlap_next, hash, position, parent_id, '%s'
		FROM %s._output
	`, runID, alias))
	if err != nil {
		return 0, err
	}

	return result.RowsAffected()
}

// mergeFeatures merges chunk features from run output to corpus.
func (m *Merger) mergeFeatures(ctx context.Context, tx *sql.Tx, alias string) error {
	// Check if table exists
	var tableExists int
	err := tx.QueryRowContext(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM %s.sqlite_master WHERE type='table' AND name='_output_features'", alias),
	).Scan(&tableExists)
	if err != nil || tableExists == 0 {
		return nil
	}

	_, err = tx.ExecContext(ctx, fmt.Sprintf(`
		INSERT OR REPLACE INTO chunk_features (chunk_id, feature_name, feature_value, feature_meta)
		SELECT chunk_id, feature_name, feature_value, feature_meta
		FROM %s._output_features
	`, alias))
	return err
}

// mergeVectors merges chunk vectors from run output to corpus.
func (m *Merger) mergeVectors(ctx context.Context, tx *sql.Tx, alias string) error {
	// Check if table exists
	var tableExists int
	err := tx.QueryRowContext(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM %s.sqlite_master WHERE type='table' AND name='_output_vectors'", alias),
	).Scan(&tableExists)
	if err != nil || tableExists == 0 {
		return nil
	}

	_, err = tx.ExecContext(ctx, fmt.Sprintf(`
		INSERT OR REPLACE INTO chunk_vectors (chunk_id, layer, vector, dimensions, model_version)
		SELECT chunk_id, layer, vector, dimensions, model_version
		FROM %s._output_vectors
	`, alias))
	return err
}

// mergeRelations merges chunk relations from run output to corpus.
func (m *Merger) mergeRelations(ctx context.Context, tx *sql.Tx, alias, runID string) error {
	// Check if table exists
	var tableExists int
	err := tx.QueryRowContext(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM %s.sqlite_master WHERE type='table' AND name='_output_relations'", alias),
	).Scan(&tableExists)
	if err != nil || tableExists == 0 {
		return nil
	}

	_, err = tx.ExecContext(ctx, fmt.Sprintf(`
		INSERT OR IGNORE INTO chunk_relations (from_chunk_id, to_chunk_id, relation_type, weight, created_by_run)
		SELECT from_chunk_id, to_chunk_id, relation_type, weight, '%s'
		FROM %s._output_relations
	`, runID, alias))
	return err
}

// Status returns the merger status.
type Status struct {
	Running       bool      `json:"running"`
	PendingCount  int       `json:"pending_count"`
	DoneCount     int       `json:"done_count"`
	FailedCount   int       `json:"failed_count"`
	LastProcessed time.Time `json:"last_processed,omitempty"`
}

// Status returns current merger status.
func (m *Merger) Status() (*Status, error) {
	status := &Status{
		Running: m.running,
	}

	// Count pending
	if entries, err := os.ReadDir(m.queueDir); err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".db") {
				status.PendingCount++
			}
		}
	}

	// Count done
	if entries, err := os.ReadDir(m.doneDir); err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".db") {
				status.DoneCount++
			}
		}
	}

	// Count failed
	if entries, err := os.ReadDir(m.failDir); err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".db") {
				status.FailedCount++
			}
		}
	}

	return status, nil
}

// GarbageCollect removes old done files.
func (m *Merger) GarbageCollect(ctx context.Context, maxAge time.Duration) error {
	entries, err := os.ReadDir(m.doneDir)
	if err != nil {
		return err
	}

	cutoff := time.Now().Add(-maxAge)
	var removed int

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		if info.ModTime().Before(cutoff) {
			path := filepath.Join(m.doneDir, entry.Name())
			if err := os.Remove(path); err == nil {
				removed++
			}
		}
	}

	return nil
}

// RetryFailed moves failed runs back to pending.
func (m *Merger) RetryFailed(ctx context.Context) (int, error) {
	entries, err := os.ReadDir(m.failDir)
	if err != nil {
		return 0, err
	}

	var retried int
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".db") {
			continue
		}

		srcPath := filepath.Join(m.failDir, entry.Name())
		dstPath := filepath.Join(m.queueDir, entry.Name())

		if err := os.Rename(srcPath, dstPath); err == nil {
			retried++
		}
	}

	return retried, nil
}

// MergeResult holds the result of a merge operation.
type MergeResult struct {
	RunID          string    `json:"run_id"`
	WorkflowID     string    `json:"workflow_id"`
	ChunksInserted int64     `json:"chunks_inserted"`
	Duration       time.Duration `json:"duration"`
	Error          string    `json:"error,omitempty"`
}

// MarshalJSON implements json.Marshaler.
func (r *MergeResult) MarshalJSON() ([]byte, error) {
	type Alias MergeResult
	return json.Marshal(&struct {
		*Alias
		Duration string `json:"duration"`
	}{
		Alias:    (*Alias)(r),
		Duration: r.Duration.String(),
	})
}
