-- GoRAGlite v2 - Corpus Schema
-- Le graphe de connaissance consolidé
-- Toute transformation est une table. Pas d'état implicite.

PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
PRAGMA synchronous = NORMAL;

-- ============================================================================
-- LAYER 0 : Raw Import
-- ============================================================================

CREATE TABLE IF NOT EXISTS raw_files (
    id TEXT PRIMARY KEY,                    -- hash du contenu (SHA256)
    source_path TEXT NOT NULL,              -- chemin d'origine
    mime_type TEXT NOT NULL,                -- type MIME détecté
    size INTEGER NOT NULL,                  -- taille en bytes
    -- HOROS: No BLOB storage for large files. Use external_path instead.
    external_path TEXT NOT NULL,            -- chemin externe (HOROS: cp/rm compatible)
    checksum TEXT NOT NULL,                 -- hash pour vérification intégrité
    imported_at TEXT NOT NULL DEFAULT (datetime('now')),
    status TEXT NOT NULL DEFAULT 'pending'  -- pending | extracted | chunked | vectorized | failed
        CHECK (status IN ('pending', 'extracted', 'chunked', 'vectorized', 'failed'))
);

CREATE INDEX IF NOT EXISTS idx_raw_files_status ON raw_files(status);
CREATE INDEX IF NOT EXISTS idx_raw_files_mime ON raw_files(mime_type);

-- ============================================================================
-- LAYER 1 : Extraction (sortie des extracteurs)
-- ============================================================================

