// Package egocheck provides self-introspection for HOROS workers.
// Each worker must compute and store SHA256 hashes of its source files
// and databases at startup for integrity verification.
//
// This implements HOROS Dogme 5 (Fractalité) - Phase 2 AUDIT.
package egocheck

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Manifest represents a worker's self-introspection result.
type Manifest struct {
	ProcessID   string     `json:"process_id"`
	ProcessName string     `json:"process_name"`
	Timestamp   time.Time  `json:"timestamp"`
	SourceFiles []FileInfo `json:"source_files"`
	DBFiles     []FileInfo `json:"db_files"`
	TotalHash   string     `json:"total_hash"` // Combined hash of all files
}

// FileInfo contains metadata about a hashed file.
type FileInfo struct {
	Path    string `json:"path"`
	Hash    string `json:"hash"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"mod_time"`
}

// Run performs self-introspection on the given files.
func Run(processID, processName string, srcPaths, dbPaths []string) (*Manifest, error) {
	m := &Manifest{
		ProcessID:   processID,
		ProcessName: processName,
		Timestamp:   time.Now(),
	}

	// Hash source files
	for _, path := range srcPaths {
		info, err := hashFile(path)
		if err != nil {
			// Skip missing files but log them
			continue
		}
		m.SourceFiles = append(m.SourceFiles, info)
	}

	// Hash database files
	for _, path := range dbPaths {
		info, err := hashFile(path)
		if err != nil {
			continue
		}
		m.DBFiles = append(m.DBFiles, info)
	}

	// Compute total hash
	m.TotalHash = computeTotalHash(m)

	return m, nil
}

// RunDir hashes all Go files in a directory tree.
func RunDir(processID, processName, srcDir string, dbPaths []string) (*Manifest, error) {
	var srcPaths []string

	err := filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		if !info.IsDir() && filepath.Ext(path) == ".go" {
			srcPaths = append(srcPaths, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return Run(processID, processName, srcPaths, dbPaths)
}

// hashFile computes the SHA256 hash of a file.
func hashFile(path string) (FileInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return FileInfo{}, err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return FileInfo{}, err
	}

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return FileInfo{}, err
	}

	return FileInfo{
		Path:    path,
		Hash:    hex.EncodeToString(h.Sum(nil)),
		Size:    stat.Size(),
		ModTime: stat.ModTime().Unix(),
	}, nil
}

// computeTotalHash combines all file hashes into a single hash.
func computeTotalHash(m *Manifest) string {
	h := sha256.New()

	for _, f := range m.SourceFiles {
		h.Write([]byte(f.Hash))
	}
	for _, f := range m.DBFiles {
		h.Write([]byte(f.Hash))
	}

	return hex.EncodeToString(h.Sum(nil))
}

