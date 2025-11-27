// Package db provides SQLite database operations for GoRAGlite.
// Uses zombiezen.com/go/sqlite (pure Go, no CGO).
package db

import (
	"encoding/binary"
	"fmt"
	"math"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// Schema SQL for GoRAGlite - Code-only RAG system
const schema = `
-- Chunks de code source
CREATE TABLE IF NOT EXISTS chunks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    file_path TEXT NOT NULL,
    language TEXT NOT NULL,
    chunk_type TEXT NOT NULL,
    name TEXT NOT NULL,
    signature TEXT,
    content TEXT NOT NULL,
    start_line INTEGER NOT NULL,
    end_line INTEGER NOT NULL,
    hash TEXT UNIQUE NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Vecteurs de structure (AST paths)
CREATE TABLE IF NOT EXISTS vectors_structure (
    chunk_id INTEGER PRIMARY KEY,
    vector BLOB NOT NULL,
    dims INTEGER NOT NULL,
    FOREIGN KEY (chunk_id) REFERENCES chunks(id) ON DELETE CASCADE
);

-- Vecteurs lexicaux (TF-IDF sur identifiants)
CREATE TABLE IF NOT EXISTS vectors_lexical (
    chunk_id INTEGER PRIMARY KEY,
    vector BLOB NOT NULL,
    dims INTEGER NOT NULL,
    FOREIGN KEY (chunk_id) REFERENCES chunks(id) ON DELETE CASCADE
);

-- Vecteur final fusionné (pour recherche rapide)
CREATE TABLE IF NOT EXISTS vectors_final (
    chunk_id INTEGER PRIMARY KEY,
    vector BLOB NOT NULL,
    dims INTEGER NOT NULL,
    blend_weights TEXT,
    FOREIGN KEY (chunk_id) REFERENCES chunks(id) ON DELETE CASCADE
);

-- Paramètres de vectorisation par langage
CREATE TABLE IF NOT EXISTS vectorization_params (
    language TEXT NOT NULL,
    layer TEXT NOT NULL,
    param_name TEXT NOT NULL,
    param_value TEXT NOT NULL,
    PRIMARY KEY (language, layer, param_name)
);

-- Vocabulaire global (pour couche lexicale)
CREATE TABLE IF NOT EXISTS vocabulary (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    token TEXT UNIQUE NOT NULL,
    doc_freq INTEGER DEFAULT 1,
    total_freq INTEGER DEFAULT 1
);

-- Index pour recherche rapide
CREATE INDEX IF NOT EXISTS idx_chunks_language ON chunks(language);
CREATE INDEX IF NOT EXISTS idx_chunks_file ON chunks(file_path);
CREATE INDEX IF NOT EXISTS idx_chunks_type ON chunks(chunk_type);
CREATE INDEX IF NOT EXISTS idx_chunks_name ON chunks(name);
`

// Default vectorization params for Go
const defaultGoParams = `
INSERT OR IGNORE INTO vectorization_params (language, layer, param_name, param_value) VALUES
    ('go', 'structure', 'dims', '256'),
    ('go', 'structure', 'ast_max_depth', '10'),
    ('go', 'structure', 'hash_seed', '42'),
    ('go', 'lexical', 'dims', '128'),
    ('go', 'lexical', 'min_token_freq', '2'),
    ('go', 'final', 'dims', '256'),
    ('go', 'final', 'weights', '{"structure": 0.7, "lexical": 0.3}');
`

// DB wraps the SQLite database connection.
type DB struct {
	conn *sqlite.Conn
}

// Open opens or creates the SQLite database.
func Open(path string) (*DB, error) {
	conn, err := sqlite.OpenConn(path, sqlite.OpenReadWrite|sqlite.OpenCreate)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	// Enable foreign keys
	if err := sqlitex.ExecScript(conn, "PRAGMA foreign_keys = ON;"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	// Initialize schema
	if err := sqlitex.ExecScript(conn, schema); err != nil {
		conn.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	// Insert default params
	if err := sqlitex.ExecScript(conn, defaultGoParams); err != nil {
		conn.Close()
		return nil, fmt.Errorf("init params: %w", err)
	}

	return &DB{conn: conn}, nil
}

// Close closes the database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}

// Chunk represents a code chunk.
type Chunk struct {
	ID        int64
	FilePath  string
	Language  string
	ChunkType string
	Name      string
	Signature string
	Content   string
	StartLine int
	EndLine   int
	Hash      string
}

// InsertChunk inserts a new chunk and returns its ID.
func (db *DB) InsertChunk(c *Chunk) (int64, error) {
	stmt := db.conn.Prep(`
		INSERT OR IGNORE INTO chunks
		(file_path, language, chunk_type, name, signature, content, start_line, end_line, hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	defer stmt.Reset()

	stmt.BindText(1, c.FilePath)
	stmt.BindText(2, c.Language)
	stmt.BindText(3, c.ChunkType)
	stmt.BindText(4, c.Name)
	stmt.BindText(5, c.Signature)
	stmt.BindText(6, c.Content)
	stmt.BindInt64(7, int64(c.StartLine))
	stmt.BindInt64(8, int64(c.EndLine))
	stmt.BindText(9, c.Hash)

	if _, err := stmt.Step(); err != nil {
		return 0, err
	}

	// Get the ID (either new or existing)
	id := db.conn.LastInsertRowID()
	if id == 0 {
		// Was ignored, find existing
		findStmt := db.conn.Prep("SELECT id FROM chunks WHERE hash = ?")
		defer findStmt.Reset()
		findStmt.BindText(1, c.Hash)
		if hasRow, err := findStmt.Step(); err != nil {
			return 0, err
		} else if hasRow {
			id = findStmt.ColumnInt64(0)
		}
	}

	return id, nil
}

// InsertVector inserts a vector for a chunk in the specified layer.
func (db *DB) InsertVector(chunkID int64, layer string, vec []float32) error {
	blob := float32ToBytes(vec)
	table := "vectors_" + layer

	stmt := db.conn.Prep(fmt.Sprintf(`
		INSERT OR REPLACE INTO %s (chunk_id, vector, dims)
		VALUES (?, ?, ?)`, table))
	defer stmt.Reset()

	stmt.BindInt64(1, chunkID)
	stmt.BindBytes(2, blob)
	stmt.BindInt64(3, int64(len(vec)))

	_, err := stmt.Step()
	return err
}

// GetVector retrieves a vector for a chunk from the specified layer.
func (db *DB) GetVector(chunkID int64, layer string) ([]float32, error) {
	table := "vectors_" + layer

	stmt := db.conn.Prep(fmt.Sprintf(`
		SELECT vector, dims FROM %s WHERE chunk_id = ?`, table))
	defer stmt.Reset()

	stmt.BindInt64(1, chunkID)

	hasRow, err := stmt.Step()
	if err != nil {
		return nil, err
	}
	if !hasRow {
		return nil, fmt.Errorf("vector not found for chunk %d in layer %s", chunkID, layer)
	}

	dims := int(stmt.ColumnInt64(1))
	blob := make([]byte, dims*4)
	stmt.ColumnBytes(0, blob)

	return bytesToFloat32(blob, dims), nil
}

// GetAllVectors retrieves all vectors from a layer with their chunk IDs.
func (db *DB) GetAllVectors(layer string) ([]int64, [][]float32, error) {
	table := "vectors_" + layer

	stmt := db.conn.Prep(fmt.Sprintf(`SELECT chunk_id, vector, dims FROM %s`, table))
	defer stmt.Reset()

	var ids []int64
	var vecs [][]float32

	for {
		hasRow, err := stmt.Step()
		if err != nil {
			return nil, nil, err
		}
		if !hasRow {
			break
		}

		id := stmt.ColumnInt64(0)
		dims := int(stmt.ColumnInt64(2))
		blob := make([]byte, dims*4)
		stmt.ColumnBytes(1, blob)

		ids = append(ids, id)
		vecs = append(vecs, bytesToFloat32(blob, dims))
	}

	return ids, vecs, nil
}

// GetChunk retrieves a chunk by ID.
func (db *DB) GetChunk(id int64) (*Chunk, error) {
	stmt := db.conn.Prep(`
		SELECT id, file_path, language, chunk_type, name, signature,
		       content, start_line, end_line, hash
		FROM chunks WHERE id = ?`)
	defer stmt.Reset()

	stmt.BindInt64(1, id)

	hasRow, err := stmt.Step()
	if err != nil {
		return nil, err
	}
	if !hasRow {
		return nil, fmt.Errorf("chunk %d not found", id)
	}

	c := &Chunk{
		ID:        stmt.ColumnInt64(0),
		FilePath:  stmt.ColumnText(1),
		Language:  stmt.ColumnText(2),
		ChunkType: stmt.ColumnText(3),
		Name:      stmt.ColumnText(4),
		Signature: stmt.ColumnText(5),
		Content:   stmt.ColumnText(6),
		StartLine: int(stmt.ColumnInt64(7)),
		EndLine:   int(stmt.ColumnInt64(8)),
		Hash:      stmt.ColumnText(9),
	}

	return c, nil
}

// GetParam retrieves a vectorization parameter.
func (db *DB) GetParam(language, layer, name string) (string, error) {
	stmt := db.conn.Prep(`
		SELECT param_value FROM vectorization_params
		WHERE language = ? AND layer = ? AND param_name = ?`)
	defer stmt.Reset()

	stmt.BindText(1, language)
	stmt.BindText(2, layer)
	stmt.BindText(3, name)

	hasRow, err := stmt.Step()
	if err != nil {
		return "", err
	}
	if !hasRow {
		return "", fmt.Errorf("param not found: %s/%s/%s", language, layer, name)
	}

	return stmt.ColumnText(0), nil
}

// Stats returns basic statistics about the database.
func (db *DB) Stats() (chunks, vectors int, err error) {
	stmt := db.conn.Prep("SELECT COUNT(*) FROM chunks")
	if hasRow, _ := stmt.Step(); hasRow {
		chunks = int(stmt.ColumnInt64(0))
	}
	stmt.Reset()

	stmt2 := db.conn.Prep("SELECT COUNT(*) FROM vectors_final")
	if hasRow, _ := stmt2.Step(); hasRow {
		vectors = int(stmt2.ColumnInt64(0))
	}
	stmt2.Reset()

	return chunks, vectors, nil
}

// Helper: convert []float32 to bytes (little endian)
func float32ToBytes(vec []float32) []byte {
	buf := make([]byte, len(vec)*4)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

// Helper: convert bytes to []float32
func bytesToFloat32(buf []byte, dims int) []float32 {
	vec := make([]float32, dims)
	for i := 0; i < dims; i++ {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(buf[i*4:]))
	}
	return vec
}