CREATE TABLE IF NOT EXISTS extracted_segments (
    id TEXT PRIMARY KEY,
    file_id TEXT NOT NULL REFERENCES raw_files(id) ON DELETE CASCADE,
    extractor TEXT NOT NULL,                -- 'pdftotext', 'pandoc', 'xlsx_parser', etc.
    extractor_version TEXT NOT NULL,        -- pour reproductibilité
    segment_type TEXT NOT NULL              -- 'text', 'table', 'image_ocr', 'metadata', 'code'
        CHECK (segment_type IN ('text', 'table', 'image_ocr', 'metadata', 'code')),
    content TEXT NOT NULL,
    page INTEGER,                           -- numéro de page si applicable
    position INTEGER NOT NULL,              -- ordre dans le document
    bbox TEXT,                              -- JSON coordonnées si applicable
    confidence REAL,                        -- score de confiance OCR/parsing
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_segments_file ON extracted_segments(file_id);
CREATE INDEX IF NOT EXISTS idx_segments_type ON extracted_segments(segment_type);

-- ============================================================================
-- LAYER 2 : Parse (structure locale)
-- ============================================================================

CREATE TABLE IF NOT EXISTS parsed_units (
    id TEXT PRIMARY KEY,
    segment_id TEXT NOT NULL REFERENCES extracted_segments(id) ON DELETE CASCADE,
    unit_type TEXT NOT NULL                 -- 'paragraph', 'heading', 'list_item', 'cell', 'code_block'
        CHECK (unit_type IN ('paragraph', 'heading', 'list_item', 'cell', 'code_block', 'sentence')),
    level INTEGER DEFAULT 0,                -- profondeur hiérarchique
    content TEXT NOT NULL,
    tokens INTEGER,                         -- compte approximatif de tokens
    parent_id TEXT REFERENCES parsed_units(id), -- hiérarchie
    position INTEGER NOT NULL,              -- ordre dans le segment
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_units_segment ON parsed_units(segment_id);
CREATE INDEX IF NOT EXISTS idx_units_parent ON parsed_units(parent_id);

-- ============================================================================
-- LAYER 3 : Chunks (unités sémantiques finales)
-- ============================================================================

CREATE TABLE IF NOT EXISTS chunks (
    id TEXT PRIMARY KEY,
    file_id TEXT NOT NULL REFERENCES raw_files(id) ON DELETE CASCADE,
    unit_ids TEXT,                          -- JSON array des units source
    content TEXT NOT NULL,
    token_count INTEGER NOT NULL,
    chunk_type TEXT NOT NULL                -- 'semantic', 'fixed_window', 'sentence', 'paragraph'
        CHECK (chunk_type IN ('semantic', 'fixed_window', 'sentence', 'paragraph')),
    overlap_prev INTEGER DEFAULT 0,         -- tokens de chevauchement avec chunk précédent
    overlap_next INTEGER DEFAULT 0,         -- tokens de chevauchement avec chunk suivant
    hash TEXT NOT NULL,                     -- hash du contenu (déduplication)
    position INTEGER NOT NULL,              -- ordre dans le fichier source
    parent_id TEXT REFERENCES chunks(id),   -- hiérarchie optionnelle
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    created_by_run TEXT                     -- ID du run qui l'a créé
);

CREATE INDEX IF NOT EXISTS idx_chunks_file ON chunks(file_id);
CREATE INDEX IF NOT EXISTS idx_chunks_hash ON chunks(hash);
CREATE INDEX IF NOT EXISTS idx_chunks_type ON chunks(chunk_type);

-- FTS5 pour recherche full-text
CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(
    content,
    content=chunks,
    content_rowid=rowid
);

-- Triggers pour synchroniser FTS
CREATE TRIGGER IF NOT EXISTS chunks_ai AFTER INSERT ON chunks BEGIN
    INSERT INTO chunks_fts(rowid, content) VALUES (NEW.rowid, NEW.content);
END;

CREATE TRIGGER IF NOT EXISTS chunks_ad AFTER DELETE ON chunks BEGIN
    INSERT INTO chunks_fts(chunks_fts, rowid, content) VALUES('delete', OLD.rowid, OLD.content);
END;

CREATE TRIGGER IF NOT EXISTS chunks_au AFTER UPDATE ON chunks BEGIN
    INSERT INTO chunks_fts(chunks_fts, rowid, content) VALUES('delete', OLD.rowid, OLD.content);
    INSERT INTO chunks_fts(rowid, content) VALUES (NEW.rowid, NEW.content);
END;

-- ============================================================================
-- LAYER 4 : Features (caractéristiques extraites)
-- ============================================================================

CREATE TABLE IF NOT EXISTS chunk_features (
    chunk_id TEXT NOT NULL REFERENCES chunks(id) ON DELETE CASCADE,
    feature_name TEXT NOT NULL,
    feature_value REAL NOT NULL,
    feature_meta TEXT,                      -- JSON (unité, source, etc.)
    PRIMARY KEY (chunk_id, feature_name)
);

CREATE INDEX IF NOT EXISTS idx_features_name ON chunk_features(feature_name);

-- ============================================================================
-- LAYER 5 : Vectors (projections vectorielles)
-- ============================================================================

CREATE TABLE IF NOT EXISTS chunk_vectors (
    chunk_id TEXT NOT NULL REFERENCES chunks(id) ON DELETE CASCADE,
    layer TEXT NOT NULL                     -- 'structure', 'lexical', 'contextual', 'blend'
        CHECK (layer IN ('structure', 'lexical', 'contextual', 'blend')),
    vector BLOB NOT NULL,                   -- float32[] sérialisé
    dimensions INTEGER NOT NULL,
    model_version TEXT NOT NULL,            -- version de l'algo de vectorisation
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (chunk_id, layer)
);

CREATE INDEX IF NOT EXISTS idx_vectors_layer ON chunk_vectors(layer);

-- ============================================================================
-- Relations entre chunks (graphe)
-- ============================================================================

CREATE TABLE IF NOT EXISTS chunk_relations (
    from_chunk_id TEXT NOT NULL REFERENCES chunks(id) ON DELETE CASCADE,
    to_chunk_id TEXT NOT NULL REFERENCES chunks(id) ON DELETE CASCADE,
    relation_type TEXT NOT NULL             -- 'references', 'follows', 'parent_of', 'similar_to'
        CHECK (relation_type IN ('references', 'follows', 'parent_of', 'similar_to', 'calls', 'imports')),
    weight REAL NOT NULL DEFAULT 1.0        -- force de la relation (0-1)
        CHECK (weight >= 0 AND weight <= 1),
    created_by_run TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (from_chunk_id, to_chunk_id, relation_type)
);

CREATE INDEX IF NOT EXISTS idx_relations_from ON chunk_relations(from_chunk_id);
CREATE INDEX IF NOT EXISTS idx_relations_to ON chunk_relations(to_chunk_id);
CREATE INDEX IF NOT EXISTS idx_relations_type ON chunk_relations(relation_type);

-- ============================================================================
-- Index du corpus (métadonnées des index)
-- ============================================================================

CREATE TABLE IF NOT EXISTS corpus_index (
    id TEXT PRIMARY KEY,
    index_type TEXT NOT NULL                -- 'fts', 'vector', 'graph'
        CHECK (index_type IN ('fts', 'vector', 'graph')),
    target_table TEXT NOT NULL,             -- chunks, chunk_vectors, chunk_relations
    config TEXT,                            -- JSON (paramètres de l'index)
    built_at TEXT,
    status TEXT NOT NULL DEFAULT 'pending'  -- 'pending', 'building', 'ready', 'stale'
        CHECK (status IN ('pending', 'building', 'ready', 'stale'))
);

-- ============================================================================
-- Historique des runs (métadonnées seulement, pas les données)
-- ============================================================================

CREATE TABLE IF NOT EXISTS run_history (
    run_id TEXT PRIMARY KEY,
    workflow_id TEXT NOT NULL,
    workflow_version INTEGER NOT NULL,
    input_hash TEXT,                        -- hash de l'input snapshot
    started_at TEXT NOT NULL,
    finished_at TEXT,
    status TEXT NOT NULL DEFAULT 'pending'  -- pending | running | completed | failed | merged
        CHECK (status IN ('pending', 'running', 'completed', 'failed', 'merged')),
    worker_id TEXT,                         -- quel worker l'a exécuté
    db_path TEXT,                           -- chemin vers run.db si conservé
    rows_produced INTEGER,                  -- nombre de lignes dans _output
    merge_status TEXT DEFAULT 'pending'     -- pending | merged | skipped | failed
        CHECK (merge_status IN ('pending', 'merged', 'skipped', 'failed')),
    notes TEXT                              -- JSON (erreurs, warnings, métriques)
);

CREATE INDEX IF NOT EXISTS idx_runs_workflow ON run_history(workflow_id);
CREATE INDEX IF NOT EXISTS idx_runs_status ON run_history(status);

-- ============================================================================
-- Versioning des extracteurs (reproductibilité)
-- ============================================================================

CREATE TABLE IF NOT EXISTS extractor_versions (
    extractor_name TEXT NOT NULL,
    version TEXT NOT NULL,
    hash TEXT,                              -- hash du binaire/code
    config_schema TEXT,                     -- JSON (paramètres acceptés)
    deprecated INTEGER NOT NULL DEFAULT 0,
    registered_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (extractor_name, version)
);

-- ============================================================================
-- Feedback utilisateur (boucle d'apprentissage)
-- ============================================================================

CREATE TABLE IF NOT EXISTS feedback_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    query_id TEXT,
    query_text TEXT,
    result_chunk_id TEXT REFERENCES chunks(id),
    action TEXT NOT NULL                    -- 'click', 'ignore', 'upvote', 'downvote'
        CHECK (action IN ('click', 'ignore', 'upvote', 'downvote')),
    position INTEGER,                       -- rang dans les résultats
    session_id TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_feedback_query ON feedback_log(query_id);
CREATE INDEX IF NOT EXISTS idx_feedback_chunk ON feedback_log(result_chunk_id);

-- ============================================================================
-- Audit log (traçabilité)
-- ============================================================================

CREATE TABLE IF NOT EXISTS audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp TEXT NOT NULL DEFAULT (datetime('now')),
    actor TEXT NOT NULL,                    -- 'orchestrator', 'worker_{id}', 'merger', 'admin', 'user'
    action TEXT NOT NULL,                   -- 'create_run', 'merge', 'modify_workflow', etc.
    target TEXT,                            -- run_id, workflow_id, chunk_id
    details TEXT,                           -- JSON
    ip_address TEXT
);

CREATE INDEX IF NOT EXISTS idx_audit_actor ON audit_log(actor);
CREATE INDEX IF NOT EXISTS idx_audit_action ON audit_log(action);
CREATE INDEX IF NOT EXISTS idx_audit_timestamp ON audit_log(timestamp);

-- ============================================================================
-- Configuration globale
-- ============================================================================

CREATE TABLE IF NOT EXISTS config (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    description TEXT,
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Valeurs par défaut
INSERT OR IGNORE INTO config (key, value, description) VALUES
    ('version', '2.0.0', 'GoRAGlite version'),
    ('max_chunk_tokens', '512', 'Maximum tokens per chunk'),
    ('default_overlap', '50', 'Default overlap between chunks'),
    ('gc_retention_days', '7', 'Days to keep completed runs'),
    ('merger_batch_size', '100', 'Max runs to merge in one batch'),
    ('vector_dimensions', '256', 'Default vector dimensions'),
    ('blend_weights_structure', '0.45', 'Weight for structure layer'),
    ('blend_weights_lexical', '0.30', 'Weight for lexical layer'),
    ('blend_weights_contextual', '0.25', 'Weight for contextual layer');
