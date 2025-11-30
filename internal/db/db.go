// Package db provides SQLite database operations for GoRAGlite.
// Following the principle: Go = I/O, SQL = logique.
//
// HOROS Compliance:
// - Uses modernc.org/sqlite (pure Go, no CGO)
// - Embeds via assets/ package (no ".." in go:embed)
// - Supports ATTACH/DETACH with defer pattern
package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"goraglite/assets"
)

// DB wraps a SQLite database connection with GoRAGlite-specific operations.
type DB struct {
	*sql.DB
	path     string
	dbType   DBType
	mu       sync.RWMutex
	attached map[string]string // alias -> path
}

// DBType identifies the type of database.
type DBType int

const (
	DBTypeCorpus DBType = iota
	DBTypeWorkflows
	DBTypeRun
)

// Config holds database configuration.
type Config struct {
	Path            string
	Type            DBType
	WALMode         bool
	ForeignKeys     bool
	BusyTimeout     time.Duration
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// DefaultConfig returns sensible defaults for a GoRAGlite database.
func DefaultConfig(path string, dbType DBType) Config {
	return Config{
		Path:            path,
		Type:            dbType,
		WALMode:         true,
		ForeignKeys:     true,
		BusyTimeout:     5 * time.Second,
		MaxOpenConns:    1, // SQLite single-writer
		MaxIdleConns:    1,
		ConnMaxLifetime: time.Hour,
	}
}

// Open opens or creates a database with the given configuration.
func Open(cfg Config) (*DB, error) {
	// Ensure directory exists
	dir := filepath.Dir(cfg.Path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}

	// Build connection string for modernc.org/sqlite
	// Note: modernc uses different pragma syntax than mattn
	dsn := cfg.Path

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	// Set pragmas via SQL (modernc.org/sqlite style)
	pragmas := []string{
		"PRAGMA busy_timeout = " + fmt.Sprintf("%d", cfg.BusyTimeout.Milliseconds()),
	}
	if cfg.WALMode {
		pragmas = append(pragmas, "PRAGMA journal_mode = WAL")
	}
	if cfg.ForeignKeys {
		pragmas = append(pragmas, "PRAGMA foreign_keys = ON")
	}
	// HOROS required pragmas
	pragmas = append(pragmas, "PRAGMA synchronous = NORMAL")

	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("set pragma %q: %w", pragma, err)
		}
	}

	// Verify connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return &DB{
		DB:       db,
		path:     cfg.Path,
		dbType:   cfg.Type,
		attached: make(map[string]string),
	}, nil
}

// OpenCorpus opens or creates the corpus database.
func OpenCorpus(dataDir string) (*DB, error) {
	path := filepath.Join(dataDir, "corpus.db")
	db, err := Open(DefaultConfig(path, DBTypeCorpus))
	if err != nil {
		return nil, err
	}

	// Initialize schema if needed
	if err := db.initSchema("corpus.sql"); err != nil {
		db.Close()
		return nil, fmt.Errorf("init corpus schema: %w", err)
	}

	return db, nil
}

// OpenWorkflows opens or creates the workflows database.
func OpenWorkflows(dataDir string) (*DB, error) {
	path := filepath.Join(dataDir, "workflows.db")
	db, err := Open(DefaultConfig(path, DBTypeWorkflows))
	if err != nil {
		return nil, err
	}

	// Initialize schema if needed
	if err := db.initSchema("workflows.sql"); err != nil {
		db.Close()
		return nil, fmt.Errorf("init workflows schema: %w", err)
	}

	return db, nil
}

// CreateRun creates a new run database.
func CreateRun(runsDir, runID string) (*DB, error) {
	path := filepath.Join(runsDir, fmt.Sprintf("%s.db", runID))
	db, err := Open(DefaultConfig(path, DBTypeRun))
	if err != nil {
		return nil, err
	}

	// Initialize schema
	if err := db.initSchema("run.sql"); err != nil {
		db.Close()
		os.Remove(path) // Clean up on failure
		return nil, fmt.Errorf("init run schema: %w", err)
	}

	return db, nil
}

