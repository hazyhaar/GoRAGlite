// Package db provides SQLite database operations for GoRAGlite.
package db

import (
	"database/sql"
	"encoding/binary"
	"math"

	_ "github.com/mattn/go-sqlite3"
)

// Schema SQL for GoRAGlite - Code-only RAG system
const schema = `
-- Chunks de code source
CREATE TABLE IF NOT EXISTS chunks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    file_path TEXT NOT NULL,
    language TEXT NOT NULL,
    chunk_type TEXT NOT NULL,      -- 'function', 'type', 'method', 'const', 'var'
    name TEXT NOT NULL,            -- Nom de la fonction/type/etc
    signature TEXT,                -- Signature complète (pour fonctions)
    content TEXT NOT NULL,
    start_line INTEGER NOT NULL,
    end_line INTEGER NOT NULL,
    hash TEXT UNIQUE NOT NULL,     -- SHA256 du contenu pour déduplication
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Vecteurs de structure (AST paths)
CREATE TABLE IF NOT EXISTS vectors_structure (
    chunk_id INTEGER PRIMARY KEY,
    vector BLOB NOT NULL,          -- float32[] serialisé
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
    blend_weights TEXT,            -- JSON: {"structure": 0.7, "lexical": 0.3}
    FOREIGN KEY (chunk_id) REFERENCES chunks(id) ON DELETE CASCADE
);

-- Paramètres de vectorisation par langage
CREATE TABLE IF NOT EXISTS vectorization_params (
    language TEXT NOT NULL,
    layer TEXT NOT NULL,           -- 'structure', 'lexical', 'final'
    param_name TEXT NOT NULL,
    param_value TEXT NOT NULL,     -- JSON value
    PRIMARY KEY (language, layer, param_name)
);

-- Vocabulaire global (pour couche lexicale)
CREATE TABLE IF NOT EXISTS vocabulary (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    token TEXT UNIQUE NOT NULL,
    doc_freq INTEGER DEFAULT 1,    -- Dans combien de chunks ce token apparaît
    total_freq INTEGER DEFAULT 1   -- Fréquence totale
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
	conn *sql.DB
}

// Open opens or creates the SQLite database.
func Open(path string) (*DB, error) {
	conn, err := sql.Open("sqlite3", path+"?_foreign_keys=on")
	if err != nil {
		return nil, err
	}

	// Initialize schema
	if _, err := conn.Exec(schema); err != nil {
		conn.Close()
		return nil, err
	}

	// Insert default params
	if _, err := conn.Exec(defaultGoParams); err != nil {
		conn.Close()
		return nil, err
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
	res, err := db.conn.Exec(`
		INSERT OR IGNORE INTO chunks
		(file_path, language, chunk_type, name, signature, content, start_line, end_line, hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.FilePath, c.Language, c.ChunkType, c.Name, c.Signature,
		c.Content, c.StartLine, c.EndLine, c.Hash)
	if err != nil {
		return 0, err
	}

	// If ignored (duplicate hash), find existing
	id, err := res.LastInsertId()
	if err != nil || id == 0 {
		row := db.conn.QueryRow("SELECT id FROM chunks WHERE hash = ?", c.Hash)
		row.Scan(&id)
	}
	return id, nil
}

// InsertVector inserts a vector for a chunk in the specified layer.
func (db *DB) InsertVector(chunkID int64, layer string, vec []float32) error {
	blob := float32ToBytes(vec)
	table := "vectors_" + layer

	_, err := db.conn.Exec(`
		INSERT OR REPLACE INTO `+table+` (chunk_id, vector, dims)
		VALUES (?, ?, ?)`, chunkID, blob, len(vec))
	return err
}

// GetVector retrieves a vector for a chunk from the specified layer.
func (db *DB) GetVector(chunkID int64, layer string) ([]float32, error) {
	table := "vectors_" + layer
	var blob []byte
	var dims int

	err := db.conn.QueryRow(`
		SELECT vector, dims FROM `+table+` WHERE chunk_id = ?`, chunkID).
		Scan(&blob, &dims)
	if err != nil {
		return nil, err
	}

	return bytesToFloat32(blob, dims), nil
}

// GetAllVectors retrieves all vectors from a layer with their chunk IDs.
func (db *DB) GetAllVectors(layer string) ([]int64, [][]float32, error) {
	table := "vectors_" + layer
	rows, err := db.conn.Query(`SELECT chunk_id, vector, dims FROM ` + table)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var ids []int64
	var vecs [][]float32

	for rows.Next() {
		var id int64
		var blob []byte
		var dims int
		if err := rows.Scan(&id, &blob, &dims); err != nil {
			return nil, nil, err
		}
		ids = append(ids, id)
		vecs = append(vecs, bytesToFloat32(blob, dims))
	}

	return ids, vecs, rows.Err()
}

// GetChunk retrieves a chunk by ID.
func (db *DB) GetChunk(id int64) (*Chunk, error) {
	c := &Chunk{}
	err := db.conn.QueryRow(`
		SELECT id, file_path, language, chunk_type, name, signature,
		       content, start_line, end_line, hash
		FROM chunks WHERE id = ?`, id).
		Scan(&c.ID, &c.FilePath, &c.Language, &c.ChunkType, &c.Name,
			&c.Signature, &c.Content, &c.StartLine, &c.EndLine, &c.Hash)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// GetParam retrieves a vectorization parameter.
func (db *DB) GetParam(language, layer, name string) (string, error) {
	var value string
	err := db.conn.QueryRow(`
		SELECT param_value FROM vectorization_params
		WHERE language = ? AND layer = ? AND param_name = ?`,
		language, layer, name).Scan(&value)
	return value, err
}

// Stats returns basic statistics about the database.
func (db *DB) Stats() (chunks, vectors int, err error) {
	db.conn.QueryRow("SELECT COUNT(*) FROM chunks").Scan(&chunks)
	db.conn.QueryRow("SELECT COUNT(*) FROM vectors_final").Scan(&vectors)
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