// StoreInDB stores the manifest in a database.
func StoreInDB(ctx context.Context, db *sql.DB, manifest *Manifest) error {
	// Create table if needed
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS ego_manifests (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			process_id TEXT NOT NULL,
			process_name TEXT NOT NULL,
			timestamp INTEGER NOT NULL,
			file_path TEXT NOT NULL,
			file_type TEXT NOT NULL,
			hash_sha256 TEXT NOT NULL,
			size_bytes INTEGER NOT NULL,
			mod_time INTEGER NOT NULL,
			UNIQUE(process_id, file_path)
		);

		CREATE INDEX IF NOT EXISTS idx_ego_process ON ego_manifests(process_id);
		CREATE INDEX IF NOT EXISTS idx_ego_timestamp ON ego_manifests(timestamp);
	`)
	if err != nil {
		return fmt.Errorf("create ego_manifests table: %w", err)
	}

	// Store manifest in transaction
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	ts := manifest.Timestamp.Unix()

	// Delete old entries for this process
	_, err = tx.ExecContext(ctx, "DELETE FROM ego_manifests WHERE process_id = ?", manifest.ProcessID)
	if err != nil {
		return err
	}

	// Insert source files
	for _, f := range manifest.SourceFiles {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO ego_manifests (process_id, process_name, timestamp, file_path, file_type, hash_sha256, size_bytes, mod_time)
			VALUES (?, ?, ?, ?, 'source', ?, ?, ?)
		`, manifest.ProcessID, manifest.ProcessName, ts, f.Path, f.Hash, f.Size, f.ModTime)
		if err != nil {
			return err
		}
	}

	// Insert db files
	for _, f := range manifest.DBFiles {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO ego_manifests (process_id, process_name, timestamp, file_path, file_type, hash_sha256, size_bytes, mod_time)
			VALUES (?, ?, ?, ?, 'database', ?, ?, ?)
		`, manifest.ProcessID, manifest.ProcessName, ts, f.Path, f.Hash, f.Size, f.ModTime)
		if err != nil {
			return err
		}
	}

	// Store total hash in a summary row
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ego_manifests (process_id, process_name, timestamp, file_path, file_type, hash_sha256, size_bytes, mod_time)
		VALUES (?, ?, ?, '__TOTAL__', 'summary', ?, 0, ?)
	`, manifest.ProcessID, manifest.ProcessName, ts, manifest.TotalHash, ts)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// LoadFromDB loads the latest manifest for a process.
func LoadFromDB(ctx context.Context, db *sql.DB, processID string) (*Manifest, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT process_name, timestamp, file_path, file_type, hash_sha256, size_bytes, mod_time
		FROM ego_manifests
		WHERE process_id = ?
		ORDER BY timestamp DESC
	`, processID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var m *Manifest
	for rows.Next() {
		var processName, filePath, fileType, hash string
		var ts, size, modTime int64

		if err := rows.Scan(&processName, &ts, &filePath, &fileType, &hash, &size, &modTime); err != nil {
			return nil, err
		}

		if m == nil {
			m = &Manifest{
				ProcessID:   processID,
				ProcessName: processName,
				Timestamp:   time.Unix(ts, 0),
			}
		}

		info := FileInfo{
			Path:    filePath,
			Hash:    hash,
			Size:    size,
			ModTime: modTime,
		}

		switch fileType {
		case "source":
			m.SourceFiles = append(m.SourceFiles, info)
		case "database":
			m.DBFiles = append(m.DBFiles, info)
		case "summary":
			m.TotalHash = hash
		}
	}

	return m, nil
}

// Verify checks if the current files match the stored manifest.
func Verify(ctx context.Context, db *sql.DB, processID string, srcPaths, dbPaths []string) (bool, []string, error) {
	stored, err := LoadFromDB(ctx, db, processID)
	if err != nil {
		return false, nil, err
	}
	if stored == nil {
		return false, []string{"no stored manifest found"}, nil
	}

	current, err := Run(processID, stored.ProcessName, srcPaths, dbPaths)
	if err != nil {
		return false, nil, err
	}

	var mismatches []string

	// Compare total hashes
	if stored.TotalHash != current.TotalHash {
		mismatches = append(mismatches, fmt.Sprintf("total hash mismatch: stored=%s current=%s", stored.TotalHash[:12], current.TotalHash[:12]))
	}

	// Build maps for comparison
	storedSrc := make(map[string]string)
	for _, f := range stored.SourceFiles {
		storedSrc[f.Path] = f.Hash
	}

	for _, f := range current.SourceFiles {
		if storedHash, ok := storedSrc[f.Path]; ok {
			if storedHash != f.Hash {
				mismatches = append(mismatches, fmt.Sprintf("source file changed: %s", f.Path))
			}
		} else {
			mismatches = append(mismatches, fmt.Sprintf("new source file: %s", f.Path))
		}
	}

	return len(mismatches) == 0, mismatches, nil
}

// ToJSON serializes the manifest to JSON.
func (m *Manifest) ToJSON() ([]byte, error) {
	return json.MarshalIndent(m, "", "  ")
}