// initSchema initializes the database with the embedded schema.
func (db *DB) initSchema(schemaFile string) error {
	// Use assets package (HOROS compliant - no ".." in embed path)
	schemaPath := fmt.Sprintf("schema/%s", schemaFile)
	schema, err := assets.SchemaFS.ReadFile(schemaPath)
	if err != nil {
		return fmt.Errorf("read schema %s: %w", schemaFile, err)
	}

	_, err = db.Exec(string(schema))
	if err != nil {
		return fmt.Errorf("execute schema %s: %w", schemaFile, err)
	}

	return nil
}

// Attach attaches another database with the given alias.
// HOROS pattern: Always use with defer Detach()
func (db *DB) Attach(ctx context.Context, path, alias string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if _, exists := db.attached[alias]; exists {
		return fmt.Errorf("alias %q already attached", alias)
	}

	query := fmt.Sprintf("ATTACH DATABASE %q AS %s", path, alias)
	if _, err := db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("attach %s: %w", path, err)
	}

	db.attached[alias] = path
	return nil
}

// Detach detaches a previously attached database.
func (db *DB) Detach(ctx context.Context, alias string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if _, exists := db.attached[alias]; !exists {
		return fmt.Errorf("alias %q not attached", alias)
	}

	query := fmt.Sprintf("DETACH DATABASE %s", alias)
	if _, err := db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("detach %s: %w", alias, err)
	}

	delete(db.attached, alias)
	return nil
}

// Path returns the database file path.
func (db *DB) Path() string {
	return db.path
}

// Type returns the database type.
func (db *DB) Type() DBType {
	return db.dbType
}

// Transaction executes a function within a transaction.
func (db *DB) Transaction(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("rollback failed: %v (original error: %w)", rbErr, err)
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// TableExists checks if a table exists in the database.
func (db *DB) TableExists(ctx context.Context, tableName string) (bool, error) {
	var count int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?",
		tableName,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// RowCount returns the number of rows in a table.
func (db *DB) RowCount(ctx context.Context, tableName string) (int64, error) {
	var count int64
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s", tableName)
	err := db.QueryRowContext(ctx, query).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

// Vacuum performs database maintenance.
func (db *DB) Vacuum(ctx context.Context) error {
	_, err := db.ExecContext(ctx, "VACUUM")
	return err
}

// Checkpoint forces a WAL checkpoint.
func (db *DB) Checkpoint(ctx context.Context) error {
	_, err := db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	return err
}

// GetConfig retrieves a configuration value from the config table.
func (db *DB) GetConfig(ctx context.Context, key string) (string, error) {
	var value string
	err := db.QueryRowContext(ctx,
		"SELECT value FROM config WHERE key = ?",
		key,
	).Scan(&value)
	if err != nil {
		return "", err
	}
	return value, nil
}

// SetConfig sets a configuration value.
func (db *DB) SetConfig(ctx context.Context, key, value string) error {
	_, err := db.ExecContext(ctx,
		"INSERT OR REPLACE INTO config (key, value, updated_at) VALUES (?, ?, datetime('now'))",
		key, value,
	)
	return err
}

// Stats returns database statistics.
type Stats struct {
	Path      string
	SizeBytes int64
	Tables    int
	TotalRows int64
	WALSize   int64
	PageSize  int
	PageCount int
	FreePages int
}

// GetStats returns database statistics.
func (db *DB) GetStats(ctx context.Context) (*Stats, error) {
	stats := &Stats{Path: db.path}

	// File size
	if info, err := os.Stat(db.path); err == nil {
		stats.SizeBytes = info.Size()
	}

	// WAL size
	walPath := db.path + "-wal"
	if info, err := os.Stat(walPath); err == nil {
		stats.WALSize = info.Size()
	}

	// Table count
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'",
	).Scan(&stats.Tables)
	if err != nil {
		return nil, err
	}

	// Page info
	db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&stats.PageSize)
	db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&stats.PageCount)
	db.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&stats.FreePages)

	return stats, nil
}
